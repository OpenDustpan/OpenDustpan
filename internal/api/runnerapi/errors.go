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

// writeError answers err in the error shape of the scope: the protocol shape
// for a runner request, and the shape of the tool-neutral API for the
// management API.
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, scope Scope, err error) {
	if scope == ScopeAuthorized {
		status, message := classifyManagement(err)
		h.logFailure(ctx, status)
		h.logFenced(ctx, err)
		h.writeJSON(ctx, w, status, managementError{Message: message, Code: status})

		return
	}

	status, body := classifyProtocol(err)
	h.logFailure(ctx, status)

	h.logFenced(ctx, err)

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

// logFailure logs the status of a failed request. The log never holds the
// error text, which may hold driver detail, a token or a key.
func (h *Handler) logFailure(ctx context.Context, status int) {
	if status >= http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "runner request failed", slog.Int("status", status))
	}
}

// classifyProtocol maps an error to the status and the body of the protocol.
// Each code and status is in protocol.Error. A message holds no secret.
func classifyProtocol(err error) (int, protocol.Error) {
	mk := func(status int, code string) (int, protocol.Error) {
		return status, protocol.Error{Code: code, Message: err.Error(), Supported: ""}
	}

	switch {
	case errors.Is(err, runner.ErrBadProof), errors.Is(err, runner.ErrReplay):
		return mk(http.StatusUnauthorized, protocol.ErrorBadProof)
	case errors.Is(err, runner.ErrKeyExpired):
		return mk(http.StatusUnauthorized, protocol.ErrorKeyExpired)
	case errors.Is(err, runner.ErrRetired):
		return mk(http.StatusForbidden, protocol.ErrorRetired)
	case errors.Is(err, runner.ErrTokenUsed):
		return mk(http.StatusUnauthorized, protocol.ErrorTokenUsed)
	case errors.Is(err, runner.ErrTokenExpired):
		return mk(http.StatusUnauthorized, protocol.ErrorTokenExpired)
	case errors.Is(err, runner.ErrVersionUnsupported):
		return mk(http.StatusUpgradeRequired, protocol.ErrorVersionUnsupported)
	case errors.Is(err, runner.ErrVersionAhead):
		return mk(http.StatusUpgradeRequired, protocol.ErrorVersionAhead)
	default:
		return classifyProtocolOther(err, mk)
	}
}

// classifyProtocolOther maps the errors of claims and requests.
func classifyProtocolOther(
	err error, mk func(status int, code string) (int, protocol.Error),
) (int, protocol.Error) {
	switch {
	case errors.Is(err, runner.ErrClaimLost), errors.Is(err, durable.ErrClaimLost):
		return mk(http.StatusConflict, protocol.ErrorClaimLost)
	case errors.Is(err, job.ErrCanceled), errors.Is(err, durable.ErrCanceled):
		return mk(http.StatusConflict, protocol.ErrorCanceled)
	case errors.Is(err, runner.ErrConflict), errors.Is(err, runner.ErrRunnerOnline):
		return mk(http.StatusConflict, protocol.ErrorConflict)
	case errors.Is(err, runner.ErrNotFound), errors.Is(err, durable.ErrNotFound), errors.Is(err, namespace.ErrNotFound):
		return mk(http.StatusNotFound, protocol.ErrorNotFound)
	case errors.Is(err, runner.ErrInvalid), errors.Is(err, errBadBody):
		return mk(http.StatusBadRequest, protocol.ErrorInvalid)
	case errors.Is(err, runner.ErrLimit):
		return mk(http.StatusTooManyRequests, protocol.ErrorRateLimited)
	case errors.Is(err, namespace.ErrFenced):
		// The copy of the sentinel only: a wrap adds the name of the
		// operation, which the log holds.
		return http.StatusServiceUnavailable, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: namespace.ErrFenced.Error(), Supported: "",
		}
	case  errors.Is(err, namespace.ErrNotHome),
		errors.Is(err, maintenance.ErrInMaintenance):
		return mk(http.StatusServiceUnavailable, protocol.ErrorUnavailable)
	default:
		return http.StatusInternalServerError, protocol.Error{
			Code: protocol.ErrorUnavailable, Message: "internal error", Supported: "",
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
		return http.StatusForbidden, err.Error()
	case errors.Is(err, identity.ErrDenied):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, errNotFound), errors.Is(err, runner.ErrNotFound), errors.Is(err, namespace.ErrNotFound):
		return http.StatusNotFound, errNotFound.Error()
	case errors.Is(err, runner.ErrInvalid), errors.Is(err, errBadBody):
		return http.StatusBadRequest, err.Error()
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
		return http.StatusConflict, err.Error()
	case errors.Is(err, runner.ErrLimit):
		return http.StatusConflict, err.Error()
	case errors.Is(err, runner.ErrRetired):
		return http.StatusConflict, "the runner is retired"
	case errors.Is(err, namespace.ErrFenced):
		return http.StatusConflict, namespace.ErrFenced.Error()
	case errors.Is(err, namespace.ErrNotHome):
		return http.StatusMisdirectedRequest, "namespace is not homed in this region"
	case errors.Is(err, maintenance.ErrInMaintenance):
		return http.StatusServiceUnavailable, maintenance.ErrInMaintenance.Error() + ": try again later"
	default:
		return http.StatusInternalServerError, "internal error"
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
