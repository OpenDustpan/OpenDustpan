package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
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
	// ActionsFor, when it is not nil, picks from Actions the actions that
	// the resolved object needs, for a route whose object can be of more
	// than one kind. Nil means every entry of Actions.
	ActionsFor func(obj identity.Object) []identity.Action
	// OpaqueDenial, when it is true, answers a refused authorization
	// with the plain coded text, without the action and the kind of the
	// object. Set it on a route whose object comes from stored state that
	// the caller may not see, so that the 403 is the same whatever that
	// state is.
	OpaqueDenial bool
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// ActionsOn returns the actions that the route authorizes on obj: the
// choice of ActionsFor when it is set, else Actions.
func (r Route) ActionsOn(obj identity.Object) []identity.Action {
	if r.ActionsFor != nil {
		return r.ActionsFor(obj)
	}

	return r.Actions
}

// authorizedRoute returns a row that needs actions on the resolved object and
// answers a refusal with the named action and object kind.
func authorizedRoute(
	method, path string, actions []identity.Action, resolve ObjectResolver, serve RouteHandler,
) Route {
	return Route{
		Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions,
		Resolve: resolve, ActionsFor: nil, OpaqueDenial: false, Serve: serve,
	}
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
	routes = append(routes, h.workerRoutes()...)
	routes = append(routes, h.restoreRoutes()...)
	routes = append(routes, h.maintenanceRoutes()...)
	routes = append(routes, h.auditChainRoutes()...)
	routes = append(routes, h.deletedUnitRoutes()...)
	routes = append(routes, h.namespaceOverrideRoutes()...)
	routes = append(routes, h.ipAllowlistRoutes()...)
	routes = append(routes, h.gateConfigRoutes()...)
	routes = append(routes, h.principalRoutes()...)
	routes = append(routes, h.namespacePrincipalRoutes()...)
	routes = append(routes, h.groupRoutes()...)
	routes = append(routes, h.roleRoutes()...)
	routes = append(routes, h.grantRoutes()...)
	routes = append(routes, h.reviewRoutes()...)
	routes = append(routes, h.tokenRoutes()...)
	routes = append(routes, h.ownTokenRoutes()...)
	routes = append(routes, h.tokenPolicyRoutes()...)
	routes = append(routes, h.sessionPolicyRoutes()...)
	routes = append(routes, h.localUserRoutes()...)
	routes = append(routes, h.passkeyRoutes()...)
	routes = append(routes, h.passkeySignInRoutes()...)
	routes = append(routes, h.auditSinkRoutes()...)
	routes = append(routes, h.checkpointRoutes()...)
	routes = append(routes, h.instanceRoutes()...)
	routes = append(routes, h.storeCheckRoutes()...)
	routes = append(routes, h.signInRoutes()...)
	routes = append(routes, h.devSignInRoutes()...)
	routes = append(routes, h.capabilitiesRoutes()...)
	routes = append(routes, h.openAPIRoutes()...)
	routes = append(routes, h.signInProviderRoutes()...)
	routes = append(routes, h.preferencesRoutes()...)
	routes = append(routes, h.settingsRoutes()...)
	routes = append(routes, h.gitHostRoutes()...)
	routes = append(routes, h.gitHostLinkRoutes()...)

	return routes
}

// auditSinkRoutes returns the audit sink rows of the route table: the list
// of the configured audit sinks with their delivery state.
func (h *Handler) auditSinkRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/server/audit/sinks",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.listAuditSinks,
		},
	}
}

// coreRoutes returns the namespace and metrics rows of the route table.
func (h *Handler) coreRoutes() []Route {
	return []Route{
		authorizedRoute(http.MethodPost, "/api/server/namespaces",
			[]identity.Action{identity.ActionServerCreateNamespace}, resolveCreateNamespace, h.createNamespace),
		authorizedRoute(http.MethodGet, "/api/server/namespaces",
			[]identity.Action{identity.ActionCallerRead}, resolveCaller, h.listNamespaces),
		authorizedRoute(http.MethodGet, "/api/server/metrics",
			[]identity.Action{identity.ActionServerObserve}, resolveServer, h.serveMetrics),
	}
}

