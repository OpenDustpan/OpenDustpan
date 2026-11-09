package jobtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
)

func testLostWebhook(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryReviewRequest)

	if !followUp(t, h, j.ID, job.KindReviewWatch) {
		t.Fatalf("the delivery of a review request started no %s operation", job.KindReviewWatch)
	}

	// The merge happens on the forge, and its webhook does not arrive. The
	// team's CI then writes the state.
	closedBy := writeState(t, h, f, true)
	if fv := finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()); fv.VersionID != closedBy {
		t.Fatalf("final version = %s, want the write %s", fv.VersionID, closedBy)
	}

	// The review watch operation polls the forge and sees the merge at the
	// time of the state write.
	merged := change(&j, job.EventMerged)
	merged.MergedAt = finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()).At

	got := apply(t, h, f, merged)
	if got.State != job.StateSucceeded || got.Closed == nil || got.Closed.VersionID != closedBy ||
		got.Verification != job.VerificationPending {
		t.Fatalf("merge after the state version = %s, %+v, %q; want succeeded, closed by %s, pending",
			got.State, got.Closed, got.Verification, closedBy)
	}

	if !followUp(t, h, j.ID, job.KindVerify) {
		t.Fatalf("the late close started no %s operation", job.KindVerify)
	}

	verify(t, h, f, j.ID)
}

func testMergeBeforeOldVersion(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := deliver(t, h, f, f.unit, job.DeliveryReviewRequest)

	writeState(t, h, f, true)

	// The merge is after the last final version of the unit.
	merged := change(&j, job.EventMerged)
	merged.MergedAt = finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()).At.Add(time.Second)

	if got := apply(t, h, f, merged); got.State != job.StateAwaitingState || got.Closed != nil {
		t.Fatalf("merge after the last final version = %s, %+v; want awaiting-state, not closed", got.State,
			got.Closed)
	}
}

func testLateTrailer(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationApply, "")
	j.Spec.Executor, j.Spec.Steps, j.Spec.Pool, j.Spec.Profile = job.ExecutorWriteBack, nil, "", ""
	j.Spec.Delivery = job.DeliveryDirectPush
	j.Spec.Edits = []job.FileEdit{{Path: "network.tfvars.json", Keys: map[string][]byte{"size": []byte(`"large"`)}}}
	j.Phases = j.Phases[:1]
	j.Phases[0].Kind = job.PhaseDeliver

	stored := create(t, h, f, j, dispatchOf(j))
	first := claimOp(t, h, "instance-a", job.KindWriteBack, longRenew)
	stored = claimed(t, h, f, &stored, "instance-a")

	// The commit lands, and instance-a stops before it records it. The
	// team's CI applies the commit and writes the state.
	closedBy := writeState(t, h, f, true)
	committedAt := finalVersionOf(t, h, f.fence.Namespace, f.unit.UnitID()).At

	shortenOp(t, h, first.Claim)
	claimAfterExpiry(t, h, "instance-b", job.KindWriteBack)
	stored = claimed(t, h, f, &stored, "instance-b")

	if !job.NeedsCommitLookup(&stored.Phases[0]) {
		t.Fatalf("takeover with %d claims needs no commit lookup; want one", stored.Phases[0].Claims)
	}

	// The takeover finds the commit by its trailer, and records it with the
	// time that the forge gives.
	found := change(&stored, job.EventDelivered)
	found.Delivery = &job.DeliveryResult{
		Delivery: job.DeliveryDirectPush, Commit: commitDef, Branch: mainRef, ReviewRequest: "",
		MergedAt: time.Time{}, DeliveredAt: committedAt,
	}

	got := apply(t, h, f, found)
	if got.State != job.StateSucceeded || got.Closed == nil || got.Closed.VersionID != closedBy {
		t.Fatalf("late delivery = %s, %+v; want succeeded, closed by %s", got.State, got.Closed, closedBy)
	}

	if !followUp(t, h, j.ID, job.KindVerify) {
		t.Fatalf("the late close started no %s operation", job.KindVerify)
	}
}

