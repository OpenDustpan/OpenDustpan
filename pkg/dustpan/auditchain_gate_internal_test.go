package dustpan

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// gateRoute is one method and path of the gate table.
type gateRoute struct {
	method string
	path   string
}

// gateState is one cached chain record of the gate table.
type gateState struct {
	name       string
	state      audit.ChainState
	pending    bool
	wantRefuse bool
}

// TestAuditChainGate proves which routes the gate refuses. Without a waiting
// change and with no mismatch every route passes. While a change waits, and
// when the setting differs from the stored state, only the probes, GET of
// the metrics route, GET and POST of the chain route, the maintenance routes
// and the console files pass.
func TestAuditChainGate(t *testing.T) {
	t.Parallel()

	states := []gateState{
		{name: "steady", state: audit.ChainStateOn, pending: false, wantRefuse: false},
		{name: "pending", state: audit.ChainStateOn, pending: true, wantRefuse: true},
		{name: "mismatch", state: audit.ChainStateOff, pending: false, wantRefuse: true},
	}

	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()

			runGateState(t, state)
		})
	}
}

// runGateState serves one cached chain record and checks every route.
func runGateState(t *testing.T, state gateState) {
	t.Helper()

	handler := gateHandler(t, state)

	for _, route := range gateRefused() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			want := http.StatusNoContent
			if state.wantRefuse {
				want = http.StatusServiceUnavailable
			}

			checkGate(t, handler, route, want)
		})
	}

	for _, route := range gatePassed() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			checkGate(t, handler, route, http.StatusNoContent)
		})
	}
}

// gateHandler answers the gate over a chain whose cache holds state.
func gateHandler(t *testing.T, state gateState) http.Handler {
	t.Helper()

	chain := newTestAuditChain(t, audit.ChainOn)
	chain.limit = time.Hour
	chain.rec = audit.ChainRecord{
		ChangedAt:          time.Now(),
		ChangedBy:          "alice",
		State:              state.state,
		Pending:            state.pending,
		EnteredMaintenance: false,
		MigratingAt:        time.Time{},
		LogReplaced:        false,
	}
	chain.readAt = time.Now()
	chain.read = true

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	return withAuditChainGate(next, chain, slog.New(slog.DiscardHandler))
}

// checkGate serves route and proves the status, with a light check of the
// error shape on TFE paths.
func checkGate(t *testing.T, handler http.Handler, route gateRoute, want int) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), route.method, route.path, nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != want {
		t.Fatalf("%s %s answers %d, want %d", route.method, route.path, rec.Code, want)
	}

	if want == http.StatusServiceUnavailable && strings.HasPrefix(route.path, "/api/v2/") {
		if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
			t.Fatalf("%s %s media type = %q, want the TFE JSON:API error", route.method, route.path, got)
		}
	}

	if want == http.StatusServiceUnavailable {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s %s Cache-Control = %q, want no-store", route.method, route.path, got)
		}
	}
}

// gateRefused answers the routes the gate refuses while a change waits.
func gateRefused() []gateRoute {
	return []gateRoute{
		{method: http.MethodPost, path: "/api/stacks"},
		{method: http.MethodGet, path: "/api/stacks"},
		{method: http.MethodGet, path: "/api/audit/entries?organization=@server"},
		{method: http.MethodGet, path: "/api/v2/workspaces/x"},
		{method: http.MethodGet, path: "/tofu/state/x"},
		{method: http.MethodGet, path: "/api/inventory/deleted-units/pulumi/unit-1"},
		{method: http.MethodPost, path: "/runner/v1/claim"},
		{method: http.MethodPut, path: "/api/server/audit/chain"},
		{method: http.MethodGet, path: "/api/dustpan/v1/maintenancex"},
	}
}

// gatePassed answers the routes that always pass the gate.
func gatePassed() []gateRoute {
	return []gateRoute{
		{method: http.MethodGet, path: "/health/live"},
		{method: http.MethodGet, path: "/api/server/metrics"},
		{method: http.MethodGet, path: "/api/server/audit/chain"},
		{method: http.MethodPost, path: "/api/server/audit/chain"},
		{method: http.MethodGet, path: "/api/dustpan/v1/maintenance"},
		{method: http.MethodPost, path: "/api/dustpan/v1/maintenance/enter"},
		{method: http.MethodGet, path: "/"},
	}
}
