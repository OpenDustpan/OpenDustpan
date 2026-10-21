package notifications_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/pkg/identity"
)

// routeTarget fills the path values of a route.
func routeTarget(route apinotifications.Route) string {
	return strings.NewReplacer("{namespace}", "acme", "{sink_id}", "snk-aaaaaaaaaaaaaaaaaaaa").Replace(route.Path)
}

// TestRoutesDeclareActions proves that each route is authorized, declares its
// actions, and takes the Idempotency-Key header exactly when it changes data.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	byPattern := map[string]apinotifications.Route{}

	for _, route := range f.handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		if route.Scope != apinotifications.ScopeAuthorized || len(route.Actions) == 0 || route.Resolve == nil {
			t.Fatalf("route %q must be authorized with actions and a resolver", pattern)
		}

		mutating := route.Method != http.MethodGet
		if route.IdempotencyKey != mutating || (route.Answer != nil) != mutating {
			t.Errorf("route %q: takes the key = %t, has an answer = %t, want both %t",
				pattern, route.IdempotencyKey, route.Answer != nil, mutating)
		}

		wantManage := mutating
		if has := containsAction(route.Actions, identity.ActionNamespaceManageNotifications); has != wantManage {
			t.Errorf("route %q: manage-notifications = %t, want %t", pattern, has, wantManage)
		}
	}

	// The routes of ADR 145: the list and the get, the create, the replace,
	// the delete and the test.
	want := []string{
		"GET " + base, "POST " + base, "GET " + base + "/{sink_id}", "PUT " + base + "/{sink_id}",
		"DELETE " + base + "/{sink_id}", "POST " + base + "/{sink_id}/test",
	}
	want = replaceNamespace(want)

	if len(byPattern) != len(want) {
		t.Fatalf("routes = %d, want %d", len(byPattern), len(want))
	}

	for _, pattern := range want {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("route %q is missing", pattern)
		}
	}

	for _, pattern := range f.handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	if len(f.handler.RegisteredPatterns()) != len(byPattern) {
		t.Fatalf("served %d patterns, declared %d", len(f.handler.RegisteredPatterns()), len(byPattern))
	}

	for pattern, route := range byPattern {
		// A request without a token fails closed with 401, never 404 and never
		// the handler.
		got := f.do(t, send("", route.Method, routeTarget(route), ""))
		wantCode(t, "route "+pattern+" without a token", got, http.StatusUnauthorized)
	}

	if f.hook.count() != 0 {
		t.Errorf("sender got %d messages without a token, want none", f.hook.count())
	}
}

func containsAction(list []identity.Action, a identity.Action) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}

	return false
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

	for _, route := range newFixture(t).handler.Routes() {
		for _, denied := range route.Actions {
			t.Run(route.Method+" "+route.Path+" without "+string(denied), func(t *testing.T) {
				t.Parallel()

				f := newFixture(t, denied)

				got := f.do(t, send("admin", route.Method, routeTarget(route), "{}"))
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

	f := newFixture(t)
	_ = f

	cases := []struct {
		name string
		cfg  apinotifications.Config
	}{
		{"no service", apinotifications.Config{
			Sinks: nil, Authenticator: testAuthn{}, Authorizer: testAuthz{deny: nil}, Logger: slog.New(slog.DiscardHandler),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := apinotifications.NewHandler(tc.cfg); err == nil {
				t.Fatal("NewHandler = nil, want an error")
			}
		})
	}
}
