package audit

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/routekit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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
// namespace that the request acts on.
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
	// Path is the ServeMux path pattern, for example /api/audit/entries.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions the route needs. Only authorized routes set
	// it, and the wrapper authorizes every entry on the resolved object.
	Actions []identity.Action
	// ServerActions lists the actions the route needs when the resolved object
	// is the installation (@server). When it is empty, Actions applies.
	ServerActions []identity.Action
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
// console reads only this API, and it ships on its own schedule.
func (h *Handler) buildRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet,
			Path:   "/api/audit/entries",
			// The object is the namespace in the organization query value, or
			// the installation for the value @server.
			Scope:         ScopeAuthorized,
			Actions:       []identity.Action{identity.ActionNamespaceReadAudit},
			ServerActions: []identity.Action{identity.ActionServerReadAudit},
			Resolve:       resolveAuditObject,
			Serve:         h.listEntries,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/audit/entries/{sequence}",
			// The object is the namespace in the organization query value, or
			// the installation for the value @server, as for the list.
			Scope:         ScopeAuthorized,
			Actions:       []identity.Action{identity.ActionNamespaceReadAudit},
			ServerActions: []identity.Action{identity.ActionServerReadAudit},
			Resolve:       resolveAuditObject,
			Serve:         h.getEntry,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/audit/check",
			// The object is the namespace in the organization query value, or
			// the installation for the value @server.
			Scope:         ScopeAuthorized,
			Actions:       []identity.Action{identity.ActionNamespaceReadAudit},
			ServerActions: []identity.Action{identity.ActionServerReadAudit},
			Resolve:       resolveAuditObject,
			Serve:         h.checkChain,
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

// resolveAuditObject returns the object in the organization query value: the
// installation for the scope @server, else the namespace of the value. The
// value is required: without it the request names no object, so the request
// fails with a 400 before authorization. A namespace name is validated the way
// the namespace service does, so an invalid value fails closed before
// authorization.
func resolveAuditObject(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	org := queryOf(r).Get("organization")
	if org == "" {
		return identity.Object{}, errMissingOrganization
	}

	if org == audit.ServerScope.String() {
		return identity.ServerObject(), nil
	}

	ns, err := namespace.ParseName(org)
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(ns), nil
}

// kit returns the route steps that this API shares with the other
// tool-neutral APIs.
func (h *Handler) kit() routekit.Guard {
	return routekit.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
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

		p, caller, err := h.kit().Authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		// A session request carries the session, as on every tool-neutral
		// API: the refusal record names the console channel, and a check of a
		// fresh sign-in sees the session.
		ctx = routekit.WithCaller(ctx, caller)
		r = r.WithContext(ctx)

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		actions := route.Actions

		if obj.IsServer() {
			// A route with no installation action refuses the installation
			// object. It never falls back to a namespace action.
			if len(route.ServerActions) == 0 {
				h.writeError(
					ctx,
					w,
					errcode.Refusal(route.Path+" has no action for the installation"),
				)

				return
			}

			actions = route.ServerActions
		}

		if err := h.kit().AuthorizeAll(r, p, obj, actions); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		route.Serve(w, r, p, obj)
	}
}
