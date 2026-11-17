package runnerapi

import (
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

const (
	// ScopeProxyHandoffToken is the session exchange of a job proxy: the proxy
	// handoff token and the signature of the new session key authenticate
	// it, and the session holds identity.ActionRunnerClaim on the pool of its
	// runner. A launcher handoff token never opens a proxy session.
	ScopeProxyHandoffToken Scope = ScopeSessionKey + 1 + iota
	// ScopeProxySessionKey is a request of a job proxy: the proof of the key
	// of its proxy session authenticates it. A launcher session never passes
	// this scope, and a proxy session never passes ScopeSessionKey.
	ScopeProxySessionKey
)

// proxyRoutes returns the routes of the job proxy, and the route of the
// runner that asks for its handoff token.
func (h *Handler) proxyRoutes() []Route {
	claim := []identity.Action{identity.ActionRunnerClaim}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    protocol.PathClaimProxyHandoff,
			Scope:   ScopeRunnerKey,
			Actions: claim,
			Serve:   h.proxyHandoff,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathProxySession,
			Scope:   ScopeProxyHandoffToken,
			Actions: claim,
			Serve:   h.openProxySession,
		},
		{
			Method:  http.MethodPost,
			Path:    protocol.PathProxyUpstreamCredential,
			Scope:   ScopeProxySessionKey,
			Actions: claim,
			Serve:   h.upstreamCredential,
		},
	}
}

// sessionKindOf returns the kind of session that a route of the scope
// accepts, and false for a scope that has no session: a join, a runner key
// and an unknown scope pass no session (fail closed).
func sessionKindOf(scope Scope) (runner.SessionKind, bool) {
	kind, ok := map[Scope]runner.SessionKind{
		ScopeHandoffToken:      runner.SessionKindLauncher,
		ScopeSessionKey:        runner.SessionKindLauncher,
		ScopeProxyHandoffToken: runner.SessionKindProxy,
		ScopeProxySessionKey:   runner.SessionKindProxy,
	}[scope]

	return kind, ok
}
