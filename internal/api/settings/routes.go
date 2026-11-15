package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// TargetResolver finds the object of a request: the namespace and, for a unit
// route, the unit. It answers errNotFound for an object that does not exist.
// It runs after the sign-in and before any authorization check.
type TargetResolver func(r *http.Request) (target, error)

// RouteHandler serves a route after the registration wrapper has authenticated
// and authorized it.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target)

// Route declares one endpoint: its ServeMux pattern, how it is protected, the
// actions it needs, how to find its object, and how to serve it. NewHandler
// registers every route from the table that buildRoutes returns, so a route
// without a row is never served.
//
// The registration wrapper runs the checks in this order. A refusal of the
// first two answers the one 404 of an unknown object, so a caller learns
// nothing about an object that it cannot read:
//
//  1. identity.ActionNamespaceRead on the namespace of the path.
//  2. Reads on the object of the path.
//  3. Actions on the object of the path. A refusal answers 403, because the
//     caller can already read the object.
//
// A write that names a Git host connection or changes the steps needs one more
// action each. The handler checks them, because the body decides them, and it
// records each refusal in the audit log.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Reads lists the read actions that the route needs on its object.
	Reads []identity.Action
	// Actions lists the other actions that the route needs on its object.
	Actions []identity.Action
	// Extra lists the actions that the handler may check after the wrapper,
	// because the body decides them. The description of the API lists them.
	Extra []identity.Action
	// Keyed says that the route takes the Idempotency-Key header. A request with
	// the header to a route that does not take it answers 400, so the server
	// never ignores a key without an error.
	Keyed bool
	// Resolve finds the object of the request.
	Resolve TargetResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

const (
	unitSettingsPath = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/settings"
	suggestionsPath  = unitSettingsPath + "/suggestions"
	defaultsPath     = "/api/dustpan/v1/namespaces/{namespace}/settings/unit-defaults"
	bulkPath         = "/api/dustpan/v1/namespaces/{namespace}/unit-settings/bulk-put"
)

// buildRoutes returns the route table. It is the single source of the patterns
// that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionUnitRead}
	manage := []identity.Action{identity.ActionUnitManage}
	extra := []identity.Action{identity.ActionNamespaceUseGitHostConnection, identity.ActionUnitDefineSteps}

	return []Route{
		{
			Method: http.MethodGet, Path: unitSettingsPath, Scope: ScopeAuthorized, Reads: read, Actions: nil,
			Extra: nil, Keyed: false, Resolve: h.resolveUnit, Serve: h.getSettings,
		},
		{
			Method: http.MethodPut, Path: unitSettingsPath, Scope: ScopeAuthorized, Reads: read, Actions: manage,
			Extra: extra, Keyed: true, Resolve: h.resolveUnit, Serve: h.putSettings,
		},
		{
			Method: http.MethodDelete, Path: unitSettingsPath, Scope: ScopeAuthorized, Reads: read,
			Actions: manage, Extra: nil, Keyed: true, Resolve: h.resolveUnit, Serve: h.deleteSettings,
		},
		{
			Method: http.MethodGet, Path: suggestionsPath, Scope: ScopeAuthorized, Reads: read, Actions: nil,
			Extra: nil, Keyed: false, Resolve: h.resolveUnit, Serve: h.getSuggestions,
		},
		{
			Method: http.MethodGet, Path: defaultsPath, Scope: ScopeAuthorized, Reads: nil, Actions: nil,
			Extra: nil, Keyed: false, Resolve: h.resolveNamespace, Serve: h.getDefaults,
		},
		{
			Method: http.MethodPut, Path: defaultsPath, Scope: ScopeAuthorized, Reads: nil,
			Actions: []identity.Action{identity.ActionNamespaceAdmin}, Extra: nil, Keyed: true,
			Resolve: h.resolveNamespace, Serve: h.putDefaults,
		},
		{
			Method: http.MethodPost, Path: bulkPath, Scope: ScopeAuthorized, Reads: nil, Actions: nil,
			Extra: slicesConcat(read, manage, extra), Keyed: true, Resolve: h.resolveNamespace, Serve: h.bulkPut,
		},
	}
}

func slicesConcat(lists ...[]identity.Action) []identity.Action {
	var out []identity.Action

	for _, l := range lists {
		out = append(out, l...)
	}

	return out
}

// Routes returns a copy of the route table. The action lists are copied too, so
// callers cannot mutate handler state through the result.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i := range h.routes {
		out[i] = h.routes[i]
		out[i].Reads = append([]identity.Action(nil), h.routes[i].Reads...)
		out[i].Actions = append([]identity.Action(nil), h.routes[i].Actions...)
		out[i].Extra = append([]identity.Action(nil), h.routes[i].Extra...)
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

// enforce wraps one route table row: it authenticates, finds the object, runs
// the checks in the order of Route, and only then runs the handler.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
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

		// A route that does not take the key answers 400: the server never
		// ignores the key without an error.
		//
		// Defense in depth: the route table gives a row without the key to GET
		// only, and the pattern of a row names its method, so a write never
		// reaches this check today. It stays so that a new row for a write that
		// forgets the key still refuses the header, and it relies on neither the
		// route table nor the method of the mux pattern.
		if !rt.Keyed && r.Method != http.MethodGet && idempotent.Present(r) {
			h.writeError(ctx, w, errKeyUnsupported)

			return
		}

		if err = h.checkAddress(ctx, r, p); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		t, err := rt.Resolve(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		g := route.Guard{Tokens: nil, Sessions: nil, Refusals: h.guard}
		checks := route.Checks{
			Namespace:     identity.NamespaceObject(t.ns.Name),
			Reads:         rt.Reads,
			Actions:       rt.Actions,
			NotFound:      errNotFound,
			RequireAction: false,
		}
		if err := g.AuthorizeObject(ctx, r, p, t.obj, checks); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		rt.Serve(w, r, p, &t)
	}
}

// checkAddress refuses a client address that is outside the IP allowlist of
// the namespace in the path, before any lookup of the namespace or of the
// unit. The answer is the same 403 for a name that exists and for a name that
// does not exist, so it does not tell whether the unit exists. Every other
// result, also an invalid name, continues. An authorizer error stops the
// request here and writes one entry, also for a namespace that does not exist.
// A policy refusal continues, so that the later check records it once. It asks
// through Decide, which writes no entry.
//
// Defense in depth: AuthorizeObject checks the allowlist again on the found
// namespace. This check stays so that the order of the lookup never decides
// the answer.
func (h *Handler) checkAddress(
	ctx context.Context, r *http.Request, p identity.Principal,
) error {
	action := identity.ActionNamespaceRead

	// An invalid name names no namespace and has no allowlist: the lookup that
	// follows answers 404 for it.
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
