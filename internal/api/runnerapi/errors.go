package runnerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errNotFound is the one answer for an unknown namespace, an unknown pool, an
// unknown runner, and a namespace that the caller cannot read.
var errNotFound = errors.New("not found")

// retryAfterSeconds is the Retry-After value of a 503.
const retryAfterSeconds = 5

// writeError answers err in the error shape of the scope: the protocol shape
// for a runner request, and the shape of the tool-neutral API for the
// management API.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, scope Scope, err error) {
	if scope == ScopeAuthorized {
		h.writeManagement(ctx, w, err)

		return
	}

	status, body := classifyProtocol(err)
	h.logFailure(ctx, status, err)

	h.logFenced(ctx, err)
	h.logRefused(ctx, err, status, body.Code, body.Message)

	if status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}

	h.writeJSON(ctx, w, status, body)
}

// writeManagement answers err in the error shape of the management API.
func (h *Handler) writeManagement(ctx context.Context, w http.ResponseWriter, err error) {
	wr := problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classifyManagement}

	p := wr.Problem(err)
	h.logRefused(ctx, err, p.Status, p.Code, p.Message)
	wr.Write(ctx, w, err)
}

// logFenced logs a fenced write with its wrap context, which the answer never
// holds.
func (h *Handler) logFenced(ctx context.Context, err error) {
	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}
}

// logRefused logs a refusal whose answer does not hold its error text. A fence
// has its own line, and an unknown error holds no text for the log: its answer
// is "internal error" and logFailure records the status. A refusal that any
// anonymous caller can cause logs at Debug without the text of its error, so
// that a flood of forged requests does not fill the log and no wrapped secret
// reaches it. Other refusals log the text at Warn.
func (h *Handler) logRefused(ctx context.Context, err error, status int, code, sent string) {
	if sent == err.Error() || sent == internalMessage || errors.Is(err, namespace.ErrFenced) {
		return
	}

	if refusedLevel(err) == slog.LevelDebug {
		// Defense in depth: a caller without credentials causes this line, and the
		// wrap of its error can hold a session or proof detail. The line names the
		// status and the code only, and this check stays so that a new caller
		// cannot add the text of err.
		h.logger.DebugContext(ctx, "runner request refused",
			slog.Int("status", status), slog.String("code", code))

		return
	}

	h.logger.WarnContext(ctx, "runner request refused",
		slog.Int("status", status), slog.String("code", code), slog.String("error", err.Error()))
}

// refusedLevel returns the log level of a refusal. Debug is for the errors
// that a caller without credentials can cause: no sign-in, a proof that does
// not verify, an unknown runner. All other refusals are Warn.
func refusedLevel(err error) slog.Level {
	if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, runner.ErrBadProof) ||
		errors.Is(err, runner.ErrNotFound) {
		return slog.LevelDebug
	}

	return slog.LevelWarn
}

// logFailure logs the status of a failed request. It never holds the error
// text, which may hold driver detail, a token or a key. Only logFenced and
// logRefused log the text of an error, and only for an error that a case of a
// switch knows.
func (h *Handler) logFailure(ctx context.Context, status int, err error) {
	if status >= http.StatusInternalServerError {
		faultlog.LogServer(ctx, h.logger, "runner request failed",
			faultlog.Fields{Status: status, Cause: faultlog.Cause(err), Code: "", Op: ""})
	}
}

// internalMessage is the answer to an error that no case of the protocol
// switch knows.
const internalMessage = "internal error"

