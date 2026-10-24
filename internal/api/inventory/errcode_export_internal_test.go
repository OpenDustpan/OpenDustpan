package inventory

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// WriteError writes the error answer of err as each route of the handler
// does, for the black-box tests of the error codes.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(ctx, w, err)
}

// WriteOutputsError writes the error answer of a read of the outputs of obj
// that failed with err, for the black-box tests of the error codes.
func WriteOutputsError(w http.ResponseWriter, r *http.Request, obj identity.Object, err error) {
	var (
		h Handler
		p identity.Principal
	)

	h.logger = slog.New(slog.DiscardHandler)
	h.writeOutputsError(w, r, p, obj, err)
}

// The request errors of the handler, for the black-box tests of the error
// codes.
var (
	ErrMissingOrganization = errMissingOrganization
	ErrMissingUnit         = errMissingUnit
	ErrOutputSharingBody   = errOutputSharingBody
	ErrCollectionBody      = errCollectionBody
	ErrMoveBody            = errMoveBody
)

// The errors of the deleted-unit and the lock routes, for the black-box tests
// of the error codes.
var (
	ErrInvalidQuery = errInvalidQuery
	ErrInvalidBody  = errInvalidBody
	ErrNoHistory    = errNoHistory
	ErrLockBody     = errLockBody
	ErrLockText     = errLockText
)

// The errors of the saved view routes, for the black-box tests of the error
// codes.
var (
	ErrViewBody           = errViewBody
	ErrViewIdempotencyKey = errViewIdempotencyKey
)
