package profile

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
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
	// every declared action on the resolved object before the handler runs.
	ScopeAuthorized
)

// ObjectResolver returns the authorization object for a request.
type ObjectResolver func(w http.ResponseWriter, r *http.Request) (identity.Object, error)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs and how to serve it.
type Route struct {
	Method string
	Path   string
	Scope  Scope
	// Actions lists the actions the wrapper authorizes before the handler
	// runs. A handler can need more: a read with showSecrets also needs
	// profile:open, and a save that changes where secrets go also needs
	// profile:open. The service checks those with the same authorizer.
	Actions []identity.Action
	Resolve ObjectResolver
	// IdempotencyKey says that the route takes the Idempotency-Key header.
	// No route takes it yet: a request with the header answers 400, so the
	// server never ignores a key without an error.
	IdempotencyKey bool
	Serve          RouteHandler
}

const (
	profilesPath  = v1Prefix + "/namespaces/{namespace}/profiles"
	profilePath   = profilesPath + "/{profile}"
	revisionsPath = profilePath + "/revisions"
)

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionProfileRead}
	write := []identity.Action{identity.ActionProfileWrite}
	manage := []identity.Action{identity.ActionProfileManage}

	return []Route{
		h.route(http.MethodGet, profilesPath, read, h.list),
		h.route(http.MethodPost, profilesPath, write, h.create),
		h.route(http.MethodGet, profilePath, read, h.get),
		h.route(http.MethodDelete, profilePath, manage, h.delete),
		h.route(http.MethodGet, revisionsPath, read, h.listRevisions),
		h.route(http.MethodPost, revisionsPath, write, h.save),
		h.route(http.MethodGet, revisionsPath+"/{revision}", read, h.readRevision),
	}
}

// route makes one authorized row. No route takes the Idempotency-Key header
// yet.
func (*Handler) route(method, path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: false, Serve: serve,
	}
}

// Routes returns a copy of the route table.
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
// it, so the route test can list every served route.
type patternMux struct {
	servemux *http.ServeMux

	patterns []string
}

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

// resolveNamespace returns the namespace named in the path. Profile actions
// are checked on the namespace: no grant on a single profile exists yet.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, fmt.Errorf("namespace: %w", err)
	}

	return identity.NamespaceObject(name), nil
}

// enforce wraps one route table row. It authenticates, resolves the object,
// authorizes every declared action on it, and only then runs the handler.
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

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		for _, action := range route.Actions {
			if err = h.authz.Authorize(ctx, p, action, obj); err != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		if mutating(r.Method) && !route.IdempotencyKey && r.Header.Get("Idempotency-Key") != "" {
			h.writeError(ctx, w, errIdempotencyKey)

			return
		}

		route.Serve(w, r, p, obj)
	}
}

// errIdempotencyKey is the answer to a key on a route that does not take it.
var errIdempotencyKey = badRequest(
	"request.idempotency-key-unsupported",
	"this route does not take the Idempotency-Key header yet: send the request without it",
)

// mutating reports whether the method changes data.
func mutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}