// classifyProtocol maps an error to the status and the body of the protocol.
// Each code and status is in protocol.Error. The message is the sentence of
// the sentinel that matched, never err.Error(): a wrap adds the name of the
// operation, which the log holds.
func classifyProtocol(err error) (int, protocol.Error) {
	mk := func(status int, code string, sentinels ...error) (int, protocol.Error) {
		return status, protocol.Error{Code: code, Message: sentenceOf(err, sentinels), Supported: ""}
	}

	// A database that does not answer comes first: the runner tries again
	// after the Retry-After time, whatever else the error wraps.
	if errors.Is(err, namespace.ErrUnavailable) {
		return mk(http.StatusServiceUnavailable, protocol.ErrorUnavailable, namespace.ErrUnavailable)
	}

	// A plan that read an old state version failed its phase. The runner does
	// not try the plan again.
	if errors.Is(err, job.ErrPlanStale) {
		return mk(http.StatusConflict, protocol.ErrorPlanStale, job.ErrPlanStale)
	}

	switch {
	case errors.Is(err, runner.ErrBadProof), errors.Is(err, runner.ErrReplay):
		return mk(http.StatusUnauthorized, protocol.ErrorBadProof, runner.ErrBadProof, runner.ErrReplay)
	case errors.Is(err, runner.ErrKeyExpired):
		return mk(http.StatusUnauthorized, protocol.ErrorKeyExpired, runner.ErrKeyExpired)
	case errors.Is(err, runner.ErrRetired):
		return mk(http.StatusForbidden, protocol.ErrorRetired, runner.ErrRetired)
	case errors.Is(err, runner.ErrTokenUsed):
		return mk(http.StatusUnauthorized, protocol.ErrorTokenUsed, runner.ErrTokenUsed)
	case errors.Is(err, runner.ErrTokenExpired):
		return mk(http.StatusUnauthorized, protocol.ErrorTokenExpired, runner.ErrTokenExpired)
	case errors.Is(err, runner.ErrVersionUnsupported):
		return mk(http.StatusUpgradeRequired, protocol.ErrorVersionUnsupported, runner.ErrVersionUnsupported)
	case errors.Is(err, runner.ErrVersionAhead):
		return mk(http.StatusUpgradeRequired, protocol.ErrorVersionAhead, runner.ErrVersionAhead)
	default:
		return classifyProtocolOther(err, mk)
	}
}

// sentenceOf returns the sentence of the first sentinel that err wraps. The
// switch that calls it matched one of them already; the fallback keeps the
// answer closed if a caller breaks that rule.
func sentenceOf(err error, sentinels []error) string {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s.Error()
		}
	}

	return internalMessage
}

// classifyProtocolOther maps the errors of claims and requests.
func classifyProtocolOther(
	err error, mk func(status int, code string, sentinels ...error) (int, protocol.Error),
) (int, protocol.Error) {
	switch {
	case errors.Is(err, runner.ErrClaimLost), errors.Is(err, durable.ErrClaimLost), errors.Is(err, job.ErrClaimLost):
		return mk(
			http.StatusConflict, protocol.ErrorClaimLost, runner.ErrClaimLost, durable.ErrClaimLost, job.ErrClaimLost,
		)
	case errors.Is(err, job.ErrCanceled), errors.Is(err, durable.ErrCanceled):
		return mk(http.StatusConflict, protocol.ErrorCanceled, job.ErrCanceled, durable.ErrCanceled)
	case errors.Is(err, runner.ErrConflict), errors.Is(err, runner.ErrRunnerOnline), errors.Is(err, job.ErrConflict),
		errors.Is(err, durable.ErrStepDone):
		return mk(
			http.StatusConflict, protocol.ErrorConflict,
			job.ErrLogDiffers, runner.ErrConflict, runner.ErrRunnerOnline, job.ErrConflict, durable.ErrStepDone,
		)
	case errors.Is(err, runner.ErrNotFound), errors.Is(err, durable.ErrNotFound), errors.Is(err, namespace.ErrNotFound),
		errors.Is(err, job.ErrNotFound):
		return mk(
			http.StatusNotFound, protocol.ErrorNotFound,
			runner.ErrNotFound, durable.ErrNotFound, namespace.ErrNotFound, job.ErrNotFound,
		)
	case errors.Is(err, runner.ErrInvalid), errors.Is(err, job.ErrInvalid), errors.Is(err, errBadBody):
		return mk(http.StatusBadRequest, protocol.ErrorInvalid, runner.ErrInvalid, job.ErrInvalid, errBadBody)
	case errors.Is(err, runner.ErrLimit):
		return mk(http.StatusTooManyRequests, protocol.ErrorRateLimited, runner.ErrLimit)
	case errors.Is(err, namespace.ErrFenced):
		// The copy of the sentinel only: a wrap adds the name of the
		// operation, which the log holds.
		return http.StatusServiceUnavailable, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: namespace.ErrFenced.Error(), Supported: "",
		}
	case errors.Is(err, namespace.ErrNotHome),
		errors.Is(err, maintenance.ErrInMaintenance):
		return mk(
			http.StatusServiceUnavailable, protocol.ErrorUnavailable,
			namespace.ErrNotHome, maintenance.ErrInMaintenance,
		)
	case errors.Is(err, schema.ErrBinaryBelowMinimum):
		// The text names both versions and nothing else, so a runner can show it.
		message := schema.ErrBinaryBelowMinimum.Error()
		if below, ok := errors.AsType[*schema.MinimumError](err); ok {
			message = below.WriteText()
		}

		return http.StatusServiceUnavailable, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: message, Supported: "",
		}
	default:
		return http.StatusInternalServerError, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: internalMessage, Supported: "",
		}
	}
}

