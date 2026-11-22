package jobtest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// claimCases returns the cases of the apply claims of a job. They run only
// for a harness whose database also keeps the claims (Harness.Claims).
func claimCases() []suiteCase {
	return []suiteCase{
		{"a cancel of a queued job ends its claims in the same transaction", testClaimsEndOnCancel},
		{"the cancel of a damaged job ends its claims", testClaimsEndOnDamagedCancel},
		{"a job that rejects ends its claims, and a job that goes on keeps them", testClaimsEndOnFail},
	}
}

// registerJobClaim stores one accepted claim of jobID on the workspace of
// the unit, and returns its ID. The claims of other jobs stay as they are.
func registerJobClaim(t *testing.T, h Harness, f fixture, jobID job.ID, address string) string {
	t.Helper()

	wsID := workspace.ID(f.unit.UnitID())
	id := "clm-" + string(jobID)
	claim := workspace.Claim{
		ID: id, WorkspaceID: wsID, Base: "sv-1", Addresses: []string{address}, Principal: "al", JobID: string(jobID),
		Trigger: "", State: workspace.ClaimAccepted, StartedAt: time.Now().UTC().Truncate(time.Millisecond),
		EndedAt: time.Time{}, LeaseEndsAt: time.Time{}, RenewedAt: time.Time{},
	}
	e := entry(f.unit, identity.ActionUnitClaimRegister)
	e.ObjectID = id

	err := h.Claims.WithClaims(t.Context(), wsID, f.fence, func(tx workspace.ClaimTx) error {
		return tx.Insert(t.Context(), claim, e)
	})
	if err != nil {
		t.Fatalf("register the claim of %s: %v", jobID, err)
	}

	return id
}

// claimState returns the state of the claim id on the workspace of the unit.
func claimState(t *testing.T, h Harness, f fixture, id string) workspace.Claim {
	t.Helper()

	list, err := h.Claims.ListClaims(t.Context(), workspace.ID(f.unit.UnitID()))
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}

	var got workspace.Claim

	found := false

	for i := range list {
		if list[i].ID == id {
			got, found = list[i], true
		}
	}

	if !found {
		t.Fatalf("claim %s is not in the list %+v", id, list)
	}

	return got
}

// wantClaimEnded fails t unless the claim id is ended with an end time and
// the audit log holds one entry of its end, by the server, that names the
// claim and the job.
func wantClaimEnded(t *testing.T, h Harness, f fixture, id string, jobID job.ID) {
	t.Helper()

	got := claimState(t, h, f, id)
	if got.State != workspace.ClaimEnded || got.EndedAt.IsZero() {
		t.Fatalf("claim %s = %+v, want an ended claim with an end time", id, got)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	var ends []audit.Entry

	for i := range entries {
		if entries[i].ObjectID == id && entries[i].Subject == "@server" {
			ends = append(ends, entries[i])
		}
	}

	if len(ends) != 1 || ends[0].JobID != string(jobID) || ends[0].Reason == "" {
		t.Fatalf("entries of the end of claim %s = %+v, want one entry by @server with the job and a cause", id, ends)
	}
}

// wantClaimAccepted fails t unless the claim id is accepted.
func wantClaimAccepted(t *testing.T, h Harness, f fixture, id string) {
	t.Helper()

	if got := claimState(t, h, f, id); got.State != workspace.ClaimAccepted || !got.EndedAt.IsZero() {
		t.Fatalf("claim %s = %+v, want an accepted claim", id, got)
	}
}

func testClaimsEndOnCancel(t *testing.T, h Harness) {
	t.Helper()

	if h.Claims == nil {
		t.Skip("the harness keeps no claims")
	}

	f := setUp(t, h)
	mine := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	other := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	mineClaim := registerJobClaim(t, h, f, mine.ID, "aws_instance.a")
	otherClaim := registerJobClaim(t, h, f, other.ID, "aws_instance.b")

	apply(t, h, f, change(&mine, job.EventCancel))

	wantClaimEnded(t, h, f, mineClaim, mine.ID)
	wantClaimAccepted(t, h, f, otherClaim)
}

func testClaimsEndOnDamagedCancel(t *testing.T, h Harness) {
	t.Helper()

	if h.Claims == nil {
		t.Skip("the harness keeps no claims")
	}

	f := setUp(t, h)
	j := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	claim := registerJobClaim(t, h, f, j.ID, "aws_instance.a")

	if err := h.Domain.DamageJobBody(t.Context(), j.ID); err != nil {
		t.Fatalf("DamageJobBody: %v", err)
	}

	cancelEntry := []audit.Entry{entry(f.unit, job.ActionJobCancel)}

	if _, err := h.Jobs.CancelDamaged(t.Context(), j.ID, f.fence, cancelEntry); err != nil {
		t.Fatalf("CancelDamaged: %v", err)
	}

	wantClaimEnded(t, h, f, claim, j.ID)
}

func testClaimsEndOnFail(t *testing.T, h Harness) {
	t.Helper()

	if h.Claims == nil {
		t.Skip("the harness keeps no claims")
	}

	f := setUp(t, h)
	j := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	claim := registerJobClaim(t, h, f, j.ID, "aws_instance.a")

	// A change that keeps the job open keeps its claims.
	waiting := apply(t, h, f, change(&j, job.EventNeedApproval))
	if waiting.State.Terminal() {
		t.Fatalf("job after need-approval is %s, want an open state", waiting.State)
	}

	wantClaimAccepted(t, h, f, claim)

	// A terminal event of another kind than a cancel ends them too.
	rejected := change(&waiting, job.EventRejected)
	rejected.Reason = "rejected"

	if got := apply(t, h, f, rejected); !got.State.Terminal() {
		t.Fatalf("job after rejected is %s, want a terminal state", got.State)
	}

	wantClaimEnded(t, h, f, claim, j.ID)
}
