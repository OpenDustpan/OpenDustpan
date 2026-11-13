package jobtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	if stored.State != job.StateQueued || stored.Version < 1 || stored.CreatedAt.IsZero() {
		t.Fatalf("Create = state %s, version %d, created %v; want queued, a version and a time",
			stored.State, stored.Version, stored.CreatedAt)
	}

	got, err := h.Jobs.Get(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	if got.ID != stored.ID || got.Spec.Unit.String() != f.unit.String() || len(got.Phases) != 1 ||
		got.Spec.Steps[0].Args[0] != "tofu" || got.Spec.Profile != "prf-default" || got.Source != job.SourceAPI {
		t.Fatalf("Get = %+v, want the stored job with source %s", got, job.SourceAPI)
	}
}

func testCreateTrigger(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	withRef := newJob(f.unit, job.OperationPlan, "")
	withRef.Trigger = &job.TriggerRef{ID: "trg-1", Saver: "user:ada"}
	withRef.Source = job.SourceTrigger

	plain := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	stored := create(t, h, f, withRef, nil)

	for _, tt := range []struct {
		name string
		id   job.ID
		want *job.TriggerRef
	}{
		{"a job that a trigger started", stored.ID, &job.TriggerRef{ID: "trg-1", Saver: "user:ada"}},
		{"a job that no trigger started", plain.ID, nil},
	} {
		got, err := h.Jobs.Get(t.Context(), tt.id)
		if err != nil {
			t.Fatalf("%s: Get = %v", tt.name, err)
		}

		if (got.Trigger == nil) != (tt.want == nil) || (tt.want != nil && *got.Trigger != *tt.want) {
			t.Errorf("%s: trigger = %+v, want %+v", tt.name, got.Trigger, tt.want)
		}
	}
}

// testCreateDefaulted proves that a job keeps the list of the values that the
// request did not give, and that a job with no list reads back with none.
func testCreateDefaulted(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	for _, tt := range []struct {
		name string
		list []string
	}{
		{"nothing defaulted", nil},
		{"the pool", []string{"pool"}},
		{"all three", []string{"pool", "location", "profile"}},
	} {
		j := newJob(f.unit, job.OperationPlan, "")
		j.Spec.Defaulted = tt.list

		stored := create(t, h, f, j, nil)

		got, err := h.Jobs.Get(t.Context(), stored.ID)
		if err != nil {
			t.Fatalf("%s: Get = %v", tt.name, err)
		}

		if !slices.Equal(got.Spec.Defaulted, tt.list) {
			t.Errorf("%s: Spec.Defaulted = %v, want %v", tt.name, got.Spec.Defaulted, tt.list)
		}
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Jobs.Get(t.Context(), "job-unknown"); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func testCreateKeyOnce(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-1"), nil)

	again, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, "start-1"), nil, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second Create = %s, created %v, error %v; want %s", again.ID, created, err, first.ID)
	}
}

func testCreateKeyConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	create(t, h, f, newJob(f.unit, job.OperationPlan, "start-1"), nil)

	_, _, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationApply, "start-1"), nil, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if !errors.Is(err, job.ErrKeyConflict) {
		t.Fatalf("Create with another spec = %v, want ErrKeyConflict", err)
	}
}

// hashA and hashB are two request hashes.
const (
	hashA = "hash-a"
	hashB = "hash-b"
)

func testCreateKeyHashConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := newJob(f.unit, job.OperationPlan, "start-1")
	first.RequestHash = hashA
	stored := create(t, h, f, first, nil)

	same := newJob(f.unit, job.OperationPlan, "start-1")
	same.RequestHash = hashA

	again, created, err := h.Jobs.Create(t.Context(), *same, nil, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || created || again.ID != stored.ID || again.RequestHash != hashA {
		t.Fatalf("Create with the same hash = %s, created %v, error %v; want the first job %s", again.ID, created, err,
			stored.ID)
	}

	other := newJob(f.unit, job.OperationPlan, "start-1")
	other.RequestHash = hashB

	_, _, err = h.Jobs.Create(t.Context(), *other, nil, f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if !errors.Is(err, job.ErrKeyConflict) {
		t.Fatalf("Create with the same spec and another hash = %v, want ErrKeyConflict", err)
	}
}

func testCreateAwaitingApproval(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationPlan, "")
	j.State = job.StateAwaitingApproval
	stored := create(t, h, f, j, nil)

	if stored.State != job.StateAwaitingApproval || stored.Wait != job.WaitNone {
		t.Fatalf("Create = state %s, wait %q; want awaiting-approval with no wait", stored.State, stored.Wait)
	}

	got, err := h.Jobs.Get(t.Context(), stored.ID)
	if err != nil || got.State != job.StateAwaitingApproval {
		t.Fatalf("Get = state %s, error %v; want awaiting-approval", got.State, err)
	}

	// A job that waits for approval has no dispatch.
	d := newJob(f.unit, job.OperationPlan, "")
	d.State = job.StateAwaitingApproval

	_, _, err = h.Jobs.Create(t.Context(), *d, dispatchOf(d), f.fence, nil)
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("Create awaiting-approval with a dispatch = %v, want ErrInvalid", err)
	}
}

