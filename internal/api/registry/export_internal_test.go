package registry

import (
	"context"
	"log/slog"
	"net/http"
)

// WriteError writes the answer of err as a handler of the API does, so the
// external tests check the code and the arguments of an error that no request
// of the test reaches.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}
