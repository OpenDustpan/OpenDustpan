// Package problem writes the error answers of the tool-neutral APIs. It
// owns the error body, the classes of error that every tool-neutral API
// shares (authentication, a fixed answer, the Idempotency-Key, the
// namespace and maintenance), the headers of an error answer and the log
// rules: a 500 logs its status only, and a fenced write logs its wrap.
//
// Each API gives its own classifier for the errors of its domain. The
// refusals of the credential of a request (a rate limit, no sign-in, a CSRF
// or other-site session request) are answered first, so no classifier can
// answer them in other words. The classifier runs next, before the other
// shared classes, so an API can answer a shared error in its own words, for
// example a not-found that never tells that an object exists. Writer.Problem
// holds this order.
package problem

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Problem is the answer to an error: a status, a stable machine code and a
// message that is safe to show callers.
type Problem struct {
	Status int
	// Code is the stable machine code, or "" for an answer that has none.
	Code    string
	Message string
	// Args are the arguments of the code, or nil.
	Args map[string]any
	// RetryAfter is the wait that the answer names in the Retry-After header
	// and in the argument retryAfterSeconds, or 0 for none.
	RetryAfter time.Duration
	// Reauthenticate is the path that starts a new sign-in, for the refusal
	// of an old sign-in, or "".
	Reauthenticate string
}

// Body is the error body of the tool-neutral APIs. Code is the HTTP status;
// ErrorCode is the stable machine code.
type Body struct {
	Message        string         `json:"message"`
	Code           int            `json:"code"`
	ErrorCode      string         `json:"errorCode,omitempty"`
	ErrorArgs      map[string]any `json:"errorArgs,omitempty"`
	Reauthenticate string         `json:"reauthenticate,omitempty"`
}

// Error is an error with a fixed answer.
type Error struct {
	Problem Problem
}

// Error returns the message of the answer.
func (e *Error) Error() string { return e.Problem.Message }

// Fixed returns an error that answers status with code and message.
func Fixed(status int, code, message string) error {
	return &Error{Problem: answer(status, code, message)}
}

// BadRequest returns an error that answers 400 with code and message.
func BadRequest(code, message string) error {
	return Fixed(http.StatusBadRequest, code, message)
}

// Classifier returns the answer to an error of the domain of one API, and
// false for an error that it does not know.
type Classifier func(err error) (Problem, bool)

// Pass is the result of a classifier for an error that it does not know.
func Pass() (Problem, bool) {
	var none Problem

	return none, false
}

// Writer writes the error answers of one tool-neutral API.
type Writer struct {
	Logger *slog.Logger
	// Classify answers the errors of the domain of the API. It may be nil.
	Classify Classifier
}

