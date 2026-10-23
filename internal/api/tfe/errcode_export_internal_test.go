package tfe

import (
	"log/slog"
	"net/http"
)

// WriteErrorForTest answers err as writeError does, through a handler that
// holds only a logger that discards. Only tests reach it.
func WriteErrorForTest(w http.ResponseWriter, r *http.Request, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(r.Context(), w, err)
}
