package dustpan

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// errWriteCheck stands for a write check that cannot run because the store is
// not available.
var errWriteCheck = errors.New("write check cannot run")

// stubWritable is a writeChecker that answers ok and err.
type stubWritable struct {
	ok  bool
	err error
}

// CheckWritable implements writeChecker.
func (s stubWritable) CheckWritable(context.Context) (bool, error) {
	return s.ok, s.err
}

// TestReadinessStoreReadOnly proves that the probe of a process whose
// database answers that it refuses writes answers 503 and names the check
// store-read-only. A database that
// accepts writes, or a store without the check, answers 200. A check that
// cannot run does not fail on its own: the store check reports that cause,
// and one cause gives one entry in the failed list.
func TestReadinessStoreReadOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		components Components
		store      stubPinger
		writable   writeChecker
		wantStatus int
		wantBody   string
	}{
		{
			"api, read-only",
			Components{api: true, worker: false},
			stubPinger{err: nil, block: false},
			stubWritable{ok: false, err: nil},
			http.StatusServiceUnavailable, `"failed":["store-read-only"]`,
		},
		{
			"writable",
			Components{api: true, worker: false},
			stubPinger{err: nil, block: false},
			stubWritable{ok: true, err: nil},
			http.StatusOK, "",
		},
		{
			"store without the check",
			Components{api: true, worker: false},
			stubPinger{err: nil, block: false},
			nil, http.StatusOK, "",
		},
		{
			"store down, check cannot run",
			Components{api: true, worker: false},
			stubPinger{err: errProbeStoreCanary, block: false},
			stubWritable{ok: false, err: errWriteCheck},
			http.StatusServiceUnavailable, `"failed":["store"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rd := newComponentReadiness(readinessSettings{
				components: tt.components, store: tt.store, blobs: stubPinger{err: nil, block: false},
				watch: nil, chainAdmits: nil, minimum: nil, writable: tt.writable,
				timeout:  50 * time.Millisecond,
				failures: metrics.NewRegistry().NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0),
				logger:   slog.New(slog.DiscardHandler),
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
			rec := httptest.NewRecorder()

			rd.ServeHTTP(rec, req)

			body := rec.Body.String()
			if rec.Code != tt.wantStatus || !strings.Contains(body, tt.wantBody) {
				t.Fatalf("probe = %d %s, want %d with %s", rec.Code, body, tt.wantStatus, tt.wantBody)
			}
		})
	}
}

// TestReadinessChecksWritesInEachComponent proves that the api and the worker
// component both run the write check, so a worker process on a read-only
// database also leaves the pool of its Service.
func TestReadinessChecksWritesInEachComponent(t *testing.T) {
	t.Parallel()

	rd := newComponentReadiness(readinessSettings{
		components: Components{api: true, worker: true}, store: stubPinger{err: nil, block: false},
		blobs: stubPinger{err: nil, block: false}, watch: nil, chainAdmits: nil, minimum: nil,
		writable: stubWritable{ok: true, err: nil}, timeout: 50 * time.Millisecond,
		failures: metrics.NewRegistry().NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0),
		logger:   slog.New(slog.DiscardHandler),
	})

	for _, part := range rd.parts {
		found := false

		for _, check := range part.pings {
			found = found || check.name == checkReadOnly
		}

		if !found {
			t.Errorf("component %s has no %s check", part.name, checkReadOnly)
		}
	}

	if len(rd.parts) != 2 {
		t.Fatalf("readiness has %d components, want 2", len(rd.parts))
	}
}
