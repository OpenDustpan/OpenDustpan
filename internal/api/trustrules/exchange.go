package trustrules

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// ExchangePath is the route of the token exchange of a namespace. It needs
// no sign-in: the token of the workload is the credential.
const ExchangePath = "/api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange"

// maxExchangeBytes bounds the body of an exchange: a token of at most
// trustrule.MaxTokenBytes and a rule ID.
const maxExchangeBytes = trustrule.MaxTokenBytes + 1024

// exchangeRequest is the body of an exchange. Rule is optional: it names the
// rule when more than one rule accepts the token.
type exchangeRequest struct {
	Token *string `dustpan:"required" json:"token"`
	Rule  *string `dustpan:"optional" json:"rule"`
}

// exchangeDocument is the answer of an exchange. Token is the secret of the
// access token: the server shows it once.
//
// Contract: never remove or rename these fields.
type exchangeDocument struct {
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"token"`
	TokenID   string    `json:"id"`
	Principal string    `json:"principal"`
	Rule      string    `json:"rule"`
}

// exchange serves POST on ExchangePath. It answers 201 with the access token.
// A token that no rule accepts answers 401 with trust-rule.no-match, and the
// answer never names the check that failed: the audit entry of the namespace
// does. A token that two rules accept, with no rule in the request, answers
// 409 with trust-rule.ambiguous.
func (h *Handler) exchange(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		h.writeError(ctx, w, fmt.Errorf("namespace in the path: %w", err))

		return
	}

	// A call with an empty body is an mTLS call. A call with a body is the
	// exchange of a token.
	if r.ContentLength == 0 {
		h.exchangeMtls(w, r, name)

		return
	}

	var in exchangeRequest
	if decodeErr := route.DecodeBody(w, r, maxExchangeBytes, &in); decodeErr != nil {
		h.writeError(ctx, w, fmt.Errorf("exchange body: %w", decodeErr))

		return
	}

	if in.Token == nil {
		h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "the body must hold token"))

		return
	}

	rule := trustrule.ID("")
	if in.Rule != nil {
		rule = trustrule.ID(*in.Rule)

		// The rule ID goes to the audit log: refuse each value that is not a
		// rule ID before any use.
		if ruleErr := rule.Validate(); ruleErr != nil {
			h.writeError(ctx, w, problem.BadRequest(errcode.RequestBodyInvalid, "rule is not a trust rule ID"))

			return
		}
	}

	res, err := h.exchanger.Exchange(ctx, trustrulesvc.ExchangeInput{
		Namespace: name, Token: *in.Token, Rule: rule, Request: h.requestEntry(r),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The answer holds a secret: no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	problem.WriteJSON(ctx, h.logger, w, http.StatusCreated, exchangeDocument{
		ExpiresAt: res.Token.ExpiresAt.UTC(), Token: res.Secret.Reveal(), TokenID: res.Token.ID,
		Principal: res.Token.PrincipalID, Rule: string(res.Rule.ID),
	})
}

// requestEntry returns the template of the audit entries of an exchange:
// where the request came from. The service fills the rest.
func (h *Handler) requestEntry(r *http.Request) audit.Entry {
	return audit.Entry{
		At: h.now().UTC(), Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")), Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: requestid.FromContext(r.Context()),
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: "",
		Origin: nil,
	}
}

// writeMtlsRefusal writes the denial entry of the first refusal of the guard.
// When the write fails, it gives the refusal back to the guard, so the next
// refusal of the window tries the write again: a failure must not hide the
// refusals that follow. A namespace that does not exist has no audit log: the
// guard frees its slot, so it does not take the slot of a real namespace.
func (h *Handler) writeMtlsRefusal(
	ctx context.Context, r *http.Request, name namespace.Name, refusal trustrulesvc.Refusal, cause error,
) {
	unwritten, err := h.mtls.RefuseCall(ctx, name, h.requestEntry(r), cause, refusal.Held)
	if err == nil {
		h.mtlsGuard.GiveBack(unwritten)

		return
	}

	if errors.Is(err, trustrulesvc.ErrBundleUnknown) {
		h.mtlsGuard.Forget(refusal)

		return
	}

	if errors.Is(err, trustrulesvc.ErrNamespaceNotHome) {
		// Another installation owns the namespace. A retry cannot write the
		// entry here, so the slot stays marked and counts the later refusals.
		h.mtlsGuard.Keep(refusal)

		return
	}

	h.mtlsGuard.Rearm(refusal)

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}

	h.logger.WarnContext(ctx, "the denial entry of a refused mTLS call failed",
		slog.String("namespace", string(name)), slog.String("error", err.Error()))
}

// countMtlsRefused counts a call that the guard refused, by its reason.
func (h *Handler) countMtlsRefused(cause error) {
	if h.onMtlsRefused == nil {
		return
	}

	if errors.Is(cause, trustrulesvc.ErrBodyTooLarge) {
		h.onMtlsRefused("body-size")

		return
	}

	h.onMtlsRefused("rate-limit")
}

// exchangeMtls serves an exchange with an empty body: the client
// certificate of the TLS handshake is the credential. The chain comes only
// from the TLS state of the request. A header never gives a chain: a header
// is a value that any caller can set.
func (h *Handler) exchangeMtls(w http.ResponseWriter, r *http.Request, name namespace.Name) {
	ctx := r.Context()

	if h.mtls == nil || h.mtlsGuard == nil {
		h.writeMtlsError(ctx, w, trustrulesvc.ErrNoCertificate)

		return
	}

	// Each call counts, also one with no certificate: it is the call that
	// costs the server no work, and the one that a flood sends.
	if refusal, err := h.mtlsGuard.Admit(clientip.String(r), string(name), r.ContentLength); err != nil {
		h.countMtlsRefused(err)

		// The first refusal of a window and namespace writes a denial entry
		// through the limiter of the exchanger. The guard counts the later
		// ones in memory, so a flood costs no store read for each call. The
		// answer is always the cause of the guard: a failure of the entry
		// does not change it.
		if refusal.First {
			h.writeMtlsRefusal(ctx, r, name, refusal, err)
		}

		h.writeMtlsError(ctx, w, err)

		return
	}

	var raw [][]byte

	if r.TLS != nil {
		for _, c := range r.TLS.PeerCertificates {
			raw = append(raw, c.Raw)
		}
	}

	chain, err := h.mtls.Verify(ctx, &trustrulesvc.MtlsVerifyInput{
		Namespace: name, RawChain: raw, Request: h.requestEntry(r),
	})
	if err != nil {
		h.writeMtlsError(ctx, w, err)

		return
	}

	res, err := h.mtls.Exchange(ctx, &trustrulesvc.MtlsExchangeInput{
		Namespace: name, Chain: chain, Request: h.requestEntry(r),
	})
	if err != nil {
		h.writeMtlsError(ctx, w, err)

		return
	}

	// The answer holds a secret: no cache may keep it.
	w.Header().Set("Cache-Control", "no-store")
	problem.WriteJSON(ctx, h.logger, w, http.StatusCreated, exchangeDocument{
		ExpiresAt: res.Token.ExpiresAt.UTC(), Token: res.Secret.Reveal(), TokenID: res.Token.ID,
		Principal: res.Token.PrincipalID, Rule: string(res.Rule.ID),
	})
}

// writeMtlsError writes the answer of a refused mTLS exchange. It uses its own
// classifier, so the answers of the token exchange stay as they are.
func (h *Handler) writeMtlsError(ctx context.Context, w http.ResponseWriter, err error) {
	problem.Writer{Logger: h.logger, KeepDenied: keepDenied, Classify: classifyMtls}.Write(ctx, w, err)
}

// classifyMtls maps the refusals of the mTLS exchange. Every answer to a
// refused certificate is 401, and the answer never names more than the code.
// An error that is not a refusal of the exchange fails closed with 500.
func classifyMtls(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, trustrulesvc.ErrRateLimited):
		return problem.New(http.StatusTooManyRequests, errcode.MTLSRateLimited,
			trustrulesvc.ErrRateLimited.Error()), true
	case errors.Is(err, trustrulesvc.ErrBodyTooLarge):
		return problem.New(http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge,
			trustrulesvc.ErrBodyTooLarge.Error()), true
	case errors.Is(err, trustrulesvc.ErrIssueFailed):
		return problem.New(http.StatusInternalServerError, errcode.TokenIssueFailed,
			trustrulesvc.ErrIssueFailed.Error()), true
	}

	return classifyMtlsCertificate(err)
}

// classifyMtlsCertificate maps the refusals of a certificate and of its rule.
func classifyMtlsCertificate(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, trustrulesvc.ErrNoCertificate):
		return problem.New(http.StatusUnauthorized, errcode.MTLSNoClientCertificate,
			trustrulesvc.ErrNoCertificate.Error()), true
	case errors.Is(err, trustrulesvc.ErrUnknownIssuer), errors.Is(err, trustrulesvc.ErrInvalid):
		return problem.New(http.StatusUnauthorized, errcode.MTLSUnknownIssuer,
			trustrulesvc.ErrUnknownIssuer.Error()), true
	case errors.Is(err, trustrulesvc.ErrExpired):
		return problem.New(http.StatusUnauthorized, errcode.MTLSExpired, trustrulesvc.ErrExpired.Error()), true
	case errors.Is(err, trustrulesvc.ErrNotYetValid):
		return problem.New(http.StatusUnauthorized, errcode.MTLSNotYetValid,
			trustrulesvc.ErrNotYetValid.Error()), true
	case errors.Is(err, trustrulesvc.ErrRevoked):
		return problem.New(http.StatusUnauthorized, errcode.MTLSRevoked, trustrulesvc.ErrRevoked.Error()), true
	case errors.Is(err, trustrulesvc.ErrRevocationUnknown):
		return problem.New(http.StatusUnauthorized, errcode.MTLSRevocationUnknown,
			trustrulesvc.ErrRevocationUnknown.Error()), true
	}

	return classifyMtlsRule(err)
}

// classifyMtlsRule maps the refusals of the rule, the bundle and the
// principal.
func classifyMtlsRule(err error) (problem.Problem, bool) {
	switch {
	// ErrBundleUnknown first: the service wraps trustrule.ErrNotFound in it.
	case errors.Is(err, trustrulesvc.ErrBundleUnknown):
		return problem.New(http.StatusUnauthorized, errcode.MTLSBundleDisabled,
			trustrulesvc.ErrBundleUnknown.Error()), true
	case errors.Is(err, trustrulesvc.ErrNoMatchingRule), errors.Is(err, trustrule.ErrNotFound):
		return problem.New(http.StatusUnauthorized, errcode.MTLSNoMatchingRule,
			trustrulesvc.ErrNoMatchingRule.Error()), true
	case errors.Is(err, trustrulesvc.ErrAmbiguousRule):
		return problem.New(http.StatusUnauthorized, errcode.MTLSAmbiguousRule,
			trustrulesvc.ErrAmbiguousRule.Error()), true
	case errors.Is(err, trustrule.ErrPrincipal):
		return problem.New(http.StatusUnauthorized, errcode.MTLSPrincipalDisabled,
			"the principal of the trust rule cannot get a token"), true
	}

	return problem.Pass()
}
