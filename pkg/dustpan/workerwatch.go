package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/durable"
)

// WithOperationLease sets the lease period of a claim on a durable
// operation. The worker takes claims with this lease. An api process counts
// 2 of these periods without a worker claim or claim renewal before it
// refuses the start of an operation. Zero means the default of the durable
// runner.
func WithOperationLease(d time.Duration) Option {
	return func(o *settings) { o.operationLease = d }
}

// stuckLeases is the number of lease periods without a worker heartbeat
// after which a worker counts as stuck or absent.
const stuckLeases = 2

// watchListLimit bounds the operations that one report reads. The pending
// and running operations are few; a limit keeps a backlog from costing a
// large read at each scrape.
const watchListLimit = 500

// watchTTL is how long a report stays valid. The metric, the readiness probe
// and the start gate share one read of the store within this time.
const watchTTL = 2 * time.Second

// silentName names the metric of the time without a worker heartbeat.
const silentName = "dustpan_worker_silent_seconds"

// oldestUnclaimedName names the metric of the oldest unclaimed operation or
// due schedule.
const oldestUnclaimedName = "dustpan_oldest_unclaimed_operation_age_seconds"

// workerWatch tells whether a worker runs. A worker records a heartbeat in
// the store each time it claims or renews a claim, at most once in a quarter
// of the lease period. Every time that the watch compares comes from the
// clock of the store, so the clock of an api process, which may differ,
// never moves a result. The clock of this process serves only to rate-limit
// the heartbeat, to keep the report cache and to time the claim loop of this
// process.
type workerWatch struct {
	store      DurableStore
	specs      func() []durable.ScheduleSpec
	lease      time.Duration
	pollEvery  time.Duration
	components Components
	owner      string
	logger     *slog.Logger
	now        func() time.Time

	// lastActive and lastBeat are times of the clock of this process in Unix
	// nanoseconds. lastBeat is 0 before the first heartbeat.
	lastActive atomic.Int64
	lastBeat   atomic.Int64

	mu       sync.Mutex
	cached   server.WorkerReport
	cachedAt time.Time
	cachedOK bool
	// watchedFrom is the time of the store when this process began to watch.
	// A worker that never beat counts as silent from then on, so a start
	// right after the process began is never a missing worker.
	watchedFrom time.Time
}

// newWorkerWatch builds the watch of cfg. The claim loop counts as running
// from the start of the process.
func newWorkerWatch(cfg *settings) *workerWatch {
	w := &workerWatch{
		store:       cfg.durableStore,
		specs:       func() []durable.ScheduleSpec { return nil },
		lease:       cfg.operationLease,
		pollEvery:   durable.DefaultPollEvery,
		components:  cfg.components,
		owner:       cfg.instanceID,
		logger:      cfg.logger,
		now:         cfg.now,
		lastActive:  atomic.Int64{},
		lastBeat:    atomic.Int64{},
		mu:          sync.Mutex{},
		cached:      emptyReport(),
		cachedAt:    time.Time{},
		cachedOK:    false,
		watchedFrom: time.Time{},
	}

	w.touch()

	return w
}

// registerWatchMetrics adds the worker gauge to registry. Without a durable
// store there is nothing to report and nothing is added.
func registerWatchMetrics(registry *metrics.Registry, w *workerWatch) {
	if w.store == nil {
		return
	}

	registry.RegisterFunc(silentName,
		"Seconds since a worker last claimed or renewed a claim, on the clock of the store.",
		w.silent)

	registry.RegisterFunc(oldestUnclaimedName,
		"Seconds that the oldest unclaimed durable operation or due schedule waits, 0 when none waits.",
		w.oldestUnclaimed)
}

// emptyReport returns a report with no operation and no schedule.
func emptyReport() server.WorkerReport {
	return server.WorkerReport{
		LeaseSeconds:           0,
		SilentSeconds:          0,
		OldestUnclaimedSeconds: 0,
		Stuck:                  false,
		Operations:             nil,
		Schedules:              nil,
	}
}

// Components implements server.WorkerSource.
func (w *workerWatch) Components() []string {
	return w.components.Strings()
}

// WorkerReport implements server.WorkerSource. A report stays valid for
// watchTTL.
func (w *workerWatch) WorkerReport(ctx context.Context) (server.WorkerReport, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.cachedOK && w.now().Sub(w.cachedAt) < watchTTL {
		return w.cached, nil
	}

	report, err := w.read(ctx)
	if err != nil {
		return emptyReport(), err
	}

	w.cached, w.cachedAt, w.cachedOK = report, w.now(), true

	return report, nil
}

// AdmitStart implements server.WorkerSource. A process that runs the worker
// component claims for itself and admits each start. When the store cannot
// be read, it admits the start too: a store error is not a missing worker.
func (w *workerWatch) AdmitStart(ctx context.Context) error {
	if w.components.Has(ComponentWorker) {
		return nil
	}

	report, err := w.WorkerReport(ctx)
	if err != nil || !report.Stuck {
		return nil //nolint:nilerr // A store error is not proof of a missing worker.
	}

	return fmt.Errorf("%w for more than %d lease periods", admit.ErrNoWorker, stuckLeases)
}

