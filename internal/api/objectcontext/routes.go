package objectcontext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
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
		Actions: slices.Concat(rt.Reads, rt.Actions),
		Resolve: func(w http.ResponseWriter, r *http.Request) (target, error) {
			return h.resolveTarget(rt, w, r)
		},
		Authorize: func(r *http.Request, p identity.Principal, t target) error {
			return h.authorizeTarget(r, p, rt, t)
		},
		Serve: rt.Serve,
	})
}

// resolveTarget reads the target of rt. When the lookup fails, it keeps the
// error in the target and returns no error, so that Authorize can check the
// client address first.
func (h *Handler) resolveTarget(rt Route, w http.ResponseWriter, r *http.Request) (target, error) {
	t, err := rt.Resolve(w, r)
	if err == nil {
		return t, nil
	}

	t.lookupErr = err

	return t, nil
}

// authorizeTarget runs the checks of rt for p on t. It checks the client
// address first, also when the lookup failed, and then answers the error of a
// failed lookup. The namespace read runs first, and the read of a namespace
// route is not repeated on the namespace.
func (h *Handler) authorizeTarget(r *http.Request, p identity.Principal, rt Route, t target) error {
	if err := h.checkAddress(r.Context(), r, p); err != nil {
		return err
	}

	if t.lookupErr != nil {
		return t.lookupErr
	}

	reads := rt.Reads
	if !t.obj.IsUnit() {
		reads = make([]identity.Action, 0, len(rt.Reads))
		for _, action := range rt.Reads {
			if action != identity.ActionNamespaceRead {
				reads = append(reads, action)
			}
		}
	}

	g := route.Guard{Tokens: nil, Sessions: nil, Refusals: h.guard}
	checks := route.Checks{
		Namespace:     identity.NamespaceObject(t.ns.Name),
		Reads:         reads,
		Actions:       rt.Actions,
		NotFound:      errNotFound,
		RequireAction: false,
	}

	return g.AuthorizeObject(r.Context(), r, p, t.obj, checks)
}

// checkAddress refuses a client address that is outside the IP allowlist of
// the namespace in the path, before the decision on the target, also when the
// lookup found nothing. The answer is the same 403 for a name that exists and
// for a name that does not exist, so it does not tell whether the object
// exists. Every other result, also an invalid name, continues. An authorizer
// error stops the request here and writes one entry, also for a namespace that
// does not exist. A policy refusal continues, so that the later check records
// it once. It asks through Decide, which writes no entry.
//
// Defense in depth: AuthorizeObject checks the allowlist again on the found
// namespace. This check stays so that the order of the lookup never decides
// the answer.
func (h *Handler) checkAddress(
	ctx context.Context, r *http.Request, p identity.Principal,
) error {
	action := identity.ActionNamespaceRead

	// An invalid name names no namespace and has no allowlist: the lookup
	// that answered before this check gave the answer.
	if name, parseErr := namespace.ParseName(r.PathValue("namespace")); parseErr == nil {
		obj := identity.NamespaceObject(name)

		err := h.guard.Decide(ctx, p, action, obj)
		if errors.Is(err, ipallow.ErrAddressNotAllowed) {
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: action, Object: obj, Reason: refusal.ReasonFor(err), ReasonWithin: nil, Window: 0,
			})

			return fmt.Errorf("authorize %s: %w", action, err)
		}

		if err != nil && !errors.Is(err, identity.ErrDenied) {
			h.guard.Record(ctx, r, p, refusal.Denial{
				Action: action, Object: obj, Reason: refusal.ReasonFor(err), ReasonWithin: nil, Window: 0,
			})

			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}