// errBadBody reports a body that is not the JSON object of its path.
var errBadBody = errors.New("the body is not the JSON object of this path")

// classifyManagement maps the errors of the runner domain to their answer in
// the management API. The refusals of the credential and a denial are answered
// before it, and the shared classes of problem.Shared run after it: an error of
// none of them answers 500 with server.internal.
func classifyManagement(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, errNotFound), errors.Is(err, runner.ErrNotFound), errors.Is(err, namespace.ErrNotFound):
		return problem.New(http.StatusNotFound, errcode.RunnerNotFound, errNotFound.Error()), true
	case errors.Is(err, runner.ErrInvalid):
		return problem.New(
			http.StatusBadRequest, errcode.RunnerInvalid,
			sentenceOf(err, []error{runner.ErrInvalidHelper, runner.ErrInvalid}),
		), true
	case errors.Is(err, errBadBody):
		return problem.New(http.StatusBadRequest, errcode.RequestBodyInvalid, errBadBody.Error()), true
	default:
		return classifyManagementConflict(err)
	}
}

// classifyManagementConflict maps the errors of a refused change of the runner
// domain.
func classifyManagementConflict(err error) (problem.Problem, bool) {
	switch {
	case errors.Is(err, runner.ErrAlreadyExists):
		return problem.New(http.StatusConflict, errcode.RunnerPoolExists,
			"a runner pool with this name exists: choose another name"), true
	case errors.Is(err, runner.ErrImmutable):
		return problem.New(http.StatusConflict, errcode.RunnerImmutable, runner.ErrImmutable.Error()), true
	case errors.Is(err, runner.ErrPoolInUse):
		return problem.New(http.StatusConflict, errcode.RunnerPoolInUse, runner.ErrPoolInUse.Error()), true
	case errors.Is(err, runner.ErrTokenUsed):
		return problem.New(http.StatusConflict, errcode.RunnerTokenUsed, runner.ErrTokenUsed.Error()), true
	case errors.Is(err, runner.ErrLimit):
		return problem.New(http.StatusConflict, errcode.RunnerLimit, runner.ErrLimit.Error()), true
	case errors.Is(err, runner.ErrRetired):
		return problem.New(http.StatusConflict, errcode.RunnerRetired, "the runner is retired"), true
	}

	// The namespace, maintenance and idempotency answers that every API shares
	// come from problem.Shared, after this classifier.
	return problem.Pass()
}

func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// decodeBody decodes the JSON object in body into v. An unknown field is an
// error, so a misspelled setting never passes quietly.
func decodeBody(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", errBadBody, err)
	}

	return nil
}
