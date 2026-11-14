package server

// The settings resource of the installation and of a namespace. Each scope
// has one resource: GET answers each declared setting with its effective
// value and its source, and PATCH changes database values with a JSON merge
// patch. The resource has one revision, which is its ETag. A PATCH needs
// If-Match.
//
//   - server:read-settings and server:manage-settings act on @server,
//   - namespace:read-settings and namespace:manage-settings act on the
//     namespace.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
)

// The paths of the settings resources.
const (
	settingsPath          = "/api/dustpan/v1/settings"
	namespaceSettingsPath = "/api/dustpan/v1/namespaces/{namespace}/settings"
)

// settingValue is the value of a setting on the wire: a string, a boolean, a
// whole number or null. The kind of the setting says which.
type settingValue any

// settingBody is one setting on the wire. value, default and databaseValue
// are a string, a boolean or a whole number, as kind says, or null. A secret
// has null in all three and says in set whether the static layer gave it a
// value.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type settingBody struct {
	Key             string       `json:"key"`
	Scope           string       `json:"scope"`
	Kind            string       `json:"kind"`
	Value           settingValue `json:"value"`
	Default         settingValue `json:"default"`
	Source          string       `json:"source"`
	Locked          bool         `json:"locked"`
	LockedBy        string       `json:"lockedBy"`
	Shadowed        bool         `json:"shadowed"`
	DatabaseValue   settingValue `json:"databaseValue"`
	Apply           string       `json:"apply"`
	Secret          bool         `json:"secret"`
	Set             bool         `json:"set"`
	DatabaseLayer   bool         `json:"databaseLayer"`
	ReducesSecurity bool         `json:"mayReduceSecurity"`
	Description     string       `json:"description"`
}

// settingsPatchRequest is the body of a change on the wire: a JSON merge patch
// with one member for each setting. A value sets the database value of the
// setting, and null deletes it.
type settingsPatchRequest map[string]settingValue

// settingsBody is the resource of one scope on the wire.
type settingsBody struct {
	Revision int64         `json:"revision"`
	Settings []settingBody `json:"settings"`
}

// settingValueSchema declares the schema of the value of a setting, which the
// kind of the setting selects.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var settingValueSchema = apidesc.Declare[settingValue](map[string]any{
	"description": "A string, a boolean or a whole number, as the kind of the setting says. It is null for a " +
		"secret, and for a database value that does not exist.",
})

// The errors of the settings routes that no domain sentinel carries.
var (
	errSettingsUnavailable = &codedError{
		status: http.StatusNotImplemented, code: errcode.SettingsUnavailable, args: nil,
		message: "the store of this server keeps no settings",
	}
	errSettingsMediaType = &codedError{
		status: http.StatusUnsupportedMediaType, code: errcode.RequestContentTypeUnsupported, args: nil,
		message: "content type must be " + mergePatchType,
	}
	errSettingsTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.RequestBodyTooLarge,
		args:    []errcode.Value{errcode.Quantity("maxBytes", setting.MaxBodyBytes)},
		message: "the body is larger than the limit of the settings",
	}
	errSettingsBody = &codedError{
		status: http.StatusBadRequest, code: errcode.SettingsPatchInvalid, args: nil,
		message: "the body must be one JSON object of settings, with a string, a boolean, a whole number " +
			"or null for each value",
	}
	errSettingsIfMatchRequired = &codedError{
		status: http.StatusPreconditionRequired, code: errcode.RequestPreconditionRequired, args: nil,
		message: `send If-Match with the ETag of the last read, for example "3"`,
	}
	errSettingsIfMatchInvalid = &codedError{
		status: http.StatusBadRequest, code: errcode.RequestIfMatchInvalid, args: nil,
		message: `If-Match must be one revision in quotes, as the ETag of a read gives it, for example "3"`,
	}
	errSettingsChanged = &codedError{
		status: http.StatusPreconditionFailed, code: errcode.RequestPreconditionFailed, args: nil,
		message: "the settings changed since the revision of If-Match: read them again",
	}
)

// settingsError maps an error of the settings service to a coded error. An
// error that it does not know passes through, so the other classifiers answer
// it: a namespace that does not exist answers 404, a store in maintenance
// mode answers 503, and anything else answers a plain 500.
func settingsError(err error) error {
	keyErr, hasKey := errors.AsType[*setting.KeyError](err)

	switch {
	case errors.Is(err, setting.ErrLocked) && hasKey:
		return &codedError{
			status: http.StatusConflict, code: errcode.SettingsLocked,
			args: []errcode.Value{
				errcode.Identifier("key", keyErr.Key), errcode.Identifier("source", string(keyErr.Source)),
			},
			message: "the setting " + keyErr.Key + " is set by " + keyErr.By + ": change it there, or send null " +
				"to delete the database value that it hides",
		}
	case errors.Is(err, setting.ErrStaticOnly) && hasKey:
		return &codedError{
			status:  http.StatusConflict,
			code:    errcode.SettingsStaticOnly,
			args:    []errcode.Value{errcode.Identifier("key", keyErr.Key)},
			message: "the setting " + keyErr.Key + " has no database layer: only a flag, a variable or the file sets it",
		}
	case errors.Is(err, setting.ErrInvalidValue) && hasKey:
		return &codedError{
			status: http.StatusBadRequest, code: errcode.SettingsValueInvalid,
			args:    []errcode.Value{errcode.Identifier("key", keyErr.Key)},
			message: "the value of " + keyErr.Key + " is not valid: " + keyErr.Reason,
		}
	case errors.Is(err, setting.ErrUnknownKey):
		return &codedError{
			status: http.StatusBadRequest, code: errcode.SettingsKeyUnknown, args: nil,
			message: "the body names a setting that this scope does not declare",
		}
	case errors.Is(err, setting.ErrInvalidPatch):
		return errSettingsBody
	case errors.Is(err, setting.ErrPreconditionFailed):
		return errSettingsChanged
	default:
		return err
	}
}

