package runnerapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// WriteError answers err as a protocol route does, with logger.
func WriteError(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, err error) {
	var h Handler

	h.logger = logger
	h.writeError(ctx, w, err)
}

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

// OutputStepsOf returns the steps of one phase kind of a job in the wire
// form of the assignment, so that a test checks the outputs that a later
// phase needs.
func OutputStepsOf(steps []job.Step, kind job.PhaseKind, phases []job.Phase, current int) []protocol.Step {
	return stepsOf(steps, kind, phases, current)
}
