package runnerpools

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
)

// proxySettingsPath is the route of the proxy settings of a pool.
const proxySettingsPath = poolPath + "/proxy-settings"

// maxProxySettingsBody bounds the body of a save of the proxy settings.
const maxProxySettingsBody = 16 << 10

// proxySettingsType is the one content type of a save of the proxy settings.
const proxySettingsType = "application/json"

// fieldUpstreamCredential is the field of the write-only credential.
const fieldUpstreamCredential = "upstreamCredential"

// versionTag is the form of the If-Match header of a save: one version in
// quotes, as the ETag of a read gives it.
var versionTag = regexp.MustCompile(`^"(0|[1-9]\d{0,17})"$`)

// The errors of the proxy settings routes that no domain sentinel carries.
var (
	errProxySettingsMediaType = problem.Fixed(http.StatusUnsupportedMediaType, errcode.RequestContentTypeUnsupported,
		"content type must be application/json")
	errProxySettingsTooLarge = &problem.Error{Problem: problem.New(
		http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge, "the proxy settings are larger than 16 KB",
		errcode.Quantity("maxBytes", maxProxySettingsBody),
	)}
	errProxySettingsIfMatch = problem.Fixed(http.StatusBadRequest, errcode.RequestIfMatchInvalid,
		`If-Match must be the version of the proxy settings in quotes, as the ETag of a read gives it`)
	errProxySettingsChanged = problem.Fixed(http.StatusPreconditionFailed, errcode.RequestPreconditionFailed,
		"the proxy settings changed since the version that you read: read them again")
)

// proxySettingsJSON is the effective proxy settings of a pool. It never holds
// the upstream credential: CredentialSet says whether the pool has one.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type proxySettingsJSON struct {
	AllowedRanges []string `json:"allowedRanges"`
	UpstreamProxy string   `json:"upstreamProxy"`
	CredentialSet bool     `json:"credentialSet"`
	// Source is default for a pool with no saved settings, or set.
	Source    string     `json:"source"`
	Version   int64      `json:"version"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

// proxySettingsRequest is the body of a save. UpstreamCredential is write-only:
// a body with no such field keeps the stored credential, and null removes it.
type proxySettingsRequest struct {
	AllowedRanges      []string                `json:"allowedRanges"`
	UpstreamProxy      string                  `json:"upstreamProxy"`
	UpstreamCredential *upstreamCredentialJSON `json:"upstreamCredential"`
}

// upstreamCredentialJSON is the upstream credential in a save.
type upstreamCredentialJSON struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// proxySettingsLabels names the proxy settings routes in the description.
func proxySettingsLabels() map[string]apidesc.Label {
	settings := apidesc.Typed[proxySettingsJSON]("RunnerPoolProxySettings")
	request := apidesc.Typed[proxySettingsRequest]("SaveRunnerPoolProxySettingsRequest",
		apidesc.Named[upstreamCredentialJSON]("RunnerPoolUpstreamCredential"))

	return map[string]apidesc.Label{
		apidesc.Pattern(http.MethodGet, proxySettingsPath): apidesc.Name("getRunnerPoolProxySettings", tagRunners).
			WithETag().
			On(apidesc.KindNamespace).
			WithBodies(apidesc.NoBody, settings),
		apidesc.Pattern(http.MethodPut, proxySettingsPath): apidesc.Name("saveRunnerPoolProxySettings", tagRunners).
			WithIfMatch().
			WithETag().
			On(apidesc.KindNamespace).
			WithBodies(request, settings),
	}
}

// proxySettingsRoutes returns the rows of the proxy settings of a pool. A nil
// proxy service means that the server serves no job proxy: no route is
// mounted. A read needs namespace:read-runners and a save needs
// namespace:manage-runners.
func (h *Handler) proxySettingsRoutes() []Route {
	if h.proxies == nil {
		return nil
	}

	read := []identity.Action{identity.ActionNamespaceReadRunners}
	manage := []identity.Action{identity.ActionNamespaceManageRunners}

	return []Route{
		{
			Method: http.MethodGet, Path: proxySettingsPath, Scope: ScopeAuthorized, Actions: read,
			Serve: h.getProxySettings,
		},
		{
			Method: http.MethodPut, Path: proxySettingsPath, Scope: ScopeAuthorized, Actions: manage,
			Serve: h.putProxySettings,
		},
	}
}

// getProxySettings answers the effective proxy settings of the pool of the
// path, with the version as the ETag.
func (h *Handler) getProxySettings(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	pool, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	settings, err := h.proxies.Settings(ctx, pool)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	h.writeProxySettings(w, r, &settings)
}

// putProxySettings saves the proxy settings of the pool of the path. The
// checks of the body come before any read of the store.
func (h *Handler) putProxySettings(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	req, err := h.proxySettingsPut(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	pool, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	req.Pool = pool

	settings, err := h.proxies.PutSettings(ctx, req)
	if errors.Is(err, runner.ErrConflict) {
		err = errProxySettingsChanged
	}

	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	h.writeProxySettings(w, r, &settings)
}

// proxySettingsPut checks the content type, the size, the If-Match header and
// the body of a save, and returns the request of the service with no pool.
func (h *Handler) proxySettingsPut(r *http.Request, c *call) (*proxysessionsvc.PutRequest, error) {
	media, _, mediaErr := mime.ParseMediaType(apidesc.Header(r, "Content-Type"))
	if mediaErr != nil || media != proxySettingsType {
		return nil, errProxySettingsMediaType
	}

	if len(c.body) > maxProxySettingsBody {
		return nil, errProxySettingsTooLarge
	}

	ifMatch := ""

	if header := apidesc.Header(r, "If-Match"); header != "" {
		m := versionTag.FindStringSubmatch(header)
		if m == nil {
			return nil, errProxySettingsIfMatch
		}

		ifMatch = m[1]
	}

	var body proxySettingsRequest
	if err := decodeBody(c.body, &body); err != nil {
		return nil, err
	}

	// A second read of the body tells a field with null from a field that is
	// not there: null removes the credential, and no field keeps it.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(c.body, &fields); err != nil {
		return nil, errBadBody
	}

	_, present := fields[fieldUpstreamCredential]

	change := proxysessionsvc.CredentialChange{Present: present, Value: nil}
	if body.UpstreamCredential != nil {
		change.Value = &proxysessionsvc.Credential{
			Username: body.UpstreamCredential.Username, Password: body.UpstreamCredential.Password,
		}
	}

	// The entry names the principal. The service adds the changes, which hold
	// no URL and no credential.
	e := h.manageEntry(r, c, runner.EventPoolProxySettingsChanged, "")
	e.Changes = nil

	// The caller sets the pool after the checks of the body.
	var req proxysessionsvc.PutRequest

	req.AllowedRanges, req.UpstreamURL, req.Credential = body.AllowedRanges, body.UpstreamProxy, change
	req.IfMatch, req.Entry = ifMatch, e

	return &req, nil
}

// writeProxySettings answers the settings with the version as the ETag.
func (h *Handler) writeProxySettings(w http.ResponseWriter, r *http.Request, s *proxysessionsvc.Settings) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(s.Version, 10)+`"`)
	h.writeJSON(r.Context(), w, http.StatusOK, proxySettingsJSON{
		AllowedRanges: s.AllowedRanges, UpstreamProxy: s.UpstreamURL, CredentialSet: s.CredentialSet,
		Source: s.Source, Version: s.Version, UpdatedBy: s.UpdatedBy, UpdatedAt: optTime(s.UpdatedAt),
	})
}
