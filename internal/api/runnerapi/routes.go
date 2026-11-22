package runnerapi

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopeJoinToken is the join of a runner: the join token and the
	// signature of the new key authenticate it, and the join token holds
	// identity.ActionRunnerJoin on its pool.
	ScopeJoinToken Scope = iota + 1
	// ScopeRunnerKey is a request of a runner: the proof of its key
	// authenticates it, and the key holds identity.ActionRunnerClaim on its
	// pool.
	ScopeRunnerKey
	// ScopeHandoffToken is the session exchange of a launcher: the handoff
	// token and the signature of the new session key authenticate it, and
	// the session holds identity.ActionRunnerClaim on the pool of its runner.
	ScopeHandoffToken
	// ScopeSessionKey is a request of a launcher: the proof of the key of its
	// job session authenticates it, and the session holds
	// identity.ActionRunnerClaim on the pool of its runner. A runner key
	// never passes this scope, and a session key never passes ScopeRunnerKey.
	ScopeSessionKey
	// ScopeJobToken is a request with a bearer job token or an access
	// token: the handler authenticates the credential itself, because the
	// token, not a runner or session key, carries the call. The wrapper
	// checks the release and reads the body only.
	ScopeJobToken
)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs and how to serve it. NewHandler registers every route
// from the table that buildRoutes returns, so a route without a row is never
// served. A route checks its credential, and Actions names the action that
// the credential holds on its pool.
type Route struct {
	// Method is the HTTP method, for example GET.
	Method string
	// Path is the ServeMux path pattern.
	Path string
	// Scope names how the route is protected.
	Scope Scope
	// Actions lists the actions that the route needs.
	Actions []identity.Action
	// Serve handles the request after the wrapper has enforced the row.
	Serve routeHandler
}

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	routes := append(append([]Route(nil), h.runnerRoutes()...), h.launcherRoutes()...)

	routes = append(append(routes, h.planRoutes()...), h.proxyRoutes()...)

	return append(routes, h.claimRoutes()...)
}

// runnerRoutes returns the routes of the runner side of the protocol.
func (h *Handler) runnerRoutes() []Route {
	join := []identity.Action{identity.ActionRunnerJoin}
	claim := []identity.Action{identity.ActionRunnerClaim}

	return []Route{
		{Method: http.MethodPost, Path: protocol.PathJoin, Scope: ScopeJoinToken, Actions: join, Serve: h.join},
		{Method: http.MethodPost, Path: protocol.PathRotate, Scope: ScopeRunnerKey, Actions: claim, Serve: h.rotate},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathHeartbeat,
			Scope:   ScopeRunnerKey,
			Actions: claim,
			Serve:   h.heartbeat,
		},
		{Method: http.MethodPost, Path: protocol.PathPoll, Scope: ScopeRunnerKey, Actions: claim, Serve: h.poll},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathClaimHandoff,
			Scope:   ScopeRunnerKey,
			Actions: claim,
			Serve:   h.handoff,
		},
		{Method: http.MethodPost, Path: protocol.PathClaimRenew, Scope: ScopeRunnerKey, Actions: claim, Serve: h.renew},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathClaimRelease,
			Scope:   ScopeRunnerKey,
			Actions: claim,
			Serve:   h.releaseClaim,
		},
	}
}

// launcherRoutes returns the routes of the launcher side of the protocol.
func (h *Handler) launcherRoutes() []Route {
	claim := []identity.Action{identity.ActionRunnerClaim}

	// Each route after the exchange is a request of the job session: the proof
	// of the session key authenticates it and the session holds the claim
	// action.
	session := func(method, path string, serve routeHandler) Route {
		return Route{Method: method, Path: path, Scope: ScopeSessionKey, Actions: claim, Serve: serve}
	}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSession,
			Scope:   ScopeHandoffToken,
			Actions: claim,
			Serve:   h.openSession,
		},
		session(http.MethodPost, protocol.PathSessionCredentials, h.credentials),
		session(http.MethodPost, protocol.PathSessionRenew, h.sessionRenew),
		session(http.MethodPost, protocol.PathSessionStepBegin, h.stepBegin),
		session(http.MethodPost, protocol.PathSessionStepEnd, h.stepEnd),
		session(http.MethodPut, protocol.PathSessionLog, h.putLog),
		session(http.MethodPost, protocol.PathSessionFinish, h.finish),
	}
}

// Routes returns a copy of the route table. The action lists are copied too,
// so callers cannot mutate handler state through the result.
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
