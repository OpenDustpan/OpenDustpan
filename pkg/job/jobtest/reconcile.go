package jobtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/job"
)

// The pause that lets the clock of a store pass a millisecond, and the number
// of jobs that the expiry case makes.
const (
	clockStep    = 2 * time.Millisecond
	expiryJobs   = 3
	smallestStep = time.Millisecond
)

// reconcileCases returns the cases of the reconcile and of the time of the last
// state change that it reads.
func reconcileCases() []suiteCase {
	return []suiteCase{
		{"a job keeps the time of its last state change, and List filters by it", testStateChangedAt},
		{"the reconcile leaves a locked job queued and dispatches it after the unlock", testReconcileLocked},
		{"the reconcile dispatches a queued job when the unit is free again", testReconcileUnitBusy},
		{"the reconcile moves a queued job with a closed gate to awaiting-approval", testReconcileGate},
		{"the reconcile dispatches a queued job whose gate has its approval", testReconcileApprovedGate},
		{"the reconcile sends a queued job with a stale plan back to its plan phase", testReconcileStalePlan},
		{"the reconcile fails a queued job whose plan phase stored no plan, and never replans it", testReconcileNoPlan},
		{"the reconcile expires the jobs past their queue, dispatch and state timeouts", testReconcileExpires},
		{"the reconcile expires nothing before a timeout", testReconcileNotYet},
		{"the reconcile ends the orphans and no other job", testReconcileOrphans},
		{"a second run of the reconcile changes nothing", testReconcileTwice},
		{"two reconcilers at the same time dispatch each queued job once", testReconcileConcurrent},
		{"a failed read of the orphans does not stop the expiry and the dispatch", testReconcileOrphanReadFails},
	}
}

// reconciler returns the reconciler over the stores of h, with a clock that is
// ahead of the real time by ahead.
func reconciler(t *testing.T, h Harness, ahead time.Duration) *job.Reconciler {
	t.Helper()

	return reconcilerOver(t, h, h.Jobs, ahead)
}

// reconcilerOver returns the reconciler over the job store jobs and the
// namespaces of h, with a clock that is ahead of the real time by ahead.
func reconcilerOver(t *testing.T, h Harness, jobs job.Store, ahead time.Duration) *job.Reconciler {
	t.Helper()

	r, err := job.NewReconciler(job.ReconcilerConfig{
		Store:           jobs,
		Namespaces:      h.Namespaces,
		QueueTimeout:    0,
		DispatchTimeout: 0,
		StateTimeout:    0,
		MaxPlanAge:      0,
		Approvers:       gate.ApproverPolicy{Production: false, IsDev: nil},
		Batch:           0,
		Now:             func() time.Time { return time.Now().Add(ahead) },
		Log:             nil,
	})
	if err != nil {
		t.Fatalf("NewReconciler = %v", err)
	}

	return r
}

// runReconcile runs one reconcile of h and fails t on an error.
func runReconcile(t *testing.T, h Harness, ahead time.Duration) job.ReconcileResult {
	t.Helper()

	res, err := reconciler(t, h, ahead).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile = %v", err)
	}

	return res
}

// reconcileEntries returns the audit entries that the reconcile wrote for the
// job id.
func reconcileEntries(t *testing.T, h Harness, f fixture, id job.ID) int {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	n := 0

	for i := range entries {
		e := &entries[i]
		if e.Subject == history.ServerSubject && e.JobID == string(id) && e.PrincipalKind == "service" &&
			strings.HasPrefix(e.Reason, job.ReconcileName+": ") {
			n++
		}
	}

	return n
}

