package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

const (
	// unverifiedSubject is the subject of the entry of a refused handoff token
	// when no runner proved its key: a launcher has no identity before its
	// session opens.
	unverifiedSubject = "unverified-launcher"
	// handoffAuditTimeout bounds the append of the entry of one refusal. The
	// append runs after the request context is done too, so a client that
	// closes the connection cannot make an entry get lost.
	handoffAuditTimeout = 5 * time.Second
	// handoffKeyKind starts the Kind of the limiter key of a handoff refusal.
	handoffKeyKind = "runner:handoff:"
)

// recordUnverifiedHandoffRefusal writes the entry of a refused exchange of a
// handoff token, when err is a refusal of a token or a claim. The request body
// has no runner ID and names no runner before the session opens, so the entry
// holds no runner ID and lands in the installation scope.
func (h *Handler) recordUnverifiedHandoffRefusal(ctx context.Context, r *http.Request, err error) {
	ref, ok := runner.NewHandoffRefusal(err)
	if !ok {
		return
	}

	h.recordHandoffRefusal(ctx, r, &ref, "")
}

// recordHandoffRefusal writes the audit entry of ref. It runs after the refused
// store call, outside its transaction, which rolled back. It returns nothing:
// the refusal stays a refusal when the append fails (fail closed), and the
// failure is logged and counted. The entry never holds the token, its hash or
// the text of an error.
//
// ns is the namespace of a verified runner. It is empty for an unverified
// request, whose entry lands in the installation scope.
func (h *Handler) recordHandoffRefusal(
	ctx context.Context, r *http.Request, ref *runner.HandoffRefusal, ns namespace.Name,
) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handoffAuditTimeout)
	defer cancel()

	// Any caller can reach the exchange route with no credential, so the
	// entries need a bound. The key holds the client address, the reason and
	// the scope. The limiter bounds its own number of keys.
	key := denylimit.Key{
		Principal: refusal.Clean(clientip.String(r), maxSessionID),
		Action:    string(runner.AuditRules()[ref.Event()].Action),
		Kind:      handoffKeyKind + string(ref.Reason),
		Scope:     handoffScope(ns),
	}

	decision := h.limiter.Allow(key)
	if !decision.Write || decision.Overflow {
		// The refusal stays and no entry is written. The counter says how many
		// entries the limit dropped.
		if h.onHandoffSuppressed != nil {
			h.onHandoffSuppressed()
		}

		return
	}

	if appendErr := h.appendHandoffRefusal(ctx, r, ref, ns, decision.Suppressed); appendErr != nil {
		// Give the write back, so the next refusal of this key writes an entry.
		h.limiter.Release(key, decision)

		faultlog.LogFault(ctx, h.logger, "audit append failed for a refused handoff token", faultlog.Cause(appendErr),
			"runnerapi.handoff-refused")

		if h.onAuditErr != nil {
			h.onAuditErr()
		}
	}
}

// recordHandoffClaimRefusal writes the entry of a refused request for the
// handoff token of a claim, by a runner that the proof of its key
// authenticated. Only a lost claim is a refusal: another error (an unknown
// operation, an unavailable store) writes nothing.
func (h *Handler) recordHandoffClaimRefusal(ctx context.Context, r *http.Request, err error, rn *runner.Runner) {
	// Defense in depth: NewVerifiedHandoffRefusal classifies err again and
	// returns false for an error that is not a refusal. This pre-filter stays
	// so that a later change of that classifier cannot make an unavailable
	// store write a refusal entry. It does not rely on that classifier.
	if !errors.Is(err, runner.ErrClaimLost) {
		return
	}

	if ref, ok := runner.NewVerifiedHandoffRefusal(err, rn.ID); ok {
		h.recordHandoffRefusal(ctx, r, &ref, rn.Namespace)
	}
}

// appendHandoffRefusal appends the entry of ref in the namespace ns, or in the
// installation scope when ns is empty or does not exist.
func (h *Handler) appendHandoffRefusal(
	ctx context.Context, r *http.Request, ref *runner.HandoffRefusal, ns namespace.Name, suppressed int,
) error {
	// Fail closed to the installation scope: with no namespace reader, the
	// entry still lands somewhere an administrator reads.
	if ns != "" && h.namespaces != nil {
		n, err := h.namespaces.Get(ctx, ns)

		switch {
		case err == nil:
			if _, err = h.audit.Append(ctx, n.Fence(), h.handoffRefusalEntry(r, ref, ns, suppressed)); err != nil {
				return fmt.Errorf("append entry: %w", err)
			}

			return nil
		case !errors.Is(err, namespace.ErrNotFound):
			return fmt.Errorf("read namespace: %w", err)
		}
	}

	ss, ok := h.audit.(audit.ServerStore)
	if !ok {
		return errNoServerAudit
	}

	if _, err := ss.AppendServer(ctx, h.handoffRefusalEntry(r, ref, audit.ServerScope, suppressed)); err != nil {
		return fmt.Errorf("append installation entry: %w", err)
	}

	return nil
}

// errNoServerAudit reports an audit store that cannot write the installation
// scope.
var errNoServerAudit = errors.New("audit store has no installation scope")

// handoffScope returns the scope part of the limiter key of ns.
func handoffScope(ns namespace.Name) string {
	if ns == "" {
		return audit.ServerScope.String()
	}

	return ns.String()
}

// handoffRefusalEntry returns the entry of ref in the scope. Its outcome is
// denied and its reason is the reason code. suppressed is the number of earlier
// refusals of this key that no entry carries.
func (h *Handler) handoffRefusalEntry(
	r *http.Request, ref *runner.HandoffRefusal, scope namespace.Name, suppressed int,
) audit.Entry {
	rule := runner.AuditRules()[ref.Event()]

	// Defense in depth: runner.NewVerifiedHandoffRefusal checks the form of
	// the runner ID too. This allowlist check stays so that a refusal built
	// without that constructor, even with Verified set, cannot put a token or
	// other text in the subject or the changes. It does not rely on that
	// constructor. An ID that fails the check gives an empty runner and the
	// unverified subject.
	runnerID := ""
	if runner.ValidRunnerID(ref.Runner) {
		runnerID = ref.Runner
	}

	subject := unverifiedSubject
	if ref.Verified && runnerID != "" {
		subject = runnerID
	}

	e := h.entry(r, scope, subject, rule, h.now())
	e.Outcome = audit.OutcomeDenied
	e.Reason = string(ref.Reason)

	if scope == audit.ServerScope {
		e.Object = audit.ServerScope.String()
	} else {
		e.Object = identity.NamespaceObject(scope).String()
	}

	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(ref.Event()), Layer: "", TakesEffect: ""},
		{Key: "reason", Old: nil, New: string(ref.Reason), Layer: "", TakesEffect: ""},
		{Key: "runner", Old: nil, New: runnerID, Layer: "", TakesEffect: ""},
		{Key: "runnerVerified", Old: nil, New: strconv.FormatBool(ref.Verified), Layer: "", TakesEffect: ""},
	}

	if suppressed > 0 {
		e.Changes = append(e.Changes,
			audit.Change{Key: "suppressed", Old: nil, New: strconv.Itoa(suppressed), Layer: "", TakesEffect: ""})
	}

	return e
}
