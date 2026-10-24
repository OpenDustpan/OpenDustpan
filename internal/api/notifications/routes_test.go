package notifications_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
)

// routeTarget fills the path values of a route.
func routeTarget(rt apinotifications.Route) string {
	return strings.NewReplacer("{namespace}", "acme", "{sink_id}", "snk-aaaaaaaaaaaaaaaaaaaa").Replace(rt.Path)
}

// TestRoutesDeclareActions proves that each route is authorized, declares its
// actions, and takes the Idempotency-Key header exactly when it changes data.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	byPattern := map[string]apinotifications.Route{}

	for _, rt := range f.handler.Routes() {
		pattern := rt.Method + " " + rt.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = rt

		checkRouteDeclaration(t, pattern, rt)
	}

	// The routes of the design: the list and the get, the create, the replace,
	// the delete and the test.
	want := replaceNamespace([]string{
		"GET " + base, "POST " + base, "GET " + base + "/{sink_id}", "PUT " + base + "/{sink_id}",
		"DELETE " + base + "/{sink_id}", "POST " + base + "/{sink_id}/test",
	})

	if len(byPattern) != len(want) {
		t.Fatalf("routes = %d, want %d", len(byPattern), len(want))
	}

	for _, pattern := range want {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("route %q is missing", pattern)
		}
	}

	checkServedPatterns(t, f, byPattern)
	checkNoTokenFailsClosed(t, f, byPattern)
}

// checkRouteDeclaration checks one row of the route table.
func checkRouteDeclaration(t *testing.T, pattern string, rt apinotifications.Route) {
	t.Helper()

	if rt.Scope != route.ScopeAuthorized || len(rt.Actions) == 0 || rt.Resolve == nil {
		t.Fatalf("route %q must be authorized with actions and a resolver", pattern)
	}

	mutating := rt.Method != http.MethodGet
	if rt.IdempotencyKey != mutating || (rt.Answer != nil) != mutating {
		t.Errorf("route %q: takes the key = %t, has an answer = %t, want both %t",
			pattern, rt.IdempotencyKey, rt.Answer != nil, mutating)
	}

	if has := slices.Contains(rt.Actions, identity.ActionNamespaceManageNotifications); has != mutating {
		t.Errorf("route %q: manage-notifications = %t, want %t", pattern, has, mutating)
	}
}

// checkServedPatterns checks that the server serves the declared patterns and
// no other.
func checkServedPatterns(t *testing.T, f fixture, byPattern map[string]apinotifications.Route) {
	t.Helper()

	served := f.handler.RegisteredPatterns()

	for _, pattern := range served {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	if len(served) != len(byPattern) {
		t.Fatalf("served %d patterns, declared %d", len(served), len(byPattern))
	}
}

// checkNoTokenFailsClosed checks that a request without a token answers 401,
// never 404 and never the handler.
func checkNoTokenFailsClosed(t *testing.T, f fixture, byPattern map[string]apinotifications.Route) {
	t.Helper()

	for pattern, rt := range byPattern {
		got := f.do(t, send("", rt.Method, routeTarget(rt), ""))
		wantCode(t, "route "+pattern+" without a token", got, http.StatusUnauthorized)
	}

	if f.hook.count() != 0 {
		t.Errorf("sender got %d messages without a token, want none", f.hook.count())
	}
}

// replaceNamespace puts the namespace acme into the patterns of the table.
func replaceNamespace(patterns []string) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = strings.ReplaceAll(p, "namespaces/acme", "namespaces/{namespace}")
	}

	return out
}

// TestRoutesEnforceDeclaredActions proves that a refused declared action stops
// the request before the handler, whatever the other actions are.
func TestRoutesEnforceDeclaredActions(t *testing.T) {
	t.Parallel()

	for _, rt := range newFixture(t).handler.Routes() {
		for _, denied := range rt.Actions {
			t.Run(rt.Method+" "+rt.Path+" without "+string(denied), func(t *testing.T) {
				t.Parallel()

				f := newFixture(t, denied)

				got := f.do(t, send("admin", rt.Method, routeTarget(rt), "{}"))
				wantCode(t, fmt.Sprintf("with %s denied", denied), got, http.StatusForbidden)

				if f.hook.count() != 0 {
					t.Errorf("sender got %d messages, want none", f.hook.count())
				}
			})
		}
	}
}

// TestNoMuxBypass proves every served route goes through the recorder.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, readErr := packageSources.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read file %s: %v", name, readErr)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	full := newFixture(t).config

	cases := []struct {
		name   string
		change func(cfg *apinotifications.Config)
	}{
		{"no service", func(cfg *apinotifications.Config) { cfg.Sinks = nil }},
		{"no authenticator", func(cfg *apinotifications.Config) { cfg.Authenticator = nil }},
		{"no authorizer", func(cfg *apinotifications.Config) { cfg.Authorizer = nil }},
		{"no logger", func(cfg *apinotifications.Config) { cfg.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			tc.change(&cfg)

			if _, err := apinotifications.NewHandler(cfg); !errors.Is(err, apinotifications.ErrMissingDependency) {
				t.Fatalf("NewHandler = %v, want ErrMissingDependency", err)
			}
		})
	}
}