// checkStateChangedFilter proves that the filter is strict: it selects the jobs
// whose state changed before the time, so the time of the change itself
// selects none.
func checkStateChangedFilter(t *testing.T, h Harness, j *job.Job) {
	t.Helper()

	queued := job.Filter{
		Namespace: "", UnitID: "", States: []job.State{job.StateQueued}, Limit: 0, After: "",
		StateChangedBefore: j.StateChangedAt, StateChangedAfter: time.Time{},
	}
	if got, err := h.Jobs.List(t.Context(), queued); err != nil || len(got.Jobs) != 0 {
		t.Fatalf("List before the state change = %v, %v; want none", ids(got.Jobs), err)
	}

	queued.StateChangedBefore = j.StateChangedAt.Add(smallestStep)
	if got, err := h.Jobs.List(t.Context(), queued); err != nil || len(got.Jobs) != 1 || got.Jobs[0].ID != j.ID {
		t.Fatalf("List after the state change = %v, %v; want %s", ids(got.Jobs), err, j.ID)
	}

	// StateChangedAfter is strict too: the time of the change itself selects
	// none, and a time before it selects the job.
	since := job.Filter{
		Namespace: "", UnitID: "", States: nil, Limit: 0, After: "",
		StateChangedBefore: time.Time{}, StateChangedAfter: j.StateChangedAt,
	}
	if got, err := h.Jobs.List(t.Context(), since); err != nil || slices.Contains(ids(got.Jobs), j.ID) {
		t.Fatalf("List of changes after the state change = %v, %v; want no %s", ids(got.Jobs), err, j.ID)
	}

	since.StateChangedAfter = j.StateChangedAt.Add(-smallestStep)
	if got, err := h.Jobs.List(t.Context(), since); err != nil || !slices.Contains(ids(got.Jobs), j.ID) {
		t.Fatalf("List of changes before the state change = %v, %v; want %s", ids(got.Jobs), err, j.ID)
	}
}

func testStateChangedAt(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	if j.StateChangedAt.IsZero() || j.StateChangedAt.After(j.UpdatedAt) {
		t.Fatalf("a new job has the state change time %v and the update time %v", j.StateChangedAt, j.UpdatedAt)
	}

	checkStateChangedFilter(t, h, &j)

	pause(t, clockStep)

	// A write that keeps the state moves the update time and not the time of
	// the state change: the replan of a queued job is such a write.
	stale := planned(t, h, f, svOne, time.Now())
	again := apply(t, h, f, change(&stale, job.EventReplan))

	if !again.StateChangedAt.Equal(stale.StateChangedAt) || !again.UpdatedAt.After(stale.UpdatedAt) {
		t.Fatalf("replan of a queued job: state change time %v then %v, update time %v then %v; want the state "+
			"change time kept and the update time moved", stale.StateChangedAt, again.StateChangedAt,
			stale.UpdatedAt, again.UpdatedAt)
	}

	moved := apply(t, h, f, dispatch(&j))
	if moved.State != job.StateDispatched || !moved.StateChangedAt.After(j.StateChangedAt) {
		t.Fatalf("dispatch = %s with the state change time %v after %v; want a later time", moved.State,
			moved.StateChangedAt, j.StateChangedAt)
	}

	got, err := h.Jobs.Get(t.Context(), j.ID)
	if err != nil || !got.StateChangedAt.Equal(moved.StateChangedAt) {
		t.Fatalf("Get = state change time %v, %v; want %v", got.StateChangedAt, err, moved.StateChangedAt)
	}
}

func testReconcileLocked(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	lockUnit(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "")
	j.Wait = job.WaitLocked
	queued := create(t, h, f, j, nil)

	res := runReconcile(t, h, 0)
	if res.Waiting != 1 || res.Dispatched != 0 {
		t.Fatalf("reconcile under a manual lock = %+v; want one waiting job", res)
	}

	assertState(t, h, queued.ID, job.StateQueued)

	if n := reconcileEntries(t, h, f, queued.ID); n != 0 {
		t.Fatalf("a refused dispatch wrote %d audit entries, want none", n)
	}

	unlockUnit(t, h, f)

	res = runReconcile(t, h, 0)
	if res.Dispatched != 1 {
		t.Fatalf("reconcile after the unlock = %+v; want one dispatch", res)
	}

	got, err := h.Jobs.Get(t.Context(), queued.ID)
	if err != nil || got.State != job.StateDispatched || got.Phases[0].Operation == "" {
		t.Fatalf("job after the dispatch = %s, operation %q, %v; want dispatched with an operation", got.State,
			got.Phases[0].Operation, err)
	}

	if n := reconcileEntries(t, h, f, queued.ID); n != 1 {
		t.Fatalf("the dispatch wrote %d audit entries of the reconcile, want 1", n)
	}
}

func testReconcileUnitBusy(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	holder := runApplyPhase(t, h, f)
	waiting := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	if res := runReconcile(t, h, 0); res.Waiting != 1 || res.Dispatched != 0 {
		t.Fatalf("reconcile while %s holds the unit = %+v; want one waiting job", holder.ID, res)
	}

	assertState(t, h, waiting.ID, job.StateQueued)

	apply(t, h, f, change(&holder, job.EventCancel))

	if res := runReconcile(t, h, 0); res.Dispatched != 1 {
		t.Fatalf("reconcile after the unit is free = %+v; want one dispatch", res)
	}

	assertState(t, h, waiting.ID, job.StateDispatched)
}

