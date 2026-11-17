package dustpan

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
)

// TestDrainReachesTheStartGateAndTheProbe proves the wiring of the drain: the
// server that NewServer returns gives the start gate its draining state and
// the drain period, and the readiness probe its drain period. Before the
// drain the gate admits. After it, the gate refuses with the period and the
// probe answers 503 with the same period in Retry-After.
func TestDrainReachesTheStartGateAndTheProbe(t *testing.T) {
	t.Parallel()

	const period = 7 * time.Second

	srv := newMuxServer(t, apiOnly(), WithDrainPeriod(period))

	if err := srv.watch.AdmitStart(t.Context()); err != nil && errors.Is(err, admit.ErrDraining) {
		t.Fatalf("AdmitStart before the drain = %v, want no drain refusal", err)
	}

	srv.ready.drain()

	err := srv.watch.AdmitStart(t.Context())
	if !errors.Is(err, admit.ErrDraining) {
		t.Fatalf("AdmitStart while draining = %v, want ErrDraining", err)
	}

	if got := admit.DrainRetryAfter(err); got != period {
		t.Errorf("DrainRetryAfter = %v, want %v", got, period)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
	rec := httptest.NewRecorder()
	srv.ready.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("probe while draining = %d, want 503", rec.Code)
	}

	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Errorf("probe Retry-After = %q, want %q", got, "7")
	}
}
