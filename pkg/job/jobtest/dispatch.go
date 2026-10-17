package jobtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

func testApplyConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	stale := change(&stored, job.EventCancel)
	stale.Version = stored.Version - 1

	if _, err := h.Jobs.Apply(t.Context(), stale, f.fence, nil); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("Apply on an older version = %v, want ErrConflict", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)
}

func testApplyTransition(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	_, err := h.Jobs.Apply(t.Context(), change(&stored, job.EventStateObserved), f.fence, nil)
	if !errors.Is(err, job.ErrTransition) {
		t.Fatalf("Apply(state-observed) on queued = %v, want ErrTransition", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)
}

// runApplyPhase creates a refresh job, whose only phase changes state, and
// dispatches it, so that it holds its unit.
func runApplyPhase(t *testing.T, h Harness, f fixture) job.Job {
	t.Helper()

	j := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	return apply(t, h, f, dispatch(&j))
}

func testUnitBusy(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := runApplyPhase(t, h, f)
	second := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	if _, err := h.Jobs.Apply(t.Context(), dispatch(&second), f.fence, nil); !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("second change dispatch while %s holds the unit = %v, want ErrUnitBusy", first.ID, err)
	}

	assertState(t, h, second.ID, job.StateQueued)
}

func testAuditRollback(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	runApplyPhase(t, h, f)
	second := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	before, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries = %v", err)
	}

	_, err = h.Jobs.Apply(t.Context(), dispatch(&second), f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("Apply = %v, want ErrUnitBusy", err)
	}

	after, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil || len(after) != len(before) {
		t.Fatalf("audit entries after a failed change = %d, %v; want %d", len(after), err, len(before))
	}
}

func testUnitFrees(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := runApplyPhase(t, h, f)
	claimOp(t, h, "runner-1", job.RunnerKind(first.Spec.Pool), time.Minute)
	first = claimed(t, h, f, &first, "runner-1")

	if got := apply(t, h, f, change(&first, job.EventLastPhaseDone)); got.State != job.StateSucceeded {
		t.Fatalf("last phase done = %s, want succeeded", got.State)
	}

	second := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)
	if got := apply(t, h, f, dispatch(&second)); got.State != job.StateDispatched {
		t.Fatalf("dispatch after the phase ended = %s, want dispatched", got.State)
	}
}

func testPlanDoesNotHold(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	runApplyPhase(t, h, f)

	plan := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	if got := apply(t, h, f, dispatch(&plan)); got.State != job.StateDispatched {
		t.Fatalf("plan dispatch = %s, want dispatched", got.State)
	}
}

func testFrozen(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	freeze(t, h, f)

	plain := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)
	if _, err := h.Jobs.Apply(t.Context(), dispatch(&plain), f.fence, nil); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("change dispatch in a freeze = %v, want ErrFrozen", err)
	}

	assertState(t, h, plain.ID, job.StateQueued)
}

func testBreakGlass(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	fr := freeze(t, h, f)

	withGlass := newJob(f.unit, job.OperationRefresh, "")
	withGlass.BreakGlass = &job.BreakGlass{Reason: "outage", Freeze: fr.ID}
	glass := create(t, h, f, withGlass, nil)

	if got := apply(t, h, f, dispatch(&glass)); got.State != job.StateDispatched {
		t.Fatalf("break-glass dispatch = %s, want dispatched", got.State)
	}
}

func testFreezeEnds(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	fr := freeze(t, h, f)
	j := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	if _, err := h.Jobs.Apply(t.Context(), dispatch(&j), f.fence, nil); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("change dispatch in a freeze = %v, want ErrFrozen", err)
	}

	err := h.Freezes.EndFreeze(t.Context(), fr.ID, "bo", f.fence, entry(f.unit, job.ActionNamespaceManageFreezes))
	if err != nil {
		t.Fatalf("EndFreeze = %v", err)
	}

	if got := apply(t, h, f, dispatch(&j)); got.State != job.StateDispatched {
		t.Fatalf("dispatch after the freeze = %s, want dispatched", got.State)
	}
}

func testCancelDispatched(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)

	got := apply(t, h, f, change(&j, job.EventCancel))
	if got.State != job.StateCanceled {
		t.Fatalf("cancel of a dispatched job = %s, want canceled", got.State)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(got.Phases[0].Operation))
	if err != nil || op.State != durable.StateCanceled {
		t.Fatalf("operation = %s, %v; want canceled", op.State, err)
	}
}

