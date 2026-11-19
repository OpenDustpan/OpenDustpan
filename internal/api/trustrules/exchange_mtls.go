package trustrules

import (
	"context"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

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
	if err := h.mtlsGuard.Admit(clientip.String(r), r.ContentLength); err != nil {
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
