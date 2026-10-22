package profile

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
// this API has no route that serves without sign-in, so its description
// marks no route public.
//
// The Actions of the row list the actions that the wrapper authorizes before
// the handler runs. A handler can need more: a read with showSecrets also
// needs profile:open, and a save that changes where secrets go also needs
// profile:open. The service checks those with the same authorizer.
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
		h.readRoute(profilesPath, read, h.list),
		h.keyedRoute(http.MethodPost, profilesPath, write, h.create, createdAnswer),
		h.readRoute(profilePath, read, h.get),
		h.keyedRoute(http.MethodDelete, profilePath, manage, h.delete, func(identity.Object) answerFunc {
			return deletedAnswer()
		}),
		h.readRoute(revisionsPath, read, h.listRevisions),
		h.keyedRoute(http.MethodPost, revisionsPath, write, h.save, func(identity.Object) answerFunc {
			return savedAnswer(http.StatusCreated)
		}),
		h.readRoute(revisionsPath+"/{revision}", read, h.readRevision),
	}
}

// readRoute makes one authorized GET row. A read does not take the
// Idempotency-Key header.
func (*Handler) readRoute(path string, actions []identity.Action, serve RouteHandler) Route {
	return Route{
		Method: http.MethodGet, Path: path, Scope: route.ScopeAuthorized, Actions: actions,
		Resolve: resolveNamespace, Serve: serve,
		IdempotencyKey: false, Answer: nil,
	}
}

// keyedRoute makes one authorized row that takes the Idempotency-Key header.
// answer makes the answer that the route keeps for a replay.
func (*Handler) keyedRoute(
	method, path string, actions []identity.Action, serve RouteHandler, answer func(identity.Object) answerFunc,
) Route {
	return Route{
		Method: method, Path: path, Scope: route.ScopeAuthorized, Actions: actions,
		Resolve: resolveNamespace, Serve: serve,
		IdempotencyKey: true, Answer: answer,
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

// resolveNamespace returns the namespace named in the path. Profile actions
// are checked on the namespace: no grant on a single profile exists yet.
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
// caller who lost the action gets 403 and never the stored answer.
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