// settingsOf converts a resource to its wire form.
func settingsOf(res setting.Resource) settingsBody {
	out := settingsBody{Revision: res.Revision, Settings: make([]settingBody, 0, len(res.Items))}

	for i := range res.Items {
		out.Settings = append(out.Settings, settingOf(&res.Items[i]))
	}

	return out
}

// settingOf converts one item to its wire form.
func settingOf(item *setting.Item) settingBody {
	body := settingBody{
		Key: item.Key, Scope: string(item.Level), Kind: string(item.Kind), Value: nil, Default: nil,
		Source: string(item.Source), Locked: item.Locked, LockedBy: item.LockedBy, Shadowed: item.Shadowed,
		DatabaseValue: nil, Apply: string(item.Apply), Secret: item.Secret, Set: item.Set,
		DatabaseLayer: item.Database, ReducesSecurity: item.ReducesSecurity, Description: item.Doc,
	}

	if item.Secret {
		return body
	}

	body.Value = typedValue(item.Kind, item.Value)
	body.Default = typedValue(item.Kind, item.Default)

	if item.HasDatabaseValue {
		body.DatabaseValue = typedValue(item.Kind, item.DatabaseValue)
	}

	return body
}

// typedValue converts the text form of a value to its JSON form.
func typedValue(kind setting.Kind, text string) any {
	switch kind {
	case setting.KindBool:
		return text == "true"
	case setting.KindInt:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil
		}

		return n
	case setting.KindString:
		return text
	default:
		return nil
	}
}

// settingsETag returns the ETag of a revision.
func settingsETag(revision int64) string {
	return `"` + strconv.FormatInt(revision, 10) + `"`
}

// settingsIfMatch returns the revision of the If-Match header. The header
// holds one revision in quotes, as the ETag of a read gives it. A change needs
// it: a missing header answers 428, and any other form answers 400. A weak
// tag never matches, so it answers 412 as a revision that changed.
func settingsIfMatch(r *http.Request) (int64, error) {
	values := apidesc.HeaderValues(r, "If-Match")
	if len(values) == 0 {
		return 0, errSettingsIfMatchRequired
	}

	if len(values) > 1 {
		return 0, errSettingsIfMatchInvalid
	}

	raw := strings.TrimSpace(values[0])

	if weak, ok := strings.CutPrefix(raw, "W/"); ok {
		if _, err := quotedRevision(weak); err != nil {
			return 0, err
		}

		return 0, errSettingsChanged
	}

	return quotedRevision(raw)
}

// quotedRevision returns the revision in s, one number in quotes.
func quotedRevision(s string) (int64, error) {
	if len(s) < 3 || s[0] != '"' || s[len(s)-1] != '"' {
		return 0, errSettingsIfMatchInvalid
	}

	v, err := strconv.ParseInt(s[1:len(s)-1], 10, 64)
	if err != nil || v < 0 {
		return 0, errSettingsIfMatchInvalid
	}

	return v, nil
}

// settingsRoutes returns the rows of the settings resources. Nil service
// means the store keeps no settings: the routes answer 501.
func (h *Handler) settingsRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   settingsPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerReadSettings,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.getSettings,
		},
		{
			Method: http.MethodPatch,
			Path:   settingsPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerManageSettings,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.patchSettings,
		},
		{
			Method: http.MethodGet, Path: namespaceSettingsPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceReadSettings}, Resolve: resolveNamespacePath,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.getSettings,
		},
		{
			Method: http.MethodPatch, Path: namespaceSettingsPath, Scope: ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceManageSettings}, Resolve: resolveNamespacePath,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.patchSettings,
		},
	}
}

// settingScope returns the scope of the settings resource that obj names.
func settingScope(obj identity.Object) setting.Scope {
	if obj.Namespace() == "" {
		return setting.InstallationScope()
	}

	return setting.NamespaceScope(obj.Namespace())
}

// manageAction returns the action that changes the settings of obj.
func manageAction(obj identity.Object) identity.Action {
	if obj.Namespace() == "" {
		return identity.ActionServerManageSettings
	}

	return identity.ActionNamespaceManageSettings
}

