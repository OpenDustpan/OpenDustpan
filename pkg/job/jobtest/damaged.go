package jobtest

import (
	"bytes"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// damagedQueueTimeout is the queue timeout of the expiry case, and
// damagedAhead is the clock of the case: past the timeout, so that the expiry
// reads the rows.
const (
	damagedQueueTimeout = time.Minute
	damagedAhead        = 2 * time.Minute
)

// damagedCases returns the cases of a damaged job row: a row whose body does
// not decode must not stop a batch reader, a person must still be able to
// cancel the job by its ID, and a cancel refuses a row that a reader can read.
func damagedCases() []suiteCase {
	return []suiteCase{
		{"a damaged row is reported by List and can be canceled by its ID", testDamagedList},
		{"CancelDamaged refuses a job that is not damaged", testDamagedCancelValid},
		{"a damaged row does not stop the reconcile from dispatching the good jobs", testDamagedDispatch},
		{"a damaged row does not stop the reconcile from expiring the good jobs", testDamagedExpire},
		{"a damaged row does not stop the reconcile from ending the good orphans", testDamagedOrphans},
		{"a damaged row does not stop a state version from closing the good jobs", testDamagedClose},
	}
}

func testDamagedList(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	good := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	bad := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	damage(t, h, bad.ID)

	if _, err := h.Jobs.Get(t.Context(), bad.ID); !errors.Is(err, job.ErrDamaged) {
		t.Fatalf("Get of a damaged job = %v, want ErrDamaged", err)
	}

	page := listPage(t, h, stateFilter(f.fence.Namespace, job.StateQueued))
	checkPage(t, page, []job.ID{good.ID}, bad.ID)

	// The unit ID of a damaged row comes from the row, not from the body:
	// the body did not decode.
	if page.Damaged[0].UnitID != f.unit.UnitID() {
		t.Fatalf("the damaged row has unit %q, want %q", page.Damaged[0].UnitID, f.unit.UnitID())
	}

	cancel := entry(f.unit, job.ActionJobCancel)
	if err := h.Jobs.CancelDamaged(t.Context(), bad.ID, f.fence, []audit.Entry{cancel}); err != nil {
		t.Fatalf("CancelDamaged = %v", err)
	}

	// The row stays where it is, in canceled state.
	checkPage(t, listPage(t, h, stateFilter(f.fence.Namespace, job.StateCanceled)), nil, bad.ID)

	// The good job did not move, and the cancel wrote its audit entry.
	assertState(t, h, good.ID, job.StateQueued)

	if n := entryCount(t, h, f, string(job.ActionJobCancel)); n != 1 {
		t.Fatalf("the cancel wrote %d audit entries, want 1", n)
	}

	// A second cancel of the canceled row is refused.
	if err := h.Jobs.CancelDamaged(t.Context(), bad.ID, f.fence, []audit.Entry{cancel}); !errors.Is(err,
		job.ErrFinished,
	) {
		t.Fatalf("a second CancelDamaged = %v, want ErrFinished", err)
	}
}

// testDamagedCancelValid proves that CancelDamaged refuses a row that a
// reader can read: only a damaged row goes through this path, and the refusal
// changes nothing.
func testDamagedCancelValid(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)
	claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), time.Minute)

	j = claimed(t, h, f, &j, "runner-1")
	if j.State != job.StateRunning {
		t.Fatalf("the job is %s, want running", j.State)
	}

	before, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	err = h.Jobs.CancelDamaged(t.Context(), j.ID, f.fence, []audit.Entry{entry(f.unit, job.ActionJobCancel)})
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CancelDamaged of a job that is not damaged = %v, want ErrInvalid", err)
	}

	got, err := h.Jobs.Get(t.Context(), j.ID)
	if err != nil || got.State != j.State || got.Version != j.Version {
		t.Fatalf("Get after a refused CancelDamaged = %s at version %d, %v; want %s at version %d", got.State,
			got.Version, err, j.State, j.Version)
	}

	after, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries after a refused cancel = %d, %v; want %d", len(after), err, len(before))
	}
}

