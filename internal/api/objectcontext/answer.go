package objectcontext

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
)

// failure is the answer to an error: the HTTP status, the message that is
// safe to show callers, and the code of the catalog with its arguments. code
// is the code that the classify function asked for. err is the error of
// errcode.New, or nil.
type failure struct {
	status  int
	message string
	code    string
	answer  errcode.Answer
	err     error
}

// fail returns the failure of status and message with the catalog code and
// its values. When errcode.New refuses the code or the values, the writer
// answers 500.
func fail(status int, message, code string, values ...errcode.Value) failure {
	a, err := errcode.New(code, values...)

	return failure{status: status, message: message, code: code, answer: a, err: err}
}

// writeFailure writes f in the error shape of this API. A failure whose code
// errcode.New refused answers 500 with server.internal: the refusal is a bug
// of this adapter, and the log names the code.
func (h *Handler) writeFailure(ctx context.Context, w http.ResponseWriter, f failure) {
	if f.err != nil {
		h.logger.ErrorContext(ctx, "error answer has no valid code", slog.String("error_code", f.code))

		f = failure{
			status: http.StatusInternalServerError, message: "internal error", code: errcode.ServerInternal,
			answer: errcode.Internal(), err: nil,
		}
	}

	if f.status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state.
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", f.status))
	}

	h.writeAnswer(ctx, w, f.status, f.message, f.answer)
}

// writeAnswer writes the error body of status and message with the code and
// the arguments of a.
func (h *Handler) writeAnswer(
	ctx context.Context, w http.ResponseWriter, status int, message string, a errcode.Answer,
) {
	h.writeJSON(ctx, w, status, errorResponse{
		Code: status, Message: message, ErrorCode: a.Code(), ErrorArgs: a.Args(),
	})
}
