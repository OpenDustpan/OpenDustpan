package dustpan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/store/schema"
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
	// draining is true from the start of the drain on. A draining process
	// answers 503 and runs no check, so a load balancer stops its requests
	// while the requests in flight complete.
	draining atomic.Bool
	// streamStop tells the log streams that the process drains, or is nil.
	streamStop *streamStop
}

// The names of the checks and the states in the readiness body.
const (
	checkStore        = "store"
	checkBlob         = "blob"
	checkClaimLoop    = "claim-loop"
	checkMinimum      = "binary-minimum"
	checkReadOnly     = "store-read-only"
	checkDraining     = "draining"
	infoAuditChain    = "audit-chain"
	chainAdmits       = "admits"
	chainWaits        = "waiting"
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

// componentChecks are the checks of one component. The pings run with the
// timeout of the probe. The tests are cheap checks of the process state. The
// infos only report state: they never make the probe answer 503.
type componentChecks struct {
	name  Component
	pings []readinessCheck
	tests []stateCheck
	infos []stateInfo
}

// stateInfo is a named state of the process that the readiness body reports
// and that never fails the probe.
type stateInfo struct {
	name  string
	value func(ctx context.Context) string
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
	// minimum checks the binary version against the minimum of the schema, or
	// is nil for a store without the check.
	minimum minimumChecker
	// writable checks that the database accepts writes, or is nil for a
	// store without the check.
	writable writeChecker
	timeout  time.Duration
	failures *metrics.Counter
	logger   *slog.Logger
	// streamStop tells the log streams that the process drains, or is nil.
	streamStop *streamStop
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
		checks:     []readinessCheck{{name: checkStore, ping: store}, {name: checkBlob, ping: blobs}},
		timeout:    timeout,
		failures:   failures,
		logger:     logger,
		parts:      nil,
		draining:   atomic.Bool{},
		streamStop: nil,
	}
}

// newComponentReadiness returns the readiness probe over the components of
// the process. GET /health/ready answers for all of them and lists each.
// GET /health/ready/{component} answers for one. Every component pings the
// store; the api component also pings the blob store; the worker component also checks that its
// claim loop ran in the last 2 claim intervals. The state of the audit chain
// gate is information in the body of the api component and never makes the
// probe answer 503: while a change of the chain waits for approval, the
// installation stays in maintenance mode and the approval needs the api
// processes to stay in the pool of the Service.
func newComponentReadiness(cfg readinessSettings) *readiness {
	rd := &readiness{
		checks:     nil,
		timeout:    cfg.timeout,
		failures:   cfg.failures,
		logger:     cfg.logger,
		parts:      nil,
		draining:   atomic.Bool{},
		streamStop: cfg.streamStop,
	}

	if cfg.components.Has(ComponentAPI) {
		part := componentChecks{
			name: ComponentAPI,
			pings: slices.Concat(
				[]readinessCheck{storeCheck(cfg.store, cfg.writable), {name: checkBlob, ping: cfg.blobs}},
				minimumChecks(cfg.minimum),
			),
			tests: nil,
			infos: nil,
		}
		if cfg.chainAdmits != nil {
			part.infos = append(part.infos, stateInfo{name: infoAuditChain, value: func(ctx context.Context) string {
				if cfg.chainAdmits(ctx) {
					return chainAdmits
				}

				return chainWaits
			}})
		}

		rd.parts = append(rd.parts, part)
	}

	if cfg.components.Has(ComponentWorker) {
		rd.parts = append(rd.parts, componentChecks{
			name: ComponentWorker,
			pings: slices.Concat([]readinessCheck{storeCheck(cfg.store, cfg.writable)},
				minimumChecks(cfg.minimum)),
			tests: []stateCheck{
				{name: checkClaimLoop, ok: func(context.Context) bool { return cfg.watch.claimLoopRan() }},
			},
			infos: nil,
		})
	}

	return rd
}

// minimumPing makes the check of the binary version against the minimum of
// the schema a readiness ping.
type minimumPing struct {
	checker minimumChecker
}

// Ping returns an error only when the check knows that the binary of this
// process is below the minimum binary version of the schema. Such a process
// cannot write, so the probe answers 503 and a load balancer takes it out of
// the pool. The body names the check binary-minimum and holds no version text.
// When the check cannot read the minimum, because the store is not available,
// it does not fail: the check of the store already reports that cause, and one
// cause gives one entry in the failed list.
func (p minimumPing) Ping(ctx context.Context) error {
	err := p.checker.CheckBinaryMinimum(ctx)
	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		return fmt.Errorf("binary below the minimum: %w", err)
	}

	return nil
}

// minimumChecks returns the readiness check of the binary minimum, or none
// for a store without the check.
func minimumChecks(checker minimumChecker) []readinessCheck {
	if checker == nil {
		return nil
	}

	return []readinessCheck{{name: checkMinimum, ping: minimumPing{checker: checker}}}
}

// writeChecker checks that the database accepts writes. It returns false for
// a server that refuses writes, for example an old primary after a failover,
// and an error when the check cannot run.
type writeChecker interface {
	CheckWritable(ctx context.Context) (bool, error)
}

// writablePing is the store check of a store with a write check. It replaces
// the ping of the store: one statement both reaches the database and reads
// whether it accepts writes.
type writablePing struct {
	checker writeChecker
}

