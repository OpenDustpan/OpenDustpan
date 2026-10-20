package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/preferences"
)

// preferencesPath is the route of the preferences of the caller.
const preferencesPath = "/api/dustpan/v1/me/preferences"

// mergePatchType is the only media type of a PATCH of the preferences. A page
// on another site cannot send it without a CORS preflight, and only the
// console origins that the server allows pass the preflight. The type check
// and the CORS allowlist together guard against a change from another site.
const mergePatchType = "application/merge-patch+json"

// codedError is an error with its status, its stable machine code and the
// arguments of the code. The code has the form <area>.<reason>. An argument
// names a field of the document and never holds a value from the request.
type codedError struct {
	status  int
	code    string
	message string
	args    map[string]any
}

func (e *codedError) Error() string { return e.message }

// codedResponse is the error body of a route that has stable machine codes:
// the fields of errorResponse, the code and its arguments. Code stays the HTTP
// status, as in every error of this API.
type codedResponse struct {
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs,omitempty"`
}

// The errors of the preferences routes that no domain sentinel carries.
var (
	errPreferencesUnavailable = &codedError{
		status: http.StatusNotImplemented, code: "preferences.unavailable", args: nil,
		message: "the store of this server keeps no preferences",
	}
	errPreferencesNotPerson = &codedError{
		status: http.StatusForbidden, code: "preferences.person-required", args: nil,
		message: "preferences belong to a person: sign in as a person to read or change them",
	}
	errPreferencesPersonGone = &codedError{
		status: http.StatusNotFound, code: "preferences.person-not-found", args: nil,
		message: "the person of this request does not exist",
	}
	errPreferencesMediaType = &codedError{
		status: http.StatusUnsupportedMediaType, code: "preferences.media-type-unsupported", args: nil,
		message: "content type must be " + mergePatchType,
	}
	errPreferencesTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: "preferences.body-too-large", args: nil,
		message: preferences.ErrBodyTooLarge.Error(),
	}
	errPreferencesBody = &codedError{
		status: http.StatusBadRequest, code: "preferences.body-invalid", args: nil,
		message: preferences.ErrInvalidBody.Error(),
	}
	errPreferencesUnknownField = &codedError{
		status: http.StatusBadRequest, code: "preferences.field-unknown", args: nil,
		message: preferences.ErrUnknownField.Error(),
	}
	errPreferencesReadOnly = &codedError{
		status: http.StatusBadRequest, code: "preferences.field-read-only",
		args:    map[string]any{"field": preferences.FieldUpdatedAt},
		message: preferences.ErrReadOnlyField.Error(),
	}
)

// preferencesError maps an error of the preferences service or of the patch
// parser to a coded error. An error that it does not know passes through, so
// the other classifiers answer it: a store in maintenance mode answers 503,
// and anything else answers a plain 500.
func preferencesError(err error) error {
	var field *preferences.FieldError

	switch {
	case errors.As(err, &field):
		return &codedError{
			status: http.StatusBadRequest, code: "preferences.field-invalid",
			args: map[string]any{"field": field.Field}, message: field.Error(),
		}
	case errors.Is(err, preferences.ErrBodyTooLarge):
		return errPreferencesTooLarge
	case errors.Is(err, preferences.ErrInvalidBody):
		return errPreferencesBody
	case errors.Is(err, preferences.ErrUnknownField):
		return errPreferencesUnknownField
	case errors.Is(err, preferences.ErrReadOnlyField):
		return errPreferencesReadOnly
	case errors.Is(err, preferences.ErrNotPerson):
		return errPreferencesNotPerson
	case errors.Is(err, preferences.ErrPersonNotFound):
		return errPreferencesPersonGone
	default:
		return err
	}
}

// preferencesResponse is the document of the preferences on the wire. A
// nullable field that has no value is null, and updatedAt is null for a
// person that never changed a preference.
//
// Contract: the console reads these fields. Never remove or rename them.
type preferencesResponse struct {
	Theme              string                  `json:"theme"`
	ColorMode          string                  `json:"colorMode"`
	TerminalSchemes    terminalSchemesResponse `json:"terminalSchemes"`
	ShortcutHints      string                  `json:"shortcutHints"`
	SingleKeyShortcuts string                  `json:"singleKeyShortcuts"`
	ShortcutHintStyle  string                  `json:"shortcutHintStyle"`
	Language           *string                 `json:"language"`
	FormattingLocale   *string                 `json:"formattingLocale"`
	HourCycle          *string                 `json:"hourCycle"`
	DateStyle          string                  `json:"dateStyle"`
	FirstDayOfWeek     *string                 `json:"firstDayOfWeek"`
	TimeZone           *string                 `json:"timeZone"`
	Digits             *string                 `json:"digits"`
	ResolvedLanguage   *string                 `json:"resolvedLanguage"`
	UpdatedAt          *int64                  `json:"updatedAt"`
}

