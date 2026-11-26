package profile

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/session"
)

// SessionPolicy gives the session policy of the installation. *session.Policies
// meets it.
type SessionPolicy interface {
	Policy(ctx context.Context) (session.Policy, error)
}

// SetFreshPolicy sets the policy that tells whether the installation requires
// a fresh step. Call it before the handler serves a request. A nil policy
// requires the step.
func (h *Handler) SetFreshPolicy(p freshstep.Policy) {
	h.freshGuard = newFreshGuard(p, h.freshCheck)
}

// newFreshGuard returns the guard of p and check. A nil policy gives a policy
// that always requires the step (fail closed).
func newFreshGuard(p freshstep.Policy, check func(ctx context.Context, subject string) error) freshstep.Guard {
	if p == nil {
		p = freshstep.PolicyFunc(nil)
	}

	g, err := freshstep.NewGuard(p, check)
	if err != nil {
		// NewGuard fails only on a nil policy or a nil check. The zero Guard
		// refuses each request (fail closed).
		return freshstep.Guard{}
	}

	return g
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

// checkFresh returns nil when the policy requires no fresh step, or when the
// request has a console session that signed in within the token freshness
// window. A request with an access token gets 403, and an old sign-in gets 401
// with the path that starts a new sign-in. Any other error fails closed.
func (h *Handler) checkFresh(r *http.Request) error {
	// Defense in depth: the guard reads the setting here, at the point of use.
	// This check does not rely on the service or the store to ask for a fresh
	// step. A nil policy requires the step.
	return h.freshGuard.Require(r.Context(), "")
}

// freshCheck is the check of the fresh guard. It returns nil when the request
// has a console session that signed in within the token freshness window.
func (h *Handler) freshCheck(ctx context.Context, _ string) error {
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
