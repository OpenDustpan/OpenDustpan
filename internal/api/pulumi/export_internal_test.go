package pulumi

import (
	"log/slog"
	"net/http"
)

// The sentinels of the adapter that a black-box test passes to WriteError.
var (
	ErrInvalidBody       = errInvalidBody
	ErrContinuationToken = errContinuationToken
	ErrStackTagFilter    = errStackTagFilter
	ErrRevealPathTooLong = errRevealPathTooLong
	ErrSessionUpdate     = errSessionUpdate
	ErrSessionSecrets    = errSessionSecrets
)

// WriteError answers err with the error writer of a handler that holds only a
// logger that discards, so that a black-box test checks the answer of each
// error without a store.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	h := new(Handler)
	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(r.Context(), w, err)
}

// CheckGitHead exposes the commit allowlist of the create handler to the
// black-box test.
func CheckGitHead(raw string) string {
	return acceptedGitHead(raw)
}
