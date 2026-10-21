package notifications

import (
	"context"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
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

// answerFunc makes the answer of a write from the result that the store
// passes. The answer is the same when a store keeps it for a replay.
type answerFunc func(result any) (idempotency.Answer, error)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs and how to serve it.
type Route struct {
	Method  string
	Path    string
	Scope   Scope
	Actions []identity.Action
	Resolve ObjectResolver
	// IdempotencyKey says that the route takes the Idempotency-Key header.
	// A request with the header to a route that does not take it answers
	// 400, so the server never ignores a key without an error.
	IdempotencyKey bool
	// Answer makes the answer of a route that takes the key from the result
	// of its write. It is nil for a route that does not take the key.
	Answer func(st *requestState) answerFunc
	// SecretAnswer says that the answer holds a secret. The stored record then
	// keeps the ID of the object that the request made in place of the answer,
	// and a replay answers 409 with that ID.
	SecretAnswer func(result any) (objectID string)
	Serve        RouteHandler
}

const (
	sinksPath = v1Prefix + "/namespaces/{namespace}/sinks"
	sinkPath  = sinksPath + "/{sink_id}"
	testPath  = sinkPath + "/test"
)

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceRead}
	manage := []identity.Action{identity.ActionNamespaceManageNotifications}

	return []Route{
		h.readRoute(sinksPath, read, h.list),
		h.keyedRoute(http.MethodPost, sinksPath, manage, h.create, createdAnswer, sinkID),
		h.readRoute(sinkPath, read, h.get),
		h.keyedRoute(http.MethodPut, sinkPath, manage, h.replace, replacedAnswer, nil),
		h.keyedRoute(http.MethodDelete, sinkPath, manage, h.delete, deletedAnswer, nil),
		h.keyedRoute(http.MethodPost, testPath, manage, h.test, testedAnswer, nil),
	}
}

// readRoute makes one authorized GET row. A read does not take the
// Idempotency-Key header.
func (*Handler) readRoute(path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: http.MethodGet, Path: path, Scope: ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: false, Answer: nil, SecretAnswer: nil, Serve: serve,
	}
}

// keyedRoute makes one authorized row that takes the Idempotency-Key header.
func (*Handler) keyedRoute(
	method, path string, actions []identity.Action, serve RouteHandler, answer func(*requestState) answerFunc,
	secret func(any) string,
) Route {
	return Route{
		Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: true, Answer: answer, SecretAnswer: secret, Serve: serve,
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

// resolveNamespace returns the namespace named in the path. Sink actions are
// checked on the namespace: no grant on a single sink exists.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, fmt.Errorf("namespace: %w", err)
	}

	return identity.NamespaceObject(name), nil
}

// requestState carries what a handler learns while it serves a request and
// what the answer of that request needs: the signing key that a create makes
// and the result of a test. The store builds the answer inside its
// transaction, after the handler set the fields.
type requestState struct {
	signingKey string
	test       sinks.TestResult
}

type stateKey struct{}

func withState(ctx context.Context, st *requestState) context.Context {
	return context.WithValue(ctx, stateKey{}, st)
}

// stateOf returns the state of the request. A request that did not pass the
// route wrapper has none, and gets a state of its own.
func stateOf(ctx context.Context) *requestState {
	if st, ok := ctx.Value(stateKey{}).(*requestState); ok {
		return st
	}

	return &requestState{}
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
		}

		r = r.WithContext(withState(ctx, &requestState{}))

		obj, err := route.Resolve(w, r)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		// Authorize every action on the object at the point of use. A replay of
		// a keyed request passes through here too, so a caller who lost the
		// action gets 403 and never the stored answer.
		for _, action := range route.Actions {
			if err = h.guard.Authorize(ctx, r, p, action, obj); err != nil {
				h.writeError(ctx, w, fmt.Errorf("authorize %s: %w", action, err))

				return
			}
		}

		if mutating(r.Method) && idempotent.Present(r) {
			h.serveKeyed(w, r, route, p, obj)

			return
		}

		route.Serve(w, r, p, obj)
	}
}

// serveKeyed serves a mutating request that has the Idempotency-Key header.
// The caller has authorized it: a replay never skips the check of the action.
func (h *Handler) serveKeyed(
	w http.ResponseWriter, r *http.Request, route Route, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if !route.IdempotencyKey || h.idem == nil {
		h.writeError(ctx, w, errIdempotencyKey)

		return
	}

	err := h.idem.Handle(w, r, idempotent.Call{
		Principal:    p.Subject,
		Build:        route.Answer(stateOf(ctx)),
		SecretAnswer: route.SecretAnswer,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { route.Serve(w, r, p, obj) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// errIdempotencyKey is the answer to a key on a route that does not take it.
var errIdempotencyKey = badRequest(
	"request.idempotency-key-unsupported",
	"this route does not take the Idempotency-Key header: send the request without it",
)

// mutating reports whether the method changes data.
func mutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}
