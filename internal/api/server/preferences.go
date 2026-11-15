package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
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

// codedError is an error with its status, its code of the catalog and the
// values of the arguments of the code. The code has the form <area>.<reason>.
// writeError builds the answer with errcode.New, so the catalog checks the
// code and its arguments. An argument names a field of the document and never
// holds a value from the request.
type codedError struct {
	status  int
	code    string
	message string
	args    []errcode.Value
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
		status: http.StatusNotImplemented, code: errcode.PreferencesUnavailable, args: nil,
		message: "the store of this server keeps no preferences",
	}
	errPreferencesNotPerson = &codedError{
		status: http.StatusForbidden, code: errcode.PreferencesPersonRequired, args: nil,
		message: "preferences belong to a person: sign in as a person to read or change them",
	}
	errPreferencesPersonGone = &codedError{
		status: http.StatusNotFound, code: errcode.PreferencesPersonNotFound, args: nil,
		message: "the person of this request does not exist",
	}
	errPreferencesMediaType = &codedError{
		status: http.StatusUnsupportedMediaType, code: errcode.PreferencesMediaTypeUnsupported, args: nil,
		message: "content type must be " + mergePatchType,
	}
	errPreferencesTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.PreferencesBodyTooLarge, args: nil,
		message: preferences.ErrBodyTooLarge.Error(),
	}
	errPreferencesBody = &codedError{
		status: http.StatusBadRequest, code: errcode.PreferencesBodyInvalid, args: nil,
		message: preferences.ErrInvalidBody.Error(),
	}
	errPreferencesUnknownField = &codedError{
		status: http.StatusBadRequest, code: errcode.PreferencesFieldUnknown, args: nil,
		message: preferences.ErrUnknownField.Error(),
	}
	errPreferencesIfMatch = &codedError{
		status: http.StatusBadRequest, code: errcode.PreferencesIfMatchInvalid, args: nil,
		message: "If-Match must be one version in quotes, as the ETag of a read gives it, or *",
	}
	errPreferencesChanged = &codedError{
		status: http.StatusPreconditionFailed, code: errcode.PreferencesPreconditionFailed, args: nil,
		message: preferences.ErrPreconditionFailed.Error(),
	}
	errPreferencesWeakTag = &codedError{
		status: http.StatusPreconditionFailed, code: errcode.PreferencesPreconditionFailed, args: nil,
		message: "If-Match holds a weak tag, which never matches: send the ETag of a read as it is",
	}
	errPreferencesReadOnly = &codedError{
		status: http.StatusBadRequest, code: errcode.PreferencesFieldReadOnly,
		args:    []errcode.Value{errcode.Identifier("field", preferences.FieldUpdatedAt)},
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
			status: http.StatusBadRequest, code: errcode.PreferencesFieldInvalid,
			args: []errcode.Value{errcode.Identifier("field", field.Field)}, message: field.Error(),
		}
	case errors.Is(err, preferences.ErrBodyTooLarge):
		return errPreferencesTooLarge
	case errors.Is(err, preferences.ErrInvalidBody):
		return errPreferencesBody
	case errors.Is(err, preferences.ErrUnknownField):
		return errPreferencesUnknownField
	case errors.Is(err, preferences.ErrReadOnlyField):
		return errPreferencesReadOnly
	case errors.Is(err, preferences.ErrPreconditionFailed):
		return errPreferencesChanged
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
// person that never changed a preference. It is a time in RFC 3339 format,
// in UTC, with milliseconds.
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
	UpdatedAt          *string                 `json:"updatedAt"`
}

