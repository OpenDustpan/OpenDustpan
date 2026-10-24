package audit //nolint:testpackage // The route wrapper is unexported: a test gives it a row that the route table never holds.

import (
	"context"
	"log/slog"
	"net/http"
)

// The refusals of the adapter, so the external tests check their codes
// without a request.
var (
	ErrMissingOrganization = errMissingOrganization
	ErrInvalidPage         = errInvalidPage
	ErrInvalidSequence     = errInvalidSequence
	ErrNoEntryReader       = errNoEntryReader
)

// ServeRoute returns the enforcement wrapper of route.
func (h *Handler) ServeRoute(route Route) http.HandlerFunc { return h.enforce(route) }

// WriteError writes the answer of err as a handler of the API does, so the
// external tests check the code of an error that no request of the test
// reaches.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}
