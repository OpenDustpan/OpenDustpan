package tfe

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/session"
)

// SessionPolicy gives the session policy of the installation. *session.Policies
// meets it.
type SessionPolicy interface {
	Policy(ctx context.Context) (session.Policy, error)
}

// errFreshStepRequired reports a request with no console session, for example
// one with an access token, on a route that needs a fresh authentication.
var errFreshStepRequired = errors.New(sessionauth.FreshStepRequiredMessage)

// errReauthenticate reports a console session whose sign-in is older than the
// token freshness window.
var errReauthenticate = errors.New("the sign-in of this console session is too old for this change")

// checkFresh returns nil when the request has a console session that signed
// in within the token freshness window. A request with an access token gets
// errFreshStepRequired, and an old sign-in gets errReauthenticate. It also
// returns the path that starts a new sign-in. Any other error fails closed.
func (h *Handler) checkFresh(r *http.Request) (string, error) {
	ctx := r.Context()

	in, ok := sessionauth.SignInFrom(ctx)
	if !ok || !sessionauth.FromSession(ctx) {
		return "", errFreshStepRequired
	}

	window := session.DefaultTokenFreshness

	if h.sessionPolicy != nil {
		policy, err := h.sessionPolicy.Policy(ctx)
		if err != nil {
			return "", err
		}

		window = policy.TokenFreshness
	}

	// A sign-in after now has a negative age and is refused like an old one.
	if sessionauth.Fresh(h.sessions.Now(), in.At, window) {
		return "", nil
	}

	return sessionauth.SignInStartPrefix + url.PathEscape(in.Provider) + "?fresh=1", errReauthenticate
}

// freshReveal wraps a route that needs a fresh authentication: an access token
// gets 403, and a console session with an old sign-in gets 401. Each refusal
// writes one failure entry of the kind reveal, best effort, and answers
// Cache-Control: no-store.
func (h *Handler) freshReveal(serve RouteHandler) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		// Defense in depth: the handler behind this wrapper does not check the
		// session itself, and the policy check does not know the sign-in time.
		path, err := h.checkFresh(r)
		if err == nil {
			serve(w, r, p, obj)

			return
		}

		ctx := r.Context()

		w.Header().Set("Cache-Control", noStoreValue)

		h.appendRevealEntry(ctx, revealAuditEntry{
			request: r, principal: p, object: obj, version: r.PathValue("state_version_id"), path: "",
			outcome: audit.OutcomeFailure,
		})

		switch {
		case errors.Is(err, errFreshStepRequired):
			h.writeCoded(ctx, w, errorClass{
				status: http.StatusForbidden, title: titleForbidden, detail: err.Error(),
				code: errcode.MFAFreshStepRequired, args: nil,
			}, "")
		case errors.Is(err, errReauthenticate):
			// meta.reauthenticate holds the path that starts a new sign-in.
			h.writeCoded(ctx, w, errorClass{
				status: http.StatusUnauthorized, title: titleUnauthorized, detail: err.Error(),
				code: errcode.MFAFreshStepRequired, args: nil,
			}, path)
		default:
			h.writeNoStore(ctx, w, err)
		}
	}
}