// attach gives the watch what it reads from the server, which exists after
// the watch, and registers its metrics in registry.
func (w *workerWatch) attach(srv *Server, registry *metrics.Registry) {
	w.specs = srv.jobSchedules
	w.owner = srv.durableOwner

	registerWatchMetrics(registry, w)
}

// touch records that this process asked the store for work now.
func (w *workerWatch) touch() {
	w.lastActive.Store(w.now().UnixNano())
}

// leasePeriod returns the lease period of the watch.
func (w *workerWatch) leasePeriod() time.Duration {
	if w.lease > 0 {
		return w.lease
	}

	return durable.DefaultLease
}

// claimLoopRan reports whether this process asked the store for work within
// 2 claim intervals. A claim interval is the longer of the poll period and
// the lease period, because a runner that works on an operation renews its
// claim instead of polling.
func (w *workerWatch) claimLoopRan() bool {
	if w.store == nil {
		return true
	}

	interval := max(w.pollEvery, w.leasePeriod())
	last := time.Unix(0, w.lastActive.Load())

	return w.now().Sub(last) <= 2*interval
}

// begin records the time of the store at which this process began to watch.
// A failed read leaves it unset: the first report sets it.
func (w *workerWatch) begin(ctx context.Context) {
	if w.store == nil {
		return
	}

	beat, err := w.store.LastBeat(ctx)
	if err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.setWatchedFrom(beat.Now)
}

// setWatchedFrom keeps the first time that it gets. The caller holds w.mu.
func (w *workerWatch) setWatchedFrom(at time.Time) {
	if w.watchedFrom.IsZero() {
		w.watchedFrom = at
	}
}

// read reads the last heartbeat, the unclaimed operations and the due
// schedules. A worker counts as stuck when no worker claimed or renewed a
// claim for more than 2 lease periods of that worker: the lease period is the
// one that the worker recorded with its heartbeat, so an api process with a
// shorter lease than its workers never counts a live worker as stuck. A
// heartbeat that records no lease, and a store with no heartbeat, count with
// the lease period of this process. The unclaimed work is information only: a
// schedule that old workers do not know, or one long job, leaves work
// unclaimed while the workers are alive. The caller holds w.mu.
func (w *workerWatch) read(ctx context.Context) (server.WorkerReport, error) {
	report := emptyReport()
	report.LeaseSeconds = w.leasePeriod().Seconds()

	if w.store == nil {
		return report, nil
	}

	beat, err := w.store.LastBeat(ctx)
	if err != nil {
		return emptyReport(), fmt.Errorf("read worker heartbeat: %w", err)
	}

	w.setWatchedFrom(beat.Now)

	silentFrom := w.watchedFrom
	if beat.At.After(silentFrom) {
		silentFrom = beat.At
	}

	lease := w.leasePeriod()
	if beat.Lease > 0 {
		lease = beat.Lease
	}

	report.LeaseSeconds = lease.Seconds()
	report.SilentSeconds = max(beat.Now.Sub(silentFrom), 0).Seconds()
	report.Stuck = report.SilentSeconds > (stuckLeases * lease).Seconds()

	if err := w.readOperations(ctx, &report, beat.Now, lease); err != nil {
		return emptyReport(), err
	}

	if err := w.readSchedules(ctx, &report); err != nil {
		return emptyReport(), err
	}

	return report, nil
}

// readOperations adds the unclaimed operations to report. It lists only
// those that wait longer than 2 lease periods, and counts all of them in the
// oldest age. now is the time of the store, and lease is the lease period
// that read decided on.
func (w *workerWatch) readOperations(
	ctx context.Context, report *server.WorkerReport, now time.Time, lease time.Duration,
) error {
	limit := stuckLeases * lease

	ops, err := w.store.List(ctx, durable.Filter{
		Kind:   "",
		States: []durable.State{durable.StatePending, durable.StateRunning},
		Limit:  watchListLimit,
	})
	if err != nil {
		return fmt.Errorf("list durable operations: %w", err)
	}

	for i := range ops {
		since, ok := unclaimedSince(&ops[i], now)
		if !ok {
			continue
		}

		age := now.Sub(since)
		report.OldestUnclaimedSeconds = max(report.OldestUnclaimedSeconds, age.Seconds())

		if age > limit {
			report.Operations = append(report.Operations, server.UnclaimedOperation{
				ID: string(ops[i].ID), Kind: string(ops[i].Kind), State: string(ops[i].State), Seconds: age.Seconds(),
			})
		}
	}

	return nil
}

// readSchedules adds the due schedules to report. The schedule carries the
// time of the store.
func (w *workerWatch) readSchedules(ctx context.Context, report *server.WorkerReport) error {
	for _, spec := range w.specs() {
		sched, err := w.store.GetSchedule(ctx, spec.Name)
		if errors.Is(err, durable.ErrNotFound) {
			continue
		}

		if err != nil {
			return fmt.Errorf("read schedule %s: %w", spec.Name, err)
		}

		if sched.Wait() > 0 {
			continue
		}

		late := sched.Now.Sub(sched.NextRunAt)
		report.OldestUnclaimedSeconds = max(report.OldestUnclaimedSeconds, late.Seconds())
		report.Schedules = append(report.Schedules, server.DueSchedule{Name: spec.Name, LateSeconds: late.Seconds()})
	}

	return nil
}

