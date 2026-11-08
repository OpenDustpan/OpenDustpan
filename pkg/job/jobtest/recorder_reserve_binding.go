package jobtest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// reserveBindingCases returns the cases of the binding of a plan reserve to
// the phase and the claim that made it.
func reserveBindingCases() []recorderCase {
	return []recorderCase{
		{"plans: a takeover claim cannot fill the reserve of the lapsed claim", testReserveTakeover},
		{"plans: the reserving claim fills its reserve", testReserveFill},
	}
}

// reservePlan reserves a plan of a under its claim and fails t on an error.
func reservePlan(t *testing.T, h RecorderHarness, a *job.Assignment) job.PlanRef {
	t.Helper()

	p, err := h.Records.ReservePlan(t.Context(), a.Claim, newPlanFor(t, h, a, recordingKey(nil)))
	if err != nil {
		t.Fatalf("ReservePlan = %v", err)
	}

	return p
}

// testReserveTakeover proves that a later claim of the same job cannot fill
// the reserve of a lapsed claim: the reserve names the phase and the claim
// that made it. The fill is ErrConflict, and the reserve stays empty.
func testReserveTakeover(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	first := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	reserved := reservePlan(t, h, first)

	lapse(t, h.Harness, first)

	second, err := claimAgainOf(t, h.Harness, runnerTwo, runnerKind(&j))
	if err != nil {
		t.Fatalf("takeover = %v", err)
	}

	hostile := reserved
	hostile.Parts = []job.PlanPart{hostilePart(job.OutputPlanDisplay)}

	if _, err = h.Records.PutPlan(t.Context(), second.Claim, hostile); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("fill by the takeover claim = %v, want ErrConflict: the second claim is live, not ErrClaimLost", err)
	}

	// The refused fill changed nothing: the row is still a reserve, and the
	// phase has no plan.
	if _, readErr := h.Records.Plan(t.Context(), f.fence.Namespace, reserved.ID); !errors.Is(readErr, job.ErrNotFound) {
		t.Fatalf("Plan after the refused fill = %v, want ErrNotFound: it still has no part", readErr)
	}

	if got := jobOf(t, h.Harness, first.Claim.Job); got.Phases[first.Claim.Phase].Plan != nil {
		t.Fatalf("plan of the phase after the refused fill = %+v, want none", got.Phases[first.Claim.Phase].Plan)
	}
}

// testReserveFill proves that the claim that made a reserve fills it.
func testReserveFill(t *testing.T, h RecorderHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	j := dispatched(t, h.Harness, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claimOf(t, h.Harness, runnerOne, longRenew, runnerKind(&j))

	reserved := reservePlan(t, h, a)

	filled := reserved
	filled.Parts = []job.PlanPart{hostilePart(job.OutputPlanDisplay)}

	out, err := h.Records.PutPlan(t.Context(), a.Claim, filled)
	if err != nil {
		t.Fatalf("PutPlan under the reserving claim = %v", err)
	}

	if out.ID != reserved.ID || len(out.Parts) != 1 {
		t.Fatalf("PutPlan = %+v, want the plan %s with one part", out, reserved.ID)
	}

	rec, err := h.Records.Plan(t.Context(), f.fence.Namespace, reserved.ID)
	if err != nil || rec.Plan == nil || len(rec.Plan.Parts) != 1 {
		t.Fatalf("Plan = %+v, %v; want the filled plan", rec.Plan, err)
	}

	if got := jobOf(t, h.Harness, a.Claim.Job); got.Phases[a.Claim.Phase].Plan == nil {
		t.Fatal("the phase has no plan after the fill")
	}
}
