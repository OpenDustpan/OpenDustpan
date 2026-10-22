package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
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
	// Path is the ServeMux path pattern, for example /api/dustpan/v1/namespaces/{namespace}/units.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions the route needs. Only authorized routes set
	// it, and the wrapper authorizes every entry on the resolved object.
	Actions []identity.Action
	// NamespaceActions lists actions that the wrapper authorizes on the
	// namespace of the resolved object, before Actions. Only authorized routes
	// set it.
	NamespaceActions []identity.Action
	// Resolve returns the object that Actions apply to. Only authorized
	// routes set it.
	Resolve ObjectResolver
	// DeniedAsNotFound makes the wrapper answer 404 when the Authorizer
	// denies an action, with the body of an unknown object, so the answer
	// does not tell a caller that the object exists. Only routes that find
	// an object by its ID alone set it.
	DeniedAsNotFound bool
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// v1Prefix is the prefix of the tool-neutral REST API.
const v1Prefix = "/api/dustpan/v1"

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
//
// Contract: never remove or rename these endpoints or status codes. The
// console reads only this API, and it ships on its own schedule.
func (h *Handler) buildRoutes() []Route {
	routes := []Route{
		{
			Method: http.MethodGet,
			Path:   v1Prefix + "/namespaces/{namespace}/units",
			// The object is the namespace of the path. The handler then
			// removes every entry whose unit the caller may not read
			// (unit:read on the reference).
			Scope:            ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionNamespaceRead},
			Resolve:          resolveCollectionNamespace,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
			Serve:            h.listUnits,
		},
		{
			Method: http.MethodGet,
			Path:   v1Prefix + "/namespaces/-/units/{unitID}",
			// The object is the unit of the path, found by its ID alone. The
			// caller needs unit:read on the unit and namespace:read on its
			// namespace. A denied caller gets the 404 of an unknown unit.
			Scope:            ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitRead},
			NamespaceActions: []identity.Action{identity.ActionNamespaceRead},
			Resolve:          h.resolveUnitByID,
			DeniedAsNotFound: true,
			Serve:            h.getUnit,
		},
		{
			Method: http.MethodGet,
			Path:   v1Prefix + "/namespaces/{namespace}/deleted-units",
			// The object is the namespace of the path. The handler then
			// removes every entry whose unit the caller may not read.
			Scope:            ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionNamespaceRead},
			Resolve:          resolveCollectionNamespace,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
			Serve:            h.listDeletedUnits,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/inventory/deleted-units/{tool}/{unitID}/restore",
			// The object is the deleted unit: its reference under the name
			// before the delete, with its stable ID.
			Scope:            ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitRestore},
			Resolve:          h.resolveDeletedUnit,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
			Serve:            h.restoreUnit,
		},
		{
			Method:           http.MethodDelete,
			Path:             "/api/inventory/deleted-units/{tool}/{unitID}",
			Scope:            ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitPurge},
			Resolve:          h.resolveDeletedUnit,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
			Serve:            h.purgeUnit,
		},
	}

	if h.collections != nil {
		routes = append(routes, h.collectionRoutes()...)
	}

	if h.locks != nil {
		routes = append(routes, h.lockRoutes()...)
	}

	if h.views != nil {
		routes = append(routes, h.viewRoutes()...)
	}

	return routes
}

// Routes returns a copy of the route table. The Actions lists are copied
// too, so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Actions = append([]identity.Action(nil), route.Actions...)
		out[i].NamespaceActions = append([]identity.Action(nil), route.NamespaceActions...)
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

// resolveListUnits returns the namespace in the organization query value.
// The value is required: without it the request names no object, so the
// request fails with a 400 before authorization. It validates the name the
// way the namespace service does, so an invalid value fails closed before
// authorization.
func resolveListUnits(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	org := queryOf(r).Get("organization")
	if org == "" {
		return identity.Object{}, errMissingOrganization
	}

	ns, err := namespace.ParseName(org)
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(ns), nil
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

		p, session, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if session {
			ctx = sessionauth.WithSession(ctx)
			r = r.WithContext(ctx)
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if err := h.authorizeRoute(ctx, r, route, p, obj); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		route.Serve(w, r, p, obj)
	}
}

// authorizeRoute authorizes every action of route: NamespaceActions on the
// namespace of obj, then Actions on obj. For a route with DeniedAsNotFound,
// a denial gives the error of an unknown object.
func (h *Handler) authorizeRoute(
	ctx context.Context, r *http.Request, route Route, p identity.Principal, obj identity.Object,
) error {
	check := func(action identity.Action, target identity.Object) error {
		err := h.guard.Authorize(ctx, r, p, action, target)
		if err == nil {
			return nil
		}

		if route.DeniedAsNotFound && errors.Is(err, identity.ErrDenied) {
			return coreinventory.ErrUnitNotFound
		}

		return fmt.Errorf("authorize %s: %w", action, err)
	}

	for _, action := range route.NamespaceActions {
		if err := check(action, identity.NamespaceObject(obj.Namespace())); err != nil {
			return err
		}
	}

	for _, action := range route.Actions {
		if err := check(action, obj); err != nil {
			return err
		}
	}

	return nil
}