func testDamagedDispatch(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	good := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	bad := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	damage(t, h, bad.ID)

	res, log := reconcileLogged(t, h, 0, 0)
	if res.Dispatched != 1 {
		t.Fatalf("Reconcile dispatched %d jobs, want 1", res.Dispatched)
	}

	assertState(t, h, good.ID, job.StateDispatched)

	// The damaged row stays queued and is reported.
	checkPage(t, listPage(t, h, stateFilter(f.fence.Namespace, job.StateQueued)), nil, bad.ID)

	// The run logged the damaged row once, with its job ID.
	checkLoggedOnce(t, log, bad.ID)
}

func testDamagedExpire(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	good := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	bad := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	damage(t, h, bad.ID)

	// The clock is past a short queue timeout, so the expiry reads both rows,
	// and the dispatch reads the damaged row again.
	res, log := reconcileLogged(t, h, damagedAhead, damagedQueueTimeout)
	if res.Expired != 1 {
		t.Fatalf("Reconcile expired %d jobs, want 1", res.Expired)
	}

	assertState(t, h, good.ID, job.StateExpired)

	// The damaged row stays queued and is reported.
	checkPage(t, listPage(t, h, stateFilter(f.fence.Namespace, job.StateQueued)), nil, bad.ID)

	// Two passes read the row, and the run logged it once.
	checkLoggedOnce(t, log, bad.ID)
}

func testDamagedOrphans(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	lost := lostOrphan(t, h, f)
	bad := damagedJob(t, h, f)

	orphans, damaged, err := h.Jobs.Orphans(t.Context(), orphanPage)
	if err != nil || len(orphans) != 1 || orphans[0].Job.ID != lost.ID || len(damaged) != 1 ||
		damaged[0].ID != bad.ID {
		t.Fatalf("Orphans = %v with damaged %v, %v; want %s and the damaged %s", orphanIDs(orphans), damaged, err,
			lost.ID, bad.ID)
	}

	res, log := reconcileLogged(t, h, 0, 0)
	if res.Ended != 1 {
		t.Fatalf("Reconcile ended %d jobs, want 1", res.Ended)
	}

	assertState(t, h, lost.ID, job.StateFailed)

	// The damaged row stays dispatched and is reported.
	checkPage(t, listPage(t, h, stateFilter(f.fence.Namespace, job.StateDispatched)), nil, bad.ID)

	checkLoggedOnce(t, log, bad.ID)
}

// lostOrphan makes one dispatched job of the unit of f whose operation ends
// with no change of its job, as a restore of the control tables leaves it.
func lostOrphan(t *testing.T, h Harness, f fixture) job.Job {
	t.Helper()

	j := newJob(f.unit, job.OperationRefresh, "")
	j = new(create(t, h, f, j, dispatchOf(j)))
	endOperation(t, h, j)

	return *j
}

// damagedJob makes one dispatched job of another unit of the namespace of f
// whose operation ends with no change of its job, and damages its body.
func damagedJob(t *testing.T, h Harness, f fixture) job.Job {
	t.Helper()

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	j := newJob(other, job.OperationRefresh, "")
	j = new(create(t, h, f, j, dispatchOf(j)))
	endOperation(t, h, j)
	damage(t, h, j.ID)

	return *j
}

// endOperation ends the durable operation of the current phase of j with no
// change of its job.
func endOperation(t *testing.T, h Harness, j *job.Job) {
	t.Helper()

	if err := h.Durable.Cancel(t.Context(), durable.ID(j.Phases[j.Current].Operation)); err != nil {
		t.Fatalf("durable Cancel = %v", err)
	}
}

