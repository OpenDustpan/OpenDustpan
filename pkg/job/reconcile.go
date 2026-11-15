package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The name, the kind and the interval of the reconcile schedule. The name and
// the kind match, as for the other schedules of the server, so the key of the
// operation of a tick names its schedule.
const (
	// ReconcileName is the name of the reconcile schedule.
	ReconcileName = "job.reconcile"
	// KindReconcile is the durable kind of the operation that one tick of the
	// reconcile schedule starts.
	KindReconcile durable.Kind = "job.reconcile"
	// ReconcileVersion is the version of KindReconcile.
	ReconcileVersion = 1
	// ReconcileInterval is the time between two ticks of the reconcile
	// schedule.
	ReconcileInterval = time.Minute
)

// Limits of one run of the reconcile.
const (
	// DefaultReconcileBatch is the number of jobs that one query of the
	// reconcile reads.
	DefaultReconcileBatch = 200
	// maxReconcilePages bounds the pages that one run reads for one kind of
	// work, so a very long queue cannot hold a run for longer than a tick.
	maxReconcilePages = 10
)

// ReconcilerConfig configures a Reconciler. A zero timeout, age or batch takes
// its default.
type ReconcilerConfig struct {
	Store Store
	// Namespaces gives the current fence of the namespace of a job.
	Namespaces NamespaceReader
	// QueueTimeout is the time that a job stays queued before it expires.
	// Zero takes DefaultQueueTimeout.
	QueueTimeout time.Duration
	// DispatchTimeout is the time that a job stays dispatched, with no claim,
	// before it expires. Zero takes DefaultDispatchTimeout.
	DispatchTimeout time.Duration
	// StateTimeout is the time that a write-back job waits for the state
	// version that closes it before it expires. Zero takes
	// DefaultStateTimeout.
	StateTimeout time.Duration
	// MaxPlanAge is the oldest plan that an apply phase applies. Zero takes
	// DefaultMaxPlanAge.
	MaxPlanAge time.Duration
	// Approvers decides which signatures count when the reconcile reads the
	// gate of a queued job, as at the dispatch of a phase: a dev subject
	// fills no slot on a production installation. The zero value is a
	// production installation that matches no dev subject, and then no
	// signature counts: fail closed.
	Approvers gate.ApproverPolicy
	// Batch is the number of jobs that one query reads. Zero takes
	// DefaultReconcileBatch.
	Batch int
	// Now is the time source, for the time of the timeouts and of the audit
	// entries. Nil takes time.Now.
	Now func() time.Time
	// Log receives the jobs that one run could not change. Nil discards.
	Log *slog.Logger
}

// Reconciler does the periodic work of the job model: it ends the orphans,
// expires the jobs past their timeouts and dispatches the queued jobs whose
// wait ended. The reconcile schedule runs it once each tick on one instance.
// Every change is one Store.Apply bound to the version that the run read, so a
// change that another instance, a cancel or an executor made first wins and
// the run skips the job. A job that a run cannot change stays as it is, and
// the next tick tries again: a refused dispatch writes nothing.
type Reconciler struct {
	cfg ReconcilerConfig
}

// NewReconciler checks cfg, fills its defaults and returns a Reconciler. It
// returns ErrInvalid for a config with no store or no namespace reader, or a
// negative timeout, age or batch.
func NewReconciler(cfg ReconcilerConfig) (*Reconciler, error) {
	if cfg.Store == nil || cfg.Namespaces == nil {
		return nil, fmt.Errorf("%w: a reconciler needs a job store and a namespace reader", ErrInvalid)
	}

	if cfg.QueueTimeout < 0 || cfg.DispatchTimeout < 0 || cfg.StateTimeout < 0 || cfg.MaxPlanAge < 0 || cfg.Batch < 0 {
		return nil, fmt.Errorf("%w: a reconciler needs timeouts, a plan age and a batch of 0 or more", ErrInvalid)
	}

	cfg.QueueTimeout = durationOr(cfg.QueueTimeout, DefaultQueueTimeout)
	cfg.DispatchTimeout = durationOr(cfg.DispatchTimeout, DefaultDispatchTimeout)
	cfg.StateTimeout = durationOr(cfg.StateTimeout, DefaultStateTimeout)
	cfg.MaxPlanAge = durationOr(cfg.MaxPlanAge, DefaultMaxPlanAge)

	if cfg.Batch == 0 {
		cfg.Batch = DefaultReconcileBatch
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	return &Reconciler{cfg: cfg}, nil
}

// durationOr returns d, or def when d is zero.
func durationOr(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}

	return d
}

