package settings_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no action, no resolver or no handler, and for a write route with no
// action. It checks both directions: every served pattern has a table row, and
// every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newRig(t, fullAccess()...).h
	byPattern := map[string]settings.Route{}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		declared := len(route.Reads) + len(route.Actions) + len(route.Extra)

		switch {
		case route.Scope != settings.ScopeAuthorized:
			t.Errorf("route %q is not authorized: this API serves no public route", pattern)
		case declared == 0 && route.Method != http.MethodGet:
			t.Errorf("route %q declares no action", pattern)
		case route.Resolve == nil || route.Serve == nil:
			t.Errorf("route %q declares no resolver or no handler", pattern)
		case route.Method != http.MethodGet && len(route.Actions)+len(route.Extra) == 0:
			t.Errorf("route %q: a write route needs an action", pattern)
		default:
		}
	}

	served := handler.RegisteredPatterns()
	if len(served) != len(byPattern) {
		t.Fatalf("served %d patterns, table has %d rows", len(served), len(byPattern))
	}

	for _, pattern := range served {
		if _, ok := byPattern[pattern]; !ok {
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

// TestRoutesCheckTheirActionsInOrder proves for each route that the wrapper
// reads the namespace first, then the reads, then the actions, and that a
// caller who lacks a read gets 404 and a caller who lacks an action gets 403.
func TestRoutesCheckTheirActionsInOrder(t *testing.T) {
	t.Parallel()

	for _, route := range newRig(t, fullAccess()...).h.Routes() {
		path := strings.NewReplacer("{namespace}", "acme", "{unit_id}", netID).Replace(route.Path)
		object := "opentofu:acme/net"

		if strings.Contains(route.Path, "/settings/unit-defaults") || strings.Contains(route.Path, "/unit-settings/") {
			object = "acme"
		}

		checks := []string{"namespace:read acme"}
		for _, a := range route.Reads {
			checks = append(checks, string(a)+" "+object)
		}

		reads := len(checks)

		for _, a := range route.Actions {
			checks = append(checks, string(a)+" "+object)
		}

		for held := range len(checks) {
			r := newRig(t, slices.Clone(checks[:held])...)
			rec := r.call(t, route.Method, path, "{}", ifMatch(`"0"`))

			want := http.StatusForbidden
			if held < reads {
				want = http.StatusNotFound
			}

			if rec.Code != want {
				t.Errorf("route %q holding %d of %d checks = %d, want %d", route.Method+" "+route.Path, held,
					len(checks), rec.Code, want)
			}

			if seen := r.authz.seen(); len(seen) == 0 || seen[0] != "namespace:read acme" {
				t.Errorf("route %q: the first check is %v, want namespace:read first", route.Method+" "+route.Path, seen)
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
