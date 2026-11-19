package runnerpools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// errNotFound is the one answer for an unknown namespace, an unknown pool, an
// unknown runner, and a namespace that the caller cannot read.
var errNotFound = errors.New("not found")

// writeError answers err in the error shape of the tool-neutral API.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, _ Scope, err error) {
	h.writeManagement(ctx, w, err)
}

// writeManagement answers err in the error shape of the management API.
func (h *Handler) writeManagement(ctx context.Context, w http.ResponseWriter, err error) {
	wr := problem.Writer{Logger: h.logger, KeepDenied: nil, Classify: classifyManagement}

	p := wr.Problem(err)
	h.logRefused(ctx, err, p.Status, p.Code, p.Message)
	wr.Write(ctx, w, err)
}

// logRefused logs a refusal whose answer does not hold its error text. A fence
// has its own line, and an unknown error holds no text for the log: its answer
// is "internal error". A refusal that any
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
// that a caller without credentials can cause: no sign-in, an unknown
// runner. All other refusals are Warn.
func refusedLevel(err error) slog.Level {
	if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, runner.ErrBadProof) ||
		errors.Is(err, runner.ErrNotFound) {
		return slog.LevelDebug
	}

	return slog.LevelWarn
}

// internalMessage is the answer to an error that no case of the management
// switch knows.
const internalMessage = "internal error"

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
		// A field error carries fixed text only, never the input of the caller.
		if fe, ok := errors.AsType[*runner.FieldError](err); ok {
			return problem.New(http.StatusBadRequest, errcode.RunnerInvalid,
				runner.ErrInvalid.Error()+": "+fe.Sentence()), true
		}

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
