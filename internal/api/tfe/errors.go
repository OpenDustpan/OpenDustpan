package tfe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// titleForbidden is the JSON:API title of a 403 answer.
const titleForbidden = "forbidden"

// titleConflict is the JSON:API title of a 409 answer.
const titleConflict = "conflict"

// titleServiceUnavailable is the JSON:API title of a 503 answer.
const titleServiceUnavailable = "service unavailable"

// titleUnauthorized is the JSON:API title of a 401 answer.
const titleUnauthorized = "unauthorized"

// titleBadRequest is the JSON:API title of a 400 answer.
const titleBadRequest = "bad request"

// titleInternal is the JSON:API title and detail of a 500 answer.
const titleInternal = "internal error"

// jsonAPIMediaType is the media type of every response, including errors.
const jsonAPIMediaType = "application/vnd.api+json"

// notFoundTitle is the title of every 404 error document.
const notFoundTitle = "not found"

// errInvalidBody reports a request body that is not valid. classify maps it
// to 422.
var errInvalidBody = errors.New("invalid request body")

// errInvalidQuery reports a query parameter that is not valid. classify maps
// it to 422, as errInvalidBody.
var errInvalidQuery = errors.New("invalid query parameter")

// errMissingFilter reports a state version list without its organization or
// workspace filter. classify maps it to 400.
var errMissingFilter = errors.New("state version list needs filter[organization][name] and filter[workspace][name]")

