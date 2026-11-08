package server

// Sign-in provider routes of the installation API. The providers belong to
// the installation, so every route acts on the object @server:
//
//   - server:read-tokens lists and reads the providers, as it reads the
//     session policy,
//   - server:manage-sign-in creates, changes and deletes them.
//
// The list holds the providers of the configuration of the server too,
// with the source "static": the API never changes them. No answer holds a
// client secret, only clientSecretSet. A change from a console session
// needs a sign-in inside the token freshness window.

import (
	"errors"
	"maps"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
)

// Paths of the sign-in provider routes. They are not under
// /api/server/sign-in/, where a provider ID names the start of a sign-in.
const (
	signInProvidersPath = "/api/server/sign-in-providers"
	signInProviderPath  = "/api/server/sign-in-providers/{id}"
)

// Sources of a provider on the wire.
const (
	providerSourceAPI    = "api"
	providerSourceStatic = "static"
)

// Errors of the sign-in provider routes.
var (
	errProviderCreateBody = errors.New(`invalid sign-in provider body: provide {"id":"...","issuer":"https://...",` +
		`"clientID":"...","clientSecret":"..."} and optionally displayName, scopes, autoCreate and enabled`)
	errProviderChangeBody = errors.New(`invalid sign-in provider change: provide "version" and one or more of ` +
		`issuer, displayName, clientID, clientSecret, scopes, autoCreate and enabled`)
)

// providerResponse is one sign-in provider on the wire. Source is "api"
// for a provider of the API and "static" for a provider of the
// configuration of the server, which has no issuer, client ID, scopes,
// version or times in the answer. ClientSecretSet is true when the
// provider has a client secret; no answer holds the secret.
//
// Contract: never remove or rename these fields.
type providerResponse struct {
	CreatedAt       *string  `json:"createdAt"`
	UpdatedAt       *string  `json:"updatedAt"`
	ID              string   `json:"id"`
	Source          string   `json:"source"`
	Issuer          string   `json:"issuer"`
	DisplayName     string   `json:"displayName"`
	ClientID        string   `json:"clientID"`
	Scopes          []string `json:"scopes"`
	Version         int64    `json:"version"`
	AutoCreate      bool     `json:"autoCreate"`
	Enabled         bool     `json:"enabled"`
	ClientSecretSet bool     `json:"clientSecretSet"`
	// Mode is "oidc" or "oauth". The fields below are set for "oauth"
	// only.
	Mode             string          `json:"mode"`
	AuthorizationURL string          `json:"authorizationURL,omitempty"`
	TokenURL         string          `json:"tokenURL,omitempty"`
	UserinfoURL      string          `json:"userinfoURL,omitempty"`
	Claims           *providerClaims `json:"claims,omitempty"`
}

