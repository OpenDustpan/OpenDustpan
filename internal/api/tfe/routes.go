package tfe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopePublic serves without sign-in. Only routes on the public list may
	// use it.
	ScopePublic Scope = iota + 1
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// every declared action on the resolved object before the handler runs.
	ScopeAuthorized
	// ScopeUpload serves without sign-in: the random token in the path is
	// the credential, like the update-lease scope of the other tool adapter.
	// The capability stands in for the declared action: creating the state
	// version already authorized it, and it scopes to one upload alone. The
	// Authorizer does not run. Only the state upload routes may use it.
	ScopeUpload
)

// ObjectResolver returns the authorization object for a request: the unit
// that the request acts on. Resolvers for routes with a body read it with
// http.MaxBytesReader and restore r.Body before they return, so the handler
// can read it again.
type ObjectResolver func(w http.ResponseWriter, r *http.Request) (identity.Object, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated (except for public routes) and authorized it. Public routes
// get zero principal and object. Authorized routes get the principal and the
// resolved object.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs, how to resolve the object of each action from the
// request, and how to serve it. NewHandler registers every route from the
// table that buildRoutes returns, so a route without a row is never served.
// The registration wrapper is the single place that enforces the table: for
// every non-public route it authenticates, resolves the object, authorizes
// every declared action on that object, and only then calls the handler.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern, for example /api/v2/ping.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions the route needs. Only authorized routes set
	// it, and the wrapper authorizes every entry on the resolved object.
	Actions []identity.Action
	// Resolve returns the object that Actions apply to. Only authorized
	// routes set it.
	Resolve ObjectResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return slices.Concat(
		h.discoveryRoutes(),
		h.workspaceRoutes(),
		h.lockRoutes(),
		h.overrideRoutes(),
		h.stateRoutes(),
		h.stateWriteRoutes(),
	)
}

// discoveryRoutes returns service discovery, ping and the entitlement set.
func (h *Handler) discoveryRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/.well-known/terraform.json",
			Scope:   ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.discovery,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/v2/ping",
			Scope:   ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.ping,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/organizations/{organization}/entitlement-set",
			// The organization is the namespace: the route reads it, so
			// namespace:read stays the gate. The handler answers 404 for an
			// unknown organization and never creates one.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceRead},
			Resolve: resolveOrganization,
			Serve:   h.entitlementSet,
		},
	}
}

// workspaceRoutes returns workspace read, create and list.
func (h *Handler) workspaceRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/v2/organizations/{organization}/workspaces/{workspace}",
			// A read needs unit:read on opentofu:<namespace>/<name>. The
			// handler answers 404 for an unknown organization or workspace.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveWorkspace,
			Serve:   h.readWorkspace,
		},
		{
			Method: http.MethodDelete,
			Path:   "/api/v2/organizations/{organization}/workspaces/{workspace}",
			// A delete needs unit:manage on opentofu:<namespace>/<name>. It is
			// a soft delete (ADR 47): the workspace keeps its state until
			// the final delete. A locked workspace answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveWorkspace,
			Serve:   h.deleteWorkspace,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/v2/organizations/{organization}/workspaces",
			// A creation needs unit:manage on the unit the body names. The
			// resolver reads the name from the body, so a bad name answers
			// 422 before authorization runs.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveCreateWorkspace,
			Serve:   h.createWorkspace,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/workspaces/{id}",
			// A read needs unit:read. The resolver maps the ID to the unit,
			// so authorization still names the unit.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.readWorkspaceByID,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/organizations/{organization}/workspaces",
			// A list needs namespace:read on the organization. The handler
			// then keeps only the workspaces the caller may unit:read and
			// answers 404 for an unknown organization.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceRead},
			Resolve: resolveOrganization,
			Serve:   h.listWorkspaces,
		},
	}
}

