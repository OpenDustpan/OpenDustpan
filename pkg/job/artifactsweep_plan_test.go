package job_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// planOperation is the operation of the plan phase of the cases.
const planOperation = durable.ID("op-plan")

// sweepFixedNow is the time of the database and of the instance in the cases.
// It is far after the time of any blob that a case writes, so every such blob is
// older than the grace period, and no case reads a wall clock.
func sweepFixedNow() time.Time {
	return time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
}

// errOperationRead is the failure of a read of an operation in the cases.
var errOperationRead = errors.New("read of the operation failed")

// planClaim returns the claim of the plan phase of job-1 with the token.
func planClaim(token int64) job.PhaseClaim {
	return job.PhaseClaim{
		Job: sweepJob, Phase: 0,
		Claim: durable.Claim{Operation: planOperation, Owner: "", Token: token, ExpiresAt: time.Time{}},
		Fence: namespace.Fence{Namespace: "", FailoverVersion: 0},
	}
}

// planPhase returns the plan phase of the cases, with the operation planOperation.
func planPhase() job.Phase {
	return job.Phase{
		Kind: job.PhasePlan, Operation: string(planOperation), Claims: 0, Executor: "", Steps: nil, StateVersion: "",
		Plan: nil, Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}
}

// planRel returns the key of the part kind of the plan id, in the directory of
// the job of the cases.
func planRel(t *testing.T, id string, kind job.OutputKind) string {
	t.Helper()

	key, err := job.PlanPartKey(sweepJob, id, kind)
	if err != nil {
		t.Fatalf("PlanPartKey = %v", err)
	}

	return key.String()
}

// reservedPlans names the plans ids as reserved plans: rows with no part.
func reservedPlans(ids ...string) job.ArtifactNames {
	return job.ArtifactNames{Plans: ids, Parts: nil, ChunkKeys: nil}
}

// planRig is a sweep of job-1 with a plan phase, the operation of the phase,
// and one blob for each kind of each plan of ids.
func planRig(
	t *testing.T, names job.ArtifactNames, state job.State, ops job.PhaseOperations, ids ...string,
) *sweepRig {
	t.Helper()

	r := newSweepRig(t, sweepSetup{
		names: sweepNames{names: names, err: nil}, ahead: 0, instance: 0, wrap: nil, jobs: nil,
		ops: ops, at: sweepFixedNow(),
	})

	r.jobs.j.State = state
	r.jobs.j.Phases = []job.Phase{planPhase()}

	for _, id := range ids {
		for _, kind := range []job.OutputKind{job.OutputPlanDisplay, job.OutputPlanApply} {
			rel := sweepPrefix() + planRel(t, id, kind)
			if err := r.store.Put(t.Context(), blob.Key(rel), strings.NewReader("x")); err != nil {
				t.Fatalf("Put = %v", err)
			}
		}
	}

	return r
}

// runningOp returns a running operation whose claim has the token and expires
// after the time of the sweep by lease.
func runningOp(token int64, lease time.Duration) durable.Operation {
	op := noOperation()
	op.State = durable.StateRunning
	op.Claim = durable.Claim{
		Operation: planOperation, Owner: "", Token: token, ExpiresAt: sweepFixedNow().Add(lease),
	}

	return op
}

// partStored reports whether the part kind of the plan id is stored.
func partStored(t *testing.T, r *sweepRig, id string, kind job.OutputKind) bool {
	t.Helper()

	_, err := r.store.Get(t.Context(), blob.Key(sweepPrefix()+planRel(t, id, kind)))
	if err != nil && !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get = %v", err)
	}

	return err == nil
}

// wantParts fails t unless the display part and the apply part of the plan id
// are stored as display and apply say.
func wantParts(t *testing.T, r *sweepRig, id string, want storedPair) {
	t.Helper()

	if got := partStored(t, r, id, job.OutputPlanDisplay); got != want.display {
		t.Errorf("display part of %s stored = %t, want %t", id, got, want.display)
	}

	if got := partStored(t, r, id, job.OutputPlanApply); got != want.apply {
		t.Errorf("apply part of %s stored = %t, want %t", id, got, want.apply)
	}
}

// storedPair says which of the two parts of a plan are stored.
type storedPair struct{ display, apply bool }

// TestSweepReservedPlans shows what happens to the parts of a plan that no
// manifest names: the plan of the live claim keeps them, because the claim can
// still commit, and the plan of any other claim loses them.
func TestSweepReservedPlans(t *testing.T) {
	t.Parallel()

	live, dead := job.PlanIDOf(planClaim(2)), job.PlanIDOf(planClaim(1))

	tests := []struct {
		name     string
		state    job.State
		op       durable.Operation
		wantLive bool
	}{
		{"a live claim keeps its plan and loses an earlier one", job.StateRunning, runningOp(2, time.Hour), true},
		{"a lease that passed", job.StateRunning, runningOp(2, -time.Minute), false},
		{"an operation that does not run", job.StateRunning, func() durable.Operation {
			op := runningOp(2, time.Hour)
			op.State = durable.StateSucceeded

			return op
		}(), false},
		{"a job that ended", job.StateFailed, runningOp(2, time.Hour), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ops := sweepOps{id: planOperation, op: tc.op, err: nil}
			r := planRig(t, reservedPlans(live, dead), tc.state, ops, live, dead)

			if _, err := r.s.SweepJob(t.Context(), &r.jobs.j); err != nil {
				t.Fatalf("SweepJob = %v", err)
			}

			wantParts(t, r, live, storedPair{display: tc.wantLive, apply: tc.wantLive})
			wantParts(t, r, dead, storedPair{display: false, apply: false})
		})
	}
}

// TestSweepPartsThatTheManifestDoesNotName shows that a stored plan keeps the
// parts of its manifest and loses any other part, also while its claim is live.
func TestSweepPartsThatTheManifestDoesNotName(t *testing.T) {
	t.Parallel()

	id := job.PlanIDOf(planClaim(1))
	names := job.ArtifactNames{
		Plans: []string{id}, Parts: map[string][]job.OutputKind{id: {job.OutputPlanDisplay}}, ChunkKeys: nil,
	}
	ops := sweepOps{id: planOperation, op: runningOp(1, time.Hour), err: nil}
	r := planRig(t, names, job.StateRunning, ops, id)

	if _, err := r.s.SweepJob(t.Context(), &r.jobs.j); err != nil {
		t.Fatalf("SweepJob = %v", err)
	}

	// The named display part stays and the apply part that the manifest does
	// not name goes.
	wantParts(t, r, id, storedPair{display: true, apply: false})
}

// TestSweepKeepsPlansWhenItCannotTellTheClaim is the hostile case of the sweep:
// a failed read of the operation never makes the sweep take a live claim for
// lost. It deletes nothing and fails.
func TestSweepKeepsPlansWhenItCannotTellTheClaim(t *testing.T) {
	t.Parallel()

	errOps := errOperationRead
	id := job.PlanIDOf(planClaim(1))
	ops := sweepOps{id: "", op: noOperation(), err: errOps}
	r := planRig(t, reservedPlans(id), job.StateRunning, ops, id)

	if _, err := r.s.SweepJob(t.Context(), &r.jobs.j); !errors.Is(err, errOps) {
		t.Fatalf("SweepJob = %v, want the failure of the read", err)
	}

	// A failed read deletes nothing.
	wantParts(t, r, id, storedPair{display: true, apply: true})
}
