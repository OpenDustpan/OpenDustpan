// Package errcode holds the stable machine codes that the HTTP adapters add to
// an answer, and the one place that decides the code of a 403.
package errcode

import (
	"errors"

	"opendustpan.org/opendustpan/internal/sessionauth"
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
	// SessionCSRF is a request with the session cookie and a missing or wrong
	// CSRF header.
	SessionCSRF = "session.csrf"
	// SessionOtherSite is a request with the session cookie from another site.
	SessionOtherSite = "session.other-site"
)

// Forbidden returns the code of a 403 that err causes, or "" when err is none
// of these causes. The order is part of the contract. The session errors and
// the allowlist error wrap identity.ErrDenied, so they come before it, as in
// the classify functions of the adapters, which pick the message in the same
// order.
func Forbidden(err error) string {
	switch {
	case errors.Is(err, sessionauth.ErrCSRF):
		return SessionCSRF
	case errors.Is(err, sessionauth.ErrOtherSite):
		return SessionOtherSite
	case errors.Is(err, ipallow.ErrAddressNotAllowed):
		return AccessIPNotAllowed
	case errors.Is(err, identity.ErrDenied):
		return AccessDenied
	default:
		return ""
	}
}
