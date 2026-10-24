package profile

import (
	"context"
	"log/slog"
	"net/http"
)

// The refusals of the adapter, so the external tests check their codes
// without a request.
var (
	ErrFreshStepRequired    = errFreshStepRequired
	ErrPrecondition         = errPrecondition
	ErrPreconditionRequired = errPreconditionRequired
)

// StaleError returns stale(err).
func StaleError(err error) error { return stale(err) }

// ReauthenticateError returns the refusal of an old sign-in with the path
// that starts a new sign-in.
func ReauthenticateError(path string) error { return reauthenticateError{path: path} }

// WriteError writes the answer of err as a handler of the API does, so the
// external tests check the code and the arguments of an error that no request
// of the test reaches.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}
