package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/errcode"
)

// failure is the answer of an error: the HTTP status, a message that is safe
// to show the caller, and the code of the catalog with its arguments. A
// failure with an empty code answers without errorCode. Each classify
// function returns a failure, and writeError writes it.
type failure struct {
	status  int
	message string
	code    string
	args    []errcode.Value
}

// fail returns the failure of status and message with no code. A classify
// function returns fail(0, "") with false for an error that it does not map.
func fail(status int, message string) failure {
	return failure{status: status, message: message, code: "", args: nil}
}

// failCoded returns the failure of status and message with code, a code of
// the catalog, and the arguments that the code declares.
func failCoded(status int, message, code string, args ...errcode.Value) failure {
	return failure{status: status, message: message, code: code, args: args}
}

// answer returns the status, the message and the answer of f. When the
// catalog refuses the code or the arguments of f, it logs the code and
// returns 500 with server.internal: the adapter has a bug, not the request.
// The log names the code and the error of the catalog, which hold no value of
// an argument.
func (h *Handler) answer(ctx context.Context, f failure) (int, string, errcode.Answer) {
	a, err := errcode.New(f.code, f.args...)
	if err != nil {
		faultlog.LogInternal(ctx, h.logger, "error answer has no valid code", faultlog.CauseUnclassified, f.code)

		return http.StatusInternalServerError, "internal error", errcode.Internal()
	}

	return f.status, f.message, a
}

// writeAnswer writes the error body of status and message with the code and
// the arguments of a.
func (h *Handler) writeAnswer(
	ctx context.Context, w http.ResponseWriter, status int, message string, a errcode.Answer,
) {
	h.writeJSON(ctx, w, status, codedResponse{
		Message: message, Code: status, ErrorCode: a.Code(), ErrorArgs: a.Args(),
	})
}

// writeCoded writes the answer of f, which has a code of the catalog.
func (h *Handler) writeCoded(ctx context.Context, w http.ResponseWriter, f failure) {
	status, message, answer := h.answer(ctx, f)
	h.writeAnswer(ctx, w, status, message, answer)
}

// causeType returns the Go type of the first error in the chain of err that
// is not a plain wrap or a plain text error, for example *fs.PathError, or the
// type of err when the chain has no such error. A log line names it as the
// cause of a 500: the type holds no value, so it never holds driver text, a
// token or state.
func causeType(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		// A redacted error only hides the text of the error it wraps. The type it
		// reports is the wrapped one, so the log keeps the cause.
		if _, redacted := e.(interface{ Redacted() }); redacted {
			continue
		}

		switch t := fmt.Sprintf("%T", e); t {
		case "*fmt.wrapError", "*fmt.wrapErrors", "*errors.errorString":
		default:
			return t
		}
	}

	return fmt.Sprintf("%T", err)
}
