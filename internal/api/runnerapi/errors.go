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

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// errNotFound is the one answer for an unknown namespace, an unknown pool, an
// unknown runner, and a namespace that the caller cannot read.
var errNotFound = errors.New("not found")

// retryAfterSeconds is the Retry-After value of a 503.
const retryAfterSeconds = 5

// managementError is the error body of the tool-neutral management API.
type managementError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// codedManagementError is managementError with the stable machine code of a
// 403. The fields of managementError keep their names and their meaning.
type codedManagementError struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
}

// writeError answers err in the error shape of the scope: the protocol shape
// for a runner request, and the shape of the tool-neutral API for the
// management API.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, scope Scope, err error) {
	if scope == ScopeAuthorized {
		status, message := classifyManagement(err)
		h.logFailure(ctx, status)
		h.logFenced(ctx, err)
		h.logRefused(ctx, err, message)

		if status == http.StatusForbidden {
			if code := errcode.Forbidden(err); code != "" {
				h.writeJSON(ctx, w, status, codedManagementError{Message: message, Code: status, ErrorCode: code})

				return
			}
		}

		h.writeJSON(ctx, w, status, managementError{Message: message, Code: status})

		return
	}

	status, body := classifyProtocol(err)
	h.logFailure(ctx, status)

	h.logFenced(ctx, err)
	h.logRefused(ctx, err, body.Message)

	if status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}

	h.writeJSON(ctx, w, status, body)
}

// logFenced logs a fenced write with its wrap context, which the answer never
// holds.
func (h *Handler) logFenced(ctx context.Context, err error) {
	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}
}

// logRefused logs the wrap text of a protocol error that the answer does not
// hold. A fence has its own line, and an unknown error holds no text for the
// log: its answer is "internal error" and logFailure records the status. A
// refusal that any anonymous caller can cause logs at Debug, so that a flood
// of forged requests does not fill the log.
func (h *Handler) logRefused(ctx context.Context, err error, sent string) {
	if sent == err.Error() || sent == internalMessage || errors.Is(err, namespace.ErrFenced) {
		return
	}

	h.logger.Log(ctx, refusedLevel(err), "runner request refused", slog.String("error", err.Error()))
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
func (h *Handler) logFailure(ctx context.Context, status int) {
	if status >= http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "runner request failed", slog.Int("status", status))
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
	case errors.Is(err, runner.ErrClaimLost), errors.Is(err, durable.ErrClaimLost):
		return mk(http.StatusConflict, protocol.ErrorClaimLost, runner.ErrClaimLost, durable.ErrClaimLost)
	case errors.Is(err, job.ErrCanceled), errors.Is(err, durable.ErrCanceled):
		return mk(http.StatusConflict, protocol.ErrorCanceled, job.ErrCanceled, durable.ErrCanceled)
	case errors.Is(err, runner.ErrConflict), errors.Is(err, runner.ErrRunnerOnline):
		return mk(http.StatusConflict, protocol.ErrorConflict, runner.ErrConflict, runner.ErrRunnerOnline)
	case errors.Is(err, runner.ErrNotFound), errors.Is(err, durable.ErrNotFound), errors.Is(err, namespace.ErrNotFound):
		return mk(
			http.StatusNotFound, protocol.ErrorNotFound,
			runner.ErrNotFound, durable.ErrNotFound, namespace.ErrNotFound,
		)
	case errors.Is(err, runner.ErrInvalid), errors.Is(err, errBadBody):
		return mk(http.StatusBadRequest, protocol.ErrorInvalid, runner.ErrInvalid, errBadBody)
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
		return mk(http.StatusServiceUnavailable, protocol.ErrorUnavailable, namespace.ErrNotHome)
	default:
		return http.StatusInternalServerError, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: internalMessage, Supported: "",
		}
	}
}

// errBadBody reports a body that is not the JSON object of its path.
var errBadBody = errors.New("the body is not the JSON object of this path")

// classifyManagement maps an error to the status and the message of the
// management API.
func classifyManagement(err error) (int, string) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later"
	case errors.Is(err, identity.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, sessionauth.ErrCSRF), errors.Is(err, sessionauth.ErrOtherSite):
		return http.StatusForbidden, sentenceOf(err, []error{sessionauth.ErrCSRF, sessionauth.ErrOtherSite})
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errNotFound), errors.Is(err, runner.ErrNotFound), errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, errNotFound.Error()
	case errors.Is(err, runner.ErrInvalid), errors.Is(err, errBadBody):
		return http.StatusBadRequest, sentenceOf(err, []error{runner.ErrInvalid, errBadBody})
	default:
		return classifyManagementConflict(err)
	}
}

// classifyManagementConflict maps the errors of a refused change.
func classifyManagementConflict(err error) (int, string) {
	switch {
	case errors.Is(err, runner.ErrAlreadyExists):
		return http.StatusConflict, "a runner pool with this name exists: choose another name"
	case errors.Is(err, runner.ErrImmutable), errors.Is(err, runner.ErrPoolInUse), errors.Is(err, runner.ErrTokenUsed):
		return http.StatusConflict, sentenceOf(
			err, []error{runner.ErrImmutable, runner.ErrPoolInUse, runner.ErrTokenUsed},
		)
	case errors.Is(err, runner.ErrLimit):
		return http.StatusConflict, runner.ErrLimit.Error()
	case errors.Is(err, runner.ErrRetired):
		return http.StatusConflict, "the runner is retired"
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, namespace.ErrFenced.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, "namespace is not homed in this region"
	case errors.Is(err, maintenance.ErrInMaintenance):
		return http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error() + ": try again later"
	default:
		return http.StatusInternalServerError, internalMessage
	}
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