// lockRoutes returns lock, unlock and force-unlock by workspace ID.
func (h *Handler) lockRoutes() []Route {
	return []Route{
		{
			Method: http.MethodPost,
			Path:   "/api/v2/workspaces/{id}/actions/lock",
			// A lock needs unit:write. The server mints the lock ID and
			// records the subject of the caller as the holder.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.lockWorkspace,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/v2/workspaces/{id}/actions/unlock",
			// An unlock needs unit:write. The holder unlocks. Another
			// principal gets 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.unlockWorkspace,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/v2/workspaces/{id}/actions/force-unlock",
			// A force-unlock needs unit:manage. It releases the lock
			// whoever holds it. A force-unlock of an unlocked workspace
			// answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.forceUnlockWorkspace,
		},
	}
}

// stateRoutes returns the state read routes: the current state version, one
// state version, its download, its outputs, the state version list, and the
// masked resource list with one masked resource (ADR 41 decisions 1 to 3, 8
// and 9). Every route needs unit:read. The download and the single output
// route also need unit:read-secrets, as they answer plain secret values.
func (h *Handler) stateRoutes() []Route {
	routes := []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions",
			// A list needs unit:read on opentofu:<namespace>/<name>. The
			// resolver reads the two filters and builds the workspace unit.
			// A missing filter answers 400; an unknown workspace answers
			// 404.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveStateVersionList,
			Serve:   h.listStateVersions,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/workspaces/{id}/current-state-version",
			// A state read needs unit:read on opentofu:<namespace>/<name>.
			// The resolver maps the ID to the unit. A workspace without
			// state answers 404.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.readCurrentState,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions/{state_version_id}",
			// A state read needs unit:read. The resolver maps the version
			// to the unit of its workspace. Unknown IDs and versions that
			// are not finalized answer 404.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveStateVersionID,
			Serve:   h.readStateVersion,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions/{state_version_id}/download",
			// A download answers plain secret values, so it needs unit:read
			// and unit:read-secrets on the unit of the workspace.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
			Resolve: h.resolveStateVersionID,
			Serve:   h.downloadState,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/workspaces/{id}/current-state-version-outputs",
			// A state read needs unit:read on opentofu:<namespace>/<name>.
			// The resolver maps the ID to the unit. A workspace without
			// state answers 404.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveWorkspaceID,
			Serve:   h.readCurrentOutputs,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-version-outputs/{output_id}",
			// A single output answers a plain secret value, so it needs
			// unit:read and unit:read-secrets. The resolver maps the output
			// to the unit of its workspace. Unknown outputs answer 404.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets},
			Resolve: h.resolveStateOutputID,
			Serve:   h.readStateOutput,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions/{state_version_id}/resources",
			// A masked resource list answers the structure with each secret
			// value as a marker, so it needs unit:read on the unit of the
			// workspace. The resolver maps the version to that unit. Unknown
			// versions answer 404; a state the client encrypted answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveStateVersionID,
			Serve:   h.listStateResources,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/v2/state-versions/{state_version_id}/resources/{address}",
			// One masked resource answers its instances with each secret
			// value as a marker, so it needs unit:read on the unit of the
			// workspace. The address in the path is path-escaped and matches
			// verbatim. Unknown versions and addresses answer 404; a state
			// the client encrypted answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: h.resolveStateVersionID,
			Serve:   h.getStateResource,
		},
		h.revealRoute(),
	}

	return slices.Concat(routes, h.stateDiffRoutes())
}

