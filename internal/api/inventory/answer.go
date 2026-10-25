package inventory

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
)

// writeFailure writes p in the error shape of the tool-neutral APIs.
func (h *Handler) writeFailure(ctx context.Context, w http.ResponseWriter, p problem.Problem) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: nil}.WriteProblem(ctx, w, p)
}
