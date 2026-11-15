// Package problem writes the error answers of the tool-neutral APIs. It
// owns the error body, the classes of error that every tool-neutral API
// shares (authentication, a fixed answer, the Idempotency-Key, the
// namespace and maintenance), the headers of an error answer and the log
// rules: a 500 logs its status, a cause class from a closed list and the
// request ID, never the error text; a fenced write logs its wrap, and a
// refusal whose answer is not its error text logs its status and code at Debug,
// never the error text.
//
// Every answer has a code of the catalog. The package writes no code of its
// own text: a class names the constant of the catalog, and the writer makes
// the answer with errcode.New. A code or a value that the catalog refuses is a
// bug of the caller, and the answer is the 500 server.internal.
//
// Each API gives its own classifier for the errors of its domain. The
// refusals of the credential of a request (a rate limit, no sign-in, a CSRF
// or other-site session request) are answered first, so no classifier can
// answer them in other words. A denial is answered next: an error that wraps
// identity.ErrDenied is a 403, whatever else it wraps, so no classifier can
// tell the state of an object that the caller has no right on. An API lists
// the few classes that wrap a denial and keep their own code in
// Writer.KeepDenied. The classifier runs after the denial, before the other
// shared classes, so an API can answer a shared error in its own words, for
// example a not-found that never tells that an object exists. Writer.Problem
// holds this order, so a new classifier needs no case for a denial.
package problem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// Problem is the answer to an error: a status, a code of the catalog with
// the values of its arguments, and a message that is safe to show callers.
type Problem struct {
	Status int
	// Code is the code of the catalog.
	Code string
	// Values are the arguments that the code declares, or nil.
	Values []errcode.Value
	// denialArgs are the arguments of the answer of a denial that the catalog
	// does not declare: the action that the caller lacks and the kind of the
	// object. denied sets them for a denial that names the two, and the writer
	// adds them to the body of a code that declares no argument.
	denialArgs map[string]any
	Message    string
	// RetryAfter is the wait that the answer names in the Retry-After header,
	// or 0 for none. The code names the same wait in its own argument.
	RetryAfter time.Duration
	// Reauthenticate is the path that starts a new sign-in, for the refusal
	// of an old sign-in, or "".
	Reauthenticate string
	// Units are the units that the answer names, in order, or nil. A refusal
	// that lists the units of a loop sets them.
	Units []Unit
	// answer is the finished code and arguments of an error that already
	// holds them, or the zero Answer.
	answer errcode.Answer
	// cause is the safe cause class of a 500, from the closed list of Cause,
	// or "" for an answer that is not a 500.
	cause string
}

// Unit is a unit that an answer names: its ID, the label of its tool and its
// name.
type Unit struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

