package pulumi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
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
// namespace or unit that the request acts on. Resolvers for routes with a
// body read it with http.MaxBytesReader and restore r.Body before they
// return, so the handler can read it again.
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
	// Path is the ServeMux path pattern, for example /api/user.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions the route needs. Only authorized routes set
	// it, and the wrapper authorizes every entry on the resolved object.
	Actions []identity.Action
	// Resolve returns the object that Actions apply to. Only authorized
	// routes set it.
	Resolve ObjectResolver
	// Serve handles the request after the wrapper has enforced the row.
	Serve RouteHandler
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/capabilities",
			Scope:   ScopePublic,
			Actions: nil,
			Resolve: nil,
			Serve:   h.capabilities,
		},
		{
			Method: http.MethodGet,
			Path:   "/api/user",
			// GET /api/user reads the caller's own record, so its object is
			// the caller itself. It lists no namespace today; when it lists
			// the readable namespaces (ADR 18) the list filters with the
			// Authorizer, and this action stays the gate.
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCallerRead},
			Resolve: resolveCurrentUser,
			Serve:   h.currentUser,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/user/stacks",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveListStacks,
			Serve:   h.listStacks,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/stacks/{namespace}/{project}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitManage},
			Resolve: resolveCreateStack,
			Serve:   h.createStack,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/stacks/{namespace}/{project}/{stack}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitRead},
			Resolve: resolveGetStack,
			Serve:   h.getStack,
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

// resolveCurrentUser returns the caller itself: GET /api/user reads the
// caller's own record and acts on no namespace or unit.
func resolveCurrentUser(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
	return identity.CallerObject(), nil
}

// resolveListStacks returns the namespace in the organization query value.
// The project query value only filters the list, so the object stays the
// namespace. It validates the project value the way listStacks does, so an
// invalid filter fails closed before authorization.
func resolveListStacks(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.URL.Query().Get("organization"))
	if err != nil {
		return identity.Object{}, err
	}

	if raw := r.URL.Query().Get("project"); raw != "" {
		if _, err := stack.ParseProjectName(raw); err != nil {
			return identity.Object{}, err
		}
	}

	return identity.NamespaceObject(ns), nil
}

// parseCreateStackRequest decodes a stack create body. The resolver and the
// handler share it, so both accept the same inputs: the whole body must be
// one JSON value.
func parseCreateStackRequest(body []byte) (createStackRequest, error) {
	var req createStackRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return createStackRequest{}, errInvalidBody
	}

	return req, nil
}

// resolveCreateStack returns the unit that the request creates. The stack
// name comes from the JSON body, so this reads the body once with
// http.MaxBytesReader and restores r.Body before returning.
func resolveCreateStack(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	req, err := parseCreateStackRequest(body)
	if err != nil {
		return identity.Object{}, err
	}

	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), req.StackName)
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// resolveGetStack returns the unit in the path values.
func resolveGetStack(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ref, err := stack.ParseRef(r.PathValue("namespace"), r.PathValue("project"), r.PathValue("stack"))
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// enforce wraps one route table row. Public routes run unwrapped. Every
// other route authenticates, resolves the object, authorizes every declared
// action on it, and only then runs the handler.
func (h *Handler) enforce(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if route.Scope == ScopePublic {
			var guest identity.Principal

			var noObject identity.Object

			route.Serve(w, r, guest, noObject)

			return
		}

		p, err := h.authenticate(r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		for _, action := range route.Actions {
			if err := h.authz.Authorize(ctx, p, action, obj); err != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		route.Serve(w, r, p, obj)
	}
}