// getSettings serves GET /api/dustpan/v1/settings and the namespace form. It
// answers 200 with each declared setting of the scope and the revision, which
// is also the ETag.
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if h.settings == nil {
		h.writeError(ctx, w, errSettingsUnavailable)

		return
	}

	res, err := h.settings.Get(ctx, settingScope(obj))
	if err != nil {
		h.writeError(ctx, w, settingsError(err))

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", settingsETag(res.Revision))
	h.writeJSON(ctx, w, http.StatusOK, settingsOf(res))
}

// patchSettings serves PATCH /api/dustpan/v1/settings and the namespace form.
// The body is a JSON merge patch: a member sets the database value of a
// setting, and null deletes it. All members change, or none do. It answers 200
// with the new resource and its ETag, also for a patch that changes nothing,
// which writes no audit entry and keeps the revision. A change that reduces
// security needs a fresh authentication. Any other refused change changes
// nothing and writes one failure entry.
func (h *Handler) patchSettings(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	if h.settings == nil {
		h.writeError(ctx, w, errSettingsUnavailable)

		return
	}

	scope := settingScope(obj)

	patch, revision, err := h.readSettingsPatch(w, r, scope)
	if err != nil {
		h.refuseSettings(w, r, p, obj, err)

		return
	}

	res, err := h.applySettings(r, p, obj, patch, revision)
	if err != nil {
		h.refuseSettings(w, r, p, obj, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", settingsETag(res.Resource.Revision))
	h.writeJSON(ctx, w, http.StatusOK, settingsOf(res.Resource))
}

// readSettingsPatch reads and checks the request of a PATCH: the content type,
// the size of the body, the patch with every value, and the If-Match header.
func (h *Handler) readSettingsPatch(
	w http.ResponseWriter, r *http.Request, scope setting.Scope,
) (setting.Patch, int64, error) {
	if !mergePatchContentType(r) {
		w.Header().Set("Accept-Patch", mergePatchType)

		return nil, 0, errSettingsMediaType
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, setting.MaxBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return nil, 0, errSettingsTooLarge
	}

	if err != nil {
		return nil, 0, errSettingsBody
	}

	patch, err := setting.ParsePatch(body, h.settings.Registry(), scope.Level())
	if err != nil {
		return nil, 0, settingsError(err)
	}

	revision, err := settingsIfMatch(r)
	if err != nil {
		return nil, 0, err
	}

	return patch, revision, nil
}

// applySettings applies patch. A change that reduces security first answers
// setting.ErrFreshStepRequired from the service, with nothing written. Then
// the request proves a fresh authentication, and the change runs again with it.
func (h *Handler) applySettings(
	r *http.Request, p identity.Principal, obj identity.Object, patch setting.Patch, revision int64,
) (setting.Result, error) {
	scope := settingScope(obj)
	req := setting.PatchRequest{Entry: h.settingsEntry(r, p, obj), IfMatch: revision, Fresh: false}

	res, err := h.settings.Patch(r.Context(), scope, patch, req)
	if !errors.Is(err, setting.ErrFreshStepRequired) {
		return res, settingsError(err)
	}

	if freshErr := h.requireFreshStep(r); freshErr != nil {
		return setting.Result{Resource: setting.Resource{Items: nil, Revision: 0}, Changed: nil}, freshErr
	}

	req.Fresh = true

	res, err = h.settings.Patch(r.Context(), scope, patch, req)

	return res, settingsError(err)
}

// requireFreshStep checks the fresh authentication of the request for a
// change that reduces security. An installation with no console sign-in has
// no authentication step to ask for, so the change needs none. Otherwise a
// request with an access token answers 403 with mfa.fresh-step-required, and
// a console session whose sign-in is too old answers 401.
func (h *Handler) requireFreshStep(r *http.Request) error {
	if h.signIn == nil {
		return nil
	}

	ctx := context.WithValue(r.Context(), freshnessKey{}, h.checkTokenFreshness)

	return requireFresh(r.WithContext(ctx))
}

// settingsEntry returns the audit entry of the caller p for a change of the
// settings of obj. The service sets the scope, the time, the kind and the
// outcome, and the store sets the action and the changed keys.
func (h *Handler) settingsEntry(r *http.Request, p identity.Principal, obj identity.Object) audit.Entry {
	entry := callerEntry(r, p, manageAction(obj))
	entry.Object = obj.String()

	return entry
}

// refuseSettings answers the refusal err of a PATCH and writes one failure
// entry for the refused change, best effort: a lost entry never changes the
// answer. A change that the revision refuses with 412 changes nothing and is
// no failure of the caller, so it writes no entry. A request that the server
// cannot attribute to a change (a store in maintenance mode answers 503)
// writes none either. The entry names no value of the request.
func (h *Handler) refuseSettings(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object, err error,
) {
	coded, ok := errors.AsType[*codedError](err)
	refused := ok && coded.status < http.StatusInternalServerError && coded.status != http.StatusPreconditionFailed

	// A sign-in that is too old refuses the change with 401, and it is a
	// refusal of the caller as well.
	if refused || errors.Is(err, errReauthenticate) {
		h.appendObjectChange(r, p, obj, serverChange{
			at: h.now(), action: manageAction(obj), object: obj.String(), outcome: audit.OutcomeFailure,
		})
	}

	h.writeError(r.Context(), w, err)
}