// Body is the error body of the tool-neutral APIs. Code is the HTTP status;
// ErrorCode is the stable machine code of the catalog.
type Body struct {
	Message        string         `json:"message"`
	Code           int            `json:"code"`
	ErrorCode      string         `json:"errorCode,omitempty"`
	ErrorArgs      map[string]any `json:"errorArgs,omitempty"`
	Reauthenticate string         `json:"reauthenticate,omitempty"`
	Units          []Unit         `json:"units,omitempty"`
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

// MaxMessageBytes is the longest refusal text that an answer repeats. Refused
// cuts the text of an error to this length. A refusal of a manual lock holds
// its removal route, so its cut uses refusal.MaxLockedTextBytes.
const MaxMessageBytes = 300

// New returns the answer of status, code and message, with the arguments that
// the code declares. The order of the arguments is the order of Fixed. A code
// or a value that the catalog refuses makes the writer answer server.internal.
func New(status int, code, message string, values ...errcode.Value) Problem {
	p := answer(status, code, message)
	p.Values = values

	return p
}

// Refused returns the answer to a refusal of a service: the text of err, cleaned
// and cut to MaxMessageBytes, with status and code. The code takes no argument.
func Refused(err error, status int, code string) Problem {
	return RefusedText(err.Error(), status, code)
}

// RefusedText returns the answer to a refusal whose text is text, cleaned and
// cut to MaxMessageBytes, with status and code. A caller that cuts the wrap
// chain of an error with Detail gives its text here.
func RefusedText(text string, status int, code string) Problem {
	return answer(status, code, refusal.Clean(text, MaxMessageBytes))
}

// Detail returns the text that err holds after sentinel: a service puts the
// sentinel before the detail, so the wrap prefix of the error never returns.
// It returns the text of sentinel when err holds no detail after it, so the
// answer never shows the wrap chain.
func Detail(err, sentinel error) string {
	marker := sentinel.Error() + ": "

	i := strings.Index(err.Error(), marker)
	if i < 0 {
		return sentinel.Error()
	}

	detail := err.Error()[i+len(marker):]
	if detail == "" {
		return sentinel.Error()
	}

	return detail
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
	// KeepDenied answers the classes of error that wrap identity.ErrDenied and
	// keep their own code. The writer calls it only for an error that wraps
	// identity.ErrDenied, so it cannot hide an error that is not a denial. Its
	// answer must have status 403, or 404 for a class that hides that an object
	// exists. For any other status the writer ignores the answer and answers the
	// standard 403 access.denied, so a denial never becomes a success or a
	// conflict. It may be nil.
	KeepDenied Classifier
	// Classify answers the errors of the domain of the API. It never sees an
	// error that wraps identity.ErrDenied: the writer answers a denial before
	// it calls Classify. It may be nil.
	Classify Classifier
}

// Write answers err. A fenced write logs its wrap, which names the write. A
// refusal whose answer is not the text of err logs its status and code only:
// never the text of err, which can wrap a secret, such as an authentication or
// session detail. Every error answer carries Cache-Control: no-store.
// WriteProblem holds the rest.
func (wr Writer) Write(ctx context.Context, w http.ResponseWriter, err error) {
	p := wr.Problem(err)
	if p.Status == http.StatusInternalServerError {
		p.cause = faultlog.Cause(err)
	}

	switch {
	case errors.Is(err, namespace.ErrFenced):
		wr.Logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	case p.Message != err.Error() && p.Status < http.StatusInternalServerError:
		// Defense in depth: the callers build an answer that holds no secret;
		// they do not rely on this line to hide a secret. The line names the
		// status and the code only.
		wr.Logger.DebugContext(ctx, "request refused",
			slog.Int("status", p.Status), slog.String("code", p.Code))
	default:
		// The answer holds the text of err, or the status is a 500: nothing to add.
	}

	wr.WriteProblem(ctx, w, p)
}

// WriteProblem writes the answer p, for a route that builds its own answer.
// A 500 logs its status, a cause class from the closed list of Cause and the
// request ID when it is safe. It never logs the error text: the text may hold
// driver detail, a token or state. A code or a value that the catalog refuses
// logs the code and answers server.internal.
func (wr Writer) WriteProblem(ctx context.Context, w http.ResponseWriter, p Problem) {
	var (
		made    errcode.Answer
		codeErr error
	)

	made, codeErr = p.made()
	if codeErr != nil {
		// A code or a value that the catalog refuses is a bug of the adapter.
		// The log names the code only.
		faultlog.LogInternal(ctx, wr.Logger, "error code refused", faultlog.CauseUnclassified, p.Code)

		p = Internal()
		made = errcode.Internal()
	}

	if p.Status == http.StatusInternalServerError {
		faultlog.LogInternal(ctx, wr.Logger, "request failed", p.cause, "")
	}

	w.Header().Set("Cache-Control", "no-store")

	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(RetryAfterSeconds(p.RetryAfter), 10))
	}

	args := made.Args()
	if p.denialArgs != nil {
		// Defense in depth: the catalog declares the arguments of a code, and
		// it gives every declared argument to every answer of the code. The
		// arguments of a denial belong to a code that declares none, so a code
		// that declares arguments keeps the arguments of the catalog only.
		if spec, known := errcode.Lookup(made.Code()); known && len(spec.Args) == 0 {
			args = p.denialArgs
		}
	}

	WriteJSON(ctx, wr.Logger, w, p.Status, Body{
		Message: p.Message, Code: p.Status, ErrorCode: made.Code(), ErrorArgs: args,
		Reauthenticate: p.Reauthenticate, Units: p.Units,
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
//  2. KeepDenied, for an error that wraps identity.ErrDenied: the classes
//     of a denial that keep their own code, with status 403 or 404 only;
//  3. any other error that wraps identity.ErrDenied, as a 403 forbidden, so a
//     refusal that wraps a denial never tells the state of an object;
//  4. the classifier of the API, which answers the refusals of its domain in
//     its own words;
//  5. the other shared classes.
//
// An error that none of them knows is a 500.
func (wr Writer) Problem(err error) Problem {
	if p, ok := credential(err); ok {
		return p
	}

	if errors.Is(err, identity.ErrDenied) {
		return wr.denied(err)
	}

	// A database that does not answer is the same for every API, and no class
	// of a domain may turn it into another answer.
	if errors.Is(err, namespace.ErrUnavailable) {
		return StoreUnavailable()
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

// denied returns the answer to an error that wraps identity.ErrDenied.
func (wr Writer) denied(err error) Problem {
	if wr.KeepDenied != nil {
		// Defense in depth: the adapters list only 403 classes. This check stays
		// so that a wrong KeepDenied cannot turn a denial into a success or a
		// conflict, and it does not rely on the review of each adapter.
		if p, ok := wr.KeepDenied(err); ok &&
			(p.Status == http.StatusForbidden || p.Status == http.StatusNotFound) {
			return p
		}
	}

	// A denial of the typed error names the missing action and the kind of
	// the object. It never names the object itself: the caller may not see
	// it. The arguments stay out of the catalog, because a denial that does
	// not carry the action answers without them, and every answer of a code
	// carries each argument that the code declares.
	if denial, ok := errors.AsType[*identity.DeniedError](err); ok && denial != nil &&
		errcode.Forbidden(err) == errcode.AccessDenied {
		// Defense in depth: a value that an argument cannot hold must not
		// turn the denial into a 500 or into a body with an invalid
		// argument. The standard answer below stays a 403.
		if args, argErr := errcode.DeniedArgs(string(denial.Action), denial.ObjectKind()); argErr == nil {
			p := answer(http.StatusForbidden, errcode.AccessDenied,
				"forbidden: you need the action "+string(denial.Action)+" on this "+denial.ObjectKind())
			p.denialArgs = args

			return p
		}
	}

	return answer(http.StatusForbidden, errcode.Forbidden(err), "forbidden")
}

// StoreRetryAfter is the wait that every API names in Retry-After when the
// database does not answer.
const StoreRetryAfter = 5 * time.Second

// StoreUnavailable returns the answer to a database that does not answer: 503
// with the wait StoreRetryAfter and the code namespace.unavailable.
func StoreUnavailable() Problem {
	p := answer(http.StatusServiceUnavailable, errcode.NamespaceUnavailable,
		namespace.ErrUnavailable.Error()+": try again after the Retry-After time")
	p.RetryAfter = StoreRetryAfter

	return p
}

// SetStoreRetryAfter sets the Retry-After header of the answer to a database
// that does not answer.
func SetStoreRetryAfter(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.FormatInt(RetryAfterSeconds(StoreRetryAfter), 10))
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

	if p, ok := Namespace(err); ok {
		return p, true
	}

	return Worker(err)
}

// Worker returns the answer to a start that the worker check refused: 503
// with the command that starts a worker.
func Worker(err error) (Problem, bool) {
	if errors.Is(err, admit.ErrNoWorker) {
		return answer(http.StatusServiceUnavailable, errcode.WorkerAbsent, admit.Message), true
	}

	return Pass()
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
	// The answer is the text of the sentinel that matched. The wrap text names
	// the operation, and the log holds it.
	case errors.Is(err, sessionauth.ErrCSRF):
		return answer(http.StatusForbidden, errcode.Forbidden(err), sessionauth.ErrCSRF.Error()), true
	case errors.Is(err, sessionauth.ErrOtherSite):
		return answer(http.StatusForbidden, errcode.Forbidden(err), sessionauth.ErrOtherSite.Error()), true
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
		return Built(ie.Status, ie.Message, ie.Answer.Code(), ie.Answer, nil), true
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
			namespace.ErrNotHome.Error(),
		), true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return answer(http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance,
			maintenance.ErrInMaintenance.Error()+": try again later"), true
	case errors.Is(err, schema.ErrBinaryBelowMinimum):
		return belowMinimum(err), true
	default:
		return Pass()
	}
}

// belowMinimum returns the answer to a write that this binary is too old to
// make: 503, the text that names both versions, and the two versions as the
// arguments of the code.
func belowMinimum(err error) Problem {
	below, ok := errors.AsType[*schema.MinimumError](err)
	if !ok {
		return answer(http.StatusServiceUnavailable, errcode.SchemaBinaryBelowMinimum,
			schema.ErrBinaryBelowMinimum.Error())
	}

	p := answer(http.StatusServiceUnavailable, errcode.SchemaBinaryBelowMinimum, below.WriteText())
	p.Values = []errcode.Value{
		errcode.Identifier("binaryVersion", below.BinaryVersion),
		errcode.Identifier("minimumVersion", below.SchemaMinimum),
	}

	return p
}

// Built returns the answer of status and message for an answer a that
// errcode.New made, with the error of that call. A refused answer keeps the
// code and no values, so the writer logs the code and answers server.internal.
func Built(status int, message, code string, a errcode.Answer, err error) Problem {
	p := answer(status, code, message)
	if err == nil {
		p.answer = a
	}

	return p
}

// answer returns a Problem whose code takes no argument.
func answer(status int, code, message string) Problem {
	return Problem{
		Status: status, Code: code, Values: nil, denialArgs: nil, Message: message, RetryAfter: 0,
		Reauthenticate: "", Units: nil, answer: errcode.Answer{}, cause: "",
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
