package runnerapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"opendustpan.org/opendustpan/pkg/runner"
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

// JobPhaseOf calls the check that every step route makes first, on the
// handler h and the session sess. It returns the error of the check.
func JobPhaseOf(ctx context.Context, h *Handler, sess runner.Session) error {
	var c call

	c.session = sess

	_, err := h.jobPhaseOf(httptest.NewRequestWithContext(ctx, http.MethodPost, "/", http.NoBody), &c)

	return err
}

// JobPhaseWithoutReader is JobPhaseOf with the job reader of h cleared for the
// call and put back after it.
func JobPhaseWithoutReader(ctx context.Context, h *Handler, sess runner.Session) error {
	reader := h.jobs
	h.jobs = nil

	defer func() { h.jobs = reader }()

	return JobPhaseOf(ctx, h, sess)
}