// providerClaims is the claim mapping of a provider in the oauth mode: the
// names of the claims of the user-info answer.
type providerClaims struct {
	Subject  string `json:"subject"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// providerClaimsChange is the claim mapping in a request. A missing claim
// takes its default at a create, and keeps its value at a change.
type providerClaimsChange struct {
	Subject  *string `json:"subject"`
	Name     *string `json:"name"`
	Username *string `json:"username"`
	Email    *string `json:"email"`
}

// providersResponse is the list of the sign-in providers, ordered by ID.
//
// Contract: never remove or rename the providers field.
type providersResponse struct {
	Providers []providerResponse `json:"providers"`
}

// providerCreateRequest is the body of a new provider. displayName is the
// ID when absent; scopes is empty when absent; enabled is true when
// absent; mode is "oidc" when absent. autoCreate is true when absent for
// the oidc mode, and false for the oauth mode: any account at a plain
// OAuth 2.0 provider, such as any GitHub user, can sign in. A provider in the oidc mode
// needs issuer; a provider in the oauth mode needs authorizationURL,
// tokenURL and userinfoURL, and no issuer.
type providerCreateRequest struct {
	ID               *string               `json:"id"`
	Mode             *string               `json:"mode"`
	AuthorizationURL *string               `json:"authorizationURL"`
	TokenURL         *string               `json:"tokenURL"`
	UserinfoURL      *string               `json:"userinfoURL"`
	Claims           *providerClaimsChange `json:"claims"`
	Issuer           *string               `json:"issuer"`
	DisplayName      *string               `json:"displayName"`
	ClientID         *string               `json:"clientID"`
	ClientSecret     *string               `json:"clientSecret"`
	Scopes           *[]string             `json:"scopes"`
	AutoCreate       *bool                 `json:"autoCreate"`
	Enabled          *bool                 `json:"enabled"`
}

// providerChangeRequest is the body of a provider change. version is the
// version that the change read. A missing field keeps its value.
type providerChangeRequest struct {
	Mode             *string               `json:"mode"`
	AuthorizationURL *string               `json:"authorizationURL"`
	TokenURL         *string               `json:"tokenURL"`
	UserinfoURL      *string               `json:"userinfoURL"`
	Claims           *providerClaimsChange `json:"claims"`
	Issuer           *string               `json:"issuer"`
	DisplayName      *string               `json:"displayName"`
	ClientID         *string               `json:"clientID"`
	ClientSecret     *string               `json:"clientSecret"`
	Scopes           *[]string             `json:"scopes"`
	AutoCreate       *bool                 `json:"autoCreate"`
	Enabled          *bool                 `json:"enabled"`
	Version          *int64                `json:"version"`
}

// signInProviderRoutes returns the sign-in provider rows of the route
// table. A handler with no provider service mounts no route.
func (h *Handler) signInProviderRoutes() []Route {
	if h.signIn == nil || h.signIn.admin == nil {
		return nil
	}

	read := []identity.Action{identity.ActionServerReadTokens}
	manage := []identity.Action{identity.ActionServerManageSignIn}

	return []Route{
		{
			Method: http.MethodGet, Path: signInProvidersPath, Scope: ScopeAuthorized, Actions: read,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.listSignInProviders,
		},
		{
			Method: http.MethodPost, Path: signInProvidersPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.createSignInProvider,
		},
		{
			Method: http.MethodGet, Path: signInProviderPath, Scope: ScopeAuthorized, Actions: read,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.readSignInProvider,
		},
		{
			Method: http.MethodPatch, Path: signInProviderPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.changeSignInProvider,
		},
		{
			Method: http.MethodDelete, Path: signInProviderPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false, Serve: h.deleteSignInProvider,
		},
	}
}

// listSignInProviders serves GET /api/server/sign-in-providers.
func (h *Handler) listSignInProviders(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	stored, err := h.signIn.admin.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := providersResponse{Providers: make([]providerResponse, 0, len(h.signIn.providers)+len(stored))}

	for _, id := range slices.Sorted(maps.Keys(h.signIn.providers)) {
		out.Providers = append(out.Providers, staticProviderOf(h.signIn.providers[id]))
	}

	for i := range stored {
		if _, static := h.signIn.providers[stored[i].ID]; !static {
			out.Providers = append(out.Providers, storedProviderOf(&stored[i]))
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, out)
}

// readSignInProvider serves GET /api/server/sign-in-providers/{id}.
func (h *Handler) readSignInProvider(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()
	id := r.PathValue("id")

	if p, ok := h.signIn.providers[id]; ok {
		h.writeJSON(ctx, w, http.StatusOK, staticProviderOf(p))

		return
	}

	p, err := h.signIn.admin.Get(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, storedProviderOf(&p))
}

// createSignInProvider serves POST /api/server/sign-in-providers: it
// answers 201 with the new provider.
func (h *Handler) createSignInProvider(
	w http.ResponseWriter, r *http.Request, caller identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	p, err := h.createProvider(w, r, caller)
	if err != nil {
		h.failProviderChange(w, r, caller, p.ID, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, storedProviderOf(&p))
}

// createProvider checks the freshness of a console session, decodes the
// body of r and creates the provider. On an error, the ID of the result is
// the ID of the body, when it has one.
func (h *Handler) createProvider(
	w http.ResponseWriter, r *http.Request, caller identity.Principal,
) (session.ProviderConfig, error) {
	var none session.ProviderConfig

	if err := h.checkTokenFreshness(r); err != nil {
		return none, err
	}

	var req providerCreateRequest
	if err := decodeStrict(w, r, &req, errProviderCreateBody); err != nil {
		return none, err
	}

	if req.ID == nil || req.ClientID == nil || req.ClientSecret == nil {
		return none, errProviderCreateBody
	}

	none.ID = *req.ID

	mode := session.ProviderMode(valueOr(req.Mode, string(session.ModeOIDC)))
	if mode == session.ModeOIDC && req.Issuer == nil {
		return none, errProviderCreateBody
	}

	in := session.ProviderInput{
		ID: *req.ID, Mode: mode, Issuer: valueOr(req.Issuer, ""), DisplayName: valueOr(req.DisplayName, *req.ID),
		ClientID: *req.ClientID, ClientSecret: *req.ClientSecret, Scopes: valueOr(req.Scopes, []string{}),
		OAuth: session.OAuthSettings{
			AuthorizationURL: valueOr(req.AuthorizationURL, ""), TokenURL: valueOr(req.TokenURL, ""),
			UserinfoURL: valueOr(req.UserinfoURL, ""), SubjectClaim: req.Claims.subject(), NameClaim: req.Claims.name(),
			UsernameClaim: req.Claims.username(), EmailClaim: req.Claims.email(),
		},
		AutoCreate: valueOr(req.AutoCreate, mode != session.ModeOAuth), Enabled: valueOr(req.Enabled, true),
	}

	p, err := h.signIn.admin.Create(r.Context(), callerEntry(r, caller, identity.ActionServerManageSignIn), in)
	if err != nil {
		return none, err
	}

	return p, nil
}

// changeSignInProvider serves PATCH /api/server/sign-in-providers/{id}:
// it answers 200 with the changed provider.
func (h *Handler) changeSignInProvider(
	w http.ResponseWriter, r *http.Request, caller identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	id := r.PathValue("id")

	p, err := h.changeProvider(w, r, caller, id)
	if err != nil {
		h.failProviderChange(w, r, caller, id, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, storedProviderOf(&p))
}

// changeProvider checks the freshness of a console session, decodes the
// body of r and changes the provider id.
func (h *Handler) changeProvider(
	w http.ResponseWriter, r *http.Request, caller identity.Principal, id string,
) (session.ProviderConfig, error) {
	if err := h.checkTokenFreshness(r); err != nil {
		return session.ProviderConfig{}, err
	}

	var req providerChangeRequest

	if err := decodeChange(w, r, &req, errProviderChangeBody, req.empty); err != nil {
		return session.ProviderConfig{}, err
	}

	c := session.ProviderChange{
		Mode: (*session.ProviderMode)(req.Mode), Issuer: req.Issuer, DisplayName: req.DisplayName,
		ClientID: req.ClientID, ClientSecret: req.ClientSecret, Scopes: req.Scopes, OAuth: req.oauthChange(),
		AutoCreate: req.AutoCreate, Enabled: req.Enabled, Version: *req.Version,
	}

	p, err := h.signIn.admin.Update(r.Context(), callerEntry(r, caller, identity.ActionServerManageSignIn), id, c)
	if err != nil {
		return session.ProviderConfig{}, err
	}

	return p, nil
}

// deleteSignInProvider serves DELETE /api/server/sign-in-providers/{id}:
// it answers 204. A provider with principals answers 409.
func (h *Handler) deleteSignInProvider(
	w http.ResponseWriter, r *http.Request, caller identity.Principal, _ identity.Object,
) {
	ctx := r.Context()
	id := r.PathValue("id")

	err := h.checkTokenFreshness(r)
	if err == nil {
		err = h.signIn.admin.Delete(ctx, callerEntry(r, caller, identity.ActionServerManageSignIn), id)
	}

	if err != nil {
		h.failProviderChange(w, r, caller, id, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// failProviderChange writes the failure entry of a refused change of the
// provider id and answers err. The entry names an invalid ID with
// invalidProviderRef, so a request cannot put text of any length into
// the audit log.
func (h *Handler) failProviderChange(
	w http.ResponseWriter, r *http.Request, caller identity.Principal, id string, err error,
) {
	object := invalidProviderRef
	if session.ValidProviderID(id) {
		object = session.ProviderRef(id)
	}

	h.appendServerChange(r, caller, serverChange{
		at: h.signIn.sessions.Now(), action: identity.ActionServerManageSignIn, object: object,
		outcome: audit.OutcomeFailure,
	})
	h.writeError(r.Context(), w, err)
}

// invalidProviderRef is the audit object of a refused change of a provider
// ID that is not valid. No valid ID holds "?", so it names no provider.
const invalidProviderRef = "sign-in-provider:?"

// storedProviderOf returns the wire form of a provider of the API.
func storedProviderOf(p *session.ProviderConfig) providerResponse {
	out := providerResponse{
		CreatedAt: optionalWireTime(p.CreatedAt), UpdatedAt: optionalWireTime(p.UpdatedAt), ID: p.ID,
		Source: providerSourceAPI, Issuer: p.Issuer, DisplayName: p.DisplayName, ClientID: p.ClientID,
		Scopes: valueOr(&p.Scopes, []string{}), Version: p.Version, AutoCreate: p.AutoCreate, Enabled: p.Enabled,
		ClientSecretSet: len(p.SealedSecret) > 0, Mode: string(p.Mode), AuthorizationURL: "", TokenURL: "",
		UserinfoURL: "", Claims: nil,
	}

	if p.Mode == session.ModeOAuth {
		out.AuthorizationURL, out.TokenURL, out.UserinfoURL = p.OAuth.AuthorizationURL, p.OAuth.TokenURL,
			p.OAuth.UserinfoURL
		out.Claims = &providerClaims{
			Subject: p.OAuth.SubjectClaim, Name: p.OAuth.NameClaim, Username: p.OAuth.UsernameClaim,
			Email: p.OAuth.EmailClaim,
		}
	}

	return out
}

// empty reports whether req has no version or changes nothing.
func (req *providerChangeRequest) empty() bool {
	return req.Version == nil || (req.Issuer == nil && req.DisplayName == nil && req.ClientID == nil &&
		req.ClientSecret == nil && req.Scopes == nil && req.AutoCreate == nil && req.Enabled == nil &&
		req.Mode == nil && req.noOAuth())
}

// noOAuth reports whether req changes no OAuth setting.
func (req *providerChangeRequest) noOAuth() bool {
	return req.AuthorizationURL == nil && req.TokenURL == nil && req.UserinfoURL == nil && req.Claims == nil
}

// oauthChange returns the change of the OAuth settings of req.
func (req *providerChangeRequest) oauthChange() session.OAuthChange {
	c := session.OAuthChange{
		AuthorizationURL: req.AuthorizationURL, TokenURL: req.TokenURL, UserinfoURL: req.UserinfoURL,
		SubjectClaim: nil, NameClaim: nil, UsernameClaim: nil, EmailClaim: nil,
	}

	if req.Claims != nil {
		c.SubjectClaim, c.NameClaim, c.UsernameClaim, c.EmailClaim = req.Claims.Subject, req.Claims.Name,
			req.Claims.Username, req.Claims.Email
	}

	return c
}

// subject returns the subject claim of c, or "" when c or the claim is
// absent.
func (c *providerClaimsChange) subject() string {
	if c == nil {
		return ""
	}

	return valueOr(c.Subject, "")
}

// name returns the name claim of c, or "".
func (c *providerClaimsChange) name() string {
	if c == nil {
		return ""
	}

	return valueOr(c.Name, "")
}

// username returns the username claim of c, or "".
func (c *providerClaimsChange) username() string {
	if c == nil {
		return ""
	}

	return valueOr(c.Username, "")
}

// email returns the email claim of c, or "".
func (c *providerClaimsChange) email() string {
	if c == nil {
		return ""
	}

	return valueOr(c.Email, "")
}

// staticProviderOf returns the wire form of a provider of the
// configuration of the server.
func staticProviderOf(p SignInProvider) providerResponse {
	return providerResponse{
		CreatedAt: nil, UpdatedAt: nil, ID: p.ID, Source: providerSourceStatic, Issuer: "",
		DisplayName: p.DisplayName, ClientID: "", Scopes: []string{}, Version: 0, AutoCreate: p.AutoCreate,
		Enabled: true, ClientSecretSet: true, Mode: string(session.ModeOIDC), AuthorizationURL: "", TokenURL: "",
		UserinfoURL: "", Claims: nil,
	}
}

// classifyProviders maps the sign-in provider errors to their status and
// message.
func classifyProviders(err error) (failure, bool) {
	switch {
	case errors.Is(err, errProviderCreateBody), errors.Is(err, errProviderChangeBody),
		errors.Is(err, session.ErrInvalidProvider):
		return fail(http.StatusBadRequest, err.Error()), true
	case errors.Is(err, session.ErrProviderNotFound):
		return fail(http.StatusNotFound, "sign-in provider not found"), true
	case errors.Is(err, session.ErrProviderStatic), errors.Is(err, session.ErrProviderInUse),
		errors.Is(err, session.ErrIssuerChanged), errors.Is(err, session.ErrProviderChanged),
		errors.Is(err, session.ErrProviderExists):
		return fail(http.StatusConflict, err.Error()), true
	case errors.Is(err, secret.ErrKeyNotConfigured):
		return fail(http.StatusConflict, secret.ErrKeyNotConfigured.Error()), true
	default:
		return fail(0, ""), false
	}
}

// valueOr returns *v, or fallback for nil.
func valueOr[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}

	return *v
}
