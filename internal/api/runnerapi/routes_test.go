package runnerapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// protocolPaths are the paths of the runner side of the protocol that this
// adapter serves, with the action that their credential holds.
func protocolPaths() map[string]identity.Action {
	return map[string]identity.Action{
		protocol.PathJoin:         identity.ActionRunnerJoin,
		protocol.PathRotate:       identity.ActionRunnerClaim,
		protocol.PathHeartbeat:    identity.ActionRunnerClaim,
		protocol.PathPoll:         identity.ActionRunnerClaim,
		protocol.PathClaimHandoff: identity.ActionRunnerClaim,
		protocol.PathClaimRenew:   identity.ActionRunnerClaim,
		protocol.PathClaimRelease: identity.ActionRunnerClaim,

		protocol.PathSession:          identity.ActionRunnerClaim,
		protocol.PathSessionRenew:     identity.ActionRunnerClaim,
		protocol.PathSessionStepBegin: identity.ActionRunnerClaim,
		protocol.PathSessionStepEnd:   identity.ActionRunnerClaim,
		protocol.PathSessionLog:       identity.ActionRunnerClaim,
		protocol.PathSessionFinish:    identity.ActionRunnerClaim,

		protocol.PathSessionPlanUpload: identity.ActionRunnerClaim,
		protocol.PathSessionPlanCommit: identity.ActionRunnerClaim,
		protocol.PathSessionPlanPart:   identity.ActionRunnerClaim,
	}
}

// TestRouteTable proves the rules of the route table: the table and the mux
// list the same patterns, each route declares an action and a handler, and a
// protocol route declares the action of its credential.
func TestRouteTable(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	routes := e.handler.Routes()
	paths := protocolPaths()
	seen := map[string]bool{}
	patterns := make([]string, 0, len(routes))

	for i := range routes {
		route := &routes[i]
		patterns = append(patterns, route.Method+" "+route.Path)

		if len(route.Actions) == 0 || route.Serve == nil {
			t.Errorf("route %s %s declares no action or has no handler", route.Method, route.Path)
		}

		if !strings.HasPrefix(route.Path, "/runner/v1/") {
			t.Errorf("route %s %s is outside /runner/v1", route.Method, route.Path)
			continue
		}

		checkProtocolRoute(t, route, paths)

		seen[route.Path] = true
	}

	for path := range paths {
		if !seen[path] {
			t.Errorf("protocol path %s has no route", path)
		}
	}

	registered := e.handler.RegisteredPatterns()

	slices.Sort(patterns)
	slices.Sort(registered)

	if !slices.Equal(patterns, registered) {
		t.Errorf("table patterns = %v, registered = %v, want equal", patterns, registered)
	}
}

func checkProtocolRoute(t *testing.T, route *runnerapi.Route, paths map[string]identity.Action) {
	t.Helper()

	want, ok := paths[route.Path]
	if !ok {
		t.Errorf("route %s is a protocol path that this adapter does not list", route.Path)

		return
	}

	if len(route.Actions) != 1 || route.Actions[0] != want {
		t.Errorf("route %s declares %v, want %s", route.Path, route.Actions, want)
	}

	wantScope := runnerapi.ScopeRunnerKey

	switch {
	case route.Path == protocol.PathJoin:
		wantScope = runnerapi.ScopeJoinToken
	case route.Path == protocol.PathSession:
		wantScope = runnerapi.ScopeHandoffToken
	case strings.HasPrefix(route.Path, protocol.PathSession+"/"):
		wantScope = runnerapi.ScopeSessionKey
	default:
	}

	if route.Scope != wantScope {
		t.Errorf("route %s has scope %d, want %d", route.Path, route.Scope, wantScope)
	}
}

// TestRoutesRefuseARequestWithNoCredential sends each route a request with
// no credential and proves that none answers with success.
func TestRoutesRefuseARequestWithNoCredential(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for _, route := range e.handler.Routes() {
		path := strings.NewReplacer(
			"{operation}", "op-1",
		).Replace(route.Path)

		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), route.Method, e.server.URL+path, http.NoBody)
			if err != nil {
				t.Fatalf("request: %v", err)
			}

			req.Header.Set(protocol.HeaderRelease, "0.4.0")

			status, _ := e.send(req)
			if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
				t.Fatalf("status = %d, want a 4xx refusal", status)
			}
		})
	}
}
