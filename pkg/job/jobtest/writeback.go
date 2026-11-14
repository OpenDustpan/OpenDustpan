package jobtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// deliver stores a write-back job of unit with the delivery d that
// delivered and waits for a state version.
func deliver(t *testing.T, h Harness, f fixture, unit identity.Object, d job.Delivery) job.Job {
	t.Helper()

	j := newJob(unit, job.OperationApply, "")
	j.Spec.Executor, j.Spec.Steps, j.Spec.Pool, j.Spec.Profile, j.Spec.Delivery = job.ExecutorWriteBack, nil, "", "", d
	j.Spec.Edits = []job.FileEdit{{Path: "network.tfvars.json", Keys: map[string][]byte{"size": []byte(`"large"`)}}}
	j.Phases = j.Phases[:1]
	j.Phases[0].Kind = job.PhaseDeliver

	stored := create(t, h, f, j, dispatchOf(j))
	claimOp(t, h, "instance-a", job.KindWriteBack, time.Minute)
	stored = claimed(t, h, f, &stored, "instance-a")

	branch, review := mainRef, ""
	if d == job.DeliveryReviewRequest {
		branch, review = "dustpan/"+string(j.ID), "https://forge.example/acme/infra/pull/7"
	}

	done := change(&stored, job.EventDelivered)
	done.Delivery = &job.DeliveryResult{
		Delivery: d, Commit: commitDef, Branch: branch, ReviewRequest: review, MergedAt: time.Time{},
		DeliveredAt: time.Now().UTC(),
	}
	stored = apply(t, h, f, done)

	if stored.State != job.StateAwaitingState || stored.Delivery == nil || stored.Delivery.DeliveredAt.IsZero() {
		t.Fatalf("delivered job = %s, %+v; want awaiting-state with a delivery time", stored.State,
			stored.Delivery)
	}

	return stored
}

// followUp reports whether the operation of kind k of the job id exists:
// a start with its key returns it and created false.
func followUp(t *testing.T, h Harness, id job.ID, k durable.Kind) bool {
	t.Helper()

	_, created, err := h.Durable.Start(t.Context(), durable.StartRequest{
		Key: job.FollowUpKey(id, k), Kind: k, Version: 1, Input: nil,
	})
	if err != nil {
		t.Fatalf("durable Start(%s) = %v", k, err)
	}

	return !created
}

func testCancelAwaitingState(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryReviewRequest)

	got := apply(t, h, f, change(&j, job.EventCancel))
	if got.State != job.StateCanceled {
		t.Fatalf("cancel in awaiting-state = %s, want canceled", got.State)
	}

	if !followUp(t, h, j.ID, job.KindAbandon) {
		t.Fatalf("cancel in awaiting-state started no %s operation", job.KindAbandon)
	}
}

func testObserveStaleFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryDirectPush)

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	if h.WriteState == nil {
		t.Fatal("the harness gives no state writer")
	}

	_, err := h.WriteState(t.Context(), f.fence, f.unit, true)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("WriteState with a stale fence = %v, want ErrFenced", err)
	}

	assertState(t, h, j.ID, job.StateAwaitingState)
}

func testFinalStateVersionCloses(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	delivered := deliver(t, h, f, f.unit, job.DeliveryDirectPush)

	// A final write of another unit closes none of this unit.
	writeStateOf(t, h, f.fence, other, true)
	assertState(t, h, delivered.ID, job.StateAwaitingState)

	elsewhere := deliver(t, h, f, other, job.DeliveryDirectPush)

	// An intermediate write of the unit closes none of its jobs.
	writeState(t, h, f, false)
	assertState(t, h, delivered.ID, job.StateAwaitingState)

	closedBy := writeState(t, h, f, true)

	closed, err := h.Jobs.Get(t.Context(), delivered.ID)
	if err != nil || closed.State != job.StateSucceeded || closed.Closed == nil ||
		closed.Closed.VersionID != closedBy || closed.Verification != job.VerificationPending {
		t.Fatalf("closed job = %s, %+v, %q, %v; want succeeded, closed by %s, verification pending",
			closed.State, closed.Closed, closed.Verification, err, closedBy)
	}

	if !followUp(t, h, delivered.ID, job.KindVerify) {
		t.Fatalf("the close started no %s operation", job.KindVerify)
	}

	assertState(t, h, elsewhere.ID, job.StateAwaitingState)
}