func testReconcileGate(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationRefresh, "")
	j.Phases[0].Gates = []job.Gate{{
		Type: job.GateApproval, Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0,
		BindPlan: false,
	}}
	queued := create(t, h, f, j, nil)

	if res := runReconcile(t, h, 0); res.Gated != 1 || res.Dispatched != 0 {
		t.Fatalf("reconcile of a queued job with a closed gate = %+v; want one gated job", res)
	}

	assertState(t, h, queued.ID, job.StateAwaitingApproval)
}

func testReconcileApprovedGate(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationRefresh, "")
	j.Phases[0].Gates = []job.Gate{{
		Type: job.GateApproval, Approvals: 1, Groups: nil, GroupIDs: nil, AllowSelf: false, Timeout: 0,
		BindPlan: false,
	}}
	j.Phases[0].Approvals = []job.Approval{{
		Gate: job.GateApproval, Subject: "bo", Agent: "", Verdict: job.VerdictApprove, Comment: "", Plan: nil,
		At: time.Now().UTC(), Auth: "", AuthAt: time.Time{},
	}}
	queued := create(t, h, f, j, nil)

	// An approved job goes back to queued, and the reconcile dispatches it. It
	// does not send the job to approval again.
	if res := runReconcile(t, h, 0); res.Dispatched != 1 || res.Gated != 0 {
		t.Fatalf("reconcile of a queued job with an approved gate = %+v; want one dispatch", res)
	}

	assertState(t, h, queued.ID, job.StateDispatched)
}

func testReconcileStalePlan(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	if err := h.Domain.SetStateVersion(t.Context(), f.fence, f.unit, svTwo); err != nil {
		t.Fatalf("SetStateVersion = %v", err)
	}

	stale := planned(t, h, f, svOne, time.Now())

	if res := runReconcile(t, h, 0); res.Replanned != 1 || res.Dispatched != 0 {
		t.Fatalf("reconcile of a job with a stale plan = %+v; want one replan", res)
	}

	got, err := h.Jobs.Get(t.Context(), stale.ID)
	if err != nil || got.State != job.StateQueued || got.Current != 0 || got.Phases[0].Plan != nil {
		t.Fatalf("job after the replan = %s, phase %d, plan %+v, %v; want queued at the plan phase with no plan",
			got.State, got.Current, got.Phases[0].Plan, err)
	}

	apply(t, h, f, change(&got, job.EventCancel))

	fresh := planned(t, h, f, svTwo, time.Now())

	if res := runReconcile(t, h, 0); res.Dispatched != 1 {
		t.Fatalf("reconcile of a job with a fresh plan = %+v; want one dispatch", res)
	}

	assertState(t, h, fresh.ID, job.StateDispatched)
}

// testReconcileNoPlan proves that a job whose plan phase stored no plan ends
// failed with the reason job.ReasonNoPlan, once, and that no run of the
// reconcile sends it back to its plan phase.
func testReconcileNoPlan(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	j := newJob(f.unit, job.OperationApply, "")
	j.Current = 1
	j.Phases[0].StateVersion = svOne
	noPlan := create(t, h, f, j, nil)

	if res := runReconcile(t, h, 0); res.Ended != 1 || res.Replanned != 0 || res.Dispatched != 0 {
		t.Fatalf("reconcile of a job whose plan phase stored no plan = %+v; want one ended job and no replan", res)
	}

	got, err := h.Jobs.Get(t.Context(), noPlan.ID)
	if err != nil || got.State != job.StateFailed || got.Reason != job.ReasonNoPlan || got.Current != 1 {
		t.Fatalf("job after the reconcile = %s, phase %d, reason %q, %v; want failed at the apply phase with %q",
			got.State, got.Current, got.Reason, err, job.ReasonNoPlan)
	}

	checkNoLaterChange(t, h)

	if n := reconcileEntries(t, h, f, noPlan.ID); n != 1 {
		t.Fatalf("reconcile audit entries for the job = %d, want 1", n)
	}
}