// jsonAPIError is one entry of a JSON:API error document.
type jsonAPIError struct {
	Status string `json:"status"`
	// Code is the code of the catalog of the error. It is omitted only for an
	// error path that has no code yet.
	Code   string `json:"code,omitempty"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// Meta holds the arguments of the code, and the path that starts a new
	// sign-in for the refusal of an old sign-in. It is omitted when it holds
	// neither.
	Meta *jsonAPIErrorMeta `json:"meta,omitempty"`
}

// jsonAPIErrorMeta is the meta member of an error.
type jsonAPIErrorMeta struct {
	// Args are the arguments of the code: the values that the detail names.
	Args map[string]any `json:"args,omitempty"`
	// Reauthenticate is the path that starts a new sign-in.
	Reauthenticate string `json:"reauthenticate,omitempty"`
}

// jsonAPIDocument is the error shape the TFE client reads: an errors array
// of status, title and detail.
type jsonAPIDocument struct {
	Errors []jsonAPIError `json:"errors"`
}

// errorClass is a classified error: the status, title and detail safe to
// show callers, the code of the catalog, and the values of the arguments of
// the code.
type errorClass struct {
	status int
	title  string
	detail string
	code   string
	args   []errcode.Value
}

// unclassified returns what a classify function returns, with false, for an
// error that it does not map. It is no answer.
func unclassified() errorClass {
	var none errorClass

	return none
}

// writeError answers err as a JSON:API error document. A 500 never returns
// an internal error text: it logs the status and answers "internal error".
func (h *Handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, namespace.ErrFenced) {
		h.logger.WarnContext(ctx, "write fenced", slog.String("error", err.Error()))
	}

	if manual, ok := errors.AsType[*manuallock.LockedError](err); ok {
		h.writeManualLock(ctx, w, manual, err.Error())

		return
	}

	class := classify(err)
	if class.status == http.StatusInternalServerError {
		// The log names the status only, never the error text: the text
		// may hold driver detail, a token, or state (ADR 32 decision 7).
		h.logger.ErrorContext(ctx, "request failed", slog.Int("status", class.status))
		h.writeInternal(ctx, w)

		return
	}

	if class.status == http.StatusServiceUnavailable {
		bodygate.SetRetryAfterJitter(w)
	}

	h.writeClassError(ctx, w, class)
}

// writeManualLock answers 409 for a request that a manual lock refuses, with
// the code lock.locked and its arguments. detail is the text of the refusal.
func (h *Handler) writeManualLock(
	ctx context.Context, w http.ResponseWriter, manual *manuallock.LockedError, detail string,
) {
	answer, err := refusal.Locked(manual)
	if err != nil {
		h.logger.ErrorContext(ctx, "error code failed", slog.String("error_code", errcode.LockLocked))
		h.writeInternal(ctx, w)

		return
	}

	h.writeAnswer(ctx, w, errorClass{
		status: http.StatusConflict, title: titleConflict, detail: detail, code: errcode.LockLocked, args: nil,
	}, answer, "")
}

// writeInternal answers 500 without an internal error text.
func (h *Handler) writeInternal(ctx context.Context, w http.ResponseWriter) {
	h.writeAnswer(ctx, w, errorClass{
		status: http.StatusInternalServerError, title: titleInternal, detail: titleInternal,
		code: errcode.ServerInternal, args: nil,
	}, errcode.Internal(), "")
}

// writeClassError writes one JSON:API error document for a classified error,
// with the code of the catalog and its arguments.
func (h *Handler) writeClassError(ctx context.Context, w http.ResponseWriter, class errorClass) {
	h.writeCoded(ctx, w, class, "")
}

// writeCoded writes one JSON:API error document for class. A non-empty
// reauthenticate goes in meta.reauthenticate. A code or an argument that the
// catalog refuses is a bug in this adapter: the answer is then 500 with
// server.internal, and the log names the code.
func (h *Handler) writeCoded(ctx context.Context, w http.ResponseWriter, class errorClass, reauthenticate string) {
	if class.code == "" {
		// An error path that has no code yet answers without one.
		var none errcode.Answer

		h.writeAnswer(ctx, w, class, none, reauthenticate)

		return
	}

	answer, err := errcode.New(class.code, class.args...)
	if err != nil {
		h.logger.ErrorContext(ctx, "error code failed", slog.String("error_code", class.code))
		h.writeInternal(ctx, w)

		return
	}

	h.writeAnswer(ctx, w, class, answer, reauthenticate)
}

// writeAnswer writes one JSON:API error document with the status, the title
// and the detail of class, and the code and the arguments of answer. A
// non-empty reauthenticate goes in meta.reauthenticate.
func (h *Handler) writeAnswer(
	ctx context.Context, w http.ResponseWriter, class errorClass, answer errcode.Answer, reauthenticate string,
) {
	var meta *jsonAPIErrorMeta
	if args := answer.Args(); args != nil || reauthenticate != "" {
		meta = &jsonAPIErrorMeta{Args: args, Reauthenticate: reauthenticate}
	}

	h.writeJSONAPIDocument(ctx, w, class.status, jsonAPIError{
		Status: strconv.Itoa(class.status), Code: answer.Code(), Title: class.title, Detail: class.detail, Meta: meta,
	})
}

// writeJSONAPIDocument writes a JSON:API error document with the one error e.
func (h *Handler) writeJSONAPIDocument(ctx context.Context, w http.ResponseWriter, status int, e jsonAPIError) {
	w.Header().Set("Content-Type", jsonAPIMediaType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(jsonAPIDocument{Errors: []jsonAPIError{e}}); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// classifyTooLarge maps the errors of a limit: an oversized body or a budget
// that can never fit to 413, a full body budget to 503, and a source over its
// failure budget to 429. ok is false for any other error.
func classifyTooLarge(err error) (errorClass, bool) {
	switch {
	case errors.Is(err, workspace.ErrBusy):
		return errorClass{
			status: http.StatusServiceUnavailable,
			title:  titleServiceUnavailable,
			detail: "server is busy: try again after the Retry-After time",
			code:   errcode.StateVersionBusy,
			args:   nil,
		}, true
	case errors.Is(err, workspace.ErrTooLarge), errors.Is(err, workspace.ErrBudgetTooSmall):
		return errorClass{
			status: http.StatusRequestEntityTooLarge, title: "too large", detail: err.Error(), code: "", args: nil,
		}, true
	case errors.Is(err, identity.ErrRateLimited):
		// A source over its failure budget (ADR 53).
		return errorClass{
			status: http.StatusTooManyRequests,
			title:  "too many requests",
			detail: "too many failed sign-ins from this address: try again later",
			code:   "",
			args:   nil,
		}, true
	}

	return unclassified(), false
}

// classifyNamespace maps the namespace errors of a write: a fenced write to
// 409, a namespace homed in another region to 421, and a database that does
// not answer to 503. ok is false for any other error.
func classifyNamespace(err error) (errorClass, bool) {
	switch {
	case errors.Is(err, outputshare.ErrUnknownProducer):
		// The body is the body of a workspace that does not exist. The error
		// text names the check and the producer, so it never reaches the
		// caller.
		return errorClass{
			status: http.StatusNotFound, title: notFoundTitle, detail: notFoundTitle,
			code: errcode.WorkspaceNotFound, args: nil,
		}, true
	case errors.Is(err, namespace.ErrFenced):
		// The copy of the sentinel only: a wrap adds the name of the
		// operation, which the log holds.
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: namespace.ErrFenced.Error(), code: "", args: nil,
		}, true
	case errors.Is(err, namespace.ErrNotHome):
		return errorClass{
			status: http.StatusMisdirectedRequest,
			title:  "misdirected request",
			detail: err.Error(),
			code:   "",
			args:   nil,
		}, true
	case errors.Is(err, maintenance.ErrInMaintenance):
		return errorClass{
			status: http.StatusServiceUnavailable,
			title:  titleServiceUnavailable,
			detail: maintenance.ErrInMaintenance.Error() + ": try again after the Retry-After time",
			code:   "",
			args:   nil,
		}, true
	case errors.Is(err, namespace.ErrUnavailable):
		return errorClass{
			status: http.StatusServiceUnavailable,
			title:  titleServiceUnavailable,
			detail: "database unavailable: try again after the Retry-After time",
			code:   "",
			args:   nil,
		}, true
	}

	return unclassified(), false
}

// classifyConflict maps the errors of a request that conflicts with the state
// of the workspace to 409: a lock, a pending state version, a client-encrypted
// state, a concurrent change. ok is false for any other error. The order of
// the cases decides the code of an error that matches more than one case.
func classifyConflict(err error) (errorClass, bool) {
	switch {
	case errors.Is(err, outputshare.ErrClientEncrypted):
		// The detail names the reason and no object: the error of the
		// checker holds the check and the unit for the audit log.
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: clientEncryptedDetail,
			code: errcode.WorkspaceClientEncrypted, args: nil,
		}, true
	case errors.Is(err, workspace.ErrNotLocked):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(),
			code: errcode.WorkspaceNotLocked, args: nil,
		}, true
	case errors.Is(err, workspace.ErrLockMismatch):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(),
			code: errcode.WorkspaceLockMismatch, args: nil,
		}, true
	case errors.Is(err, workspace.ErrClientEncrypted):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(),
			code: errcode.WorkspaceClientEncrypted, args: nil,
		}, true
	case errors.Is(err, workspace.ErrAlreadyExists):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(),
			code: errcode.WorkspaceAlreadyExists, args: nil,
		}, true
	case errors.Is(err, workspace.ErrLocked), errors.Is(err, workspace.ErrLockedByOther):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(),
			code: errcode.WorkspaceLocked, args: nil,
		}, true
	case errors.Is(err, workspace.ErrPendingState),
		errors.Is(err, workspace.ErrConcurrentModification),
		errors.Is(err, workspace.ErrTooManyPending):
		return errorClass{
			status: http.StatusConflict, title: titleConflict, detail: err.Error(), code: "", args: nil,
		}, true
	}

	return unclassified(), false
}

// classify maps domain errors to a status, title, detail and code safe to
// show callers. Unknown errors map to 500 with a generic detail and the code
// server.internal.
func classify(err error) errorClass {
	if class, ok := classifyTooLarge(err); ok {
		return class
	}

	if class, ok := classifyNamespace(err); ok {
		return class
	}

	if class, ok := classifyConflict(err); ok {
		return class
	}

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return errorClass{
			status: http.StatusUnauthorized, title: titleUnauthorized, detail: "unauthenticated", code: "", args: nil,
		}
	case errors.Is(err, errMissingFilter):
		return errorClass{
			status: http.StatusBadRequest,
			title:  titleBadRequest,
			detail: err.Error(),
			code:   "",
			args:   nil,
		}
	case errors.Is(err, workspace.ErrOverrideRefused), errors.Is(err, sessionauth.ErrCSRF),
		errors.Is(err, sessionauth.ErrOtherSite):
		// The detail names the switch and the action that allow the override,
		// or the CSRF header that a console session request lacks.
		return errorClass{status: http.StatusForbidden, title: titleForbidden, detail: err.Error(), code: "", args: nil}
	case errors.Is(err, identity.ErrDenied), errors.Is(err, workspace.ErrUploadDenied):
		return errorClass{
			status: http.StatusForbidden,
			title:  titleForbidden,
			detail: titleForbidden,
			code:   "",
			args:   nil,
		}
	case errors.Is(err, workspace.ErrNotFound):
		return errorClass{
			status: http.StatusNotFound, title: notFoundTitle, detail: err.Error(),
			code: errcode.WorkspaceNotFound, args: nil,
		}
	case errors.Is(err, namespace.ErrNotFound),
		errors.Is(err, workspace.ErrNoState), errors.Is(err, workspace.ErrStateVersionNotFound),
		errors.Is(err, workspace.ErrResourceNotFound), errors.Is(err, workspace.ErrNoStateAt):
		return errorClass{status: http.StatusNotFound, title: notFoundTitle, detail: err.Error(), code: "", args: nil}
	case errors.Is(err, errInvalidBody),
		errors.Is(err, errInvalidQuery),
		errors.Is(err, audit.ErrInvalidReason),
		errors.Is(err, errRevealPathTooLong),
		errors.Is(err, workspace.ErrInvalidName),
		errors.Is(err, namespace.ErrInvalidName),
		errors.Is(err, identity.ErrInvalidObject),
		errors.Is(err, workspace.ErrInvalidState),
		errors.Is(err, workspace.ErrStaleSerial),
		errors.Is(err, workspace.ErrLineageMismatch),
		errors.Is(err, workspace.ErrChecksumMismatch):
		return errorClass{
			status: http.StatusUnprocessableEntity, title: "unprocessable", detail: err.Error(), code: "", args: nil,
		}
	default:
		return errorClass{
			status: http.StatusInternalServerError, title: titleInternal, detail: titleInternal,
			code: errcode.ServerInternal, args: nil,
		}
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
