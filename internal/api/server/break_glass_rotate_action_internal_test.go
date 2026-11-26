package server

import (
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The helpers of this file serve the black-box tests of the rotation handler.
// The tests call the handler without the route in front of it, so the checks of
// the handler are the only layer that they reach.

// NewBreakGlassRotateRefusalHandlerForTest returns a handler that has only an
// authorizer, an audit store and a clock. It has no principal service and no
// passkey service, so a handler check that is removed reads the account first
// and fails on the missing service.
func NewBreakGlassRotateRefusalHandlerForTest(
	authz identity.Authorizer, entries audit.ServerStore, now time.Time,
) *Handler {
	h := new(Handler)
	h.authz = authz
	h.audit = entries
	h.logger = slog.New(slog.DiscardHandler)
	h.now = func() time.Time { return now }

	return h
}

// WithoutAccessForTest returns a copy of h with no access service, as a server
// that wires none.
func WithoutAccessForTest(h *Handler) *Handler {
	c := *h
	c.access = nil

	return &c
}

// ServeBreakGlassRotateForTest calls the rotation handler of h with p as the
// caller, without the route and its checks.
func ServeBreakGlassRotateForTest(h *Handler, w http.ResponseWriter, r *http.Request, p identity.Principal) {
	h.rotateBreakGlass(w, r, p, identity.ServerObject())
}