// terminalSchemesResponse holds the color scheme of each color mode.
type terminalSchemesResponse struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// nullable returns nil for the empty text, which is the null of a field.
func nullable(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

// preferencesOf converts a document to its wire form.
func preferencesOf(d preferences.Document) preferencesResponse {
	var updated *int64

	if !d.UpdatedAt.IsZero() {
		ms := d.UpdatedAt.UnixMilli()
		updated = &ms
	}

	return preferencesResponse{
		Theme:              d.Theme,
		ColorMode:          d.ColorMode,
		TerminalSchemes:    terminalSchemesResponse{Light: d.TerminalSchemes.Light, Dark: d.TerminalSchemes.Dark},
		ShortcutHints:      d.ShortcutHints,
		SingleKeyShortcuts: d.SingleKeyShortcuts,
		ShortcutHintStyle:  d.ShortcutHintStyle,
		Language:           nullable(d.Language),
		FormattingLocale:   nullable(d.FormattingLocale),
		HourCycle:          nullable(d.HourCycle),
		DateStyle:          d.DateStyle,
		FirstDayOfWeek:     nullable(d.FirstDayOfWeek),
		TimeZone:           nullable(d.TimeZone),
		Digits:             nullable(d.Digits),
		ResolvedLanguage:   nullable(d.ResolvedLanguage),
		UpdatedAt:          updated,
	}
}

// preferencesRoutes returns the rows of the preferences of the caller. A
// request under act-as acts on its subject, so both routes read and change
// the preferences of the person and never of the agent. A caller that is not
// a person answers 403.
func (h *Handler) preferencesRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: preferencesPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerRead}, Resolve: resolveCaller, ActionsFor: nil,
			Serve: h.getPreferences,
		},
		{
			Method: http.MethodPatch, Path: preferencesPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerManagePreferences}, Resolve: resolveCaller,
			ActionsFor: nil, Serve: h.patchPreferences,
		},
	}
}

// getPreferences answers GET /api/dustpan/v1/me/preferences: the full
// document, with the default of each field that the person never set.
func (h *Handler) getPreferences(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.preferences == nil {
		h.writeError(ctx, w, errPreferencesUnavailable)

		return
	}

	// Defense in depth: the service refuses a caller that is not a person too.
	if !p.ActsAsPerson() {
		h.writeError(ctx, w, errPreferencesNotPerson)

		return
	}

	doc, err := h.preferences.Get(ctx, p)
	if err != nil {
		h.writeError(ctx, w, preferencesError(err))

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, preferencesOf(doc))
}

// patchPreferences answers PATCH /api/dustpan/v1/me/preferences. The body is a
// JSON merge patch (RFC 7396): a field that the body leaves out keeps its
// value, and null sets a field back to its default. It answers 200 with the
// full document, also for a patch that changes nothing. Such a patch does not
// change updatedAt and writes no audit entry.
func (h *Handler) patchPreferences(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.preferences == nil {
		h.writeError(ctx, w, errPreferencesUnavailable)

		return
	}

	if !p.ActsAsPerson() {
		h.writeError(ctx, w, errPreferencesNotPerson)

		return
	}

	if !mergePatchContentType(r) {
		w.Header().Set("Accept-Patch", mergePatchType)
		h.writeError(ctx, w, errPreferencesMediaType)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, preferences.MaxBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		h.writeError(ctx, w, errPreferencesTooLarge)

		return
	}

	if err != nil {
		h.writeError(ctx, w, errPreferencesBody)

		return
	}

	patch, err := preferences.ParsePatch(body)
	if err != nil {
		h.writeError(ctx, w, preferencesError(err))

		return
	}

	res, err := h.preferences.Update(ctx, p, patch, callerEntry(r, p, identity.ActionCallerManagePreferences))
	if err != nil {
		h.writeError(ctx, w, preferencesError(err))

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, preferencesOf(res.Document))
}

// mergePatchContentType reports whether the Content-Type of r is the merge
// patch type, with no parameter or with charset=utf-8 only.
func mergePatchContentType(r *http.Request) bool {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != mergePatchType {
		return false
	}

	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}

	return true
}