func testCreateBadState(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	for _, state := range []job.State{job.StateRunning, job.StateSucceeded, job.StateDispatched, ""} {
		j := newJob(f.unit, job.OperationPlan, "")
		j.State = state

		_, _, err := h.Jobs.Create(t.Context(), *j, nil, f.fence, nil)
		if !errors.Is(err, job.ErrInvalid) {
			t.Errorf("Create in state %q = %v, want ErrInvalid", state, err)
		}
	}
}

func testCreateDispatch(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationPlan, "")
	stored := create(t, h, f, j, dispatchOf(j))

	if stored.State != job.StateDispatched || stored.Phases[0].Operation == "" {
		t.Fatalf("Create = state %s, operation %q; want dispatched with an operation",
			stored.State, stored.Phases[0].Operation)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(stored.Phases[0].Operation))
	if err != nil || op.Key != job.PhaseKey(j.ID, 0) || op.Kind != job.RunnerKind(j.Spec.Pool) {
		t.Fatalf("durable Get = %+v, %v; want the operation of phase 0", op, err)
	}
}

func testCreateUnitBusy(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	runApplyPhase(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "")

	_, _, err := h.Jobs.Create(t.Context(), *j, dispatchOf(j), f.fence, nil)
	if !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("Create with a change dispatch on a busy unit = %v, want ErrUnitBusy", err)
	}

	if _, err := h.Jobs.Get(t.Context(), j.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get after a failed Create = %v, want ErrNotFound", err)
	}
}

func testCreateFrozen(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	freeze(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "")

	_, _, err := h.Jobs.Create(t.Context(), *j, dispatchOf(j), f.fence, nil)
	if !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("Create with a change dispatch in a freeze = %v, want ErrFrozen", err)
	}

	plan := newJob(f.unit, job.OperationPlan, "")
	if got := create(t, h, f, plan, dispatchOf(plan)); got.State != job.StateDispatched {
		t.Fatalf("Create with a plan dispatch in a freeze = %s, want dispatched", got.State)
	}
}

func testCreateLimit(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	for range job.MaxOpenPerUnit {
		create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	}

	_, _, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, ""), nil, f.fence, nil)
	if !errors.Is(err, job.ErrLimit) {
		t.Fatalf("Create above %d open jobs = %v, want ErrLimit", job.MaxOpenPerUnit, err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	if _, err := h.Jobs.Apply(t.Context(), change(&stored, job.EventCancel), f.fence, nil); !errors.Is(err,
		namespace.ErrFenced) {
		t.Fatalf("Apply with a stale fence = %v, want ErrFenced", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)

	_, _, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, ""), nil, f.fence, nil)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create with a stale fence = %v, want ErrFenced", err)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	older := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	newer := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	apply(t, h, f, change(&newer, job.EventCancel))

	all, err := h.Jobs.List(t.Context(), job.Filter{
		Namespace: f.fence.Namespace, UnitID: "", States: nil, Limit: 0, After: "",
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	})
	if err != nil || len(all.Jobs) != 2 || all.Jobs[0].ID != newer.ID || all.Jobs[1].ID != older.ID {
		t.Fatalf("List = %v, %v; want %s then %s", ids(all.Jobs), err, newer.ID, older.ID)
	}

	queued, err := h.Jobs.List(t.Context(), job.Filter{
		Namespace: f.fence.Namespace, UnitID: f.unit.UnitID(), States: []job.State{job.StateQueued}, Limit: 0,
		After:              "",
		StateChangedBefore: time.Time{}, StateChangedAfter: time.Time{},
	})
	if err != nil || !slices.Equal(ids(queued.Jobs), []job.ID{older.ID}) {
		t.Fatalf("List(queued) = %v, %v; want %s", ids(queued.Jobs), err, older.ID)
	}
}

func testFreezes(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	e := entry(f.unit, job.ActionNamespaceManageFreezes)
	fr := newFreeze(f)
	fr.StartsAt = time.Now()

	bad := fr
	bad.Reason = ""

	if err := h.Freezes.CreateFreeze(t.Context(), bad, f.fence, e); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze with no reason = %v, want ErrInvalid", err)
	}

	if err := h.Freezes.CreateFreeze(t.Context(), fr, f.fence, e); err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	listed, err := h.Freezes.ListFreezes(t.Context(), f.fence.Namespace, time.Now().Add(-time.Hour))
	if err != nil || len(listed) != 1 || listed[0].ID != fr.ID {
		t.Fatalf("ListFreezes = %v, %v; want %s", listed, err, fr.ID)
	}

	if err := h.Freezes.EndFreeze(t.Context(), fr.ID, "bo", f.fence, e); err != nil {
		t.Fatalf("EndFreeze = %v", err)
	}

	if err := h.Freezes.EndFreeze(t.Context(), fr.ID, "bo", f.fence, e); !errors.Is(err, job.ErrFinished) {
		t.Fatalf("second EndFreeze = %v, want ErrFinished", err)
	}

	if err := h.Freezes.EndFreeze(t.Context(), "frz-unknown", "bo", f.fence, e); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("EndFreeze of an unknown ID = %v, want ErrNotFound", err)
	}
}
