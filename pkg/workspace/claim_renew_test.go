package workspace_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestCheckRenewableRefusesWhatCannotRenew sends each kind of claim straight to
// the rule of the renewal, as if the store and the handler failed to check:
// the claim of another principal, an ended claim, a claim with no lease and a
// claim whose lease ended. Only a live claim of the principal with a live
// lease passes.
func TestCheckRenewableRefusesWhatCannotRenew(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	live := workspace.Claim{
		ID: "clm-1", WorkspaceID: "ws-1", Base: "sv-1", Addresses: []string{"a"}, Principal: "ada", JobID: "",
		Trigger: "", State: workspace.ClaimAccepted, StartedAt: now, EndedAt: time.Time{},
		LeaseEndsAt: now.Add(time.Minute), RenewedAt: time.Time{},
	}
	renewal := workspace.ClaimRenewal{
		Workspace: "ws-1", ClaimID: "clm-1", Principal: "ada", Now: now, Lease: workspace.ClaimLease,
	}

	if err := workspace.CheckRenewable(&live, renewal); err != nil {
		t.Fatalf("a live claim of the principal = %v, want nil", err)
	}

	for _, c := range []struct {
		name   string
		mutate func(c *workspace.Claim)
		want   error
	}{
		{"another principal", func(c *workspace.Claim) { c.Principal = "bob" }, workspace.ErrClaimNotFound},
		{
			"ended claim with a live lease",
			func(c *workspace.Claim) { c.State, c.EndedAt = workspace.ClaimEnded, now },
			workspace.ErrClaimEnded,
		},
		{
			"claim of a job",
			func(c *workspace.Claim) { c.JobID, c.LeaseEndsAt = "job-1", time.Time{} },
			workspace.ErrClaimInvalid,
		},
		{"lease that ends now", func(c *workspace.Claim) { c.LeaseEndsAt = now }, workspace.ErrClaimEnded},
		{
			"lease that ended",
			func(c *workspace.Claim) { c.LeaseEndsAt = now.Add(-time.Second) },
			workspace.ErrClaimEnded,
		},
	} {
		claim := live
		c.mutate(&claim)

		if err := workspace.CheckRenewable(&claim, renewal); !errors.Is(err, c.want) {
			t.Errorf("%s: CheckRenewable = %v, want %v", c.name, err, c.want)
		}
	}
}