// ReconcileResult counts what one run of the reconcile changed.
type ReconcileResult struct {
	// Ended counts the orphans that the run ended (AfterOperationEnded).
	Ended int
	// Expired counts the jobs that the run expired.
	Expired int
	// Dispatched counts the queued jobs that the run dispatched.
	Dispatched int
	// Gated counts the queued jobs that the run moved to awaiting-approval,
	// because the gate of their current phase is not open.
	Gated int
	// Replanned counts the queued jobs that the run sent back to their plan
	// phase, because their plan was stale.
	Replanned int
	// Waiting counts the queued jobs that the run left queued, because the
	// unit is busy, a freeze is active or a manual lock covers the unit.
	Waiting int
	// Skipped counts the jobs that a concurrent change or a stale fence kept
	// the run from changing. The next tick sees them again.
	Skipped int
	// Failed counts the jobs that the run could not change, for a reason that
	// it logged.
	Failed int
}

// Reconcile runs the work once: first it ends the orphans, so that their units
// are free, then it expires the jobs past a timeout, and last it dispatches
// the queued jobs. It returns an error for a read of the expiry or the dispatch
// that failed and for maintenance mode, which refuses every write. A failed
// read of the orphans is logged and counted, and the run goes on. A job that it cannot change is
// in the result and in the log, not in the error.
func (r *Reconciler) Reconcile(ctx context.Context) (ReconcileResult, error) {
	run := reconcileRun{
		r:       r,
		now:     r.cfg.Now().UTC(),
		fences:  make(map[namespace.Name]namespace.Fence),
		damaged: make(map[ID]struct{}),
		res: ReconcileResult{
			Ended:      0,
			Expired:    0,
			Dispatched: 0,
			Gated:      0,
			Replanned:  0,
			Waiting:    0,
			Skipped:    0,
			Failed:     0,
		},
	}

	steps := []func(context.Context) error{run.endOrphans, run.expire, run.dispatchQueued}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return run.res, err
		}
	}

	return run.res, nil
}

// reconcileRun is the state of one run: its time, the fences that it read and
// the count of its changes.
type reconcileRun struct {
	r      *Reconciler
	now    time.Time
	fences map[namespace.Name]namespace.Fence
	// damaged holds the job IDs of the damaged rows that the run reported, so
	// that one row is logged once in one run.
	damaged map[ID]struct{}
	res     ReconcileResult
}

// fence returns the current fence of ns, read once for each run.
func (run *reconcileRun) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	if f, ok := run.fences[ns]; ok {
		return f, nil
	}

	n, err := run.r.cfg.Namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("namespace %s: %w", ns, err)
	}

	f := n.Fence()
	run.fences[ns] = f

	return f, nil
}

// endOrphans applies the event of AfterOperationEnded to each job that
// Store.Orphans returns. The pages repeat while the previous page ended jobs:
// an ended job leaves the next read.
func (run *reconcileRun) endOrphans(ctx context.Context) error {
	for range maxReconcilePages {
		orphans, damaged, err := run.r.cfg.Store.Orphans(ctx, run.r.cfg.Batch)
		if err != nil {
			// The expiry and the dispatch do not need the orphans. They run in
			// this tick, and the next tick reads the orphans again.
			run.res.Failed++
			run.r.cfg.Log.WarnContext(
				ctx,
				"job reconcile could not read the orphans",
				slog.String("error", err.Error()),
			)

			return nil
		}

		run.reportDamaged(ctx, damaged)

		ended := 0

		for i := range orphans {
			ok, err := run.endOrphan(ctx, &orphans[i])
			if err != nil {
				return err
			}

			if ok {
				ended++
			}
		}

		if ended == 0 || len(orphans)+len(damaged) < run.r.cfg.Batch {
			return nil
		}
	}

	return nil
}

