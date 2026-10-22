package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
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
// scope, the actions it needs, its resolver and its handler) and the two
// fields of the authorization of this API. NewHandler registers every route
// from the table that buildRoutes returns, so a route without a row is never
// served. The shared wrapper is the single place that enforces the table: for
// every non-public route it authenticates, resolves the object, authorizes
// every declared action on that object, and only then calls the handler.
type Route struct {
	route.Route[identity.Object]

	// NamespaceActions lists actions that the wrapper authorizes on the
	// namespace of the resolved object, before Actions. Only authorized routes
	// set it.
	NamespaceActions []identity.Action
	// DeniedAsNotFound makes the wrapper answer 404 when the Authorizer
	// denies an action, with the body of an unknown object, so the answer
	// does not tell a caller that the object exists. Only routes that find
	// an object by its ID alone set it.
	DeniedAsNotFound bool
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
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionNamespaceRead},
			Resolve:          resolveCollectionNamespace,
			Serve:            h.listUnits,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		},
		{
			Method: http.MethodGet,
			Path:   v1Prefix + "/namespaces/-/units/{unitID}",
			// The object is the unit of the path, found by its ID alone. The
			// caller needs unit:read on the unit and namespace:read on its
			// namespace. A denied caller gets the 404 of an unknown unit.
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitRead},
			Resolve:          h.resolveUnitByID,
			Serve:            h.getUnit,
			NamespaceActions: []identity.Action{identity.ActionNamespaceRead},
			DeniedAsNotFound: true,
		},
		{
			Method: http.MethodGet,
			Path:   v1Prefix + "/namespaces/{namespace}/deleted-units",
			// The object is the namespace of the path. The handler then
			// removes every entry whose unit the caller may not read.
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionNamespaceRead},
			Resolve:          resolveCollectionNamespace,
			Serve:            h.listDeletedUnits,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		},
		{
			Method: http.MethodPost,
			Path:   "/api/inventory/deleted-units/{tool}/{unitID}/restore",
			// The object is the deleted unit: its reference under the name
			// before the delete, with its stable ID.
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitRestore},
			Resolve:          h.resolveDeletedUnit,
			Serve:            h.restoreUnit,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		},
		{
			Method:           http.MethodDelete,
			Path:             "/api/inventory/deleted-units/{tool}/{unitID}",
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{identity.ActionUnitPurge},
			Resolve:          h.resolveDeletedUnit,
			Serve:            h.purgeUnit,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
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
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Actions = append([]identity.Action(nil), rt.Actions...)
		out[i].NamespaceActions = append([]identity.Action(nil), rt.NamespaceActions...)
	}

	return out
}

// RegisteredPatterns returns a copy of every ServeMux pattern the handler
// serves, in registration order. The route test compares this with the route
// table in both directions.
func (h *Handler) RegisteredPatterns() []string {
	return h.mux.Patterns()
}

// resolveListUnits returns the namespace in the organization query value.
// The value is required: without it the request names no object, so the
// request fails with a 400 before authorization. It validates the name the
// way the namespace service does, so an invalid value fails closed before
// authorization.
func resolveListUnits(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	org := r.URL.Query().Get("organization")
	if org == "" {
		return identity.Object{}, errMissingOrganization
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

// enforce wraps one route table row in the shared wrapper. Public routes
// serve with no principal. Every other route authenticates, resolves the
// object, authorizes every declared action on it, and only then runs the
// handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return route.Enforce(h.kit(), h.writeError, route.Steps[identity.Object]{
		Scope:   rt.Scope,
		Actions: slices.Concat(rt.NamespaceActions, rt.Actions),
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, obj identity.Object) error {
			return h.authorizeRoute(r.Context(), r, rt, p, obj)
		},
		Serve: rt.Serve,
	})
}

// authorizeRoute authorizes every action of route: NamespaceActions on the
// namespace of obj, then Actions on obj. For a route with DeniedAsNotFound,
// a denial gives the error of an unknown object.
func (h *Handler) authorizeRoute(
	ctx context.Context, r *http.Request, rt Route, p identity.Principal, obj identity.Object,
) error {
	check := func(action identity.Action, target identity.Object) error {
		err := h.guard.Authorize(ctx, r, p, action, target)
		if err == nil {
			return nil
		}

		if rt.DeniedAsNotFound && errors.Is(err, identity.ErrDenied) {
			return coreinventory.ErrUnitNotFound
		}

		return fmt.Errorf("authorize %s: %w", action, err)
	}

	for _, action := range rt.NamespaceActions {
		if err := check(action, identity.NamespaceObject(obj.Namespace())); err != nil {
			return err
		}
	}

	for _, action := range rt.Actions {
		if err := check(action, obj); err != nil {
			return err
		}
	}

	return nil
}
