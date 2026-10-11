package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

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
// an internal error text: it logs the error and answers "internal error".
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	class := classify(err)
	if class.status == http.StatusInternalServerError {
		h.logger.ErrorContext(ctx, "request failed", slog.String("error", err.Error()))
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
	h.writeJSONAPIError(ctx, w, http.StatusForbidden, "forbidden", detail)
}

// writeNotFound answers 404.
func (h *Handler) writeNotFound(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusNotFound, notFoundTitle, detail)
}

// writeConflict answers 409.
func (h *Handler) writeConflict(ctx context.Context, w http.ResponseWriter, detail string) {
	h.writeJSONAPIError(ctx, w, http.StatusConflict, "conflict", detail)
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

// classify maps domain errors to a status, title and detail safe to show
// callers. Unknown errors map to 500 with a generic detail.
func classify(err error) errorClass {
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return errorClass{status: http.StatusUnauthorized, title: "unauthorized", detail: "unauthenticated"}
	case errors.Is(err, errMissingFilter):
		return errorClass{status: http.StatusBadRequest, title: "bad request", detail: err.Error()}
	case errors.Is(err, identity.ErrDenied), errors.Is(err, workspace.ErrUploadDenied):
		return errorClass{status: http.StatusForbidden, title: "forbidden", detail: "forbidden"}
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, namespace.ErrNotFound),
		errors.Is(err, workspace.ErrNoState), errors.Is(err, workspace.ErrStateVersionNotFound):
		return errorClass{status: http.StatusNotFound, title: notFoundTitle, detail: err.Error()}
	case errors.Is(err, workspace.ErrAlreadyExists),
		errors.Is(err, workspace.ErrLocked),
		errors.Is(err, workspace.ErrNotLocked),
		errors.Is(err, workspace.ErrLockedByOther),
		errors.Is(err, workspace.ErrConcurrentModification),
		errors.Is(err, workspace.ErrTooManyPending),
		errors.Is(err, namespace.ErrFenced):
		return errorClass{status: http.StatusConflict, title: "conflict", detail: err.Error()}
	case errors.Is(err, workspace.ErrTooLarge):
		return errorClass{status: http.StatusRequestEntityTooLarge, title: "too large", detail: err.Error()}
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