// checkNoLaterChange runs the reconcile twice and proves that it changes
// nothing: a failed job is final, so it never plans again.
func checkNoLaterChange(t *testing.T, h Harness) {
	t.Helper()

	for range 2 {
		if res := runReconcile(t, h, 0); res.Ended != 0 || res.Replanned != 0 || res.Dispatched != 0 {
			t.Fatalf("later reconcile = %+v; want no change", res)
		}
	}
}

// expiryFixture holds one job for each timeout of the reconcile, each on its
// own unit.
type expiryFixture struct {
	queued     job.Job
	dispatched job.Job
	awaiting   job.Job
}

// newExpiryFixture makes a locked queued job, a dispatched job that no runner
// claimed and a delivered write-back job that waits for its state version.
func newExpiryFixture(t *testing.T, h Harness, f fixture) expiryFixture {
	t.Helper()

	dns, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	db, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "db")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	lockUnit(t, h, f)

	q := newJob(f.unit, job.OperationRefresh, "")
	q.Wait = job.WaitLocked

	d := newJob(dns, job.OperationRefresh, "")

	return expiryFixture{
		queued:     create(t, h, f, q, nil),
		dispatched: create(t, h, f, d, dispatchOf(d)),
		awaiting:   deliver(t, h, f, db, job.DeliveryReviewRequest),
	}
}

func testReconcileExpires(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	fx := newExpiryFixture(t, h, f)

	// Past the longest timeout, every job is past its own.
	res := runReconcile(t, h, job.DefaultStateTimeout+time.Hour)
	if res.Expired != expiryJobs {
		t.Fatalf("reconcile past every timeout = %+v; want three expired jobs", res)
	}

	for _, id := range []job.ID{fx.queued.ID, fx.dispatched.ID, fx.awaiting.ID} {
		got, err := h.Jobs.Get(t.Context(), id)
		if err != nil || got.State != job.StateExpired || !strings.Contains(got.Reason, "more than") {
			t.Fatalf("job %s after the reconcile = %s, %q, %v; want expired with the reason", id, got.State,
				got.Reason, err)
		}

		if n := reconcileEntries(t, h, f, id); n != 1 {
			t.Fatalf("the expiry of %s wrote %d audit entries of the reconcile, want 1", id, n)
		}
	}

	// The operation of the expired dispatched job stays pending: the claimer
	// that takes it ends it, as the case of the claimer proves.
	op, err := h.Durable.Get(t.Context(), durable.ID(fx.dispatched.Phases[0].Operation))
	if err != nil || op.State != durable.StatePending {
		t.Fatalf("operation of the expired dispatched job = %s, %v; want pending", op.State, err)
	}

	if !followUp(t, h, fx.awaiting.ID, job.KindAbandon) {
		t.Fatalf("expiry of a delivered job started no %s operation", job.KindAbandon)
	}
}

func testReconcileNotYet(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	fx := newExpiryFixture(t, h, f)

	// A little less than the shortest timeout (the dispatch timeout): no job is
	// past a timeout.
	if res := runReconcile(t, h, job.DefaultDispatchTimeout-time.Hour); res.Expired != 0 {
		t.Fatalf("reconcile before every timeout = %+v; want no expired job", res)
	}

	assertState(t, h, fx.queued.ID, job.StateQueued)
	assertState(t, h, fx.dispatched.ID, job.StateDispatched)
	assertState(t, h, fx.awaiting.ID, job.StateAwaitingState)

	// Past the dispatch timeout and before the queue timeout: only the
	// dispatched job expires.
	if res := runReconcile(t, h, job.DefaultDispatchTimeout+time.Hour); res.Expired != 1 {
		t.Fatalf("reconcile past the dispatch timeout = %+v; want one expired job", res)
	}

	assertState(t, h, fx.queued.ID, job.StateQueued)
	assertState(t, h, fx.dispatched.ID, job.StateExpired)
	assertState(t, h, fx.awaiting.ID, job.StateAwaitingState)
}

