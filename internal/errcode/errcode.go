// Package errcode holds the catalog of the stable machine codes that the HTTP
// adapters add to an error answer, the arguments of each code, and the one
// place that decides the code of a 403.
package errcode

import (
	"errors"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/accesstoken"
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
	// SignInRefused is a sign-in that the server refuses for a reason that is
	// not a missing permission.
	SignInRefused = "sign-in.refused"
	// AccessNoAction is a request on a route that declares no authorization
	// action for its object. The server refuses it and never falls back to
	// another action.
	AccessNoAction = "access.no-action"
)

type refusal struct{ code, reason string }

func (r refusal) Error() string { return identity.ErrDenied.Error() + ": " + r.reason }

func (refusal) Unwrap() error { return identity.ErrDenied }

// Refusal returns an error that answers 403 like identity.ErrDenied, with the
// text "permission denied: " and reason, and with code in place of
// access.denied. Use it for a refusal that is not a missing permission, such
// as a refused sign-in: a person who gets access.denied needs a grant, and
// this person does not. code is a code of the catalog, for example
// SignInRefused. A code that is not in the catalog is ignored, and Forbidden
// gives the error the code of its other causes, at least access.denied. Every
// other error that wraps identity.ErrDenied gets access.denied, so check each
// new one.
func Refusal(code, reason string) error { return refusal{code: code, reason: reason} }

// Forbidden returns the code of a 403 that err causes, or "" when err is none
// of these causes. It never returns "" for an error that wraps
// identity.ErrDenied. The order is part of the contract. A refusal of Refusal
// comes first, because it names its own code. The session errors and the
// allowlist error wrap identity.ErrDenied, so they come before it, as in the
// classify functions of the adapters, which pick the message in the same
// order. A refusal whose code is not in the catalog counts as no refusal.
func Forbidden(err error) string {
	// Defense in depth: Refusal stores any string, and the review of each
	// caller does not stop a typo. A code that is not in the catalog gets the
	// other causes, so that a denial never answers "" or an unknown code.
	if r, ok := errors.AsType[refusal](err); ok {
		if _, known := Lookup(r.code); known {
			return r.code
		}
	}

	switch {
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
	case errors.Is(err, accesstoken.ErrReachExceeded):
		// The token service wraps this refusal in identity.ErrDenied, so it
		// comes before the missing permission.
		return TokenReachExceeded
	case errors.Is(err, identity.ErrDenied):
		return AccessDenied
	default:
		return ""
	}
}

// Internal returns the answer of ServerInternal. It cannot fail, so a caller
// that has no better answer after New failed uses it.
func Internal() Answer { return Answer{code: ServerInternal, args: nil} }