// Write answers err. A 500 logs its status only, never the error text: the
// text may hold driver detail, a token or state. A fenced write logs its
// wrap, which names the write. Every error answer carries
// Cache-Control: no-store.
func (wr Writer) Write(ctx context.Context, w http.ResponseWriter, err error) {
	p := wr.Problem(err)
	if p.Status == http.StatusInternalServerError {
		wr.Logger.ErrorContext(ctx, "request failed", slog.Int("status", p.Status))
	}

	if errors.Is(err, namespace.ErrFenced) {
		wr.Logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	w.Header().Set("Cache-Control", "no-store")

	args := p.Args

	if p.RetryAfter > 0 {
		// The wait is whole seconds, rounded up, and at least 1.
		wait := max(1, int((p.RetryAfter+time.Second-1)/time.Second))
		w.Header().Set("Retry-After", strconv.Itoa(wait))

		args = withArg(args, "retryAfterSeconds", wait)
	}

	WriteJSON(ctx, wr.Logger, w, p.Status, Body{
		Message: p.Message, Code: p.Status, ErrorCode: p.Code, ErrorArgs: args, Reauthenticate: p.Reauthenticate,
	})
}

// Problem returns the answer to err. The order is part of the contract:
//
//  1. the refusals of the credential of a request (a rate limit, no
//     sign-in, a CSRF or other-site session request), so no classifier can
//     answer them in other words;
//  2. the classifier of the API, so an API can answer a refusal that wraps
//     identity.ErrDenied, or a shared error, in its own words;
//  3. the other shared classes.
//
// An error that none of them knows is a 500.
func (wr Writer) Problem(err error) Problem {
	if p, ok := credential(err); ok {
		return p
	}

	if wr.Classify != nil {
		if p, ok := wr.Classify(err); ok {
			return p
		}
	}

	if p, ok := Shared(err); ok {
		return p
	}

	return Internal()
}

// Internal returns the answer to an error that no class knows.
func Internal() Problem {
	return Problem{
		Status: http.StatusInternalServerError, Code: "", Message: "internal error",
		Args: nil, RetryAfter: 0, Reauthenticate: "",
	}
}

// Shared returns the answer to an error of a class that every tool-neutral
// API shares, and false for any other error.
func Shared(err error) (Problem, bool) {
	if p, ok := Auth(err); ok {
		return p, true
	}

	if fixed, ok := errors.AsType[*Error](err); ok {
		return fixed.Problem, true
	}

	if p, ok := Idempotency(err); ok {
		return p, true
	}

	return Namespace(err)
}

// Auth returns the answer to an error of the authentication or the
// authorization of a request.
func Auth(err error) (Problem, bool) {
	if p, ok := credential(err); ok {
		return p, true
	}

	if errors.Is(err, identity.ErrDenied) {
		return answer(http.StatusForbidden, errcode.Forbidden(err), "forbidden"), true
	}

	return Pass()
}

// credential returns the answer to a refusal of the credential of a request.
func credential(err error) (Problem, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return answer(http.StatusTooManyRequests, "auth.rate-limited",
			"too many failed sign-ins from this address: try again later"), true
	case errors.Is(err, identity.ErrUnauthenticated):
		return answer(http.StatusUnauthorized, "auth.unauthenticated", "unauthenticated"), true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return answer(http.StatusForbidden, errcode.Forbidden(err), err.Error()), true
	default:
		return Pass()
	}
}

// Idempotency returns the answer to an error of the Idempotency-Key of a
// request.
func Idempotency(err error) (Problem, bool) {
	if ie, ok := errors.AsType[*idempotent.Error](err); ok {
		return answer(ie.Status, ie.Code, ie.Message), true
	}

	if errors.Is(err, idempotency.ErrLost) {
		return answer(http.StatusConflict, idempotent.CodeInProgress,
			"a newer request took this Idempotency-Key before this request ended: retry later"), true
	}

	return Pass()
}

// Namespace returns the answer to an error of the namespace of a request or
// of the maintenance mode of the installation.
func Namespace(err error) (Problem, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return answer(http.StatusBadRequest, "namespace.invalid-name", err.Error()), true
	case errors.Is(err, namespace.ErrNotFound):
		return answer(http.StatusNotFound, "namespace.not-found", "namespace not found"), true
	case errors.Is(err, namespace.ErrFenced):
		return answer(http.StatusConflict, "namespace.fenced", namespace.ErrFenced.Error()), true
	case errors.Is(err, namespace.ErrNotHome):
		return answer(
			http.StatusMisdirectedRequest,
			"namespace.not-home",
			"namespace is not homed in this region",
		), true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return answer(http.StatusServiceUnavailable, "maintenance.in-maintenance",
			maintenance.ErrInMaintenance.Error()+": try again later"), true
	default:
		return Pass()
	}
}

// answer returns a Problem with no arguments.
func answer(status int, code, message string) Problem {
	return Problem{Status: status, Code: code, Message: message, Args: nil, RetryAfter: 0, Reauthenticate: ""}
}

// withArg returns a copy of args with key set to value.
func withArg(args map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(args)+1)
	maps.Copy(out, args)

	out[key] = value

	return out
}

// WriteJSON writes body as JSON with status. A failed write logs a warning:
// the status is already sent.
func WriteJSON(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
