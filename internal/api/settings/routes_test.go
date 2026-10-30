package settings_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/pkg/identity"
)

// shapeProblem returns what is wrong with the row of a route, or "".
func shapeProblem(route *settings.Route) string {
	declared := len(route.Reads) + len(route.Actions) + len(route.Extra)

	switch {
	case route.Scope != settings.ScopeAuthorized:
		return "is not authorized: this API serves no public route"
	case declared == 0 && route.Method != http.MethodGet:
		return "declares no action"
	case route.Resolve == nil || route.Serve == nil:
		return "declares no resolver or no handler"
	case route.Method != http.MethodGet && len(route.Actions)+len(route.Extra) == 0:
		return "is a write route with no action"
	default:
		return ""
	}
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no action, no resolver or no handler, and for a write route with no
// action. It checks both directions: every served pattern has a table row, and
// every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newRig(t, fullAccess()...).h
	byPattern := map[string]bool{}

	routes := handler.Routes()
	for i := range routes {
		pattern := routes[i].Method + " " + routes[i].Path
		if byPattern[pattern] {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = true

		if problem := shapeProblem(&routes[i]); problem != "" {
			t.Errorf("route %q %s", pattern, problem)
		}
	}

	served := handler.RegisteredPatterns()
	if len(served) != len(byPattern) {
		t.Fatalf("served %d patterns, table has %d rows", len(served), len(byPattern))
	}

	for _, pattern := range served {
		if !byPattern[pattern] {
			t.Errorf("pattern %q is served and has no table row", pattern)
		}
	}
}

// TestRoutesNeedSignIn proves that each route fails closed for a request with
// no token: 401, never 404 and never a handler run.
func TestRoutesNeedSignIn(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	for _, route := range r.h.Routes() {
		path := strings.NewReplacer("{namespace}", "acme", "{unit_id}", netID).Replace(route.Path)

		rec := serve(t, r.h, request{method: route.Method, path: path, token: "", body: "{}", header: nil})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("route %q with no token = %d, want 401", route.Method+" "+route.Path, rec.Code)
		}

		rec = serve(t, r.h, request{method: route.Method, path: path, token: "wrong", body: "{}", header: nil})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("route %q with a wrong token = %d, want 401", route.Method+" "+route.Path, rec.Code)
		}
	}

	if got := r.authz.seen(); len(got) != 0 {
		t.Fatalf("a request with no sign-in reached the authorizer: %v", got)
	}
}

// checksOf returns the checks that the wrapper makes for route, in order, and
// the number of them that are reads.
func checksOf(route *settings.Route) ([]string, int) {
	object := "opentofu:acme/net"
	if strings.Contains(route.Path, "/settings/unit-defaults") || strings.Contains(route.Path, "/unit-settings/") {
		object = "acme"
	}

	checks := make([]string, 0, 1+len(route.Reads)+len(route.Actions))
	checks = append(checks, "namespace:read acme")

	for _, a := range route.Reads {
		checks = append(checks, string(a)+" "+object)
	}

	reads := len(checks)

	for _, a := range route.Actions {
		checks = append(checks, string(a)+" "+object)
	}

	return checks, reads
}

// TestRoutesCheckTheirActionsInOrder proves for each route that the wrapper
// reads the namespace first, then the reads, then the actions, and that a
// caller who lacks a read gets 404 and a caller who lacks an action gets 403.
func TestRoutesCheckTheirActionsInOrder(t *testing.T) {
	t.Parallel()

	routes := newRig(t, fullAccess()...).h.Routes()
	for i := range routes {
		route := &routes[i]
		name := route.Method + " " + route.Path
		path := strings.NewReplacer("{namespace}", "acme", "{unit_id}", netID).Replace(route.Path)
		checks, reads := checksOf(route)

		for held := range checks {
			r := newRig(t, slices.Clone(checks[:held])...)
			rec := r.call(t, route.Method, path, "{}", ifMatch(`"0"`))

			want := http.StatusForbidden
			if held < reads {
				want = http.StatusNotFound
			}

			if rec.Code != want {
				t.Errorf("route %q holding %d of %d checks = %d, want %d", name, held, len(checks), rec.Code, want)
			}

			if seen := r.authz.seen(); len(seen) == 0 || seen[0] != "namespace:read acme" {
				t.Errorf("route %q: the first check is %v, want namespace:read first", name, seen)
			}
		}
	}
}

// TestEveryRouteHasAnOperation proves that the description names each route.
func TestEveryRouteHasAnOperation(t *testing.T) {
	t.Parallel()

	h := newRig(t, fullAccess()...).h
	ops := h.Operations()

	if len(ops) != len(h.Routes()) {
		t.Fatalf("%d operations for %d routes", len(ops), len(h.Routes()))
	}

	for i := range ops {
		if ops[i].ID == "" {
			t.Errorf("operation %s %s has no ID", ops[i].Method, ops[i].Path)
		}
	}

	var extra []identity.Action

	for _, route := range h.Routes() {
		extra = append(extra, route.Extra...)
	}

	if !slices.Contains(extra, identity.ActionNamespaceUseGitHostConnection) {
		t.Fatal("no route declares the use action of a Git host connection")
	}
}