// updatePreferencesRequest is the merge patch of the PATCH route, for the
// description. The handler reads the body with preferences.ParsePatch, so
// this type is never decoded: a test proves that its members are the members
// that the parser accepts. Each member is optional. A member that is null
// sets the field back to its default.
type updatePreferencesRequest struct {
	Theme              *string                      `dustpan:"nullable" json:"theme,omitempty"`
	ColorMode          *string                      `dustpan:"nullable" json:"colorMode,omitempty"`
	TerminalSchemes    *terminalSchemesPatchRequest `dustpan:"nullable" json:"terminalSchemes,omitempty"`
	ShortcutHints      *string                      `dustpan:"nullable" json:"shortcutHints,omitempty"`
	SingleKeyShortcuts *string                      `dustpan:"nullable" json:"singleKeyShortcuts,omitempty"`
	ShortcutHintStyle  *string                      `dustpan:"nullable" json:"shortcutHintStyle,omitempty"`
	Language           *string                      `dustpan:"nullable" json:"language,omitempty"`
	FormattingLocale   *string                      `dustpan:"nullable" json:"formattingLocale,omitempty"`
	HourCycle          *string                      `dustpan:"nullable" json:"hourCycle,omitempty"`
	DateStyle          *string                      `dustpan:"nullable" json:"dateStyle,omitempty"`
	FirstDayOfWeek     *string                      `dustpan:"nullable" json:"firstDayOfWeek,omitempty"`
	TimeZone           *string                      `dustpan:"nullable" json:"timeZone,omitempty"`
	Digits             *string                      `dustpan:"nullable" json:"digits,omitempty"`
	ResolvedLanguage   *string                      `dustpan:"nullable" json:"resolvedLanguage,omitempty"`
}

// terminalSchemesPatchRequest is the terminalSchemes member of a patch: it
// names the schemes that change.
type terminalSchemesPatchRequest struct {
	Light *string `dustpan:"nullable" json:"light,omitempty"`
	Dark  *string `dustpan:"nullable" json:"dark,omitempty"`
}

// terminalSchemesResponse holds the color scheme of each color mode.
type terminalSchemesResponse struct {
	Light string `json:"light"`
	Dark  string `json:"dark"`
}

// updatedAtLayout is the RFC 3339 format of updatedAt: UTC, with milliseconds.
const updatedAtLayout = "2006-01-02T15:04:05.000Z07:00"

// preferencesETag returns the ETag of a document: its version in quotes. The
// version changes with each change of a preference, so a write with If-Match
// refuses a document that another request changed after the read.
func preferencesETag(d preferences.Document) string {
	return `"` + strconv.FormatInt(d.Version(), 10) + `"`
}

// preferencesIfMatch returns the version of the If-Match header, and whether
// the request has a precondition. A request with no If-Match header, or with *,
// has none. The header holds one version in quotes, as the ETag of a read
// gives it, or *, which matches the preferences of any person. A weak tag, W/
// and then one version in quotes, gives errPreferencesWeakTag, because it
// never matches. An empty value, a second header and any other value give
// errPreferencesIfMatch: a client that builds the header from an empty
// variable must not lose its precondition without an error.
func preferencesIfMatch(r *http.Request) (int64, bool, error) {
	values := apidesc.HeaderValues(r, "If-Match")
	if len(values) == 0 || (len(values) == 1 && strings.TrimSpace(values[0]) == "*") {
		return 0, false, nil
	}

	if len(values) > 1 {
		return 0, false, errPreferencesIfMatch
	}

	raw := strings.TrimSpace(values[0])

	if weak, ok := strings.CutPrefix(raw, "W/"); ok {
		if _, err := quotedVersion(weak); err != nil {
			return 0, false, err
		}

		// If-Match uses strong comparison, and a weak tag never matches in
		// strong comparison. So the precondition is false, also when the
		// version is the current one.
		return 0, false, errPreferencesWeakTag
	}

	v, err := quotedVersion(raw)
	if err != nil {
		return 0, false, err
	}

	return v, true, nil
}

// quotedVersion returns the version of s, one version in quotes, as the ETag
// of a read gives it. It returns errPreferencesIfMatch for any other value.
func quotedVersion(s string) (int64, error) {
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, errPreferencesIfMatch
	}

	v, err := strconv.ParseInt(s[1:len(s)-1], 10, 64)
	if err != nil || v < 0 {
		return 0, errPreferencesIfMatch
	}

	return v, nil
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
	var updated *string

	if !d.UpdatedAt.IsZero() {
		text := d.UpdatedAt.UTC().Format(updatedAtLayout)
		updated = &text
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
			Method: http.MethodGet,
			Path:   preferencesPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionCallerRead,
			},
			Resolve:      resolveCaller,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.getPreferences,
		},
		{
			Method: http.MethodPatch, Path: preferencesPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerManagePreferences}, Resolve: resolveCaller,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.patchPreferences,
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
	w.Header().Set("ETag", preferencesETag(doc))
	h.writeJSON(ctx, w, http.StatusOK, preferencesOf(doc))
}