// stateWriteRoutes returns the state write routes: create a state version,
// upload its content and its JSON rendering through capabilities, and
// restore an old version as new current state.
func (h *Handler) stateWriteRoutes() []Route {
	return []Route{
		{
			Method: http.MethodPost,
			Path:   "/api/v2/workspaces/{id}/state-versions",
			// A state version creation needs unit:write on
			// opentofu:<namespace>/<name>. The resolver maps the ID to the
			// unit without reading the body: the create body holds the
			// base64 outputs document and is far larger than the shared
			// resolver limit. A caller without the lock gets 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: h.resolveStateVersionWorkspace,
			Serve:   h.createStateVersion,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/v2/state-versions/{state_version_id}/actions/restore",
			// A restore needs unit:write on opentofu:<namespace>/<name>.
			// The resolver maps the version to the unit of its workspace.
			// Unknown IDs and versions that are not finalized answer 404.
			// A lock that another principal holds answers 409.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: h.resolveStateVersionID,
			Serve:   h.restoreStateVersion,
		},
		{
			Method: http.MethodPut,
			Path:   "/api/v2/state-uploads/{id}/{token}",
			// A state upload needs unit:write, granted when the capability
			// was minted at creation. The token in the path is the
			// credential, so the route serves without sign-in under the
			// upload scope. The PUT carries no Authorization header.
			Scope:   ScopeUpload,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: nil,
			Serve:   h.uploadState,
		},
		{
			Method: http.MethodPut,
			Path:   "/api/v2/json-state-uploads/{id}/{token}",
			// The JSON rendering upload needs unit:write, granted when the
			// capability was minted at creation. The token in the path is
			// the credential, so the route serves without sign-in under the
			// upload scope. The server accepts the rendering and discards
			// it: it keeps the state itself and the outputs of the create.
			Scope:   ScopeUpload,
			Actions: []identity.Action{identity.ActionUnitWrite},
			Resolve: nil,
			Serve:   h.uploadJSONState,
		},
	}
}

// Routes returns a copy of the route table. The Actions lists are copied
// too, so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Actions = append([]identity.Action(nil), route.Actions...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// patternMux is an http.ServeMux that records every pattern registered on
// it, so the route test can list every served route. All handler routes go
// through Handle or HandleFunc, which both record. The inner mux stays
// private and is built only by newPatternMux, so no code outside this file
// can serve a pattern without recording it. TestNoMuxBypass enforces this:
// it fails if any other file in this package builds a mux or touches the
// inner mux.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

// newPatternMux returns a patternMux ready to register routes.
func newPatternMux() *patternMux {
	return &patternMux{servemux: http.NewServeMux(), patterns: nil}
}

// Handle registers pattern and records it.
func (m *patternMux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.Handle(pattern, handler)
}

// HandleFunc registers pattern and records it.
func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.HandleFunc(pattern, handler)
}

// ServeHTTP implements http.Handler.
func (m *patternMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.servemux.ServeHTTP(w, r)
}

// Patterns returns a copy of the recorded patterns.
func (m *patternMux) Patterns() []string {
	return append([]string(nil), m.patterns...)
}

// resolveOrganization returns the namespace in the organization path value.
func resolveOrganization(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(ns), nil
}

// resolveWorkspace returns the unit in the path values: the namespace in
// organization and the workspace name in workspace.
func resolveWorkspace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		return identity.Object{}, err
	}

	name, err := workspace.ParseName(r.PathValue("workspace"))
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(workspace.Ref{Namespace: ns, Name: name})
}

// resolveWorkspaceID returns the unit for the workspace ID in the path
// value. It reads the body once with http.MaxBytesReader and restores r.Body
// before returning, so the handler can read it again. The current GET handler
// reads no body; the restore keeps the shared shape for future lock POSTs on
// the same ID. An unknown ID fails with absence, which the wrapper answers as
// 403 for a caller without rights and 404 otherwise.
func (h *Handler) resolveWorkspaceID(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	ws, err := h.workspaces.GetByID(r.Context(), workspace.ID(r.PathValue("id")))
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ws.Ref)
}

