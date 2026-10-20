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

	reducing := []string{
		"DELETE /api/server/tokens/{id}",
		"DELETE /api/server/grants/{id}",
		"DELETE /api/server/namespaces/{namespace}/grants/{id}",
		"POST /api/server/principals/{id}/disable",
	}

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