func testPlanClaimRecordsVersion(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := writeState(t, h, f, false)

	j := newJob(f.unit, job.OperationApply, "")
	stored := create(t, h, f, j, dispatchOf(j))

	claimedOp := claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), longRenew)
	stored = claimed(t, h, f, &stored, "runner-1")

	if stored.Phases[0].StateVersion != first {
		t.Fatalf("claim of the plan phase recorded %q, want %s", stored.Phases[0].StateVersion, first)
	}

	// A state write lands while the claim is lost. The takeover plans again
	// on the new version.
	second := writeState(t, h, f, false)
	shortenOp(t, h, claimedOp.Claim)
	claimAfterExpiry(t, h, "runner-2", job.RunnerKind(j.Spec.Pool))

	stored = claimed(t, h, f, &stored, "runner-2")
	if stored.Phases[0].StateVersion != second {
		t.Fatalf("takeover of the plan phase recorded %q, want %s", stored.Phases[0].StateVersion, second)
	}
}

func testCancelClaimExpires(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)

	claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), shortLease)
	j = claimed(t, h, f, &j, "runner-1")
	j = apply(t, h, f, change(&j, job.EventCancel))

	// runner-1 stops with no Finish, and its claim expires.
	waitEnded(t, h, job.RunnerKind(j.Spec.Pool), durable.ID(j.Phases[0].Operation))

	o := onlyOrphan(t, h, j.ID)
	if got := reconcile(t, h, f, &o); got.State != job.StateCanceled {
		t.Fatalf("reconcile of a canceling job with no change = %s, want canceled", got.State)
	}
}

func testRunningOperationFails(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)

	op := claimOp(t, h, "runner-1", job.RunnerKind(j.Spec.Pool), time.Minute)
	j = claimed(t, h, f, &j, "runner-1")

	err := h.Durable.Fail(t.Context(), op.Claim, durable.Failure{
		Message: "the limit of expired claims", Final: true, RetryAfter: 0,
	})
	if err != nil {
		t.Fatalf("durable Fail = %v", err)
	}

	o := onlyOrphan(t, h, j.ID)
	if got := reconcile(t, h, f, &o); got.State != job.StateFailed || got.Reason == "" {
		t.Fatalf("reconcile of a running job = %s, %q; want failed with a reason", got.State, got.Reason)
	}
}

// onlyOrphan returns the orphan of the job id, and fails t when Orphans
// returns another job or an operation that it calls missing.
func onlyOrphan(t *testing.T, h Harness, id job.ID) job.Orphan {
	t.Helper()

	orphans, _, err := h.Jobs.Orphans(t.Context(), orphanPage)
	if err != nil || len(orphans) != 1 || orphans[0].Job.ID != id || orphans[0].Missing {
		t.Fatalf("Orphans = %v, %v; want only %s with an ended operation", orphanIDs(orphans), err, id)
	}

	return orphans[0]
}

// waitEnded claims the kind k until the operation id ends, as the claim
// pass of each instance does after the claim of a canceled operation
// expires.
func waitEnded(t *testing.T, h Harness, k durable.Kind, id durable.ID) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), expiryWait)
	defer cancel()

	tick := time.NewTicker(pollEvery)
	defer tick.Stop()

	for {
		_, err := h.Durable.Claim(ctx, durable.ClaimRequest{
			Owner: "runner-2", Kinds: []durable.Kind{k}, MaxVersions: nil, Lease: time.Minute,
			TakeCanceled: false,
		})
		if err != nil && !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("durable Claim = %v", err)
		}

		op, err := h.Durable.Get(ctx, id)
		if err != nil {
			t.Fatalf("durable Get = %v", err)
		}

		if op.State.Terminal() {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("operation %s did not end within %v", id, expiryWait)
		case <-tick.C:
		}
	}
}