// patchPreferences answers PATCH /api/dustpan/v1/me/preferences. The body is a
// JSON merge patch (IETF RFC 7396): a field that the body leaves out keeps its
// value, and null sets a field back to its default. It answers 200 with the
// full document, also for a patch that changes nothing. Such a patch does not
// change updatedAt and writes no audit entry. The ETag header holds the version
// of the new document. A request with the If-Match header of an older version
// answers 412 and changes nothing. Any other refused patch changes nothing and
// writes one failure entry.
func (h *Handler) patchPreferences(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.preferences == nil {
		h.writeError(ctx, w, errPreferencesUnavailable)

		return
	}

	patch, err := readPreferencesPatch(w, r, p)
	if err != nil {
		h.refusePatch(w, r, p, err)

		return
	}

	version, hasPrecondition, err := preferencesIfMatch(r)
	if err != nil {
		h.refusePatch(w, r, p, err)

		return
	}

	var ifMatch *int64
	if hasPrecondition {
		ifMatch = &version
	}

	res, err := h.preferences.Update(ctx, p, patch, callerEntry(r, p, identity.ActionCallerManagePreferences), ifMatch)
	if err != nil {
		h.refusePatch(w, r, p, preferencesError(err))

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", preferencesETag(res.Document))
	h.writeJSON(ctx, w, http.StatusOK, preferencesOf(res.Document))
}

// readPreferencesPatch reads and checks the request of a PATCH: the caller is
// a person, the content type is the merge patch type, the body is within the
// limit, and the patch passes every check of its fields. It returns a coded
// error for each refusal.
func readPreferencesPatch(w http.ResponseWriter, r *http.Request, p identity.Principal) (preferences.Patch, error) {
	// Defense in depth: the service refuses a caller that is not a person
	// too. This check stays so that the body of such a caller is never read.
	if !p.ActsAsPerson() {
		return nil, errPreferencesNotPerson
	}

	if !mergePatchContentType(r) {
		w.Header().Set("Accept-Patch", mergePatchType)

		return nil, errPreferencesMediaType
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, preferences.MaxBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return nil, errPreferencesTooLarge
	}

	if err != nil {
		return nil, errPreferencesBody
	}

	patch, err := preferences.ParsePatch(body)
	if err != nil {
		return nil, preferencesError(err)
	}

	return patch, nil
}

// refusePatch answers the refusal err of a PATCH and writes one entry for the
// refused change, best effort: a lost entry never changes the answer. The
// entry has the action of the change and the principal reference of the caller
// as object, as the entry of a successful change has. Its outcome is denied for
// a caller that is not a person, and failure for any other refusal. It names no
// value of the request. A refused request that the server cannot attribute to
// a change (a store in maintenance mode answers 503) writes none, and a change
// that the If-Match header refuses with 412 writes none.
func (h *Handler) refusePatch(w http.ResponseWriter, r *http.Request, p identity.Principal, err error) {
	// A request with an outdated If-Match, or with a weak tag, changes nothing
	// and is no failure of the caller, so it writes no entry. Both answer 412.
	coded, ok := errors.AsType[*codedError](err)
	if ok && coded.status < http.StatusInternalServerError && coded.status != http.StatusPreconditionFailed {
		outcome := audit.OutcomeFailure
		if errors.Is(err, errPreferencesNotPerson) {
			outcome = audit.OutcomeDenied
		}

		h.appendServerChange(r, p, serverChange{
			at: h.now(), action: identity.Action(preferences.AuditAction), object: h.preferences.Object(r.Context(), p),
			outcome: outcome,
		})
	}

	h.writeError(r.Context(), w, err)
}

// mergePatchContentType reports whether the Content-Type of r is the merge
// patch type, with no parameter or with charset=utf-8 only.
func mergePatchContentType(r *http.Request) bool {
	mediaType, params, err := mime.ParseMediaType(apidesc.Header(r, "Content-Type"))
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
