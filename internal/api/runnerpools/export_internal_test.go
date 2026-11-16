package runnerpools

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// CheckAddress runs the address check of h on r for p.
func CheckAddress(ctx context.Context, h *Handler, r *http.Request, p identity.Principal) error {
	return h.checkAddress(ctx, r, p)
}

// ErrBadBody is the error of a body that is not the JSON object of its path.
var ErrBadBody = errBadBody

// WriteError answers err as a route of scope does, with logger.
func WriteError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, scope Scope, err error) {
	var h Handler

	h.logger = logger
	h.writeError(ctx, w, scope, err)
}

// WriteManagementError answers err as a route of ScopeAuthorized does, with a
// logger that discards every line.
func WriteManagementError(ctx context.Context, w http.ResponseWriter, err error) {
	WriteError(ctx, w, slog.New(slog.DiscardHandler), ScopeAuthorized, err)
}

// ErrNotFound is the not-found answer of the management API, for a test of its
// precedence.
var ErrNotFound = errNotFound
