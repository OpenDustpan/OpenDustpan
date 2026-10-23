package registry

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// RouteHandler serves a route after the registration wrapper has
// authenticated and authorized it.
type RouteHandler = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object)

// Route declares one endpoint: the shared row (its ServeMux pattern, its
// scope, the actions it needs, its resolver and its handler) and the fields
// of the Idempotency-Key. Every route has the scope route.ScopeAuthorized:
// this API has no route that serves without sign-in. The wrapper authorizes
// each of the Actions of the row on the object that Resolve returns, before
// the handler runs.
type Route struct {
	route.Route[identity.Object]

	// IdempotencyKey says that the route takes the Idempotency-Key header.
	// A request with the header to a route that does not take it answers
	// 400, so the server never ignores a key without an error.
	IdempotencyKey bool
	// Answer makes the answer of a route that takes the key from the result
	// of its write. It is nil for a route that does not take the key.
	Answer func(obj identity.Object) answerFunc
}

const (
	registryPath  = v1Prefix + "/namespaces/{namespace}/registry"
	uploadsPath   = registryPath + "/uploads"
	providersPath = registryPath + "/providers"
	versionsPath  = registryPath + "/providers/{name}/versions"
	versionPath   = versionsPath + "/{version}"
	keysPath      = registryPath + "/keys"
	keyPath       = keysPath + "/{fingerprint}"
)

// buildRoutes returns the route table. It is the single source of the patterns
// that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	read := []identity.Action{identity.ActionRegistryRead}
	publish := []identity.Action{identity.ActionRegistryPublish}
	keys := []identity.Action{identity.ActionRegistryManageKeys}

	return []Route{
		// A staged file is a large body that the middleware of the key cannot
		// hold in memory, so this route declares that it takes no key.
		h.plainRoute(http.MethodPost, uploadsPath, publish, h.stage),
		h.keyedRoute(http.MethodPut, versionPath, publish, h.publish, releaseAnswer),
		h.readRoute(providersPath, read, h.listProviders),
		h.readRoute(versionsPath, read, h.listReleases),
		h.readRoute(versionPath, read, h.getRelease),
		h.keyedRoute(http.MethodPost, keysPath, keys, h.addKey, keyAnswer),
		h.readRoute(keysPath, keys, h.listKeys),
		h.keyedRoute(http.MethodDelete, keyPath, keys, h.removeKey, removedKeyAnswer),
	}
}

// readRoute makes one authorized GET row. A read does not take the
// Idempotency-Key header.
func (*Handler) readRoute(path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: http.MethodGet, Path: path, Scope: route.ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: false, Answer: nil, Serve: serve,
	}
}

// plainRoute makes one authorized row that changes data and does not take the
// Idempotency-Key header.
func (*Handler) plainRoute(method, path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: method, Path: path, Scope: route.ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: false, Answer: nil, Serve: serve,
	}
}

// keyedRoute makes one authorized row that takes the Idempotency-Key header.
// answer makes the answer that the route keeps for a replay.
func (*Handler) keyedRoute(
	method, path string, actions []identity.Action, serve RouteHandler, answer func(identity.Object) answerFunc,
) Route {
	return Route{
		Method: method, Path: path, Scope: route.ScopeAuthorized, Actions: actions, Resolve: resolveNamespace,
		IdempotencyKey: true, Answer: answer, Serve: serve,
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

// resolveNamespace returns the namespace named in the path. Registry actions
// are checked on the namespace: no grant on a single address exists yet.
func resolveNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, fmt.Errorf("namespace: %w", err)
	}

	return identity.NamespaceObject(name), nil
}

// kit returns the route steps that this API shares with the other
// tool-neutral APIs.
func (h *Handler) kit() route.Guard {
	return route.Guard{Tokens: h.authn, Sessions: h.sessions, Refusals: h.guard}
}

// enforce wraps one route table row in the shared wrapper: it
// authenticates, resolves the object, authorizes every declared action on it,
// and only then runs the handler. A mutating request with the
// Idempotency-Key header goes to the keyed path after the same checks, so a
// replay never skips the check of the action.
func (h *Handler) enforce(rt Route) http.HandlerFunc {
	steps := route.StepsOf(h.kit(), rt.Route, route.Self)
	steps.Serve = func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		if route.Mutating(r.Method) && idempotent.Present(r) {
			h.serveKeyed(w, r, rt, p, obj)

			return
		}

		rt.Serve(w, r, p, obj)
	}

	return route.Enforce(h.kit(), h.writeError, steps)
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
		Build:        rt.Answer(obj),
		SecretAnswer: nil,
		Required:     false,
		Serve:        func(w http.ResponseWriter, r *http.Request) { rt.Serve(w, r, p, obj) },
	})
	if err != nil {
		h.writeError(ctx, w, err)
	}
}

// errIdempotencyKey is the answer to a key on a route that does not take it.
var errIdempotencyKey = problem.BadRequest(
	"request.idempotency-key-unsupported",
	"this route does not take the Idempotency-Key header: send the request without it",
)
