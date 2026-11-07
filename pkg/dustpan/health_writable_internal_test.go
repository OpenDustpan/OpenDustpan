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

// errWriteCheck stands for a write check that cannot run.
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

// writableCase is one store and write check, and the answer of the probe.
type writableCase struct {
	name       string
	store      stubPinger
	writable   writeChecker
	wantStatus int
	wantBody   string
}

// writableCases returns the table of TestReadinessStoreReadOnly.
func writableCases() []writableCase {
	up := stubPinger{err: nil, block: false}
	down := stubPinger{err: errProbeStoreCanary, block: false}

	return []writableCase{
		{
			"read-only",
			up,
			stubWritable{ok: false, err: nil},
			http.StatusServiceUnavailable,
			`"failed":["store-read-only"]`,
		},
		{"writable", up, stubWritable{ok: true, err: nil}, http.StatusOK, ""},
		{
			"check cannot run, ping would pass", up,
			stubWritable{ok: false, err: errWriteCheck},
			http.StatusServiceUnavailable, `"failed":["store"]`,
		},
		{
			"store down, check cannot run", down,
			stubWritable{ok: false, err: errWriteCheck},
			http.StatusServiceUnavailable, `"failed":["store"]`,
		},
		{"store without the check", up, nil, http.StatusOK, ""},
		{"store without the check, store down", down, nil, http.StatusServiceUnavailable, `"failed":["store"]`},
	}
}

// TestReadinessStoreReadOnly proves that the write check replaces the ping of
// the store and fails closed. A database that answers that it refuses writes
// gives 503 with the check store-read-only. A write check that cannot run
// gives 503 with the check store, also when the ping of the store would pass.
// A database that accepts writes, or a store without the check whose ping
// passes, answers 200.
func TestReadinessStoreReadOnly(t *testing.T) {
	t.Parallel()

	for _, tt := range writableCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rd := newComponentReadiness(readinessSettings{
				streamStop: nil,
				components: Components{api: true, worker: false}, store: tt.store,
				blobs: stubPinger{err: nil, block: false}, watch: nil, chainAdmits: nil, minimum: nil,
				writable: tt.writable, timeout: 50 * time.Millisecond,
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

			if strings.Contains(body, errWriteCheck.Error()) {
				t.Errorf("probe body %s holds the text of the check error", body)
			}
		})
	}
}

// TestReadinessChecksWritesInEachComponent proves that the store check of the
// api and of the worker component is the write check, so a worker process on
// a read-only database also leaves the pool of its Service.
func TestReadinessChecksWritesInEachComponent(t *testing.T) {
	t.Parallel()

	rd := newComponentReadiness(readinessSettings{
		streamStop: nil,
		components: Components{api: true, worker: true}, store: stubPinger{err: nil, block: false},
		blobs: stubPinger{err: nil, block: false}, watch: nil, chainAdmits: nil, minimum: nil,
		writable: stubWritable{ok: true, err: nil}, timeout: 50 * time.Millisecond,
		failures: metrics.NewRegistry().NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0),
		logger:   slog.New(slog.DiscardHandler),
	})

	if len(rd.parts) != 2 {
		t.Fatalf("readiness has %d components, want 2", len(rd.parts))
	}

	for _, part := range rd.parts {
		found := false

		for _, check := range part.pings {
			if _, ok := check.ping.(writablePing); ok && check.name == checkStore {
				found = true
			}
		}

		if !found {
			t.Errorf("component %s has no write check as its store check", part.name)
		}
	}
}