// beatIfDue records a heartbeat in the store when none went out in the last
// quarter of the lease period. A failed write is logged and tried again at
// the next claim: a missing heartbeat never stops a claim.
func (w *workerWatch) beatIfDue(ctx context.Context) {
	if w.store == nil {
		return
	}

	nowNs := w.now().UnixNano()
	last := w.lastBeat.Load()

	if last != 0 && nowNs-last < int64(w.leasePeriod()/heartbeatDivisor) {
		return
	}

	if !w.lastBeat.CompareAndSwap(last, nowNs) {
		return
	}

	if err := w.store.Beat(ctx, w.owner, w.leasePeriod()); err != nil {
		w.lastBeat.Store(last)

		if ctx.Err() == nil {
			w.logger.WarnContext(ctx, "worker heartbeat failed", slog.String("error", err.Error()))
		}
	}
}

// heartbeatDivisor makes the heartbeat period a quarter of the lease period,
// so that a worker beats about 8 times before an api process counts it
// absent.
const heartbeatDivisor = 4

// silent returns the metric value: seconds without a worker heartbeat, or 0
// when a read fails.
func (w *workerWatch) silent() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), readinessTimeout)
	defer cancel()

	report, err := w.WorkerReport(ctx)
	if err != nil {
		return 0
	}

	return report.SilentSeconds
}

// oldestUnclaimed returns the metric value: seconds that the oldest
// unclaimed operation or due schedule waits, or 0 when a read fails.
func (w *workerWatch) oldestUnclaimed() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), readinessTimeout)
	defer cancel()

	report, err := w.WorkerReport(ctx)
	if err != nil {
		return 0
	}

	return report.OldestUnclaimedSeconds
}

// unclaimedSince returns the time from which op waits for a claim, and false
// when a worker holds it or it is not due.
func unclaimedSince(op *durable.Operation, now time.Time) (time.Time, bool) {
	if op.State == durable.StatePending {
		since := op.CreatedAt
		if op.NotBefore.After(since) {
			since = op.NotBefore
		}

		return since, !since.After(now)
	}

	if op.State == durable.StateRunning {
		return op.Claim.ExpiresAt, !op.Claim.ExpiresAt.IsZero() && op.Claim.ExpiresAt.Before(now)
	}

	return time.Time{}, false
}

// trackingStore wraps the durable store of the worker and records each
// claim and each renewal as a sign that the claim loop runs, in this process
// and, as a heartbeat, in the store.
type trackingStore struct {
	DurableStore

	watch *workerWatch
}

// Claim records the attempt and claims through the wrapped store.
func (t trackingStore) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	return t.ops().Claim(ctx, req)
}

// errNoStepCanceler reports a wrapped store of durable operations that
// cannot cancel before a step.
var errNoStepCanceler = errors.New("the store of durable operations cannot cancel before a step")

// Extend records the renewal and extends through the wrapped store.
func (t trackingStore) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	return t.ops().Extend(ctx, c, lease)
}

// ops returns the wrapper of the operations of the store. trackingOps holds
// the one copy of the claim and renew tracking.
func (t trackingStore) ops() trackingOps {
	return trackingOps{Store: t.DurableStore, watch: t.watch}
}

// trackingOps wraps a store that keeps operations and records each claim and
// each renewal as a sign that the claim loop runs, in this process and, as a
// heartbeat, in the store. trackingStore uses it for the full durable store.
type trackingOps struct {
	durable.Store

	watch *workerWatch
}

// Claim records the attempt and claims through the wrapped store.
func (t trackingOps) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	t.watch.touch()
	t.watch.beatIfDue(ctx)

	op, err := t.Store.Claim(ctx, req)
	if err != nil {
		return op, fmt.Errorf("claim: %w", err)
	}

	return op, nil
}

// Extend records the renewal and extends through the wrapped store.
func (t trackingOps) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	t.watch.touch()
	t.watch.beatIfDue(ctx)

	claim, err := t.Store.Extend(ctx, c, lease)
	if err != nil {
		return claim, fmt.Errorf("extend: %w", err)
	}

	return claim, nil
}

// CancelBefore cancels through the wrapped store, which must implement
// durable.StepCanceler. A restore cancels through it.
func (t trackingOps) CancelBefore(ctx context.Context, id durable.ID, step string) error {
	canceler, ok := t.Store.(durable.StepCanceler)
	if !ok {
		return fmt.Errorf("cancel durable operation %s: %w", id, errNoStepCanceler)
	}

	if err := canceler.CancelBefore(ctx, id, step); err != nil {
		return fmt.Errorf("cancel before step %q: %w", step, err)
	}

	return nil
}
