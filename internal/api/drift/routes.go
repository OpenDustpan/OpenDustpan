package drift

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopePublic serves without sign-in. Only routes on the public list may
	// use it. This API has none.
	ScopePublic Scope = iota + 1
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// the checks of the route before the handler runs.
	ScopeAuthorized
)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target)

// Route declares one endpoint: its ServeMux pattern, how it is protected, the
// actions it needs, and how to serve it. NewHandler registers every route from
// the table that buildRoutes returns, so a route without a row is never served.
//
// The registration wrapper runs the checks in this order. A refusal of the
// first two answers the one 404 of an unknown unit, so a caller learns nothing
// about a unit that it cannot read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path.
//  2. Reads on the unit of the path.
//  3. Actions on the unit of the path. A refusal answers 403, because the
//     caller can already read the unit.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Reads lists the read actions that the route needs on the unit.
	Reads []identity.Action
	// Actions lists the other actions that the route needs on the unit. The
	// job service checks them again for its caller.
	Actions []identity.Action
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

const (
	unitPath       = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}"
	driftPath      = unitPath + "/drift"
	driftChecksURL = unitPath + "/drift-checks"
)

// buildRoutes returns the route table. It is the single source of the patterns
// that NewHandler registers and of what each pattern enforces. The paths hold
// the unit ID and no tool.
func (h *Handler) buildRoutes() []Route {
	routes := []Route{
		{
			Method: http.MethodGet, Path: driftPath, Scope: ScopeAuthorized,
			Reads: []identity.Action{identity.ActionUnitRead}, Actions: nil, Serve: h.readDrift,
		},
	}

	// Without a job service the server starts no drift check, so it serves no
	// route that would only answer an error.
	if h.checker != nil {
		routes = append(routes, Route{
			Method: http.MethodPost, Path: driftChecksURL, Scope: ScopeAuthorized,
			Reads:   []identity.Action{identity.ActionUnitRead},
			Actions: []identity.Action{identity.ActionUnitRunPlan}, Serve: h.startCheck,
		})
	}

	return routes
}

// Routes returns a copy of the route table. The action lists are copied too,
// so callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, route := range h.routes {
		out[i] = route
		out[i].Reads = append([]identity.Action(nil), route.Reads...)
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

// patternMux is an http.ServeMux that records every pattern registered on it,
// so the route test can list every served route.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

// newPatternMux returns a patternMux ready to register routes.
func newPatternMux() *patternMux {
	return &patternMux{servemux: http.NewServeMux(), patterns: nil}
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

// enforce wraps one route table row: it authenticates, finds the unit, runs the
// checks in the order of Route, and only then runs the handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
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

		t, err := h.resolveUnit(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if err := h.authorizeRoute(ctx, r, route, p, &t); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		route.Serve(w, r, p, &t)
	}
}

// authorizeRoute runs the checks of route for p on t. It returns errNotFound
// for a refused read check and an error that wraps identity.ErrDenied for a
// refused action.
func (h *Handler) authorizeRoute(
	ctx context.Context, r *http.Request, route Route, p identity.Principal, t *target,
) error {
	nsRead := identity.ActionNamespaceRead

	nsErr := h.guard.Authorize(ctx, r, p, nsRead, identity.NamespaceObject(t.ns.Name))
	if err := readRefusal(nsRead, nsErr); err != nil {
		return err
	}

	for _, action := range route.Reads {
		if err := readRefusal(action, h.guard.Authorize(ctx, r, p, action, t.obj)); err != nil {
			return err
		}
	}

	for _, action := range route.Actions {
		if err := h.guard.Authorize(ctx, r, p, action, t.obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// readRefusal maps the result of a read check: a refusal is the 404 of an
// unknown unit.
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