func testMergeAndVerify(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryReviewRequest)

	if !followUp(t, h, j.ID, job.KindReviewWatch) {
		t.Fatalf("the delivery of a review request started no %s operation", job.KindReviewWatch)
	}

	// The final version is written before the merge. The merge at its time
	// closes the job on it.
	closedBy := writeState(t, h, f, true)
	if fv := finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()); fv.VersionID != closedBy {
		t.Fatalf("final version = %s, want the write %s", fv.VersionID, closedBy)
	}

	merged := change(&j, job.EventMerged)
	merged.MergedAt = finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()).At
	j = apply(t, h, f, merged)

	if j.State != job.StateSucceeded || j.Closed == nil || j.Closed.VersionID != closedBy {
		t.Fatalf("merged job = %s, %+v; want succeeded, closed by %s", j.State, j.Closed, closedBy)
	}

	verify(t, h, f, j.ID)
}

// verify sets the verification of the closed job id, and checks that only
// a result is accepted.
func verify(t *testing.T, h Harness, f fixture, id job.ID) {
	t.Helper()

	err := h.Jobs.SetVerification(t.Context(), id, job.VerificationPending, f.fence, nil)
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("SetVerification(pending) = %v, want ErrInvalid", err)
	}

	if err = h.Jobs.SetVerification(t.Context(), id, job.VerificationVerified, f.fence, nil); err != nil {
		t.Fatalf("SetVerification = %v", err)
	}

	got, err := h.Jobs.Get(t.Context(), id)
	if err != nil || got.Verification != job.VerificationVerified {
		t.Fatalf("Get = %q, %v; want verified", got.Verification, err)
	}
}

// planSize is the size of the plan parts of the stale plan case.
const planSize = 2

// planned stores an apply job whose plan phase made plans that read the
// state version sv at made, and that waits to dispatch its apply phase.
func planned(t *testing.T, h Harness, f fixture, sv string, made time.Time) job.Job {
	t.Helper()

	j := newJob(f.unit, job.OperationApply, "")
	j.Current = 1
	j.Phases[0].StateVersion = sv
	j.Phases[0].Plan = &job.PlanRef{
		ID: "pln-" + string(j.ID), ItemKey: secret.NewItemKeyID(), StateVersion: sv,
		Parts: []job.PlanPart{
			{Kind: job.OutputPlanDisplay, Size: planSize, SHA256: "d1", Secret: false, StoredSize: 0, StoredMD5: ""},
			{Kind: job.OutputPlanApply, Size: planSize, SHA256: "a1", Secret: true, StoredSize: 0, StoredMD5: ""},
		},
		CreatedAt: made, ExpiresAt: history.ExpiresAt(made, history.DefaultRetention), RedactedAt: time.Time{},
	}

	return create(t, h, f, j, nil)
}

func testPlanStale(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	old := writeState(t, h, f, false)
	current := writeState(t, h, f, false)
	now := time.Now()

	tests := []struct {
		name string
		sv   string
		made time.Time
		want error
	}{
		{"another state version", old, now, job.ErrPlanStale},
		{"too old", current, now.Add(-job.DefaultMaxPlanAge - time.Hour), job.ErrPlanStale},
		{"fresh", current, now, nil},
	}

	for _, tt := range tests {
		j := planned(t, h, f, tt.sv, tt.made)
		c := dispatch(&j)
		c.PlanNotBefore = now.Add(-job.DefaultMaxPlanAge)

		got, err := h.Jobs.Apply(t.Context(), c, f.fence, nil)
		if tt.want == nil {
			if err != nil || got.State != job.StateDispatched {
				t.Fatalf("%s: dispatch = %s, %v; want dispatched", tt.name, got.State, err)
			}

			continue
		}

		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: dispatch = %v, want %v", tt.name, err, tt.want)
		}

		back := apply(t, h, f, change(&j, job.EventReplan))
		if back.State != job.StateQueued || back.Current != 0 || back.Phases[0].Plan != nil {
			t.Fatalf("%s: replan = %s, phase %d, plan %+v; want queued at the plan phase with no plan",
				tt.name, back.State, back.Current, back.Phases[0].Plan)
		}

		apply(t, h, f, change(&back, job.EventCancel))
	}
}
