package runnerapi_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestMaintenanceRefusal proves that a maintenance refusal on a runner route
// answers 503 with a Retry-After time and the maintenance sentinel text, also
// when code wraps the sentinel.
func TestMaintenanceRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"sentinel", maintenance.ErrInMaintenance},
		{"wrapped", fmt.Errorf("claim phase: %w", maintenance.ErrInMaintenance)},
		{"wrapped twice", fmt.Errorf("poll: %w", fmt.Errorf("lease row 9: %w", maintenance.ErrInMaintenance))},
	}

	for _, scope := range []runnerapi.Scope{runnerapi.ScopeRunnerKey, runnerapi.ScopeJoinToken} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("scope %d %s", scope, tc.name), func(t *testing.T) {
				t.Parallel()

				checkMaintenanceRefusal(t, scope, tc.err)
			})
		}
	}
}

// checkMaintenanceRefusal proves the answer to a maintenance refusal.
func checkMaintenanceRefusal(t *testing.T, scope runnerapi.Scope, err error) {
	t.Helper()

	rec := httptest.NewRecorder()
	runnerapi.WriteError(t.Context(), rec, slog.New(slog.DiscardHandler), scope, err)

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("status = %d, Retry-After = %q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
	}

	var body protocol.Error
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), decodeErr)
	}

	if body.Code != protocol.ErrorUnavailable || body.Message != maintenance.ErrInMaintenance.Error() {
		t.Fatalf("body = %+v, want code %q and the maintenance sentinel text", body, protocol.ErrorUnavailable)
	}
}
