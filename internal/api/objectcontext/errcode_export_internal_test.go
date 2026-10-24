package objectcontext

import (
	"context"
	"log/slog"
	"net/http"
)

// WriteError writes the error answer of err as each route of the handler
// does, for the black-box tests of the error codes.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}

// The request errors of the handler, for the black-box tests of the error
// codes.
var (
	ErrNotFound = errNotFound
	ErrBody     = errBody
	ErrInclude  = errInclude
)