func testDamagedClose(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	good := deliver(t, h, f, f.unit, job.DeliveryDirectPush)
	bad := deliver(t, h, f, f.unit, job.DeliveryDirectPush)

	damage(t, h, bad.ID)

	closed, err := h.Jobs.ObserveStateVersion(t.Context(),
		version(f.unit, svOne, time.Now().Add(time.Second), true), f.fence)
	if err != nil || len(closed) != 1 || closed[0].ID != good.ID {
		t.Fatalf("ObserveStateVersion closed %v, %v; want %s", ids(closed), err, good.ID)
	}

	// The damaged row stays awaiting-state and is reported.
	checkPage(t, listPage(t, h, stateFilter(f.fence.Namespace, job.StateAwaitingState)), nil, bad.ID)
}

// damage makes the stored body of the job id one that no reader decodes.
func damage(t *testing.T, h Harness, id job.ID) {
	t.Helper()

	if err := h.Domain.DamageJobBody(t.Context(), id); err != nil {
		t.Fatalf("DamageJobBody(%s) = %v", id, err)
	}
}

// stateFilter returns the filter of the jobs of one namespace in the states.
func stateFilter(ns namespace.Name, states ...job.State) job.Filter {
	return job.Filter{
		Namespace: ns, UnitID: "", States: states, Limit: 0, After: "",
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	}
}

// checkPage proves that page holds exactly the good jobs want and one damaged
// row with the ID bad, and that the reason of the row is not empty.
func checkPage(t *testing.T, page job.Page, want []job.ID, bad job.ID) {
	t.Helper()

	if !slices.Equal(ids(page.Jobs), want) || len(page.Damaged) != 1 || page.Damaged[0].ID != bad ||
		page.Damaged[0].Reason == "" {
		t.Fatalf("page = %v with damaged %v; want %v and the damaged %s", ids(page.Jobs), page.Damaged, want, bad)
	}
}

// checkLoggedOnce proves that the log holds one warning for the job id.
func checkLoggedOnce(t *testing.T, log string, id job.ID) {
	t.Helper()

	if n := warnLines(log, string(id)); n != 1 {
		t.Fatalf("the run logged %d warnings for the damaged row %s, want 1:\n%s", n, id, log)
	}
}

// listPage reads one page and fails t on an error.
func listPage(t *testing.T, h Harness, f job.Filter) job.Page {
	t.Helper()

	page, err := h.Jobs.List(t.Context(), f)
	if err != nil {
		t.Fatalf("List = %v", err)
	}

	return page
}

// entryCount returns the number of audit entries of the namespace with the
// action.
func entryCount(t *testing.T, h Harness, f fixture, action string) int {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	n := 0

	for i := range entries {
		if entries[i].Action == action {
			n++
		}
	}

	return n
}

// reconcileLogged runs one reconcile of h with a log into a buffer, and
// returns its result and the log text. A queue timeout of 0 takes the default
// of the reconciler.
func reconcileLogged(t *testing.T, h Harness, ahead, queueTimeout time.Duration) (job.ReconcileResult, string) {
	t.Helper()

	var out bytes.Buffer

	r, err := job.NewReconciler(job.ReconcilerConfig{
		Store: h.Jobs, Namespaces: h.Namespaces, QueueTimeout: queueTimeout, DispatchTimeout: 0, StateTimeout: 0,
		MaxPlanAge: 0, Approvers: gate.ApproverPolicy{Production: false, IsDev: nil}, Batch: 0,
		Now: func() time.Time { return time.Now().Add(ahead) },
		Log: slog.New(slog.NewTextHandler(&out, nil)),
	})
	if err != nil {
		t.Fatalf("NewReconciler = %v", err)
	}

	res, err := r.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile = %v", err)
	}

	return res, out.String()
}

// warnLines returns the number of log lines of level WARN that name the job
// ID.
func warnLines(log, id string) int {
	n := 0

	for line := range strings.SplitSeq(log, "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, id) {
			n++
		}
	}

	return n
}