// resolveStateVersionWorkspace returns the unit for the workspace ID in the
// path value, without reading the body: the state version create body holds
// the base64 outputs document and is far larger than the shared resolver
// limit, so the create handler reads it. An unknown ID fails with absence,
// which the wrapper answers as 403 for a caller without rights and 404
// otherwise.
func (h *Handler) resolveStateVersionWorkspace(
	_ http.ResponseWriter, r *http.Request,
) (identity.Object, error) {
	ws, err := h.workspaces.GetByID(r.Context(), workspace.ID(r.PathValue("id")))
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ws.Ref)
}

// enforce wraps one route table row. Public and upload routes run
// unwrapped: public routes need no credential and upload routes carry their
// capability in the path. Every other route authenticates, resolves the
// object, authorizes every declared action on it, and only then runs the
// handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	switch route.Scope {
	case ScopePublic, ScopeUpload:
		return h.enforcePublic(route)
	case ScopeAuthorized:
		return h.enforceAuthorized(route)
	}

	return h.enforceAuthorized(route)
}

// enforcePublic serves a route that needs no sign-in.
func (h *Handler) enforcePublic(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var guest identity.Principal

		var noObject identity.Object

		route.Serve(w, r, guest, noObject)
	}
}

// enforceAuthorized serves a route that needs a signed-in principal and the
// Authorizer to allow every declared action on the resolved object. A
// resolver that reads the database runs before authorization, so an
// unknown ID could tell a caller what exists. Two answers are the same 404:
// an object that does not exist, and an object that exists but that the
// caller cannot read. A caller that can read the object, but not do the
// action, answers 403: it already knows that the object exists.
func (h *Handler) enforceAuthorized(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			if isAbsence(err) {
				h.writeNotFound(ctx, w, notFoundDetail)
			} else {
				h.writeError(ctx, w, err)
			}

			return
		}

		for _, action := range route.Actions {
			if err := h.guard.Authorize(ctx, r, p, action, obj); err != nil {
				h.writeDenied(ctx, w, p, obj, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		route.Serve(w, r, p, obj)
	}
}

// notFoundDetail is the one detail of an object that does not exist or
// that the caller cannot read, so the two answers do not differ.
const notFoundDetail = "not found"

// writeDenied answers a refused action: the 404 of an absent object when p
// cannot read obj either, else the error itself.
func (h *Handler) writeDenied(
	ctx context.Context,
	w http.ResponseWriter,
	p identity.Principal,
	obj identity.Object,
	err error,
) {
	if !errors.Is(err, identity.ErrDenied) {
		h.writeError(ctx, w, err)

		return
	}

	hide, hideErr := h.hidden(ctx, p, obj)
	if hideErr != nil {
		// The read check failed (a store error, not a refusal): answer
		// that error, never a 404 that hides it.
		h.writeError(ctx, w, hideErr)

		return
	}

	if hide {
		h.writeNotFound(ctx, w, notFoundDetail)

		return
	}

	h.writeError(ctx, w, err)
}

// hidden reports whether p cannot read obj: a unit without unit:read, or a
// namespace without namespace:read. Any other object is never hidden. It
// returns the error of a read check that fails for another reason than a
// refusal.
func (h *Handler) hidden(ctx context.Context, p identity.Principal, obj identity.Object) (bool, error) {
	read := identity.ActionNamespaceRead
	if obj.IsUnit() {
		read = identity.ActionUnitRead
	}

	if obj.IsServer() || obj.IsCaller() || obj.Namespace() == "" {
		return false, nil
	}

	err := h.authz.Authorize(ctx, p, read, obj)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, identity.ErrDenied):
		return true, nil
	default:
		return false, fmt.Errorf("authorize %s: %w", read, err)
	}
}

// isAbsence reports whether err means the resolver found nothing: an unknown
// workspace, state version or namespace. Only these errors hide behind the
// fallback authorization; every other resolver error answers as before.
func isAbsence(err error) bool {
	return errors.Is(err, workspace.ErrNotFound) ||
		errors.Is(err, workspace.ErrStateVersionNotFound) ||
		errors.Is(err, namespace.ErrNotFound)
}
