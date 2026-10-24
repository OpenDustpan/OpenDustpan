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

// The sentinels of the adapter, for the tests of their codes.
var (
	ErrInvalidBodyForTest       = errInvalidBody
	ErrInvalidQueryForTest      = errInvalidQuery
	ErrMissingFilterForTest     = errMissingFilter
	ErrRevealPathTooLongForTest = errRevealPathTooLong
)
