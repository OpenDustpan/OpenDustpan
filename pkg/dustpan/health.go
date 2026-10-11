package dustpan

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
)

// readinessTimeout bounds each readiness check: the store ping and the blob
// ping each get this long (ADR 32 decision 3).
const readinessTimeout = 2 * time.Second

// readinessFailuresName names the counter of failed readiness probes. The
// server registry holds it (internal/metrics); the tests register it under
// the same name.
const readinessFailuresName = "dustpan_readiness_failures_total"

// pinger reports whether a dependency answers. namespace.Store and blob.Store
// both satisfy it, so the probe reaches them with no new option.
type pinger interface {
	Ping(ctx context.Context) error
}

// readinessCheck is one dependency the readiness probe pings.
type readinessCheck struct {
	name string
	ping pinger
}

// readiness serves GET /health/ready on the root mux. It stays public: it
// names failed checks only, so it leaks no credential and no driver text.
type readiness struct {
	checks   []readinessCheck
	timeout  time.Duration
	failures *metrics.Counter
	logger   *slog.Logger
}

// newReadinessHandler returns the readiness probe over store and blobs. Each
// check gets its own timeout. failures counts each probe that answers 503.
func newReadinessHandler(
	store, blobs pinger,
	timeout time.Duration,
	failures *metrics.Counter,
	logger *slog.Logger,
) http.Handler {
	return readiness{
		checks:   []readinessCheck{{name: "store", ping: store}, {name: "blob", ping: blobs}},
		timeout:  timeout,
		failures: failures,
		logger:   logger,
	}
}

// ServeHTTP implements http.Handler.
func (rd readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	failed := pingAll(r.Context(), rd.checks, rd.timeout)
	if len(failed) == 0 {
		rd.writeJSON(r.Context(), w, http.StatusOK, readinessDoc{Status: "ok", Failed: nil})

		return
	}

	rd.failures.Inc()
	rd.writeJSON(r.Context(), w, http.StatusServiceUnavailable, readinessDoc{Status: "unavailable", Failed: failed})
}

// readinessDoc is the body of the readiness probe. Failed names the checks
// that failed and stays absent when all checks pass. It never holds error
// text.
type readinessDoc struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

func (rd readiness) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body readinessDoc) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		rd.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// pingAll pings every check with its own timeout and returns the names of the
// checks that failed, in check order. It returns only names, never errors, so
// no driver text reaches the caller.
func pingAll(ctx context.Context, checks []readinessCheck, timeout time.Duration) []string {
	errs := make([]error, len(checks))

	var wg sync.WaitGroup

	for i := range checks {
		wg.Go(func() {
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			errs[i] = checks[i].ping.Ping(checkCtx)
		})
	}

	wg.Wait()

	var failed []string

	for i := range checks {
		if errs[i] != nil {
			failed = append(failed, checks[i].name)
		}
	}

	return failed
}
