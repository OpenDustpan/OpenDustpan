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

// errProbeStoreCanary and errProbeBlobCanary are the Ping failures the probe
// tests use. A probe response must never hold their text.
var (
	errProbeStoreCanary = errors.New("store-canary-must-not-leak")
	errProbeBlobCanary  = errors.New("blob-canary-must-not-leak")
	// errProbeBlocked ends a Ping that waits for the context, so the probe's
	// own timeout ends the check.
	errProbeBlocked = errors.New("ping blocked until the context ended")
)

// stubPinger answers Ping with err, or waits for the context to end when
// block is set.
type stubPinger struct {
	err   error
	block bool
}

// Ping implements pinger.
func (s stubPinger) Ping(ctx context.Context) error {
	if s.block {
		<-ctx.Done()

		return errProbeBlocked
	}

	return s.err
}

// serveReadiness serves one GET /health/ready on a probe with a short
// timeout and returns its status and trimmed body.
func serveReadiness(
	t *testing.T,
	store, blobs stubPinger,
	failures *metrics.Counter,
) (int, string) {
	t.Helper()

	handler := newReadinessHandler(
		store,
		blobs,
		50*time.Millisecond,
		failures,
		slog.New(slog.DiscardHandler),
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// counterText renders registry in the Prometheus text format.
func counterText(t *testing.T, registry *metrics.Registry) string {
	t.Helper()

	var out strings.Builder
	if err := registry.Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	return out.String()
}

// probeBodyCase is one body case of TestReadinessProbeBodies.
type probeBodyCase struct {
	name       string
	store      stubPinger
	blobs      stubPinger
	wantStatus int
	wantBody   string
}

// TestReadinessProbeBodies proves the probe answers 200 when both checks pass
// and 503 with the failed check names and no error text otherwise. A blocked
// check ends through the timeout.
func TestReadinessProbeBodies(t *testing.T) {
	t.Parallel()

	cases := []probeBodyCase{
		{
			name:       "both answer",
			store:      stubPinger{err: nil, block: false},
			blobs:      stubPinger{err: nil, block: false},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:       "store fails",
			store:      stubPinger{err: errProbeStoreCanary, block: false},
			blobs:      stubPinger{err: nil, block: false},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","failed":["store"]}`,
		},
		{
			name:       "blob fails",
			store:      stubPinger{err: nil, block: false},
			blobs:      stubPinger{err: errProbeBlobCanary, block: false},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","failed":["blob"]}`,
		},
		{
			name:       "both fail",
			store:      stubPinger{err: errProbeStoreCanary, block: false},
			blobs:      stubPinger{err: errProbeBlobCanary, block: false},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","failed":["store","blob"]}`,
		},
		{
			name:       "slow store",
			store:      stubPinger{err: nil, block: true},
			blobs:      stubPinger{err: nil, block: false},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","failed":["store"]}`,
		},
		{
			name:       "slow blob",
			store:      stubPinger{err: nil, block: false},
			blobs:      stubPinger{err: nil, block: true},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","failed":["blob"]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkProbeBody(t, tc)
		})
	}
}

// checkProbeBody serves one probe body case and checks its status, body,
// error-text absence and bound.
func checkProbeBody(t *testing.T, tc probeBodyCase) {
	t.Helper()

	registry := metrics.NewRegistry()
	failures := registry.NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0)

	start := time.Now()
	status, body := serveReadiness(t, tc.store, tc.blobs, failures)
	elapsed := time.Since(start)

	if status != tc.wantStatus {
		t.Fatalf("status %d, want %d (body %q)", status, tc.wantStatus, body)
	}

	if body != tc.wantBody {
		t.Fatalf("body %q, want %q", body, tc.wantBody)
	}

	for _, canary := range []string{errProbeStoreCanary.Error(), errProbeBlobCanary.Error()} {
		if strings.Contains(body, canary) {
			t.Fatalf("body %q holds error text %q", body, canary)
		}
	}

	if elapsed > 10*time.Second {
		t.Fatalf("probe took %s, want the timeout to end it", elapsed)
	}
}

