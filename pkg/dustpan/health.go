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
	// parts hold the checks of each component of the process. With none, the
	// probe answers for the store and the blob store only.
	parts []componentChecks
}

// The names of the checks and the states in the readiness body.
const (
	checkStore        = "store"
	checkBlob         = "blob"
	checkAuditChain   = "audit-chain"
	checkClaimLoop    = "claim-loop"
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

// componentChecks are the checks of one component. The pings run with the
// timeout of the probe. The tests are cheap checks of the process state.
type componentChecks struct {
	name  Component
	pings []readinessCheck
	tests []stateCheck
}

// stateCheck is a named check that reads state in the process.
type stateCheck struct {
	name string
	ok   func(ctx context.Context) bool
}

// readinessSettings holds what the readiness probe of a process with
// components reads.
type readinessSettings struct {
	components  Components
	store       pinger
	blobs       pinger
	watch       *workerWatch
	chainAdmits func(context.Context) bool
	timeout     time.Duration
	failures    *metrics.Counter
	logger      *slog.Logger
}

// newReadinessHandler returns the readiness probe over store and blobs. Each
// check gets its own timeout. failures counts each probe that answers 503.
func newReadinessHandler(
	store, blobs pinger,
	timeout time.Duration,
	failures *metrics.Counter,
	logger *slog.Logger,
) http.Handler {
	return &readiness{
		checks:   []readinessCheck{{name: checkStore, ping: store}, {name: checkBlob, ping: blobs}},
		timeout:  timeout,
		failures: failures,
		logger:   logger,
		parts:    nil,
	}
}

// newComponentReadiness returns the readiness probe over the components of
// the process. GET /health/ready answers for all of them and lists each.
// GET /health/ready/{component} answers for one. Every component pings the
// store; the api component also pings the blob store and checks that the
// audit chain admits requests; the worker component also checks that its
// claim loop ran in the last 2 claim intervals.
func newComponentReadiness(cfg readinessSettings) *readiness {
	rd := &readiness{
		checks: nil, timeout: cfg.timeout, failures: cfg.failures, logger: cfg.logger, parts: nil,
	}

	if cfg.components.Has(ComponentAPI) {
		part := componentChecks{
			name:  ComponentAPI,
			pings: []readinessCheck{{name: checkStore, ping: cfg.store}, {name: checkBlob, ping: cfg.blobs}},
			tests: nil,
		}
		if cfg.chainAdmits != nil {
			part.tests = append(part.tests, stateCheck{name: checkAuditChain, ok: cfg.chainAdmits})
		}

		rd.parts = append(rd.parts, part)
	}

	if cfg.components.Has(ComponentWorker) {
		rd.parts = append(rd.parts, componentChecks{
			name:  ComponentWorker,
			pings: []readinessCheck{{name: checkStore, ping: cfg.store}},
			tests: []stateCheck{
				{name: checkClaimLoop, ok: func(context.Context) bool { return cfg.watch.claimLoopRan() }},
			},
		})
	}

	return rd
}

// ServeHTTP implements http.Handler.
func (rd *readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(rd.parts) > 0 {
		rd.serveComponents(w, r, rd.parts)

		return
	}

	failed := pingAll(r.Context(), rd.checks, rd.timeout)
	if len(failed) == 0 {
		rd.writeJSON(r.Context(), w, http.StatusOK, readinessDoc{Status: statusOK, Failed: nil})

		return
	}

	rd.failures.Inc()
	rd.writeJSON(r.Context(), w, http.StatusServiceUnavailable, readinessDoc{Status: statusUnavailable, Failed: failed})
}

// ServeOne serves GET /health/ready/{component}. A component that the
// process does not run answers 404.
func (rd *readiness) ServeOne(w http.ResponseWriter, r *http.Request) {
	name := Component(r.PathValue("component"))

	for _, part := range rd.parts {
		if part.name == name {
			rd.serveComponents(w, r, []componentChecks{part})

			return
		}
	}

	http.NotFound(w, r)
}

// readinessDoc is the body of the readiness probe. Failed names the checks
// that failed and stays absent when all checks pass. It never holds error
// text.
type readinessDoc struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

// componentDoc is the state of one component in the readiness body.
type componentDoc struct {
	Name   Component `json:"name"`
	Status string    `json:"status"`
	Failed []string  `json:"failed,omitempty"`
}

// componentsDoc is the readiness body of a process with components.
type componentsDoc struct {
	Status     string         `json:"status"`
	Components []componentDoc `json:"components"`
}

func (rd *readiness) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		rd.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// serveComponents answers for the components in parts.
func (rd *readiness) serveComponents(w http.ResponseWriter, r *http.Request, parts []componentChecks) {
	doc := componentsDoc{Status: statusOK, Components: make([]componentDoc, 0, len(parts))}

	for _, part := range parts {
		state := rd.run(r.Context(), part)
		if state.Status != statusOK {
			doc.Status = statusUnavailable
		}

		doc.Components = append(doc.Components, state)
	}

	if doc.Status == statusOK {
		rd.writeJSON(r.Context(), w, http.StatusOK, doc)

		return
	}

	rd.failures.Inc()
	rd.writeJSON(r.Context(), w, http.StatusServiceUnavailable, doc)
}

// run runs the checks of one component and answers its state.
func (rd *readiness) run(ctx context.Context, part componentChecks) componentDoc {
	failed := pingAll(ctx, part.pings, rd.timeout)

	for _, test := range part.tests {
		if !test.ok(ctx) {
			failed = append(failed, test.name)
		}
	}

	if len(failed) == 0 {
		return componentDoc{Name: part.name, Status: statusOK, Failed: nil}
	}

	return componentDoc{Name: part.name, Status: statusUnavailable, Failed: failed}
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