// endOrphan ends one orphan, and reports whether it did.
func (run *reconcileRun) endOrphan(ctx context.Context, o *Orphan) (bool, error) {
	j := &o.Job

	ev, reason, err := AfterOperationEnded(o)
	if err != nil {
		run.fail(j, "end orphan", err)

		return false, nil
	}

	why := fmt.Sprintf("job %s was %s and its phase operation %s: %s", j.ID, j.State, operationLoss(o), reason)

	changed, err := run.apply(ctx, j, &Change{
		Job: j.ID, Version: j.Version, Event: ev, Wait: WaitNone, Reason: reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}, ActionJobCancel, why)
	if err != nil {
		return false, err
	}

	if changed {
		run.res.Ended++
	}

	return changed, nil
}

// operationLoss says how the operation of an orphan was lost.
func operationLoss(o *Orphan) string {
	if o.Missing {
		return "does not exist"
	}

	return "ended with no result for the job"
}

// expiry is one timeout: the state whose jobs it limits, and its length.
type expiry struct {
	state   State
	timeout time.Duration
}

// expire ends the jobs past their queue, dispatch or state timeout with
// EventExpire. A job that expires is final: it does not start again, and its
// reason says which timeout it hit.
func (run *reconcileRun) expire(ctx context.Context) error {
	cfg := &run.r.cfg
	limits := []expiry{
		{StateQueued, cfg.QueueTimeout},
		{StateDispatched, cfg.DispatchTimeout},
		{StateAwaitingState, cfg.StateTimeout},
	}

	for _, lim := range limits {
		before := run.now.Add(-lim.timeout)

		err := run.eachJob(ctx, stateFilter(lim.state, before), func(j *Job) error {
			return run.expireJob(ctx, j, lim)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// expireJob expires j, which has been in the state of lim for longer than its
// timeout.
func (run *reconcileRun) expireJob(ctx context.Context, j *Job, lim expiry) error {
	reason := fmt.Sprintf("the job stayed %s for more than %s", lim.state, lim.timeout)

	changed, err := run.apply(ctx, j, &Change{
		Job: j.ID, Version: j.Version, Event: EventExpire, Wait: WaitNone, Reason: reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}, ActionJobCancel, reason)
	if err != nil {
		return err
	}

	if changed {
		run.res.Expired++
	}

	return nil
}

// dispatchQueued tries each queued job once. A job whose gate is not open goes
// to awaiting-approval, a job with a stale plan goes back to its plan phase,
// and any other job dispatches. A refusal for a busy unit, a freeze or a manual
// lock leaves the job queued and writes nothing.
func (run *reconcileRun) dispatchQueued(ctx context.Context) error {
	return run.eachJob(ctx, stateFilter(StateQueued, time.Time{}), func(j *Job) error {
		return run.dispatchJob(ctx, j)
	})
}

// dispatchJob moves the queued job j forward, if its wait ended.
func (run *reconcileRun) dispatchJob(ctx context.Context, j *Job) error {
	if j.Current >= len(j.Phases) {
		run.fail(j, "dispatch", fmt.Errorf("%w: job %s is queued with no phase left", ErrInvalid, j.ID))

		return nil
	}

	p := &j.Phases[j.Current]

	status := GatesStatus(p.Gates, p.Approvals, j.StartedBy, run.r.cfg.Approvers)
	if status == GateOpen {
		return run.dispatch(ctx, j)
	}

	if status == GateWaiting {
		return run.gate(ctx, j)
	}

	// A rejected gate ends the job at the rejection, so a queued job never
	// holds one. The job stays for a person to read.
	run.fail(j, "dispatch", fmt.Errorf("%w: job %s is queued behind a gate that is not open", ErrInvalid, j.ID))

	return nil
}

// dispatch applies EventDispatch to j.
func (run *reconcileRun) dispatch(ctx context.Context, j *Job) error {
	c := Change{
		Job: j.ID, Version: j.Version, Event: EventDispatch, Wait: WaitNone, Reason: "", Dispatch: dispatchOf(j),
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}

	if j.Current > 0 && j.Phases[j.Current].Kind == PhaseApply && j.Phases[j.Current-1].Kind == PhasePlan {
		c.PlanNotBefore = run.now.Add(-run.r.cfg.MaxPlanAge)
	}

	action, err := reconcileAction(j)
	if err != nil {
		run.fail(j, "dispatch", err)

		return nil
	}

	why := "the wait of the job ended: phase " + string(j.Phases[j.Current].Kind) + " dispatched"
	if j.Wait != WaitNone {
		why += " after the wait " + string(j.Wait)
	}

	changed, err := run.apply(ctx, j, &c, action, why)
	if err != nil {
		return err
	}

	if changed {
		run.res.Dispatched++
	}

	return nil
}

// gate applies EventNeedApproval to the queued job j whose gate is not open.
func (run *reconcileRun) gate(ctx context.Context, j *Job) error {
	action, err := reconcileAction(j)
	if err != nil {
		run.fail(j, "gate", err)

		return nil
	}

	changed, err := run.apply(ctx, j, &Change{
		Job: j.ID, Version: j.Version, Event: EventNeedApproval, Wait: WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}, action, "the gate of phase "+string(j.Phases[j.Current].Kind)+" needs approval")
	if err != nil {
		return err
	}

	if changed {
		run.res.Gated++
	}

	return nil
}

// reconcileAction returns the action that lets a principal start j: the action
// that a dispatch by the reconcile is an effect of.
func reconcileAction(j *Job) (identity.Action, error) {
	reqs, err := StartRequirements(j.Spec, j.BreakGlass)
	if err != nil {
		return "", err
	}

	if len(reqs) == 0 {
		return "", fmt.Errorf("%w: job %s has no start action", ErrInvalid, j.ID)
	}

	return reqs[0].Action, nil
}

// stateFilter selects the jobs in state s whose state changed before the time
// before, or every job in s when before is the zero time.
func stateFilter(s State, before time.Time) Filter {
	return Filter{
		Namespace:          "",
		UnitID:             "",
		States:             []State{s},
		Limit:              0,
		After:              "",
		StateChangedBefore: before,
		StateChangedAfter:  time.Time{},
	}
}

// eachJob calls fn for each job that f selects, newest first, in pages. A page
// reads Batch rows, and a run reads at most maxReconcilePages pages. A damaged
// row is not a job: the run reports it once and goes on.
func (run *reconcileRun) eachJob(ctx context.Context, f Filter, fn func(*Job) error) error {
	f.Limit = run.r.cfg.Batch

	for range maxReconcilePages {
		page, err := run.r.cfg.Store.List(ctx, f)
		if err != nil {
			return fmt.Errorf("list jobs: %w", err)
		}

		run.reportDamaged(ctx, page.Damaged)

		for i := range page.Jobs {
			if err := fn(&page.Jobs[i]); err != nil {
				return err
			}
		}

		if page.After == "" {
			return nil
		}

		f.After = page.After
	}

	return nil
}

// reportDamaged logs each damaged row of the run once, with its job ID and the
// kind of the damage. A damaged row stays where it is: the run never
// dispatches, expires or closes it. A person reads the row and cancels the job
// by its ID.
func (run *reconcileRun) reportDamaged(ctx context.Context, rows []DamagedRow) {
	for i := range rows {
		id := rows[i].ID
		if _, seen := run.damaged[id]; seen {
			continue
		}

		run.damaged[id] = struct{}{}

		run.r.cfg.Log.WarnContext(ctx, "job reconcile skipped a damaged job row", slog.String("job", string(id)),
			slog.String("reason", rows[i].Reason))
	}
}

// apply applies c to j with one audit entry, and reports whether the job
// changed. A conflict, a stale fence and a namespace that left are not errors:
// another writer got there first, or the namespace is gone, and the next tick
// reads the job again. A refusal of the dispatch rules counts the job as
// waiting. Maintenance mode is an error, because it refuses every write. Any
// other error is logged and counted for the job.
func (run *reconcileRun) apply(
	ctx context.Context,
	j *Job,
	c *Change,
	action identity.Action,
	why string,
) (bool, error) {
	fence, err := run.fence(ctx, j.Spec.Unit.Namespace())
	if err != nil {
		run.skip(j, err)

		return false, nil
	}

	_, err = run.r.cfg.Store.Apply(ctx, *c, fence, []audit.Entry{run.entry(j, action, why)})

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, maintenance.ErrInMaintenance):
		return false, fmt.Errorf("apply %s to job %s: %w", c.Event, j.ID, err)
	case errors.Is(err, ErrConflict) || errors.Is(err, namespace.ErrFenced) || errors.Is(err, namespace.ErrNotFound) ||
		errors.Is(err, ErrNotFound) || errors.Is(err, ErrFinished) || errors.Is(err, ErrTransition):
		run.skip(j, err)

		return false, nil
	case c.Event == EventDispatch:
		return run.afterRefusedDispatch(ctx, j, err)
	default:
		run.fail(j, string(c.Event), err)

		return false, nil
	}
}

// afterRefusedDispatch handles the error of a dispatch. A busy unit, a freeze
// and a manual lock are waits: the job stays queued. A stale plan sends the
// job back to its plan phase. Any other error is a failure of this job.
func (run *reconcileRun) afterRefusedDispatch(ctx context.Context, j *Job, err error) (bool, error) {
	switch {
	case errors.Is(err, ErrUnitBusy) || errors.Is(err, ErrFrozen) || errors.Is(err, ErrLocked):
		run.res.Waiting++

		return false, nil
	case errors.Is(err, ErrNoPlan):
		// Checked before ErrPlanStale, which an ErrNoPlan error also wraps: a
		// new plan phase would store no plan either, so the job would plan
		// again with no end.
		return false, run.failNoPlan(ctx, j)
	case errors.Is(err, ErrPlanStale):
		return false, run.replan(ctx, j, err)
	default:
		run.fail(j, string(EventDispatch), err)

		return false, nil
	}
}

// ReasonNoPlan is the reason of a job that failed because its plan phase
// stored no plan and the next phase needs one.
const ReasonNoPlan = "the plan phase stored no plan, so the next phase cannot run: read the log of the plan phase " +
	"and start a new job"

// failNoPlan applies EventPhaseFailed to the queued job j whose plan phase
// stored no plan, with the reason ReasonNoPlan.
func (run *reconcileRun) failNoPlan(ctx context.Context, j *Job) error {
	action, err := reconcileAction(j)
	if err != nil {
		run.fail(j, "fail no plan", err)

		return nil
	}

	changed, err := run.apply(ctx, j, &Change{
		Job: j.ID, Version: j.Version, Event: EventPhaseFailed, Wait: WaitNone, Reason: ReasonNoPlan, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}, action, ReasonNoPlan)
	if err != nil {
		return err
	}

	if changed {
		run.res.Ended++
	}

	return nil
}

// replan applies EventReplan to the queued job j whose plan is stale. stale is
// the error of the refused dispatch: it names the state version that the plan
// read and the current one, so the reason of the replan holds both.
func (run *reconcileRun) replan(ctx context.Context, j *Job, stale error) error {
	action, err := reconcileAction(j)
	if err != nil {
		run.fail(j, "replan", err)

		return nil
	}

	reason := stale.Error()

	changed, err := run.apply(ctx, j, &Change{
		Job: j.ID, Version: j.Version, Event: EventReplan, Wait: WaitNone, Reason: reason, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}, action, reason)
	if err != nil {
		return err
	}

	if changed {
		run.res.Replanned++
	}

	return nil
}

// skip counts a job that a concurrent change kept the run from changing.
func (run *reconcileRun) skip(j *Job, err error) {
	run.res.Skipped++
	run.r.cfg.Log.Debug("job reconcile skipped a job", slog.String("job", string(j.ID)),
		slog.String("reason", err.Error()))
}

// fail counts and logs a job that the run could not change.
func (run *reconcileRun) fail(j *Job, what string, err error) {
	run.res.Failed++
	run.r.cfg.Log.Warn("job reconcile could not change a job", slog.String("job", string(j.ID)),
		slog.String("step", what), slog.String("error", err.Error()))
}

// entry returns the audit entry of an automatic change of j: the service
// subject, the action that the change is an effect of, the unit, the job and
// the reason.
func (run *reconcileRun) entry(j *Job, action identity.Action, why string) audit.Entry {
	unit := j.Spec.Unit

	return audit.Entry{
		At:            run.now,
		Namespace:     unit.Namespace(),
		Subject:       history.ServerSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(action),
		Object:        unit.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      unit.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: audit.PrincipalKindService,
		JobID:         string(j.ID),
		Reason:        ReconcileName + ": " + why,
	}
}
