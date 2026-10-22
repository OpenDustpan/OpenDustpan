package objectcontext

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TargetResolver finds the object of a request: the namespace and, for a
// unit route, the unit. It answers errNotFound for an object that does not
// exist. It runs after the sign-in and before any authorization check.
type TargetResolver = func(w http.ResponseWriter, r *http.Request) (target, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler = func(w http.ResponseWriter, r *http.Request, p identity.Principal, t target)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs, how to find its object, and how to serve it.
// NewHandler registers every route from the table that buildRoutes returns,
// so a route without a row is never served.
//
// The registration wrapper runs the checks in this order. A refusal of the
// first two answers the one 404 of an unknown object, so a caller learns
// nothing about an object that it cannot read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path, for each
//     route.
//  2. Reads on the object of the route.
//  3. Actions on the object of the route. A refusal answers 403, because
//     the caller can already read the object.
//
// The shared row holds the pattern, the scope, the Actions, the resolver and
// the handler. Every route has the scope route.ScopeAuthorized.
type Route struct {
	route.Route[target]

	// Reads lists the read actions that the route needs on its object.
	Reads []identity.Action
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces. The
// paths hold the unit ID and no tool.
func (h *Handler) buildRoutes() []Route {
	return h.contextRoutes(
		"/api/dustpan/v1/namespaces/{namespace}/context",
		"/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context",
		h.resolveUnitByID,
	)
}

// contextRoutes returns the six rows of the context routes for one pair of
// paths: the namespace path and the unit path, whose unit is found by
// resolveUnit.
func (h *Handler) contextRoutes(namespacePath, unitPath string, resolveUnit TargetResolver) []Route {
	nsRead := []identity.Action{identity.ActionNamespaceRead}
	unitRead := []identity.Action{identity.ActionUnitRead}

	return []Route{
		{
			Method: http.MethodGet, Path: namespacePath, Scope: route.ScopeAuthorized,
			Actions: nil,
			Resolve: h.resolveNamespace, Serve: h.readContext,
			Reads: nsRead,
		},
		{
			Method: http.MethodPatch, Path: namespacePath, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceSetContext},
			Resolve: h.resolveNamespace, Serve: h.patchContext,
			Reads: nsRead,
		},
		{
			Method: http.MethodDelete, Path: namespacePath, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceSetContext},
			Resolve: h.resolveNamespace, Serve: h.deleteContext,
			Reads: nsRead,
		},
		{
			Method: http.MethodGet, Path: unitPath, Scope: route.ScopeAuthorized,
			Actions: nil,
			Resolve: resolveUnit, Serve: h.readContext,
			Reads: unitRead,
		},
		{
			Method: http.MethodPatch, Path: unitPath, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitSetContext},
			Resolve: resolveUnit, Serve: h.patchContext,
			Reads: unitRead,
		},
		{
			Method: http.MethodDelete, Path: unitPath, Scope: route.ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitSetContext},
			Resolve: resolveUnit, Serve: h.deleteContext,
			Reads: unitRead,
		},
	}
}

// Routes returns a copy of the route table. The action lists are copied too,
// so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
		out[i].Reads = append([]identity.Action(nil), rt.Reads...)
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

// kit returns the route steps that this API shares with the other
// tool-neutral APIs.
func (h *Handler) kit() route.Guard {
	return route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
}

// enforce wraps one route table row in the shared wrapper: it
// authenticates, resolves the target, runs the checks of the row in the
// order of the Route comment, and only then runs the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	return route.Enforce(h.kit(), h.writeError, route.Steps[target]{
		Scope:   rt.Scope,
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, t target) error {
			return h.authorizeRoute(r.Context(), r, rt, p, t)
		},
		Serve: rt.Serve,
	})
}

// authorizeRoute runs the checks of route for p on t. It returns errNotFound
// for a refused read check and an error that wraps identity.ErrDenied for a
// refused action.
func (h *Handler) authorizeRoute(
	ctx context.Context, r *http.Request, rt Route, p identity.Principal, t target,
) error {
	nsRead := identity.ActionNamespaceRead

	nsErr := h.guard.Authorize(ctx, r, p, nsRead, identity.NamespaceObject(t.ns.Name))
	if err := readRefusal(nsRead, nsErr); err != nil {
		return err
	}

	for _, action := range rt.Reads {
		if action == nsRead && !t.obj.IsUnit() {
			// The object is the namespace, which the first check read.
			continue
		}

		if err := readRefusal(action, h.guard.Authorize(ctx, r, p, action, t.obj)); err != nil {
			return err
		}
	}

	for _, action := range rt.Actions {
		if err := h.guard.Authorize(ctx, r, p, action, t.obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// readRefusal maps the result of a read check: a refusal is the 404 of an
// unknown object.
func readRefusal(action identity.Action, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrDenied):
		return errNotFound
	default:
		return fmt.Errorf("authorize %s: %w", action, err)
	}
}
