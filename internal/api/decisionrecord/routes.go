package decisionrecord

import (
	"errors"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// target is the object of a request, found before any authorization check:
// the scope of the path, the namespace of its records and the raw ID of the
// path.
type target struct {
	scope gate.Scope
	ns    namespace.Name
	id    string
	obj   identity.Object
	// installation is true for the installation route.
	installation bool
}

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler = func(w http.ResponseWriter, r *http.Request, p identity.Principal, t target)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the read actions it needs, how to find its object and how to serve it.
// NewHandler registers every route from the table that buildRoutes returns,
// so a route without a row is never served.
//
// The registration wrapper runs the checks in this order. A refusal of any
// of them answers the one 404 of an unknown record, so a caller learns
// nothing about a record that the caller may not read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path, for the
//     namespace route.
//  2. Reads on the object of the route.
type Route struct {
	route.Route[target]

	// Reads lists the read actions that the route needs on its object.
	Reads []identity.Action
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return []Route{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/{namespace}/decision-records/{id}",
			Scope:   route.ScopeAuthorized,
			Actions: nil,
			Resolve: h.resolveNamespace, Serve: h.readNamespaceRecord,
			Reads: []identity.Action{identity.ActionNamespaceReadGates},
		},
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/decision-records/{id}",
			Scope:   route.ScopeAuthorized,
			Actions: nil,
			Resolve: h.resolveInstallation, Serve: h.readInstallationRecord,
			Reads: []identity.Action{identity.ActionInstallationReadGates},
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
		Resolve: rt.Resolve,
		Authorize: func(r *http.Request, p identity.Principal, t target) error {
			return h.authorizeTarget(r, p, rt, t)
		},
		Serve: rt.Serve,
	})
}

// resolveNamespace reads the target of the namespace route: the namespace of
// the path and the raw ID of the path. A name that is not a namespace name
// answers the one 404.
func (h *Handler) resolveNamespace(_ http.ResponseWriter, r *http.Request) (target, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return target{}, errNotFound
	}

	return target{
		scope: gate.NamespaceScope(ns), ns: ns, id: r.PathValue("id"),
		obj:          identity.NamespaceObject(ns),
		installation: false,
	}, nil
}

// resolveInstallation reads the target of the installation route: the
// installation scope and the raw ID of the path.
func (h *Handler) resolveInstallation(_ http.ResponseWriter, r *http.Request) (target, error) {
	return target{
		scope: gate.InstallationScope(), ns: audit.ServerScope, id: r.PathValue("id"),
		obj:          identity.ServerObject(),
		installation: true,
	}, nil
}

// authorizeTarget runs the checks of rt for p on t. A refusal answers the one
// 404 of an unknown record, so the answer never tells that a record exists.
func (h *Handler) authorizeTarget(r *http.Request, p identity.Principal, rt Route, t target) error {
	if t.installation {
		if err := h.guard.Authorize(r.Context(), r, p, identity.ActionInstallationReadGates, t.obj); err != nil {
			if errors.Is(err, identity.ErrDenied) {
				return errNotFound
			}

			return err
		}
		return nil
	}

	g := route.Guard{Tokens: nil, Sessions: nil, Refusals: h.guard}
	checks := route.Checks{
		Namespace:     t.obj,
		Reads:         rt.Reads,
		Actions:       rt.Actions,
		NotFound:      errNotFound,
		RequireAction: false,
	}

	return g.AuthorizeObject(r.Context(), r, p, t.obj, checks)
}