// TestReadinessCountsFailures proves each 503 increments
// dustpan_readiness_failures_total once, and that 200 answers leave it alone:
// two passes and one failure count exactly one.
func TestReadinessCountsFailures(t *testing.T) {
	t.Parallel()

	registry := metrics.NewRegistry()
	failures := registry.NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0)

	serve := func(store, blobs stubPinger) (int, string) {
		return serveReadiness(t, store, blobs, failures)
	}

	healthy := stubPinger{err: nil, block: false}
	broken := stubPinger{err: errProbeStoreCanary, block: false}

	if status, _ := serve(healthy, healthy); status != http.StatusOK {
		t.Fatalf("first pass status %d, want %d", status, http.StatusOK)
	}

	if status, _ := serve(healthy, healthy); status != http.StatusOK {
		t.Fatalf("second pass status %d, want %d", status, http.StatusOK)
	}

	if status, _ := serve(broken, healthy); status != http.StatusServiceUnavailable {
		t.Fatalf("failure status %d, want %d", status, http.StatusServiceUnavailable)
	}

	if got := counterText(t, registry); !strings.Contains(got, "dustpan_readiness_failures_total 1\n") {
		t.Fatalf("metrics output lacks one failure:\n%s", got)
	}

	if status, _ := serve(healthy, broken); status != http.StatusServiceUnavailable {
		t.Fatalf("second failure status %d, want %d", status, http.StatusServiceUnavailable)
	}

	if got := counterText(t, registry); !strings.Contains(got, "dustpan_readiness_failures_total 2\n") {
		t.Fatalf("metrics output lacks two failures:\n%s", got)
	}
}

// TestReadinessDrains proves that a draining probe answers 503 with the
// check "draining" at once, with no ping of a dependency, and does not count
// as a failure of a dependency.
func TestReadinessDrains(t *testing.T) {
	t.Parallel()

	api, err := ParseComponents("api")
	if err != nil {
		t.Fatal(err)
	}

	blocked := stubPinger{err: nil, block: true}
	logger := slog.New(slog.DiscardHandler)
	builders := map[string]func(failures *metrics.Counter) *readiness{
		"without components": func(failures *metrics.Counter) *readiness {
			rd, ok := newReadinessHandler(blocked, blocked, time.Minute, failures, logger).(*readiness)
			if !ok {
				t.Fatal("newReadinessHandler does not return a readiness")
			}

			return rd
		},
		"with components": func(failures *metrics.Counter) *readiness {
			return newComponentReadiness(readinessSettings{
				components: api, store: blocked, blobs: blocked, watch: nil, chainAdmits: nil,
				timeout: time.Minute, failures: failures, logger: logger,
			})
		},
	}

	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry := metrics.NewRegistry()
			probe := build(registry.NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0))
			probe.drain()

			for _, path := range []string{"/health/ready", "/health/ready/api"} {
				assertDrainingAnswer(t, probe, path)
			}

			if got := counterText(t, registry); !strings.Contains(got, "dustpan_readiness_failures_total 0\n") {
				t.Errorf("a drain counted as a failure:\n%s", got)
			}
		})
	}
}

// assertDrainingAnswer sends one GET to probe and fails the test unless the
// answer is the 503 of a draining process.
func assertDrainingAnswer(t *testing.T, probe *readiness, path string) {
	t.Helper()

	const want = `{"status":"unavailable","failed":["draining"]}`

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	req.SetPathValue("component", "api")

	rec := httptest.NewRecorder()
	if path == "/health/ready" {
		probe.ServeHTTP(rec, req)
	} else {
		probe.ServeOne(rec, req)
	}

	if got := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusServiceUnavailable || got != want {
		t.Errorf("GET %s while draining = %d %s, want 503 %s", path, rec.Code, got, want)
	}
}
