package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeMetricsRouteListsTheCounter(t *testing.T) {
	t.Parallel()

	srv := newBuiltinSQLiteServer(t, filepath.Join(t.TempDir(), "dustpan.db"))

	t.Cleanup(func() {
		if closeErr := srv.Close(); closeErr != nil {
			t.Errorf("close server: %v", closeErr)
		}
	})

	srv.Metrics().AddClaimPollSkipped(5)

	h := startedHandler(t, srv)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/metrics", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics = %d, want 200", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "dustpan_job_claim_poll_skipped_total 5") {
		t.Fatalf("the metrics route misses the counter:\n%s", rec.Body.String())
	}
}
