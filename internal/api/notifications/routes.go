package notifications

import (
	"context"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// answerFunc makes the answer of a write from the result that the store
// passes. The answer is the same when a store keeps it for a replay.
type answerFunc func(result any) (idempotency.Answer, error)

// Route declares one endpoint: the shared row (its ServeMux pattern, its
// scope, the actions it needs, its resolver and its handler) and the fields
// of the Idempotency-Key. Every route has the scope route.ScopeAuthorized:
// this API has no route that serves without sign-in, so its description
// marks no route public.
type Route struct {
	route.Route[identity.Object]

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
		keyedRoute(keyedSpec{
			method: http.MethodPost, path: sinksPath, actions: manage, serve: h.create, answer: h.createdAnswer,
			secret: sinkID,
		}),
		h.readRoute(sinkPath, read, h.get),
		keyedRoute(keyedSpec{
			method: http.MethodPut, path: sinkPath, actions: manage, serve: h.replace, answer: h.replacedAnswer,
			secret: nil,
		}),
		keyedRoute(keyedSpec{
			method: http.MethodDelete, path: sinkPath, actions: manage, serve: h.delete, answer: deletedAnswer,
			secret: nil,
		}),
		keyedRoute(keyedSpec{
			method: http.MethodPost, path: testPath, actions: manage, serve: h.test, answer: testedAnswer,
			secret: nil,
		}),
		h.readRoute(subscriptionsPath, read, h.listSubscriptions),
		keyedRoute(keyedSpec{
			method: http.MethodPost, path: subscriptionsPath, actions: manage, serve: h.createSubscription,
			answer: subscriptionCreatedAnswer, secret: nil,
		}),
		h.readRoute(subscriptionPath, read, h.getSubscription),
		keyedRoute(keyedSpec{
			method: http.MethodPut, path: subscriptionPath, actions: manage, serve: h.replaceSubscription,
			answer: subscriptionReplacedAnswer, secret: nil,
		}),
		keyedRoute(keyedSpec{
			method: http.MethodDelete, path: subscriptionPath, actions: manage, serve: h.deleteSubscription,
			answer: deletedAnswer, secret: nil,
		}),
		h.readRoute(historyPath, read, h.history),
		keyedRoute(keyedSpec{
			method: http.MethodPost, path: redeliverPath, actions: manage, serve: h.redeliver,
			answer: deletedAnswer, secret: nil,
		}),
	}
}

// readRoute makes one authorized GET row. A read does not take the
// Idempotency-Key header.
func (*Handler) readRoute(path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: http.MethodGet, Path: path, Scope: route.ScopeAuthorized, Actions: actions,
		Resolve: resolveNamespace, Serve: serve,
		IdempotencyKey: false, Answer: nil, SecretAnswer: nil,
	}
}

// keyedSpec describes a route that takes the Idempotency-Key header. secret
// is set for a route whose answer holds a secret.
type keyedSpec struct {
	method  string
	path    string
	actions []identity.Action
	serve   RouteHandler
	answer  func(*requestState) answerFunc
	secret  func(any) string
}

// keyedRoute makes one authorized row that takes the Idempotency-Key header.
func keyedRoute(spec keyedSpec) Route {
	return Route{
		Method: spec.method, Path: spec.path, Scope: route.ScopeAuthorized, Actions: spec.actions,
		Resolve: resolveNamespace, Serve: spec.serve,
		IdempotencyKey: true, Answer: spec.answer, SecretAnswer: spec.secret,
	}
}

// Routes returns a copy of the route table.
func (h *Handler) Routes() []Route {
	out := make([]Route, len(h.routes))
	for i, rt := range h.routes {
		out[i] = rt
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

// newState returns an empty state of a request.
func newState() *requestState {
	return &requestState{signingKey: "", test: sinks.TestResult{Delivered: false, Class: "", Message: ""}}
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

	return newState()
}

// kit returns the route steps that this API shares with the other
// tool-neutral APIs.
func (h *Handler) kit() route.Guard {
	return route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
}

// enforce wraps one route table row in the shared wrapper: it
// authenticates, resolves the object, authorizes every declared action on it,
// and only then runs the handler. A replay of a keyed request passes through
// the same checks, so a caller who lost the action gets 403 and never the
// stored answer. The request state that the answers read is on the context
// before the wrapper runs.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	steps := route.StepsOf(h.kit(), rt.Route, route.Self)
	steps.Serve = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		if route.Mutating(r.Method) && idempotent.Present(r) {
			h.serveKeyed(w, r, rt, p, obj)

			return
		}

		rt.Serve(w, r, p, obj)
	}

	enforced := route.Enforce(h.kit(), h.writeError, steps)

	return func(w http.ResponseWriter, r *http.Request) {
		enforced(w, r.WithContext(withState(r.Context(), newState())))
	}
}

// serveKeyed serves a mutating request that has the Idempotency-Key header.
// The caller has authorized it: a replay never skips the check of the action.
func (h *Handler) serveKeyed(
	w http.ResponseWriter, r *http.Request, rt Route, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if !rt.IdempotencyKey || h.idem == nil {
		h.writeError(ctx, w, errIdempotencyKey)

		return
	}

	err := h.idem.Handle(w, r, idempotent.Call{
		Principal:    p.Subject,
		Build:        rt.Answer(stateOf(ctx)),
		SecretAnswer: rt.SecretAnswer,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { rt.Serve(w, r, p, obj) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// errIdempotencyKey is the answer to a key on a route that does not take it.
var errIdempotencyKey = problem.BadRequest(
	errcode.RequestIdempotencyKeyUnsupported,
	"this route does not take the Idempotency-Key header: send the request without it",
)
