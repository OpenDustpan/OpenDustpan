// Package faultlog writes the one log line of a failure of an HTTP adapter:
// the status, a cause from a closed list, the request ID, the route pattern
// and a few constant names. It takes no error and no free attribute, so the
// line can never hold an error text, a path or an input value. It has no
// dependency on the adapters, so every adapter package may import it.
package faultlog

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
)

// The cause classes of a 500. Each value is a fixed string that the code
// writes. A cause class never holds a part of an error text.
const (
	CauseDeadlineExceeded = "deadline_exceeded"
	CauseCanceled         = "canceled"
	CauseNetwork          = "network"
	CauseUnexpectedEOF    = "unexpected_eof"
	CauseFileMissing      = "file_missing"
	CauseFilePermission   = "file_permission"
	CauseUnclassified     = "unclassified"
)

// LogInternal logs one 500 at Error level with msg, the status, a cause class
// from the closed list of Cause, the request ID when it is safe, the route
// pattern of the request, and code, a code of the catalog, when it is not "".
// Every adapter that answers a 500 calls it, so every 500 log line has the
// same fields. msg must be a string literal at the call site, and a gate test
// checks that. The helper takes no free attribute and no error: the line can
// never hold an error text, a path or an input value.
func LogInternal(ctx context.Context, logger *slog.Logger, msg, cause, code string) {
	LogServer(ctx, logger, msg, Fields{Status: http.StatusInternalServerError, Cause: cause, Code: code, Op: ""})
}

// LogFault logs a failure that is not an answer of status 5xx, for example a
// lost audit entry or a broken invariant, with the fields of LogInternal and
// no status. op is a constant that names the operation, or "".
func LogFault(ctx context.Context, logger *slog.Logger, msg, cause, op string) {
	LogServer(ctx, logger, msg, Fields{Status: 0, Cause: cause, Code: "", Op: op})
}

// Fields are the fields of one failure log line. It holds no free text: every
// field is checked against a closed list or a fixed form before it is logged.
type Fields struct {
	// Status is the status of the answer, or 0 when the failure has no answer.
	Status int
	// Cause is a class from the closed list of Cause. Any other value, and an
	// empty one, logs CauseUnclassified.
	Cause string
	// Code is a code of the catalog. A value that is not a catalog-style code
	// is left out.
	Code string
	// Op is a constant that names the operation of a call site. It is never
	// input data. A value that is not a short code is left out.
	Op string
}

// LogServer logs msg and entry at Error level. msg must be a string literal at
// the call site, and a gate test checks that. It is the one place of internal/api
// that writes a failure log line, and a gate test checks that.
func LogServer(ctx context.Context, logger *slog.Logger, msg string, entry Fields) {
	if !logger.Enabled(ctx, slog.LevelError) {
		return
	}

	cause := entry.Cause
	if !knownCause(cause) {
		cause = CauseUnclassified
	}

	rec := slog.NewRecord(time.Now(), slog.LevelError, msg, 0)

	if entry.Status != 0 {
		rec.AddAttrs(slog.Int("status", entry.Status))
	}

	rec.AddAttrs(slog.String("cause", cause))

	// Defense in depth: the root middleware makes the request ID, and this
	// check does not rely on it. A value that is not safe is left out.
	if id, ok := safeRequestID(requestid.FromContext(ctx)); ok {
		rec.AddAttrs(slog.String("request_id", id))
	}

	// Defense in depth: a mux makes the route pattern from its own constants.
	// This check does not rely on it: a value with CR, LF or an input is left
	// out.
	if route := requestid.RouteFromContext(ctx); route != "" && routePattern.MatchString(route) {
		rec.AddAttrs(slog.String("route", route))
	}

	if entry.Op != "" && opName.MatchString(entry.Op) {
		rec.AddAttrs(slog.String("op", entry.Op))
	}

	if entry.Code != "" && opName.MatchString(entry.Code) {
		rec.AddAttrs(slog.String("error_code", entry.Code))
	}

	if err := logger.Handler().Handle(ctx, rec); err != nil {
		// The answer is already decided. The log names no text of the failure.
		logger.WarnContext(ctx, "error log line was not written")
	}
}

var (
	// routePattern is the form of a route pattern: an optional method, a space
	// and a path of letters, digits and the signs of a pattern.
	routePattern = regexp.MustCompile(`^[A-Z]{0,7} ?[A-Za-z0-9/{}._~:*=-]{1,200}$`)
	// opName is the form of an op and of a catalog code: lowercase letters,
	// digits, dot, underscore and hyphen.
	opName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// Cause returns the cause class of err: one value of the closed list of the
// Cause constants, never a part of err.Error(). The order is part of the
// contract, because context.DeadlineExceeded also implements net.Error. A nil
// error returns CauseUnclassified.
func Cause(err error) string {
	switch {
	case err == nil:
		return CauseUnclassified
	case errors.Is(err, context.DeadlineExceeded):
		return CauseDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return CauseCanceled
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return CauseUnexpectedEOF
	case errors.Is(err, fs.ErrNotExist):
		return CauseFileMissing
	case errors.Is(err, fs.ErrPermission):
		return CauseFilePermission
	}

	// Defense in depth: AsType returns a non-nil error when ok is true. The nil
	// check stays so that a future change to the match cannot return the class
	// for an empty match.
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr != nil {
		return CauseNetwork
	}

	return CauseUnclassified
}

// knownCause returns true when cause is one value of the closed list.
func knownCause(cause string) bool {
	switch cause {
	case CauseUnclassified, CauseDeadlineExceeded, CauseCanceled, CauseUnexpectedEOF,
		CauseFileMissing, CauseFilePermission, CauseNetwork:
		return true
	default:
		return false
	}
}

// safeRequestID returns id and true only when id is 1 to 64 lowercase hex
// bytes, the form that requestid.New makes. Otherwise it returns "" and false.
func safeRequestID(id string) (string, bool) {
	if id == "" || len(id) > 64 {
		return "", false
	}

	// Defense in depth: requestid.New makes hex only, but the context value may
	// come from a caller that is not the root middleware. This check stays so
	// that a value with CR, LF or a secret never reaches the log, and it does
	// not rely on the slog handler quoting.
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}

	return id, true
}
