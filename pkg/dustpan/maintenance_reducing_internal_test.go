package dustpan

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestIsReducingRunnerRoute sends hostile requests straight to the check of
// the gate, as if the router before it had failed: only the two reducing
// routes with a canonical path pass.
func TestIsReducingRunnerRoute(t *testing.T) {
	t.Parallel()

	routes := newReducingRunnerRoutes()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"retire", http.MethodPost, "/api/runners/acme/runners/rn-1/retire", true},
		{"revoke", http.MethodPost, "/api/runners/acme/pools/pool-1/join-tokens/rjt-1/revoke", true},
		{"retire with GET", http.MethodGet, "/api/runners/acme/runners/rn-1/retire", false},
		{"drain", http.MethodPost, "/api/runners/acme/runners/rn-1/drain", false},
		{"pools", http.MethodPost, "/api/runners/acme/pools", false},
		{"retire with dot segments", http.MethodPost, "/api/runners/acme/runners/../runners/rn-1/retire", false},
		{"retire with a double slash", http.MethodPost, "/api/runners/acme//runners/rn-1/retire", false},
		{"retire with a trailing path", http.MethodPost, "/api/runners/acme/runners/rn-1/retire/extra", false},
		{
			"retire with an encoded dot segment",
			http.MethodPost,
			"/api/runners/acme/runners/%2e%2e/runners/rn-1/retire",
			false,
		},
		{"retire under another prefix", http.MethodPost, "/api/other/acme/runners/rn-1/retire", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/", nil)
			req.URL.Path = tc.path

			if got := isReducingRunnerRoute(routes, req); got != tc.want {
				t.Fatalf("isReducingRunnerRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestReducingRunnerRoutesFollowTheList proves that the gate reads the list:
// each entry under the runner prefix passes with its method and a filled
// pattern, and no entry of another API passes.
func TestReducingRunnerRoutesFollowTheList(t *testing.T) {
	t.Parallel()

	routes := newReducingRunnerRoutes()
	fill := strings.NewReplacer("{namespace}", "acme", "{runner}", "rn-1", "{pool}", "pool-1", "{id}", "rjt-1",
		"{principal}", "p-1", "{collection}", "c-1")

	for _, entry := range maintenance.ReducingRoutes() {
		want := strings.HasPrefix(entry.Pattern, runnerAPIPrefix)

		t.Run(entry.Method+" "+entry.Pattern, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), entry.Method, "/", nil)
			req.URL.Path = fill.Replace(entry.Pattern)

			if got := isReducingRunnerRoute(routes, req); got != want {
				t.Fatalf("gate passes %s %s = %v, want %v", entry.Method, req.URL.Path, got, want)
			}
		})
	}
}

// maintenanceStore is a maintenance.Store that always reads maintenance mode.
type maintenanceStore struct{}

func (maintenanceStore) GetState(context.Context) (maintenance.State, error) {
	return maintenance.StateMaintenance, nil
}

func (maintenanceStore) SetState(context.Context, maintenance.State) error { return nil }

func (maintenanceStore) SetStateIf(context.Context, maintenance.State, maintenance.State) error {
	return nil
}

// TestRunnerMaintenanceGateRefusesEncodedPaths sends requests with escapes in
// the path through the whole gate in maintenance mode. A request for a
// reducing route passes. Each request with an encoded dot segment, an encoded
// slash or a RawPath that differs from Path answers 503 and never reaches the
// handler.
func TestRunnerMaintenanceGateRefusesEncodedPaths(t *testing.T) {
	t.Parallel()

	const (
		retire = "/api/runners/acme/runners/rn-1/retire"
		revoke = "/api/runners/acme/pools/pool-1/join-tokens/rjt-1/revoke"
	)

	tests := []struct {
		name    string
		path    string
		rawPath string
		want    int
	}{
		{"retire", retire, "", http.StatusNoContent},
		{"revoke", revoke, "", http.StatusNoContent},
		{"encoded dot segments", "/api/runners/acme/runners/../runners/rn-1/retire", "", http.StatusServiceUnavailable},
		{
			"encoded dot segments in RawPath", "/api/runners/acme/runners/../runners/rn-1/retire",
			"/api/runners/acme/runners/%2e%2e/runners/rn-1/retire", http.StatusServiceUnavailable,
		},
		{
			"encoded slash", "/api/runners/acme/runners/rn-1/x/retire", "/api/runners/acme/runners/rn-1%2Fx/retire",
			http.StatusServiceUnavailable,
		},
		{
			"RawPath that differs from Path", retire, "/api/runners/acme/runners/rn%2D1/retire",
			http.StatusServiceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true

				w.WriteHeader(http.StatusNoContent)
			})

			h := withRunnerMaintenance(
				next, maintenance.NewWatcher(maintenanceStore{}, nil, 0), shapeInventory, slog.New(slog.DiscardHandler),
			)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
			req.URL.Path = tc.path
			req.URL.RawPath = tc.rawPath

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("POST %s (raw %q) = %d, want %d", tc.path, tc.rawPath, rec.Code, tc.want)
			}

			if reached != (tc.want == http.StatusNoContent) {
				t.Fatalf("handler reached = %v for status %d", reached, tc.want)
			}
		})
	}
}
