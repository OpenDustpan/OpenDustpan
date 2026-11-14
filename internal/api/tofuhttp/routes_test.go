//nolint:testpackage // The route table is unexported: the test reads it in place.
package tofuhttp

import (
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestEveryRouteDeclaresItsActions proves no row of the route table serves a
// request without an action: each row names the actions that the wrapper
// authorizes on the unit of the path (ADR 49, decision 6).
func TestEveryRouteDeclaresItsActions(t *testing.T) {
	t.Parallel()

	h := &Handler{
		authn:          nil,
		authz:          nil,
		logger:         nil,
		ws:             nil,
		namespaces:     nil,
		audit:          nil,
		onAuditFailure: nil,
		gate:           nil,
		guard:          nil,
		mux:            nil,
		routes:         nil,
		admit:          nil,
		placer:         nil,
	}
	routes := h.buildRoutes()

	if len(routes) == 0 {
		t.Fatal("the route table is empty")
	}

	for _, route := range routes {
		if len(route.Actions) == 0 {
			t.Errorf("%s %s declares no actions", route.Method, route.Path)
		}

		if route.Serve == nil {
			t.Errorf("%s %s has no handler", route.Method, route.Path)
		}
	}

	for _, route := range routes {
		if route.Method != http.MethodGet {
			continue
		}

		want := []identity.Action{identity.ActionUnitRead, identity.ActionUnitReadSecrets}
		if !slices.Equal(route.Actions, want) {
			t.Errorf("GET %s declares %v, want %v: the state holds plain secret values",
				route.Path, route.Actions, want)
		}
	}
}
