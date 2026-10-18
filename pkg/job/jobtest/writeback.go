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
		Delivery: d, Commit: "def456", Branch: branch, ReviewRequest: review, MergedAt: time.Time{},
		DeliveredAt: time.Now().UTC(),
	}
	stored = apply(t, h, f, done)

	if stored.State != job.StateAwaitingState || stored.Delivery == nil || stored.Delivery.DeliveredAt.IsZero() {
		t.Fatalf("delivered job = %s, %+v; want awaiting-state with a delivery time", stored.State,
			stored.Delivery)
	}

	return stored
}

// version returns a state version of unit at at.
func version(unit identity.Object, id string, at time.Time, final bool) job.StateVersion {
	return job.StateVersion{Unit: unit, VersionID: id, Commit: "", Job: "", At: at, Final: final}
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

	_, err := h.Jobs.ObserveStateVersion(t.Context(), version(f.unit, svOne, time.Now().Add(time.Second), true),
		f.fence)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("ObserveStateVersion with a stale fence = %v, want ErrFenced", err)
	}

	assertState(t, h, j.ID, job.StateAwaitingState)
}

func testObserveStateVersion(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	delivered := deliver(t, h, f, f.unit, job.DeliveryDirectPush)
	elsewhere := deliver(t, h, f, other, job.DeliveryDirectPush)
	after := time.Now().Add(time.Second)

	closesNone(t, h, f, map[string]job.StateVersion{
		"before the delivery":   version(f.unit, "sv-early", delivered.Delivery.DeliveredAt.Add(-time.Second), true),
		"an intermediate write": version(f.unit, "sv-mid", after, false),
	})

	closed, err := h.Jobs.ObserveStateVersion(t.Context(), version(f.unit, svOne, after, true), f.fence)
	if err != nil || len(closed) != 1 || closed[0].ID != delivered.ID {
		t.Fatalf("ObserveStateVersion closed %v, %v; want %s", ids(closed), err, delivered.ID)
	}

	if closed[0].State != job.StateSucceeded || closed[0].Closed == nil || closed[0].Closed.VersionID != svOne ||
		closed[0].Verification != job.VerificationPending {
		t.Fatalf("closed job = %s, %+v, %q; want succeeded, closed by sv-1, verification pending",
			closed[0].State, closed[0].Closed, closed[0].Verification)
	}

	if !followUp(t, h, delivered.ID, job.KindVerify) {
		t.Fatalf("the close started no %s operation", job.KindVerify)
	}

	assertState(t, h, elsewhere.ID, job.StateAwaitingState)
}

// closesNone fails t when a state version of versions closes a job. The
// keys name the cases.
func closesNone(t *testing.T, h Harness, f fixture, versions map[string]job.StateVersion) {
	t.Helper()

	for name := range versions {
		closed, err := h.Jobs.ObserveStateVersion(t.Context(), versions[name], f.fence)
		if err != nil || len(closed) != 0 {
			t.Fatalf("%s: closed %v, %v; want none", name, ids(closed), err)
		}
	}
}

func testMergeAndVerify(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryReviewRequest)

	if !followUp(t, h, j.ID, job.KindReviewWatch) {
		t.Fatalf("the delivery of a review request started no %s operation", job.KindReviewWatch)
	}

	closesNone(t, h, f, map[string]job.StateVersion{
		"a state version before the merge": version(f.unit, svOne, time.Now(), true),
	})

	merged := change(&j, job.EventMerged)
	merged.MergedAt = time.Now().UTC()
	j = apply(t, h, f, merged)

	if j.State != job.StateAwaitingState || j.Delivery == nil || j.Delivery.MergedAt.IsZero() {
		t.Fatalf("merged job = %s, %+v; want awaiting-state with a merge time", j.State, j.Delivery)
	}

	closed, err := h.Jobs.ObserveStateVersion(t.Context(),
		version(f.unit, svTwo, merged.MergedAt.Add(time.Second), true), f.fence)
	if err != nil || len(closed) != 1 || closed[0].ID != j.ID {
		t.Fatalf("a state version after the merge closed %v, %v; want %s", ids(closed), err, j.ID)
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
			{Kind: job.OutputPlanDisplay, Size: planSize, SHA256: "d1", Secret: false},
			{Kind: job.OutputPlanApply, Size: planSize, SHA256: "a1", Secret: true},
		},
		CreatedAt: made, ExpiresAt: history.ExpiresAt(made, history.DefaultRetention), RedactedAt: time.Time{},
	}

	return create(t, h, f, j, nil)
}

func testPlanStale(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	now := time.Now()

	if err := h.Domain.SetStateVersion(t.Context(), f.fence, f.unit, svTwo); err != nil {
		t.Fatalf("SetStateVersion = %v", err)
	}

	tests := []struct {
		name string
		sv   string
		made time.Time
		want error
	}{
		{"another state version", svOne, now, job.ErrPlanStale},
		{"too old", svTwo, now.Add(-job.DefaultMaxPlanAge - time.Hour), job.ErrPlanStale},
		{"fresh", svTwo, now, nil},
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