func testReconcileOrphans(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	live := runApplyPhase(t, h, f)
	claimOp(t, h, "runner-1", job.RunnerKind(live.Spec.Pool), time.Minute)
	live = claimed(t, h, f, &live, "runner-1")

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	lost := newJob(other, job.OperationRefresh, "")
	lost = new(create(t, h, f, lost, dispatchOf(lost)))

	// The operation of lost ends with no change of its job, as after a durable
	// failure at the limit of expired claims.
	if err = h.Durable.Cancel(t.Context(), durable.ID(lost.Phases[0].Operation)); err != nil {
		t.Fatalf("durable Cancel = %v", err)
	}

	if res := runReconcile(t, h, 0); res.Ended != 1 || res.Expired != 0 {
		t.Fatalf("reconcile with one orphan = %+v; want one ended job", res)
	}

	got, err := h.Jobs.Get(t.Context(), lost.ID)
	if err != nil || got.State != job.StateFailed || got.Reason == "" {
		t.Fatalf("orphan after the reconcile = %s, %q, %v; want failed with a reason", got.State, got.Reason, err)
	}

	if n := reconcileEntries(t, h, f, lost.ID); n != 1 {
		t.Fatalf("the end of the orphan wrote %d audit entries of the reconcile, want 1", n)
	}

	assertState(t, h, live.ID, job.StateRunning)

	if orphans, damaged, err := h.Jobs.Orphans(t.Context(), orphanPage); err != nil || len(orphans) != 0 ||
		len(damaged) != 0 {
		t.Fatalf("Orphans after the reconcile = %v, %v; want none", orphanIDs(orphans), err)
	}
}

func testReconcileTwice(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	lockUnit(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "")
	j.Wait = job.WaitLocked
	queued := create(t, h, f, j, nil)

	runReconcile(t, h, job.DefaultQueueTimeout+time.Hour)
	assertState(t, h, queued.ID, job.StateExpired)

	var none job.ReconcileResult

	if res := runReconcile(t, h, job.DefaultQueueTimeout+time.Hour); res != none {
		t.Fatalf("second reconcile = %+v; want no change", res)
	}
}

// concurrentJobs is the number of queued jobs of the concurrent case.
const concurrentJobs = 6

func testReconcileConcurrent(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	// Plan jobs do not hold the unit, so all of them can dispatch.
	queued := make([]job.ID, 0, concurrentJobs)
	for range concurrentJobs {
		queued = append(queued, create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil).ID)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		total   int
		failure error
	)

	// Both reconcilers wait at a barrier, so that they begin together. The
	// second dispatch of a job loses on the version or the state of the job
	// (ErrConflict or ErrTransition), and the reconcile counts it as skipped.
	start := make(chan struct{})

	for range 2 {
		wg.Go(func() {
			<-start

			res, err := reconciler(t, h, 0).Reconcile(t.Context())

			mu.Lock()
			defer mu.Unlock()

			total += res.Dispatched
			failure = errors.Join(failure, err)
		})
	}

	close(start)
	wg.Wait()

	// The property: each job is dispatched once and has one audit entry of the
	// reconcile, whichever reconciler won.
	if failure != nil || total != concurrentJobs {
		t.Fatalf("two parallel reconciles dispatched %d jobs, error %v; want %d dispatches, one for each job",
			total, failure, concurrentJobs)
	}

	for _, id := range queued {
		got, err := h.Jobs.Get(t.Context(), id)
		if err != nil || got.State != job.StateDispatched || got.Phases[0].Operation == "" {
			t.Fatalf("job %s = %s, %v; want dispatched with one operation", id, got.State, err)
		}

		if n := reconcileEntries(t, h, f, id); n != 1 {
			t.Fatalf("job %s has %d audit entries of the reconcile, want 1", id, n)
		}
	}
}

// failingOrphans is a job store whose read of the orphans fails.
type failingOrphans struct {
	job.Store
}

var errOrphanRead = errors.New("the read of the orphans failed")

// Orphans implements job.Store.
func (failingOrphans) Orphans(context.Context, int) ([]job.Orphan, []job.DamagedRow, error) {
	return nil, nil, errOrphanRead
}

func testReconcileOrphanReadFails(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	fx := newExpiryFixture(t, h, f)

	r := reconcilerOver(t, h, failingOrphans{Store: h.Jobs}, job.DefaultQueueTimeout+time.Hour)

	res, err := r.Reconcile(t.Context())
	if err != nil || res.Failed != 1 || res.Expired != 2 {
		t.Fatalf(
			"Reconcile with a failed read of the orphans = %+v, %v; want two expired jobs and one failure",
			res,
			err,
		)
	}

	assertState(t, h, fx.queued.ID, job.StateExpired)
	assertState(t, h, fx.dispatched.ID, job.StateExpired)
	assertState(t, h, fx.awaiting.ID, job.StateAwaitingState)
}
