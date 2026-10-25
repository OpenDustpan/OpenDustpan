package dustpan

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMaintenanceAnswersAreNoStore proves that the refusal of each error shape
// answers 503 with Retry-After and Cache-Control: no-store. The audit chain gate
// writes its refusals through the same writeRefusal.
func TestMaintenanceAnswersAreNoStore(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	for _, tc := range []struct {
		name  string
		shape errorShape
	}{
		{"Pulumi", shapePulumi},
		{"inventory", shapeInventory},
		{"TFE", shapeTFE},
		{"HTTP backend", shapeHTTPBackend},
		{"runner", shapeRunner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			writeRefusal(t.Context(), rec, tc.shape, logger, "in maintenance")

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}

			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("the refusal has no Retry-After header")
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}
