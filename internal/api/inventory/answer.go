package inventory

import (
	"context"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
)

// fail returns the answer of status and message with the catalog code and its
// values. When the catalog refuses the code or the values, the writer answers
// 500.
func fail(status int, message, code string, values ...errcode.Value) problem.Problem {
	return problem.Problem{
		Status: status, Code: code, Values: values, Message: message, RetryAfter: 0, Reauthenticate: "",
	}
}

// writeFailure writes p in the error shape of the tool-neutral APIs.
func (h *Handler) writeFailure(ctx context.Context, w http.ResponseWriter, p problem.Problem) {
	problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: nil}.WriteProblem(ctx, w, p)
}