func testCancelRunning(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)
	claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), time.Minute)

	j = claimed(t, h, f, &j, "runner-1")
	got := apply(t, h, f, change(&j, job.EventCancel))

	if got.State != job.StateCanceling {
		t.Fatalf("cancel of a running job = %s, want canceling", got.State)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(got.Phases[0].Operation))
	if err != nil || op.State != durable.StateRunning || !op.CancelRequested {
		t.Fatalf("operation = %s, cancel %v, %v; want running with a cancel request", op.State,
			op.CancelRequested, err)
	}
}

func testTakeover(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)
	operation := j.Phases[0].Operation

	claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), shortLease)
	j = claimed(t, h, f, &j, "runner-1")

	op := claimAfterExpiry(t, h, "runner-2", job.RunnerKind(j.Spec.Pool))
	if string(op.ID) != operation || op.Claim.Token < 2 {
		t.Fatalf("takeover claim = %s, token %d; want %s with a new token", op.ID, op.Claim.Token, operation)
	}

	got := claimed(t, h, f, &j, "runner-2")
	if got.State != job.StateRunning || got.Phases[0].Operation != operation || got.Phases[0].Claims != 2 ||
		got.Phases[0].Executor != "runner-2" {
		t.Fatalf("takeover = %s, operation %s, claims %d, executor %s; want running on %s, 2 claims, runner-2",
			got.State, got.Phases[0].Operation, got.Phases[0].Claims, got.Phases[0].Executor, operation)
	}
}

// shortLease expires inside one case. expiryWait is the longest wait for
// it, on the clock of the store, and pollEvery the interval of the wait.
const (
	shortLease = 300 * time.Millisecond
	expiryWait = 10 * time.Second
	pollEvery  = 25 * time.Millisecond
)

// claimAfterExpiry claims an operation of kind k for owner as soon as the
// claim before it expires on the clock of the store.
func claimAfterExpiry(t *testing.T, h Harness, owner string, k durable.Kind) durable.Operation {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), expiryWait)
	defer cancel()

	tick := time.NewTicker(pollEvery)
	defer tick.Stop()

	for {
		op, err := h.Durable.Claim(ctx, durable.ClaimRequest{
			Owner: owner, Kinds: []durable.Kind{k}, MaxVersions: nil, Lease: time.Minute,
		})
		if err == nil {
			return op
		}

		if !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("durable Claim = %v", err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no takeover within %v", expiryWait)
		case <-tick.C:
		}
	}
}

// orphanPage is the page size of the orphan reads of the restore case.
const orphanPage = 10

func testFailDuringCancel(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)
	claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), time.Minute)

	j = claimed(t, h, f, &j, "runner-1")
	j = apply(t, h, f, change(&j, job.EventCancel))

	failed := change(&j, job.EventPhaseFailed)
	failed.Reason = "the executor stopped during step \"apply\": read the state of the unit"

	got := apply(t, h, f, failed)
	if got.State != job.StateFailed || got.Reason != failed.Reason {
		t.Fatalf("failure during a cancel = %s, %q; want failed with the reason", got.State, got.Reason)
	}
}

func testOrphans(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	live := runApplyPhase(t, h, f)
	queued := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	lost := newJob(other, job.OperationRefresh, "")
	lost = new(create(t, h, f, lost, dispatchOf(lost)))

	// A restore of the control tables keeps the operations as they were.
	// Here the operation of lost ends with no change of its job.
	if err = h.Durable.Cancel(t.Context(), durable.ID(lost.Phases[0].Operation)); err != nil {
		t.Fatalf("durable Cancel = %v", err)
	}

	orphans, err := h.Jobs.Orphans(t.Context(), orphanPage)
	if err != nil || len(orphans) != 1 || orphans[0].ID != lost.ID {
		t.Fatalf("Orphans = %v, %v; want only %s (not %s or %s)", ids(orphans), err, lost.ID, live.ID, queued.ID)
	}

	failed := change(lost, job.EventPhaseFailed)
	failed.Reason = "restored"

	if got := apply(t, h, f, failed); got.State != job.StateFailed {
		t.Fatalf("reconcile of an orphan = %s, want failed", got.State)
	}

	if orphans, err = h.Jobs.Orphans(t.Context(), orphanPage); err != nil || len(orphans) != 0 {
		t.Fatalf("Orphans after the reconcile = %v, %v; want none", ids(orphans), err)
	}
}
