package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// titleForbidden is the JSON:API title of a 403 answer.
const titleForbidden = "forbidden"

// titleConflict is the JSON:API title of a 409 answer.
const titleConflict = "conflict"

// jsonAPIMediaType is the media type of every response, including errors.
const jsonAPIMediaType = "application/vnd.api+json"

// notFoundTitle is the title of every 404 error document.
const notFoundTitle = "not found"

// errInvalidBody reports a request body that is not valid. classify maps it
// to 422.
var errInvalidBody = errors.New("invalid request body")

// errMissingFilter reports a state version list without its organization or
// workspace filter. classify maps it to 400.
var errMissingFilter = errors.New("state version list needs filter[organization][name] and filter[workspace][name]")

// jsonAPIError is one entry of a JSON:API error document.
type jsonAPIError struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// jsonAPIDocument is the error shape the TFE client reads: an errors array
// of status, title and detail.
type jsonAPIDocument struct {
	Errors []jsonAPIError `json:"errors"`
}

// errorClass is a classified error: the status, title and detail safe to
// show callers.
type errorClass struct {
	status int
	title  string
	detail string
}

// writeError answers err as a JSON:API error document. A 500 never returns
// an internal error text: it logs the status and answers "internal error".
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	class := classify(err)
	if class.status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state (ADR 32 decision 7).
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", class.status))
		h.writeInternal(ctx, w)

		return
	}

	switch class.status {
	case http.StatusUnauthorized:
		h.writeUnauthorized(ctx, w, class.detail)
	case http.StatusForbidden:
		h.writeForbidden(ctx, w, class.detail)
	case http.StatusNotFound:
		h.writeNotFound(ctx, w, class.detail)
	case http.StatusConflict:
		h.writeConflict(ctx, w, class.detail)
	case http.StatusUnprocessableEntity:
		h.writeUnprocessable(ctx, w, class.detail, class.title)
	case http.StatusServiceUnavailable:
		bodygate.SetRetryAfterJitter(w)
		h.writeJSONAPIError(ctx, w, class.status, class.title, class.detail)
	default:
		h.writeJSONAPIError(ctx, w, class.status, class.title, class.detail)
	}
}

// writeUnauthorized answers 401.
func (h *Handler) writeUnauthorized(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusUnauthorized, "unauthorized", detail)
}

// writeForbidden answers 403.
func (h *Handler) writeForbidden(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusForbidden, titleForbidden, detail)
}

// writeNotFound answers 404.
func (h *Handler) writeNotFound(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusNotFound, notFoundTitle, detail)
}

// writeConflict answers 409.
func (h *Handler) writeConflict(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusConflict, titleConflict, detail)
}

// writeUnprocessable answers 422.
func (h *Handler) writeUnprocessable(ctx context.Context, w http.ResponseWriter, detail, title string) {
	h.writeJSONAPIError(ctx, w, http.StatusUnprocessableEntity, title, detail)
}

// writeInternal answers 500 without an internal error text.
func (h *Handler) writeInternal(ctx context.Context, w http.ResponseWriter) {
	h.writeJSONAPIError(ctx, w, http.StatusInternalServerError, "internal error", "internal error")
}