// renamedFailureError is a failure of a readiness check that the probe lists under
// another name than the name of the check.
type renamedFailureError struct {
	name string
}

func (f *renamedFailureError) Error() string { return "readiness check failed: " + f.name }

// errStoreCheck is the failure of the write check when it cannot run.
var errStoreCheck = errors.New("the store check cannot run")

// Ping fails closed. When the check cannot run, it fails as the store check:
// the probe cannot know that the database accepts writes. When the database
// answers that it refuses writes, it fails under the name store-read-only.
// Either way the probe answers 503, and a load balancer takes the process
// out of the pool.
func (p writablePing) Ping(ctx context.Context) error {
	ok, err := p.checker.CheckWritable(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errStoreCheck, err)
	}

	if !ok {
		return &renamedFailureError{name: checkReadOnly}
	}

	return nil
}

// storeCheck returns the store check: the write check when the store has
// one, and the ping of the store otherwise.
func storeCheck(store pinger, writable writeChecker) readinessCheck {
	if writable != nil {
		return readinessCheck{name: checkStore, ping: writablePing{checker: writable}}
	}

	return readinessCheck{name: checkStore, ping: store}
}

// ServeHTTP implements http.Handler.
func (rd *readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rd.draining.Load() {
		rd.writeDraining(w, r)

		return
	}

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
	if rd.draining.Load() {
		rd.writeDraining(w, r)

		return
	}

	name := Component(r.PathValue("component"))

	for _, part := range rd.parts {
		if part.name == name {
			rd.serveComponents(w, r, []componentChecks{part})

			return
		}
	}

	writeRouteError(
		w, http.StatusNotFound, "not found: this process does not run this component",
		routeAnswer(errcode.ServerComponentNotFound),
	)
}

// drain makes every later probe answer 503 with the failed check "draining".
func (rd *readiness) drain() {
	rd.draining.Store(true)
	rd.streamStop.stop()
}

// writeDraining answers a probe of a draining process. It is not a failure of
// a dependency, so it does not count on the failure counter.
func (rd *readiness) writeDraining(w http.ResponseWriter, r *http.Request) {
	rd.writeJSON(r.Context(), w, http.StatusServiceUnavailable,
		readinessDoc{Status: statusUnavailable, Failed: []string{checkDraining}})
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
	Name   Component         `json:"name"`
	Status string            `json:"status"`
	Failed []string          `json:"failed,omitempty"`
	Info   map[string]string `json:"info,omitempty"`
}

// componentsDoc is the readiness body of a process with components. Status
// and Failed keep the meaning that they have in readinessDoc: Failed names
// the checks that failed in any component, and stays absent when all pass.
// Components is the list of the components of the process with the state of
// each.
type componentsDoc struct {
	Status     string         `json:"status"`
	Failed     []string       `json:"failed,omitempty"`
	Components []componentDoc `json:"components"`
}

func (rd *readiness) writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")

	// A cache must not keep a 503 answer, or a load balancer sees the old state.
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Cache-Control", "no-store")
	}

	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		rd.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// serveComponents answers for the components in parts.
func (rd *readiness) serveComponents(w http.ResponseWriter, r *http.Request, parts []componentChecks) {
	doc := componentsDoc{Status: statusOK, Failed: nil, Components: make([]componentDoc, 0, len(parts))}

	for _, part := range parts {
		state := rd.run(r.Context(), part)
		if state.Status != statusOK {
			doc.Status = statusUnavailable
		}

		for _, name := range state.Failed {
			if !slices.Contains(doc.Failed, name) {
				doc.Failed = append(doc.Failed, name)
			}
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

	var info map[string]string

	if len(part.infos) > 0 {
		info = make(map[string]string, len(part.infos))
		for _, item := range part.infos {
			info[item.name] = item.value(ctx)
		}
	}

	if len(failed) == 0 {
		return componentDoc{Name: part.name, Status: statusOK, Failed: nil, Info: info}
	}

	return componentDoc{Name: part.name, Status: statusUnavailable, Failed: failed, Info: info}
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
		if errs[i] == nil {
			continue
		}

		name := checks[i].name

		if renamed, ok := errors.AsType[*renamedFailureError](errs[i]); ok && renamed != nil {
			name = renamed.name
		}

		failed = append(failed, name)
	}

	return failed
}

// WithDrainPeriod sets the drain: the time between the stop signal and the
// start of the shutdown. In it, GET /health/ready answers 503 and the server
// serves all other requests. The default is 0: the server shuts down at
// once, and GET /health/ready still answers 503 from the stop signal on. The
// binary sets DefaultDrainPeriod and has no flag for it. NewServer fails with
// ErrInvalidOption for a value below 0 or above 15 seconds. The upper bound
// keeps the drain and the 30 second shutdown together at 45 seconds or less:
// the pod grace period of the Helm chart is 60 seconds, and its preStop wait
// takes 15 of them.
func WithDrainPeriod(period time.Duration) Option {
	return func(o *settings) { o.drainPeriod = period }
}

// checkDrainPeriod refuses a drain that is negative or that, with the
// shutdown, passes maxStopTime.
func checkDrainPeriod(period time.Duration) error {
	if period < 0 || period+shutdownTimeout > maxStopTime {
		return fmt.Errorf("%w: WithDrainPeriod needs 0 to %s, got %s",
			ErrInvalidOption, maxStopTime-shutdownTimeout, period)
	}

	return nil
}
