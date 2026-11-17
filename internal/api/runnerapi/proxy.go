package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// errWrongSessionKind reports a session of one kind on a path of the other
// kind. The answer is 403 with protocol.ErrorWrongSessionKind.
var errWrongSessionKind = errors.New("the session is not of the kind of this path")

// changeReason is the key of the change that names the reason of a refusal.
const changeReason = "reason"

// proxyHandoff issues the handoff token of the proxy session of a claim of
// the runner.
func (h *Handler) proxyHandoff(w http.ResponseWriter, r *http.Request, c *call) {
	// Fail closed: a server with no proxy session service issues no token that
	// nothing can use.
	if h.proxies == nil {
		h.writeError(r.Context(), w, fmt.Errorf("%w: this server serves no job proxy", runner.ErrNotFound))

		return
	}

	h.issueHandoff(w, r, c, runner.SessionKindProxy)
}

// openProxySession exchanges a proxy handoff token for a proxy session and
// answers the proxy assignment. The checks of the token, its kind, the claim
// and the session wait limit are those of OpenSession, in its transaction.
func (h *Handler) openProxySession(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	// Nothing is used until the server can answer the assignment.
	if h.proxies == nil || h.operations == nil {
		h.writeError(ctx, w, fmt.Errorf("%w: this server serves no job proxy", runner.ErrNotFound))

		return
	}

	now := h.now()
	key := runner.Key{Public: c.handoff.PublicKey, CreatedAt: now}

	sess, err := h.handoffs.OpenSession(ctx, sha256Hex(c.handoff.HandoffToken), runner.SessionKindProxy, key, now)
	if err != nil {
		h.recordUnverifiedHandoffRefusal(ctx, r, err)

		// A launcher handoff token is unknown to a proxy session. The answer is
		// the one of a token that does not authenticate this path, and the token
		// stays unused.
		if errors.Is(err, runner.ErrNotFound) {
			err = errProofRefused
		}

		h.writeError(ctx, w, err)

		return
	}

	sp, err := h.phaseOf(r, &sess)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule := runner.AuditRules()[runner.EventProxySessionOpened]

	assignment, err := h.proxies.Assignment(ctx, &proxysessionsvc.AssignmentRequest{
		Session: sess, Pool: sp.pool, Entry: h.entry(r, sp.pool.Namespace, sess.ID, rule, now),
		ServerHost: h.serverHost(),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.logger.InfoContext(ctx, "job proxy opened a proxy session", slog.String("session", sess.ID),
		slog.String("operation", sess.Operation), slog.String("runner", sess.Runner))
	h.writeJSON(ctx, w, http.StatusOK, protocol.ProxySessionResponse{
		Session: sess.ID, ExpiresAt: sp.claim.Claim.ExpiresAt.UTC(), Lease: protocol.FromStd(sp.pool.EffectiveLease()),
		Assignment: proxyAssignmentOf(&assignment),
	})
}

// upstreamCredential hands the upstream credential of the pool to the proxy
// session. Each call is one reveal in the audit log. No error and no log line
// holds the credential.
func (h *Handler) upstreamCredential(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	if h.proxies == nil {
		h.writeError(ctx, w, fmt.Errorf("%w: this server serves no job proxy", runner.ErrNotFound))

		return
	}

	// The path has no body. A body is a request of another shape.
	if len(c.body) != 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: this path has no body", runner.ErrInvalid))

		return
	}

	sp, err := h.phaseOf(r, &c.session)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule := runner.AuditRules()[runner.EventProxyCredentialIssued]

	cred, err := h.proxies.Credential(ctx, &proxysessionsvc.SessionRequest{
		Session: c.session, Pool: sp.pool, Entry: h.entry(r, sp.pool.Namespace, c.session.ID, rule, h.now()),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.logger.InfoContext(ctx, "job proxy got the upstream credential", slog.String("session", c.session.ID))
	h.writeJSON(ctx, w, http.StatusOK, protocol.UpstreamCredentialResponse{
		Username: cred.Username, Password: cred.Password,
	})
}

// serverHost returns the host of the public URL of the server, for the tunnel
// rule of the job proxy.
func (h *Handler) serverHost() string {
	u, err := url.Parse(h.serverURL)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

// proxyAssignmentOf returns the wire form of a.
func proxyAssignmentOf(a *proxysessionsvc.Assignment) protocol.ProxyAssignment {
	rules := make([]protocol.ProxyRule, 0, len(a.Rules))

	for i := range a.Rules {
		r := &a.Rules[i]

		ops := make([]string, 0, len(r.Operations))
		for _, op := range r.Operations {
			ops = append(ops, string(op))
		}

		rules = append(rules, protocol.ProxyRule{
			Scope: r.Scope, ID: r.ID, Operations: ops, Host: r.Host, Port: r.Port, Mode: string(r.Mode),
			Methods: r.Methods, Paths: r.Paths, Credential: r.Credential, ClientCertificate: r.ClientCertificate,
			ServerTrust: r.ServerTrust,
		})
	}

	return protocol.ProxyAssignment{
		Namespace: a.Namespace.String(), Unit: a.Unit, Operation: string(a.Operation), Rules: rules,
		RulesDigest: a.RulesDigest, ServerHost: a.ServerHost, AllowedRanges: a.AllowedRanges,
		UpstreamURL: a.UpstreamURL,
	}
}

// recordWrongKind writes the denied entry of a session of one kind on a path
// of the other kind, in the namespace of the runner of the session. The proof
// of the session key verified, so the entry names the session. It returns
// nothing: the refusal stays when the append fails, and the failure is logged
// and counted.
func (h *Handler) recordWrongKind(r *http.Request, rt Route, sess *runner.Session) {
	if h.audit == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), handoffAuditTimeout)
	defer cancel()

	rn, err := h.members.Runner(ctx, sess.Runner)
	if err != nil {
		h.handoffAppendFailed(ctx, fmt.Errorf("select runner: %w", err))

		return
	}

	fence, err := h.fenceOf(r, rn.Namespace)
	if err != nil {
		h.handoffAppendFailed(ctx, err)

		return
	}

	e := h.entry(r, rn.Namespace, sess.ID, runner.AuditRules()[runner.EventHandoffRefused], h.now())
	e.Outcome = audit.OutcomeDenied
	e.Reason = protocol.ErrorWrongSessionKind

	// The kind is the stored kind of the session, which the store set from a
	// fixed value. A kind that is not known stands as a fixed placeholder.
	kind := string(sess.Kind)
	if _, parseErr := runner.ParseSessionKind(kind); parseErr != nil {
		kind = "(unknown)"
	}

	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(runner.EventHandoffRefused), Layer: "", TakesEffect: ""},
		{Key: changeReason, Old: nil, New: protocol.ErrorWrongSessionKind, Layer: "", TakesEffect: ""},
		{Key: "path", Old: nil, New: rt.Path, Layer: "", TakesEffect: ""},
		{Key: "kind", Old: nil, New: kind, Layer: "", TakesEffect: ""},
	}

	if _, err = h.audit.Append(ctx, fence, e); err != nil {
		h.handoffAppendFailed(ctx, fmt.Errorf("append entry: %w", err))
	}
}