// backupRoutes returns the backup rows of the route table (ADR 30).
func (h *Handler) backupRoutes() []Route {
	return []Route{
		authorizedRoute(http.MethodPost, "/api/server/backups",
			[]identity.Action{identity.ActionServerBackup}, resolveServer, h.startBackup),
		authorizedRoute(http.MethodGet, "/api/server/backups",
			[]identity.Action{identity.ActionServerBackup}, resolveServer, h.listBackups),
		authorizedRoute(http.MethodGet, "/api/server/backups/{id}",
			[]identity.Action{identity.ActionServerBackup}, resolveServer, h.getBackup),
	}
}

// restoreRoutes returns the restore rows of the route table (ADR 38
// decision 1): start a restore, list restores, read one restore, and cancel
// one restore before its swap.
func (h *Handler) restoreRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodPost,
			Path:    "/api/server/restores",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.startRestore,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/restores",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.listRestores,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/restores/{id}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.getRestore,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/server/restores/{id}/cancel",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerRestore},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.cancelRestore,
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
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.checkBackup,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/server/upgrade-check",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionServerObserve},
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveUpgradeCheck,
		},
	}
}

// maintenanceRoutes returns the maintenance routes (ADR 38).
func (h *Handler) maintenanceRoutes() []Route {
	return []Route{
		authorizedRoute(http.MethodGet, "/api/server/maintenance",
			[]identity.Action{identity.ActionServerMaintenance}, resolveServer, h.getMaintenance),
		authorizedRoute(http.MethodPost, "/api/server/maintenance/enter",
			[]identity.Action{identity.ActionServerMaintenance}, resolveServer, h.enterMaintenance),
		authorizedRoute(http.MethodPost, "/api/server/maintenance/leave",
			[]identity.Action{identity.ActionServerMaintenance}, resolveServer, h.leaveMaintenance),
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
	m.servemux.Handle(pattern, requestid.WithRoute(pattern, handler))
}

// HandleFunc registers pattern and records it.
func (m *patternMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.Handle(pattern, requestid.WithRoute(pattern, http.HandlerFunc(handler)))
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

// principalKey carries the authenticated principal for resolvers that
// need it. Only enforce sets it, before Resolve runs, so a resolver that
// reads it sees the caller of this request. Resolvers that need no caller
// ignore it.
type principalKey struct{}

// withPrincipal returns ctx carrying p for a resolver.
func withPrincipal(ctx context.Context, p identity.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principalFromRequest returns the principal that enforce stored on r, and
// false when r went around enforce.
func principalFromRequest(r *http.Request) (identity.Principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(identity.Principal)

	return p, ok
}

// enforceAuthorized serves a route that needs a signed-in principal and the
// Authorizer to allow every declared action on the resolved object.
func (h *Handler) enforceAuthorized(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		p, caller, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		ctx = withPrincipal(ctx, p)
		if caller != nil {
			ctx = context.WithValue(ctx, sessionKey{}, *caller)
		}

		r = r.WithContext(ctx)

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		actions := route.ActionsOn(obj)
		if len(actions) == 0 {
			h.writeError(ctx, w, errcode.Refusal(errcode.AccessNoAction, fmt.Sprintf("%s %s needs no action on %q",
				route.Method, route.Path, obj.String())))

			return
		}

		for _, action := range actions {
			if err := h.guard.Authorize(ctx, r, p, action, obj); err != nil {
				h.writeError(ctx, w, authorizeRefusal(route, action, err))

				return
			}
		}

		route.Serve(w, r, p, obj)
	}
}

// authorizeRefusal returns the error that answers a refused action, with the
// action in its text. On a route with OpaqueDenial, a plain denial is opaque,
// so the answer does not name the action or the kind of the object.
func authorizeRefusal(route Route, action identity.Action, err error) error {
	refusal := fmt.Errorf("authorize %s: %w", action, err)

	// Defense in depth: the route still authorizes every action on the
	// stored object; this layer only stops the 403 text from telling which
	// object kind the stored state gave.
	if route.OpaqueDenial && errors.Is(err, identity.ErrDenied) &&
		errcode.Forbidden(err) == errcode.AccessDenied {
		return opaqueDenialError{err: refusal}
	}

	return refusal
}

// opaqueDenialError answers a refused authorization with the plain denial text:
// it keeps the full inner text for the log and unwraps only to
// identity.ErrDenied, so the answer names neither the action nor the kind of
// the object.
type opaqueDenialError struct {
	err error
}

// Error returns the full text of the denial, with the action and the object.
func (e opaqueDenialError) Error() string { return e.err.Error() }

// Unwrap returns only the sentinel, so the chain holds no typed denial.
func (e opaqueDenialError) Unwrap() error { return identity.ErrDenied }
