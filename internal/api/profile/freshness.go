package profile

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/session"
)

// SessionPolicy gives the session policy of the installation. *session.Policies
// meets it.
type SessionPolicy interface {
	Policy(ctx context.Context) (session.Policy, error)
}

// errFreshStepRequired reports a request with no console session, for example
// one with an access token, on a reveal.
var errFreshStepRequired = errors.New(sessionauth.FreshStepRequiredMessage)

// errReauthenticate reports a console session whose sign-in is older than the
// token freshness window.
var errReauthenticate = errors.New("the sign-in of this console session is too old for this change")

// reauthenticateError is the refusal of an old sign-in. path starts a new
// sign-in.
type reauthenticateError struct {
	path string
}

func (reauthenticateError) Error() string { return errReauthenticate.Error() }

// Unwrap returns errReauthenticate.
func (reauthenticateError) Unwrap() error { return errReauthenticate }

// checkFresh returns nil when the request has a console session that signed
// in within the token freshness window. A request with an access token gets
// 403, and an old sign-in gets 401 with the path that starts a new sign-in.
// Any other error fails closed.
func (h *Handler) checkFresh(r *http.Request) error {
	ctx := r.Context()

	in, ok := sessionauth.SignInFrom(ctx)
	if !ok || !sessionauth.FromSession(ctx) {
		return errFreshStepRequired
	}

	window := session.DefaultTokenFreshness

	if h.policy != nil {
		policy, err := h.policy.Policy(ctx)
		if err != nil {
			return err
		}

		window = policy.TokenFreshness
	}

	// A sign-in after now has a negative age and is refused like an old one.
	if sessionauth.Fresh(h.sessions.Now(), in.At, window) {
		return nil
	}

	return reauthenticateError{path: sessionauth.SignInStartPrefix + url.PathEscape(in.Provider) + "?fresh=1"}
}
