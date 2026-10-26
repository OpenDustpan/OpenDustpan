package server_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestReducingRoutesAnswerInMaintenanceMode proves that the routes which only
// reduce access answer their success status through the real route, the
// service and the store while the installation is in maintenance mode: a
// revoked access token, a revoked grant on the installation and in a
// namespace, a disabled principal. Incident response needs each of them.
func TestReducingRoutesAnswerInMaintenanceMode(t *testing.T) {
	t.Parallel()

	reducing := serverReducingKeys()

	handler := newTestHandler(t)
	found := 0

	for _, route := range handler.Routes() {
		key := route.Method + " " + route.Path
		if !slices.Contains(reducing, key) {
			continue
		}

		found++

		t.Run(key, func(t *testing.T) {
			t.Parallel()

			h, st := newServiceBackedHandlerOf(
				t,
				tokenAuthenticator{},
				selectiveAuthorizer{deny: map[identity.Action]bool{}},
				failedSeed(),
			)

			if err := st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
				t.Fatalf("enter maintenance mode: %v", err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, routeRequest(t, route, "test-token"))

			if want := wantSuccessStatus(route); rec.Code != want {
				t.Fatalf("%s in maintenance mode answers %d, want %d (%s)", key, rec.Code, want, rec.Body.String())
			}
		})
	}

	if found != len(reducing) {
		t.Fatalf("found %d of the %d reducing routes in the table", found, len(reducing))
	}
}

// serverReducingKeys returns "METHOD pattern" of each entry of the list of
// reducing routes that belongs to the installation API.
func serverReducingKeys() []string {
	var keys []string

	for _, r := range maintenance.ReducingRoutes() {
		if strings.HasPrefix(r.Pattern, "/api/server/") {
			keys = append(keys, r.Method+" "+r.Pattern)
		}
	}

	return keys
}

// notReductions holds the routes whose shape looks like an access reduction
// and that the list leaves out on purpose, each with its reason.
func notReductions() map[string]string {
	return map[string]string{
		"DELETE /api/server/sign-in-providers/{id}": "deletes the provider configuration and journals no revocation",
	}
}

// reductionPathWords are the path segments that name a credential or a
// runner: a DELETE below one of them is a candidate for the list.
func reductionPathWords() []string {
	return []string{
		"token", "tokens", "grant", "grants", "unit-grants", "session", "sessions",
		"sign-in-provider", "sign-in-providers", "runner", "runners", "join-token", "join-tokens",
	}
}

// looksReducing reports whether the route has the shape of an access
// reduction: a path that ends in /revoke, /retire or /disable, or a DELETE on
// a path with a credential or runner segment.
func looksReducing(method, path string) bool {
	for _, suffix := range []string{"/revoke", "/retire", "/disable"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}

	if method != http.MethodDelete {
		return false
	}

	for segment := range strings.SplitSeq(path, "/") {
		if slices.Contains(reductionPathWords(), segment) {
			return true
		}
	}

	return false
}

// TestReductionShapedRoutesAreOnTheList fails when a registered route has
// the shape of an access reduction and is neither on the list of reducing
// routes nor named in notReductions with a reason. The action names do not
// tell a reduction (they are manage actions), so the shape of the route does.
func TestReductionShapedRoutesAreOnTheList(t *testing.T) {
	t.Parallel()

	listed := map[string]bool{}
	for _, r := range maintenance.ReducingRoutes() {
		listed[r.Method+" "+r.Pattern] = true
	}

	for _, route := range newTestHandler(t).Routes() {
		key := route.Method + " " + route.Path
		if !looksReducing(route.Method, route.Path) || listed[key] {
			continue
		}

		if reason := notReductions()[key]; reason == "" {
			t.Errorf("%s has the shape of an access reduction and is not on maintenance.ReducingRoutes; "+
				"add it there, or name it in notReductions with a reason", key)
		}
	}
}

// TestReducingListMatchesRoutes proves the other side: every entry of the
// list that belongs to the installation API is a registered route, with the
// actions that the list names.
func TestReducingListMatchesRoutes(t *testing.T) {
	t.Parallel()

	registered := map[string][]string{}

	for _, route := range newTestHandler(t).Routes() {
		actions := make([]string, 0, len(route.Actions))
		for _, a := range route.Actions {
			actions = append(actions, string(a))
		}

		registered[route.Method+" "+route.Path] = actions
	}

	for _, r := range maintenance.ReducingRoutes() {
		if !strings.HasPrefix(r.Pattern, "/api/server/") {
			continue
		}

		key := r.Method + " " + r.Pattern

		got, ok := registered[key]
		if !ok {
			t.Errorf("%s is on the list and is not a registered route", key)

			continue
		}

		if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(r.Actions))) {
			t.Errorf("%s declares the actions %v, the list names %v", key, got, r.Actions)
		}
	}
}

// TestWideningRoutesStayRefusedInMaintenanceMode proves the other side: the
// routes that grant or widen access answer 503 in maintenance mode, also for
// the caller who may call them.
func TestWideningRoutesStayRefusedInMaintenanceMode(t *testing.T) {
	t.Parallel()

	widening := []string{
		"POST /api/server/principals/{id}/enable",
		"POST /api/server/grants",
		"POST /api/server/principals/{principal}/tokens",
		"DELETE /api/server/principals/{id}",
	}

	handler := newTestHandler(t)
	found := 0

	for _, route := range handler.Routes() {
		key := route.Method + " " + route.Path
		if !slices.Contains(widening, key) {
			continue
		}

		found++

		if slices.Contains(freshAuthRoutes(), key) {
			// An access token never gives a fresh authentication, so the route
			// refuses it before the maintenance check.
			// TestFreshAuthTokenCreateInMaintenanceMode covers the maintenance
			// refusal with a console session.
			continue
		}

		t.Run(key, func(t *testing.T) {
			t.Parallel()

			h, st := newServiceBackedHandlerOf(
				t,
				tokenAuthenticator{},
				selectiveAuthorizer{deny: map[identity.Action]bool{}},
				failedSeed(),
			)

			if err := st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
				t.Fatalf("enter maintenance mode: %v", err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, routeRequest(t, route, "test-token"))

			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "maintenance") {
				t.Fatalf("%s in maintenance mode answers %d %s, want 503 for maintenance mode", key, rec.Code,
					rec.Body.String())
			}
		})
	}

	if found != len(widening) {
		t.Fatalf("found %d of the %d widening routes in the table", found, len(widening))
	}
}
