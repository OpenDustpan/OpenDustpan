package dustpan

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// TestReadinessUnavailableIsNotCached proves that every 503 answer of the
// readiness probe says that a cache must not keep it: a failed check, a
// draining process and a failed component.
func TestReadinessUnavailableIsNotCached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		drain bool
		build func(failures *metrics.Counter) *readiness
	}{
		{
			name:  "failed check",
			drain: false,
			build: func(failures *metrics.Counter) *readiness {
				return newReadinessProbe(t, stubPinger{err: errProbeStoreCanary, block: false}, failures)
			},
		},
		{
			name:  "draining",
			drain: true,
			build: func(failures *metrics.Counter) *readiness {
				return newReadinessProbe(t, stubPinger{err: nil, block: false}, failures)
			},
		},
		{
			name:  "failed component",
			drain: false,
			build: func(failures *metrics.Counter) *readiness {
				return newComponentReadiness(readinessSettings{
					streamStop:  nil,
					components:  Components{api: true, worker: false},
					store:       stubPinger{err: errProbeStoreCanary, block: false},
					blobs:       stubPinger{err: nil, block: false},
					watch:       nil,
					chainAdmits: nil,
					minimum:     nil,
					writable:    nil,
					timeout:     time.Minute,
					failures:    failures,
					logger:      slog.New(slog.DiscardHandler),
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry := metrics.NewRegistry()
			failures := registry.NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0)

			probe := tc.build(failures)
			if tc.drain {
				probe.drain()
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
			rec := httptest.NewRecorder()
			probe.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

// newReadinessProbe returns the readiness probe over store and a healthy blob
// store, with no components.
func newReadinessProbe(t *testing.T, store stubPinger, failures *metrics.Counter) *readiness {
	t.Helper()

	rd, ok := newReadinessHandler(store, stubPinger{err: nil, block: false}, time.Minute, failures,
		slog.New(slog.DiscardHandler)).(*readiness)
	if !ok {
		t.Fatal("newReadinessHandler does not return a readiness")
	}

	return rd
}
