package runnerapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"opendustpan.org/opendustpan/pkg/job"
)

// ErrBadBody is the error of a body that is not the JSON object of its path.
var ErrBadBody = errBadBody

// WriteError answers err as a route of scope does, with logger.
func WriteError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, scope Scope, err error) {
	var h Handler

	h.logger = logger
	h.writeError(ctx, w, scope, err)
}

// WriteManagementError answers err as a route of ScopeAuthorized does, with a
// logger that discards every line.
func WriteManagementError(ctx context.Context, w http.ResponseWriter, err error) {
	WriteError(ctx, w, slog.New(slog.DiscardHandler), ScopeAuthorized, err)
}

// ErrNotFound is the not-found answer of the management API, for a test of its
// precedence.
var ErrNotFound = errNotFound

// JobPhaseWithoutReader calls the check that every step route makes first, on a
// handler that has the recorder and no job reader, which NewHandler never
// builds. It returns the error of the check.
func JobPhaseWithoutReader(ctx context.Context, recorder job.CheckedRecorder) error {
	h := new(Handler)
	h.recorder = recorder

	var c call

	_, err := h.jobPhaseOf(httptest.NewRequestWithContext(ctx, http.MethodPost, "/", http.NoBody), &c)

	return err
}
