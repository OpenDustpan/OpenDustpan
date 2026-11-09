package jobtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

// liveLease is the lease of a claim that is still live when the sweep runs: it
// passes the time of the sweep, which is sweepAfter ahead.
const liveLease = sweepAfter + 2*time.Hour

// errOperationRead is the failure of a read of an operation in the cases.
var errOperationRead = errors.New("read of the operation failed")

// sweepPlanPartCases returns the cases of the sweep of the parts of a plan
// that need no state write.
func sweepPlanPartCases() []artifactSweepCase {
	return []artifactSweepCase{
		{"sweep: the parts of a plan whose claim was lost go", testSweepAbandonedPlanParts},
		{
			"sweep: the parts of a live claim stay, and a failed read of the claim deletes nothing",
			testSweepLiveClaimParts,
		},
	}
}

// stateSweepPlanPartCases returns the cases of the sweep of the parts of a
// plan that need a state write.
func stateSweepPlanPartCases() []artifactSweepCase {
	return []artifactSweepCase{
		{"sweep: a part that the committed plan does not name goes", testSweepUnnamedPartAfterCommit},
	}
}

// planPartRel returns the full key of the part kind of the plan of the claim of
// a.
func planPartRel(t *testing.T, h ArtifactSweepHarness, a *job.Assignment, kind job.OutputKind) string {
	t.Helper()

	return namespacePrefix(t, h, a) + planKey(t, a.Claim.Job, job.PlanIDOf(a.Claim), kind)
}

// wantPartNotFound fails t unless the reader finds no part kind in the plan.
func wantPartNotFound(t *testing.T, h ArtifactSweepHarness, a *job.Assignment, plan string, kind job.OutputKind) {
	t.Helper()

	_, err := h.Reader.OpenPlanPart(t.Context(), a.Claim.Fence.Namespace, plan, kind)
	if !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("OpenPlanPart(%s) = %v, want ErrNotFound", kind, err)
	}
}

func testSweepAbandonedPlanParts(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	a := openPlan(t, h.RecorderHarness, f, "")

	putPart(t, h.RecorderHarness, a, job.OutputPlanDisplay, displayJSON)
	putPart(t, h.RecorderHarness, a, job.OutputPlanApply, applyBinary)

	display, apply := planPartRel(t, h, a, job.OutputPlanDisplay), planPartRel(t, h, a, job.OutputPlanApply)
	j := sweptJob(t, h, a)

	// The claim is live and the parts are young: nothing goes.
	if res, err := newSweeper(t, h, newGuardSwitch(), 0).SweepJob(t.Context(), &j); err != nil || res.Deleted != 0 {
		t.Fatalf("young sweep = %+v, %v; want nothing deleted", res, err)
	}

	// The claim lapses and no one commits. The parts get old, and then they go.
	lapse(t, h.Harness, a)

	res, err := newSweeper(t, h, newGuardSwitch(), sweepAfter).SweepJob(t.Context(), &j)
	if err != nil || res.Deleted != planBlobs {
		t.Fatalf("old sweep = %+v, %v; want the %d parts deleted", res, err, planBlobs)
	}

	requireBlobs(t, h, nil, []string{display, apply})

	// The parts of a plan that never committed are not readable as a plan.
	wantPartNotFound(t, h, a, job.PlanIDOf(a.Claim), job.OutputPlanApply)
}

func testSweepUnnamedPartAfterCommit(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	writeState(t, h.Harness, f, false)

	a := openPlan(t, h.RecorderHarness, f, "")
	putStandardParts(t, h.RecorderHarness, a)

	plan, err := planRecorder(t, h.RecorderHarness).CommitPlan(t.Context(), a.Claim, standardClaim()[:1])
	if err != nil {
		t.Fatalf("CommitPlan = %v", err)
	}

	display, apply := planPartRel(t, h, a, job.OutputPlanDisplay), planPartRel(t, h, a, job.OutputPlanApply)
	j := sweptJob(t, h, a)

	res, err := newSweeper(t, h, newGuardSwitch(), sweepAfter).SweepJob(t.Context(), &j)
	if err != nil || res.Deleted != 1 {
		t.Fatalf("sweep = %+v, %v; want only the part that the plan does not name deleted", res, err)
	}

	requireBlobs(t, h, []string{display}, []string{apply})

	// The committed part still reads, and the part that was left out does not.
	assertPart(t, h.RecorderHarness, plan.ID, job.OutputPlanDisplay, displayJSON)
	wantPartNotFound(t, h, a, plan.ID, job.OutputPlanApply)
}

// failingOperations is a job.PhaseOperations whose read always fails.
type failingOperations struct{ err error }

func (f failingOperations) Get(context.Context, durable.ID) (durable.Operation, error) {
	return durable.Operation{
		ID: "", Key: "", Kind: "", Version: 0, Input: nil, State: "", Steps: nil, Attempt: 0, Failures: 0,
		Expiries: 0, LastError: "", Claim: durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}},
		CancelRequested: false, NotBefore: time.Time{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}, f.err
}

func testSweepLiveClaimParts(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})

	// The lease passes the time of the sweep, so the claim is live then.
	a := claimOf(t, h.Harness, runnerOne, liveLease, runnerKind(&j))

	putPart(t, h.RecorderHarness, a, job.OutputPlanDisplay, displayJSON)

	part := planPartRel(t, h, a, job.OutputPlanDisplay)
	swept := sweptJob(t, h, a)

	// An old part of the live claim stays: the claim can still commit it.
	res, err := newSweeper(t, h, newGuardSwitch(), sweepAfter).SweepJob(t.Context(), &swept)
	if err != nil || res.Deleted != 0 {
		t.Fatalf("sweep with a live claim = %+v, %v; want nothing deleted", res, err)
	}

	requireBlobs(t, h, []string{part}, nil)

	// A failed read of the claim deletes nothing, also after the claim ended.
	failing := failingOperations{err: errOperationRead}

	sweeper := newSweeperOver(t, h, sweepAfter, failing)

	_, err = sweeper.SweepJob(t.Context(), &swept)
	if !errors.Is(err, errOperationRead) {
		t.Fatalf("sweep with a failed read of the claim = %v, want its failure", err)
	}

	lapse(t, h.Harness, a)

	_, err = sweeper.SweepJob(t.Context(), &swept)
	if !errors.Is(err, errOperationRead) {
		t.Fatalf("sweep with a failed read after the lapse = %v, want its failure", err)
	}

	requireBlobs(t, h, []string{part}, nil)
}

// newSweeperOver is newSweeper with the operation reader ops and no guard hold.
func newSweeperOver(
	t *testing.T, h ArtifactSweepHarness, ahead time.Duration, ops job.PhaseOperations,
) *job.ArtifactSweeper {
	t.Helper()

	artifacts, err := job.NewArtifactBlobs(h.Blobs, noRefusals{})
	if err != nil {
		t.Fatalf("artifact blobs: %v", err)
	}

	s, err := job.NewArtifactSweeper(job.ArtifactSweeperConfig{
		Jobs: h.Jobs, Names: h.Names, Blobs: artifacts, Namespaces: h.Namespaces, Guard: newGuardSwitch(),
		Clock: aheadClock{inner: h.Clock, ahead: ahead}, Operations: ops, Audit: h.Audit, Logger: nil,
		Now: func() time.Time { return time.Now().Add(ahead) },
	})
	if err != nil {
		t.Fatalf("artifact sweeper: %v", err)
	}

	return s
}
