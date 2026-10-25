package runnerapi

import (
	"context"
	"log/slog"
	"net/http"
)

// ErrBadBody is the error of a body that is not the JSON object of its path.
var ErrBadBody = errBadBody

// WriteManagementError answers err as a handler answers a request of the
// management API, with a logger that discards every line.
func WriteManagementError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, ScopeAuthorized, err)
}

// ErrNotFound is the not-found answer of the management API, for a test of its
// precedence.
var ErrNotFound = errNotFound
