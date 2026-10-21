package triggers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
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

// TargetResolver finds the object of a request: the namespace, the unit and,
// for a trigger route, the trigger. It answers errNotFound for an object that
// does not exist. It runs after the sign-in and before any authorization
// check.
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
//  2. Reads on the unit of the path.
//  3. Actions on the unit of the path. A refusal answers 403, because the
//     caller can already read the unit.
//
// The manager checks the same actions again for the caller, and one more for a
// follows trigger: unit:read on its source unit.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Reads lists the read actions that the route needs on its unit.
	Reads []identity.Action
	// Actions lists the other actions that the route needs on its unit.
	Actions []identity.Action
	// Resolve finds the object of the request.
	Resolve TargetResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
	// IdempotencyKey says that the route takes the Idempotency-Key header.
	// The wrapper refuses the header on every other write.
	IdempotencyKey bool
}

const (
	unitTriggersPath = "/api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers"
	triggerPath      = unitTriggersPath + "/{trigger_id}"
	firePath         = triggerPath + "/fire"
)

// buildRoutes returns the route table. It is the single source of the patterns
// that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionUnitRead}
	change := []identity.Action{trigger.SettingsAction, trigger.StartAction}

	return []Route{
		{
			Method: http.MethodGet, Path: unitTriggersPath, Scope: ScopeAuthorized, Reads: read, Actions: nil,
			Resolve: h.resolveUnit, Serve: h.listTriggers, IdempotencyKey: false,
		},
		{
			Method: http.MethodPost, Path: unitTriggersPath, Scope: ScopeAuthorized, Reads: read, Actions: change,
			Resolve: h.resolveUnit, Serve: h.createTrigger, IdempotencyKey: false,
		},
		{
			Method: http.MethodGet, Path: triggerPath, Scope: ScopeAuthorized, Reads: read, Actions: nil,
			Resolve: h.resolveTrigger, Serve: h.getTrigger, IdempotencyKey: false,
		},
		{
			Method: http.MethodPut, Path: triggerPath, Scope: ScopeAuthorized, Reads: read, Actions: change,
			Resolve: h.resolveTrigger, Serve: h.replaceTrigger, IdempotencyKey: false,
		},
		{
			Method: http.MethodDelete,
			Path:   triggerPath,
			Scope:  ScopeAuthorized,
			Reads:  read,
			Actions: []identity.Action{
				trigger.SettingsAction,
			},
			Resolve:        h.resolveStoredTrigger,
			Serve:          h.deleteTrigger,
			IdempotencyKey: false,
		},
		{
			Method: http.MethodPost,
			Path:   firePath,
			Scope:  ScopeAuthorized,
			Reads:  read,
			Actions: []identity.Action{
				trigger.StartAction,
			},
			Resolve:        h.resolveTrigger,
			Serve:          h.fireTrigger,
			IdempotencyKey: true,
		},
	}
}

// Routes returns a copy of the route table. The action lists are copied too, so
// callers cannot mutate handler state through the result.
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

// enforce wraps one route table row: it authenticates, finds the object, runs
// the checks in the order of Route, and only then runs the handler.
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

		// A write with the key would silently lose its replay protection: the
		// routes of this API take no key, except the fire route, so the server
		// refuses the header elsewhere.
		if r.Method != http.MethodGet && !route.IdempotencyKey && len(r.Header.Values(idempotencyHeader)) > 0 {
			h.writeError(ctx, w, errKeyUnsupported)

			return
		}

		t, err := route.Resolve(r)
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

// authorizeRoute runs the checks of route for p on t. It returns errNotFound for
// a refused read check and an error that wraps identity.ErrDenied for a refused
// action.
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