// writeJSONAPIError writes one JSON:API error document.
func (h *Handler) writeJSONAPIError(
	ctx context.Context,
	w http.ResponseWriter,
	status int,
	title, detail string,
) {
	w.Header().Set("Content-Type", jsonAPIMediaType)
	w.WriteHeader(status)

	doc := jsonAPIDocument{Errors: []jsonAPIError{{Status: strconv.Itoa(status), Title: title, Detail: detail}}}

	if err := json.NewEncoder(w).Encode(doc); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// classifyTooLarge maps an oversized body or a budget that can never fit to
// 413. ok is false for any other error.
func classifyTooLarge(err error) (errorClass, bool) {
	switch {
	case errors.Is(err, workspace.ErrTooLarge), errors.Is(err, workspace.ErrBudgetTooSmall):
		return errorClass{status: http.StatusRequestEntityTooLarge, title: "too large", detail: err.Error()}, true
	case errors.Is(err, identity.ErrRateLimited):
		// A source over its failure budget (ADR 53).
		return errorClass{
			status: http.StatusTooManyRequests,
			title:  "too many requests",
			detail: "too many failed sign-ins from this address: try again later",
		}, true
	default:
		return errorClass{status: 0, title: "", detail: ""}, false
	}
}

// classifyNamespace maps the namespace errors of a write: a fenced write to
// 409, a namespace homed in another region to 421, and a database that does
// not answer to 503. ok is false for any other error.
func classifyNamespace(err error) (errorClass, bool) {
	switch {
	case errors.Is(err, namespace.ErrFenced):
		return errorClass{status: http.StatusConflict, title: titleConflict, detail: err.Error()}, true
	case errors.Is(err, namespace.ErrNotHome):
		return errorClass{
			status: http.StatusMisdirectedRequest,
			title:  "misdirected request",
			detail: err.Error(),
		}, true
	case errors.Is(err, namespace.ErrUnavailable):
		return errorClass{
			status: http.StatusServiceUnavailable,
			title:  "service unavailable",
			detail: "database unavailable: try again after the Retry-After time",
		}, true
	default:
		return errorClass{status: 0, title: "", detail: ""}, false
	}
}

// classify maps domain errors to a status, title and detail safe to show
// callers. Unknown errors map to 500 with a generic detail.
func classify(err error) errorClass {
	if class, ok := classifyTooLarge(err); ok {
		return class
	}

	if class, ok := classifyNamespace(err); ok {
		return class
	}

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return errorClass{status: http.StatusUnauthorized, title: "unauthorized", detail: "unauthenticated"}
	case errors.Is(err, errMissingFilter):
		return errorClass{status: http.StatusBadRequest, title: "bad request", detail: err.Error()}
	case errors.Is(err, workspace.ErrOverrideRefused):
		// The detail names the switch and the action that allow the override.
		return errorClass{status: http.StatusForbidden, title: titleForbidden, detail: err.Error()}
	case errors.Is(err, identity.ErrDenied), errors.Is(err, workspace.ErrUploadDenied):
		return errorClass{status: http.StatusForbidden, title: titleForbidden, detail: titleForbidden}
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, namespace.ErrNotFound),
		errors.Is(err, workspace.ErrNoState), errors.Is(err, workspace.ErrStateVersionNotFound),
		errors.Is(err, workspace.ErrResourceNotFound):
		return errorClass{status: http.StatusNotFound, title: notFoundTitle, detail: err.Error()}
	case errors.Is(err, workspace.ErrClientEncrypted),
		errors.Is(err, workspace.ErrAlreadyExists),
		errors.Is(err, workspace.ErrLocked),
		errors.Is(err, workspace.ErrPendingState),
		errors.Is(err, workspace.ErrNotLocked),
		errors.Is(err, workspace.ErrLockedByOther),
		errors.Is(err, workspace.ErrConcurrentModification),
		errors.Is(err, workspace.ErrTooManyPending):
		return errorClass{status: http.StatusConflict, title: titleConflict, detail: err.Error()}
	case errors.Is(err, workspace.ErrBusy):
		return errorClass{
			status: http.StatusServiceUnavailable,
			title:  "service unavailable",
			detail: "server is busy: try again after the Retry-After time",
		}
	case errors.Is(err, errInvalidBody),
		errors.Is(err, workspace.ErrInvalidName),
		errors.Is(err, namespace.ErrInvalidName),
		errors.Is(err, identity.ErrInvalidObject),
		errors.Is(err, workspace.ErrInvalidState),
		errors.Is(err, workspace.ErrStaleSerial),
		errors.Is(err, workspace.ErrLineageMismatch),
		errors.Is(err, workspace.ErrChecksumMismatch):
		return errorClass{status: http.StatusUnprocessableEntity, title: "unprocessable", detail: err.Error()}
	default:
		return errorClass{status: http.StatusInternalServerError, title: "internal error", detail: "internal error"}
	}
}

// writeJSON writes body as JSON with the JSON:API media type. Both success
// bodies and errors use application/vnd.api+json.
func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", jsonAPIMediaType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
