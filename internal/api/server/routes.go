package server

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
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
)

// ObjectResolver returns the authorization object for a request: the
// installation itself.
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
	// Path is the ServeMux path pattern, for example /api/server/metrics.
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
//
// Contract: never remove or rename these endpoints or status codes. The
// console and operators read only this API, and they update on their own
// schedule.
func (h *Handler) buildRoutes() []Route {
	routes := append([]Route{}, h.coreRoutes()...)
	routes = append(routes, h.backupRoutes()...)
	routes = append(routes, h.checkRoutes()...)
	routes = append(routes, h.restoreRoutes()...)
	routes = append(routes, h.maintenanceRoutes()...)
	routes = append(routes, h.namespaceOverrideRoutes()...)
	routes = append(routes, h.ipAllowlistRoutes()...)
	routes = append(routes, h.principalRoutes()...)
	routes = append(routes, h.groupRoutes()...)
	routes = append(routes, h.roleRoutes()...)
	routes = append(routes, h.grantRoutes()...)
	routes = append(routes, h.tokenRoutes()...)
	routes = append(routes, h.ownTokenRoutes()...)

	return routes
}

// coreRoutes returns the namespace and metrics rows of the route table.
func (h *Handler) coreRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/server/namespaces",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerCreateNamespace},
			Resolve: resolveCreateNamespace,
			Serve:   h.createNamespace,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/metrics",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer,
			Serve:   h.serveMetrics,
		},
	}
}

// backupRoutes returns the backup rows of the route table (ADR 30).
func (h *Handler) backupRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/server/backups",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerBackup},
			Resolve: resolveServer,
			Serve:   h.startBackup,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/backups",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerBackup},
			Resolve: resolveServer,
			Serve:   h.listBackups,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/backups/{id}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerBackup},
			Resolve: resolveServer,
			Serve:   h.getBackup,
		},
	}
}

// restoreRoutes returns the restore rows of the route table (ADR 38
// decision 1): start a restore, list restores, and read one restore.
func (h *Handler) restoreRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/server/restores",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer,
			Serve:   h.startRestore,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/restores",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer,
			Serve:   h.listRestores,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/restores/{id}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer,
			Serve:   h.getRestore,
		},
	}
}

// checkRoutes returns the check rows of the route table (ADR 39 decision 3):
// backup check and upgrade check.
func (h *Handler) checkRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/server/backups/{id}/check",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerBackup},
			Resolve: resolveServer,
			Serve:   h.checkBackup,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/upgrade-check",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer,
			Serve:   h.serveUpgradeCheck,
		},
	}
}

// maintenanceRoutes returns the maintenance routes (ADR 38).
func (h *Handler) maintenanceRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/server/maintenance",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerMaintenance},
			Resolve: resolveServer,
			Serve:   h.getMaintenance,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/server/maintenance/enter",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerMaintenance},
			Resolve: resolveServer,
			Serve:   h.enterMaintenance,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/server/maintenance/leave",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerMaintenance},
			Resolve: resolveServer,
			Serve:   h.leaveMaintenance,
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

// enforce wraps one route table row. Public routes run unwrapped. Every
// other route authenticates, resolves the object, authorizes every declared
// action on it, and only then runs the handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	switch route.Scope {
	case ScopePublic:
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
// Authorizer to allow every declared action on the resolved object.
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
			h.writeError(ctx, w, err)

			return
		}

		for _, action := range route.Actions {
			if err := h.authz.Authorize(ctx, p, action, obj); err != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		route.Serve(w, r, p, obj)
	}
}
