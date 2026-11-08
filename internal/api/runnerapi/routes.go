package runnerapi

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Scope names how a route is protected. Every route declares one explicitly
// and the route test enforces the meaning of each value.
type Scope int

const (
	// ScopeAuthorized needs a signed-in principal and the Authorizer to allow
	// the actions of the route on the namespace of the path.
	ScopeAuthorized Scope = iota + 1
	// ScopeJoinToken is the join of a runner: the join token and the
	// signature of the new key authenticate it, and the join token holds
	// identity.ActionRunnerJoin on its pool.
	ScopeJoinToken
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
)

// Route declares one endpoint: its ServeMux pattern, how it is protected,
// the actions it needs and how to serve it. NewHandler registers every route
// from the table that buildRoutes returns, so a route without a row is never
// served.
//
// A route of ScopeAuthorized runs the checks in this order:
//
//  1. The credential is authenticated. A request without a valid credential
//     answers 401, a session that fails the CSRF check or comes from
//     another site answers 403, and too many failed sign-ins answer 429.
//  2. The client address is checked against the IP allowlist of the namespace
//     of the path. A refused address answers 403.
//  3. The namespace of the path is read. An unknown namespace answers 404.
//  4. identity.ActionNamespaceRead on the namespace. A refusal answers 404,
//     the answer of an unknown namespace, so a caller learns nothing about a
//     namespace that it cannot read. This read check applies the IP allowlist
//     a second time, and an allowlist refusal there keeps its 403: it is not
//     mapped to 404.
//  5. Actions on the namespace of the path. A refusal answers 403.
//
// A route of the other scopes checks its credential, and Actions names the
// action that the credential holds on its pool.
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

// Path patterns of the management API.
const (
	poolsPath       = "/api/runners/{namespace}/pools"
	poolPath        = poolsPath + "/{pool}"
	joinTokensPath  = poolPath + "/join-tokens"
	joinTokenPath   = joinTokensPath + "/{id}/revoke"
	poolRunnersPath = poolPath + "/runners"
	drainPath       = "/api/runners/{namespace}/runners/{runner}/drain"
	retirePath      = "/api/runners/{namespace}/runners/{runner}/retire"
)

// buildRoutes returns the route table. It is the single source of the
// patterns that NewHandler registers and of what each pattern enforces.
func (h *Handler) buildRoutes() []Route {
	return slices.Concat(h.managementRoutes(), h.runnerRoutes(), h.launcherRoutes(), h.planRoutes())
}

// managementRoutes returns the routes of the management API.
func (h *Handler) managementRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadRunners}
	manage := []identity.Action{identity.ActionNamespaceManageRunners}

	return []Route{
		{Method: http.MethodGet, Path: poolsPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listPools},
		{Method: http.MethodPost, Path: poolsPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.createPool},
		{Method: http.MethodGet, Path: poolPath, Scope: ScopeAuthorized, Actions: read, Serve: h.getPool},
		{Method: http.MethodPatch, Path: poolPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.updatePool},
		{Method: http.MethodDelete, Path: poolPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.deletePool},
		{Method: http.MethodGet, Path: joinTokensPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listJoinTokens},
		{
			Method:  http.MethodPost,
			Path:    joinTokensPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Serve:   h.createJoinToken,
		},
		{
			Method:  http.MethodPost,
			Path:    joinTokenPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Serve:   h.revokeJoinToken,
		},
		{Method: http.MethodGet, Path: poolRunnersPath, Scope: ScopeAuthorized, Actions: read, Serve: h.listRunners},
		{Method: http.MethodPost, Path: drainPath, Scope: ScopeAuthorized, Actions: manage, Serve: h.drainRunner},
		{Method: http.MethodPost, Path: retirePath, Scope: ScopeAuthorized, Actions: manage, Serve: h.retireRunner},
	}
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

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSession,
			Scope:   ScopeHandoffToken,
			Actions: claim,
			Serve:   h.openSession,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSessionRenew,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.sessionRenew,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSessionStepBegin,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.stepBegin,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSessionStepEnd,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.stepEnd,
		},
		{
			Method:  http.MethodPut,
			Path:    protocol.PathSessionLog,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.putLog,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathSessionFinish,
			Scope:   ScopeSessionKey,
			Actions: claim,
			Serve:   h.finish,
		},
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
