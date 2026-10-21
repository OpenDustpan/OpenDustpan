// Package errcode holds the stable machine codes that the HTTP adapters add to
// an answer, and the one place that decides the code of a 403.
package errcode

import (
	"errors"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// The codes of a 403. A code is never removed and never changes its meaning.
const (
	// AccessDenied is a request that the caller has no permission for.
	AccessDenied = "access.denied"
	// AccessIPNotAllowed is a request from an address outside the IP
	// allowlist of the namespace.
	AccessIPNotAllowed = "access.ip-not-allowed"
	// GateLimited is a request that an access limit forbids.
	GateLimited = "gate.limited"
	// GateRuleFailed is a request that a gate or an access limit refused
	// because it failed to evaluate.
	GateRuleFailed = "gate.rule-failed"
	// SessionCSRF is a request with the session cookie and a missing or wrong
	// CSRF header.
	SessionCSRF = "session.csrf"
	// SessionOtherSite is a request with the session cookie from another site.
	SessionOtherSite = "session.other-site"
)

// errNoCode marks a refusal that wraps identity.ErrDenied but is not a missing
// permission.
var errNoCode = errors.New("refusal without a code")

type refusal struct{ reason string }

func (r refusal) Error() string { return identity.ErrDenied.Error() + ": " + r.reason }

func (refusal) Unwrap() []error { return []error{identity.ErrDenied, errNoCode} }

// Refusal returns an error that answers 403 like identity.ErrDenied, with the
// text "permission denied: " and reason, and that has no code. Use it for a
// refusal that is not a missing permission, such as a refused sign-in: a person
// who gets access.denied needs a grant, and this person does not. Every other
// error that wraps identity.ErrDenied gets access.denied, so check each new one.
func Refusal(reason string) error { return refusal{reason: reason} }

// Forbidden returns the code of a 403 that err causes, or "" when err is none
// of these causes. The order is part of the contract. The session errors and
// the allowlist error wrap identity.ErrDenied, so they come before it, as in
// the classify functions of the adapters, which pick the message in the same
// order.
func Forbidden(err error) string {
	switch {
	case errors.Is(err, errNoCode):
		return ""
	case errors.Is(err, sessionauth.ErrCSRF):
		return SessionCSRF
	case errors.Is(err, sessionauth.ErrOtherSite):
		return SessionOtherSite
	case errors.Is(err, ipallow.ErrAddressNotAllowed):
		return AccessIPNotAllowed
	case errors.Is(err, gate.ErrLimited):
		// The limited authorizer wraps this refusal in identity.ErrDenied, so
		// it comes before the missing permission.
		return GateLimited
	case errors.Is(err, gate.ErrRuleFailed):
		return GateRuleFailed
	case errors.Is(err, identity.ErrDenied):
		return AccessDenied
	default:
		return ""
	}
}
