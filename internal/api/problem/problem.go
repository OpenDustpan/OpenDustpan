// Package problem writes the error answers of the tool-neutral APIs. It
// owns the error body, the classes of error that every tool-neutral API
// shares (authentication, a fixed answer, the Idempotency-Key, the
// namespace and maintenance), the headers of an error answer and the log
// rules: a 500 logs its status only, and a fenced write logs its wrap.
//
// Every answer has a code of the catalog. The package writes no code of its
// own text: a class names the constant of the catalog, and the writer makes
// the answer with errcode.New. A code or a value that the catalog refuses is a
// bug of the caller, and the answer is the 500 server.internal.
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
	"fmt"
	"log/slog"
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

// Problem is the answer to an error: a status, a code of the catalog with
// the values of its arguments, and a message that is safe to show callers.
type Problem struct {
	Status int
	// Code is the code of the catalog.
	Code string
	// Values are the arguments that the code declares, or nil.
	Values  []errcode.Value
	Message string
	// RetryAfter is the wait that the answer names in the Retry-After header,
	// or 0 for none. The code names the same wait in its own argument.
	RetryAfter time.Duration
	// Reauthenticate is the path that starts a new sign-in, for the refusal
	// of an old sign-in, or "".
	Reauthenticate string
	// answer is the finished code and arguments of an error that already
	// holds them, or the zero Answer.
	answer errcode.Answer
}

// Body is the error body of the tool-neutral APIs. Code is the HTTP status;
// ErrorCode is the stable machine code of the catalog.
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

// Fixed returns an error that answers status with code and message. The code
// takes no argument.
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
// wrap, which names the write. A code or a value that the catalog refuses
// logs the code and answers server.internal. Every error answer carries
// Cache-Control: no-store.
func (wr Writer) Write(ctx context.Context, w http.ResponseWriter, err error) {
	p := wr.Problem(err)

	var (
		made    errcode.Answer
		codeErr error
	)

	made, codeErr = p.made()
	if codeErr != nil {
		// A code or a value that the catalog refuses is a bug of the adapter.
		// The log names the code only.
		wr.Logger.ErrorContext(ctx, "error code refused", slog.String("error_code", p.Code))

		p = Internal()
		made = errcode.Internal()
	}

	if p.Status == http.StatusInternalServerError {
		wr.Logger.ErrorContext(ctx, "request failed", slog.Int("status", p.Status))
	}

	if errors.Is(err, namespace.ErrFenced) {
		wr.Logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	w.Header().Set("Cache-Control", "no-store")

	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(RetryAfterSeconds(p.RetryAfter), 10))
	}

	WriteJSON(ctx, wr.Logger, w, p.Status, Body{
		Message: p.Message, Code: p.Status, ErrorCode: made.Code(), ErrorArgs: made.Args(),
		Reauthenticate: p.Reauthenticate,
	})
}

// RetryAfterSeconds returns wait in whole seconds, rounded up, and at least
// 1.
func RetryAfterSeconds(wait time.Duration) int64 {
	return int64(max(1, (wait+time.Second-1)/time.Second))
}

// made returns the code and the arguments of p.
func (p Problem) made() (errcode.Answer, error) {
	if p.answer.Code() != "" {
		return p.answer, nil
	}

	made, err := errcode.New(p.Code, p.Values...)
	if err != nil {
		return made, fmt.Errorf("make the answer of %q: %w", p.Code, err)
	}

	return made, nil
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
	return answer(http.StatusInternalServerError, errcode.ServerInternal, "internal error")
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
		return answer(http.StatusTooManyRequests, errcode.AuthRateLimited,
			"too many failed sign-ins from this address: try again later"), true
	case errors.Is(err, identity.ErrUnauthenticated):
		return answer(http.StatusUnauthorized, errcode.AuthUnauthenticated, "unauthenticated"), true
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return answer(http.StatusForbidden, errcode.Forbidden(err), err.Error()), true
	default:
		return Pass()
	}
}

// Idempotency returns the answer to an error of the Idempotency-Key of a
// request.
func Idempotency(err error) (Problem, bool) {
	if errors.Is(err, idempotency.ErrLost) {
		err = idempotent.Lost()
	}

	if ie, ok := errors.AsType[*idempotent.Error](err); ok {
		p := answer(ie.Status, ie.Answer.Code(), ie.Message)
		p.answer = ie.Answer

		return p, true
	}

	return Pass()
}

// Namespace returns the answer to an error of the namespace of a request or
// of the maintenance mode of the installation.
func Namespace(err error) (Problem, bool) {
	switch {
	case errors.Is(err, namespace.ErrInvalidName):
		return answer(http.StatusBadRequest, errcode.NamespaceInvalidName, err.Error()), true
	case errors.Is(err, namespace.ErrNotFound):
		return answer(http.StatusNotFound, errcode.NamespaceNotFound, "namespace not found"), true
	case errors.Is(err, namespace.ErrFenced):
		return answer(http.StatusConflict, errcode.NamespaceFenced, namespace.ErrFenced.Error()), true
	case errors.Is(err, namespace.ErrNotHome):
		return answer(
			http.StatusMisdirectedRequest,
			errcode.NamespaceNotHome,
			"namespace is not homed in this region",
		), true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return answer(http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance,
			maintenance.ErrInMaintenance.Error()+": try again later"), true
	default:
		return Pass()
	}
}

// answer returns a Problem whose code takes no argument.
func answer(status int, code, message string) Problem {
	return Problem{
		Status: status, Code: code, Values: nil, Message: message, RetryAfter: 0, Reauthenticate: "",
		answer: errcode.Answer{},
	}
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
