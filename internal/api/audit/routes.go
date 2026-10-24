package audit

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ObjectResolver returns the authorization object for a request: the
// namespace that the request acts on.
type ObjectResolver = func(w http.ResponseWriter, r *http.Request) (identity.Object, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated (except for public routes) and authorized it. Public routes
// get zero principal and object. Authorized routes get the principal and the
// resolved object.
type RouteHandler = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// Route declares one endpoint: the shared row (its ServeMux pattern, its
// scope, the actions it needs, its resolver and its handler) and the actions
// for the installation. NewHandler registers every route from the table that
// buildRoutes returns, so a route without a row is never served. The shared
// wrapper is the single place that enforces the table: for every non-public
// route it authenticates, resolves the object, authorizes every declared
// action on that object, and only then calls the handler.
type Route struct {
	route.Route[identity.Object]

	// ServerActions lists the actions the route needs when the resolved object
	// is the installation (@server). A route without them refuses the
	// installation.
	ServerActions []identity.Action
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
//
// Contract: never remove or rename these endpoints or status codes. The
// console reads only this API, and it ships on its own schedule.
func (h *Handler) buildRoutes() []Route {
	// The object of each route is the namespace in the organization query
	// value, or the installation for the value @server.
	return []Route{
		readRoute("/api/audit/entries", h.listEntries),
		readRoute("/api/audit/entries/{sequence}", h.getEntry),
		readRoute("/api/audit/check", h.checkChain),
	}
}

// readRoute makes one authorized GET row on the audit object of the request.
func readRoute(path string, serve RouteHandler) Route {
	return Route{
		Method:        http.MethodGet,
		Path:          path,
		Scope:         route.ScopeAuthorized,
		Actions:       []identity.Action{identity.ActionNamespaceReadAudit},
		Resolve:       resolveAuditObject,
		Serve:         serve,
		ServerActions: []identity.Action{identity.ActionServerReadAudit},
	}
}

// Routes returns a copy of the route table. The Actions lists are copied
// too, so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Actions = append([]identity.Action(nil), rt.Actions...)
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
func (h *Handler) kit() route.Guard {
	return route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
}

// enforce wraps one route table row in the shared wrapper. A public route
// serves with no principal. An authorized route authenticates, resolves the
// object, authorizes the actions of the row on it, and only then runs the
// handler. The actions are ServerActions for the installation object, and
// Actions for a namespace.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return route.Enforce(h.kit(), h.writeError, route.Steps[identity.Object]{
		Scope:   rt.Scope,
		Actions: slices.Concat(rt.Actions, rt.ServerActions),
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, obj identity.Object) error {
			actions := rt.Actions

			if obj.IsServer() {
				// A route with no installation action refuses the installation
				// object. It never falls back to a namespace action.
				if len(rt.ServerActions) == 0 {
					return errcode.Refusal(errcode.AccessNoAction, rt.Path+" has no action for the installation")
				}

				actions = rt.ServerActions
			}

			return h.kit().AuthorizeAll(r, p, obj, actions)
		},
		Serve: rt.Serve,
	})
}