func testCreateWaits(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	runApplyPhase(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "")
	if _, _, err := h.Jobs.Create(t.Context(), *j, dispatchOf(j), f.fence, nil); !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("Create with a dispatch on a busy unit = %v, want ErrUnitBusy", err)
	}

	// The service then creates the job with no dispatch, queued behind the
	// busy unit.
	j.Wait = job.WaitUnitBusy

	stored := create(t, h, f, j, nil)
	if stored.State != job.StateQueued || stored.Wait != job.WaitUnitBusy || stored.Phases[0].Operation != "" {
		t.Fatalf("Create with no dispatch = %s, wait %q, operation %q; want queued, unit-busy, no operation",
			stored.State, stored.Wait, stored.Phases[0].Operation)
	}

	got, err := h.Jobs.Get(t.Context(), stored.ID)
	if err != nil || got.Wait != job.WaitUnitBusy {
		t.Fatalf("Get = wait %q, %v; want unit-busy", got.Wait, err)
	}
}

func testConfirmationStored(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	none := job.Decision{Allow: true, Reason: "", Gates: nil, PolicyVersion: ""}

	j := newJob(f.unit, job.OperationRefresh, "")
	j.StartedBy, j.Agent, j.AgentMode = "al", "dusty", job.AgentModeApproveFirst

	kinds := []job.PhaseKind{job.PhaseApply}
	for i, gates := range job.WithConfirmations(kinds, job.PolicyGates(kinds, none)) {
		j.Phases[i].Gates = gates
	}

	stored := create(t, h, f, j, nil)
	if len(stored.Phases[0].Gates) != 1 || stored.Phases[0].Gates[0].Type != job.GateConfirmation ||
		stored.AgentMode != job.AgentModeApproveFirst {
		t.Fatalf("Create = gates %+v, mode %q; want one confirmation gate in approve-first", stored.Phases[0].Gates,
			stored.AgentMode)
	}

	stored = apply(t, h, f, change(&stored, job.EventNeedApproval))
	now := time.Now().UTC()

	confirm := change(&stored, job.EventApproved)
	confirm.Approval = &job.Approval{
		Gate: job.GateConfirmation, Subject: "al", Agent: "", Verdict: job.VerdictApprove, Comment: "",
		Plan: nil, At: now, Auth: job.ConfirmAuthMFA, AuthAt: now.Add(-time.Minute),
	}

	got := apply(t, h, f, confirm)
	if got.State != job.StateQueued || len(got.Phases[0].Approvals) != 1 {
		t.Fatalf("confirmation = %s, %d approvals; want queued with one", got.State, len(got.Phases[0].Approvals))
	}

	a := got.Phases[0].Approvals[0]
	if a.Gate != job.GateConfirmation || a.Auth != job.ConfirmAuthMFA || !a.AuthAt.Equal(confirm.Approval.AuthAt) {
		t.Fatalf("stored confirmation = gate %q, auth %q at %v; want a confirmation with MFA at %v", a.Gate,
			a.Auth, a.AuthAt, confirm.Approval.AuthAt)
	}

	policy := gate.ApproverPolicy{Production: false, IsDev: nil}

	s := job.GatesStatus(got.Phases[0].Gates, got.Phases[0].Approvals, got.StartedBy, policy)
	if s != job.GateOpen {
		t.Fatalf("GatesStatus of the stored job = %s, want open", s)
	}
}

func testCreateLocked(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	lockUnit(t, h, f)

	for _, op := range []job.Operation{job.OperationRefresh, job.OperationPlan} {
		j := newJob(f.unit, op, "")
		if _, _, err := h.Jobs.Create(t.Context(), *j, dispatchOf(j), f.fence, nil); !errors.Is(err, job.ErrLocked) {
			t.Fatalf("Create of %s with a dispatch on a locked unit = %v, want ErrLocked", op, err)
		}

		// The service then creates the job with no dispatch, queued with the
		// reason locked.
		j.Wait = job.WaitLocked

		stored := create(t, h, f, j, nil)
		if stored.State != job.StateQueued || stored.Wait != job.WaitLocked || stored.Phases[0].Operation != "" {
			t.Fatalf("Create of %s with no dispatch = %s, wait %q, operation %q; want queued, locked, no operation",
				op, stored.State, stored.Wait, stored.Phases[0].Operation)
		}
	}
}
