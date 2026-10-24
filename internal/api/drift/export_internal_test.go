package drift

import (
	"context"
	"log/slog"
	"net/http"
)

// WriteError answers err as a handler does, with a logger that discards
// every line.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}
