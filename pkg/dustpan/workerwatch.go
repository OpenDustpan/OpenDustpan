package dustpan

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/durable"
)

// WithOperationLease sets the lease period of a claim on a durable
// operation. The worker takes claims with this lease. An api process counts
// 2 of these periods without a worker claim before it refuses the start of
// an operation. Zero means the default of the durable runner.
func WithOperationLease(d time.Duration) Option {
	return func(o *settings) { o.operationLease = d }
}

// stuckLeases is the number of lease periods without a claim after which a
// worker counts as stuck or absent.
const stuckLeases = 2

// watchListLimit bounds the operations that one report reads. The pending
// and running operations are few; a limit keeps a backlog from costing a
// large read at each scrape.
const watchListLimit = 500

// watchTTL is how long a report stays valid. The metric, the readiness probe
// and the start gate share one read of the store within this time.
const watchTTL = 2 * time.Second

// oldestUnclaimedName names the metric of the oldest unclaimed operation or
// due schedule.
const oldestUnclaimedName = "dustpan_oldest_unclaimed_operation_age_seconds"

// workerWatch reads the durable store to tell whether a worker claims work.
// It also records when this process last asked the store for a claim.
type workerWatch struct {
	store      DurableStore
	specs      func() []durable.ScheduleSpec
	lease      time.Duration
	pollEvery  time.Duration
	components Components
	now        func() time.Time

	lastActive atomic.Int64

	mu       sync.Mutex
	cached   server.WorkerReport
	cachedAt time.Time
	cachedOK bool
}

// newWorkerWatch builds the watch of cfg. The claim loop counts as running
// from the start of the process.
func newWorkerWatch(cfg *settings) *workerWatch {
	w := &workerWatch{
		store:      cfg.durableStore,
		specs:      func() []durable.ScheduleSpec { return nil },
		lease:      cfg.operationLease,
		pollEvery:  durable.DefaultPollEvery,
		components: cfg.components,
		now:        cfg.now,
		lastActive: atomic.Int64{},
		mu:         sync.Mutex{},
		cached:     emptyReport(),
		cachedAt:   time.Time{},
		cachedOK:   false,
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

	registry.RegisterFunc(oldestUnclaimedName,
		"Seconds that the oldest unclaimed durable operation or due schedule waits, 0 when none waits.",
		w.oldestUnclaimed)
}

// emptyReport returns a report with no operation and no schedule.
func emptyReport() server.WorkerReport {
	return server.WorkerReport{
		LeaseSeconds:           0,
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

	return fmt.Errorf("%w for more than %d lease periods", server.ErrNoWorker, stuckLeases)
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

// read reads the unclaimed operations and the due schedules. An operation is
// unclaimed when it is pending past its not-before time, or running with an
// expired claim.
func (w *workerWatch) read(ctx context.Context) (server.WorkerReport, error) {
	report := emptyReport()
	report.LeaseSeconds = w.leasePeriod().Seconds()

	if w.store == nil {
		return report, nil
	}

	if err := w.readOperations(ctx, &report); err != nil {
		return emptyReport(), err
	}

	if err := w.readSchedules(ctx, &report); err != nil {
		return emptyReport(), err
	}

	report.Stuck = report.OldestUnclaimedSeconds > (stuckLeases * w.leasePeriod()).Seconds()

	return report, nil
}

// readOperations adds the unclaimed operations to report. It lists only
// those that wait longer than 2 lease periods, and counts all of them in the
// oldest age.
func (w *workerWatch) readOperations(ctx context.Context, report *server.WorkerReport) error {
	now := w.now()
	limit := stuckLeases * w.leasePeriod()

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

// readSchedules adds the due schedules to report.
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
// claim and each renewal as a sign that the claim loop runs.
type trackingStore struct {
	DurableStore

	watch *workerWatch
}

// Claim records the attempt and claims through the wrapped store.
func (t trackingStore) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	t.watch.touch()

	op, err := t.DurableStore.Claim(ctx, req)
	if err != nil {
		return op, fmt.Errorf("claim: %w", err)
	}

	return op, nil
}

// Extend records the renewal and extends through the wrapped store.
func (t trackingStore) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	t.watch.touch()

	claim, err := t.DurableStore.Extend(ctx, c, lease)
	if err != nil {
		return claim, fmt.Errorf("extend: %w", err)
	}

	return claim, nil
}
