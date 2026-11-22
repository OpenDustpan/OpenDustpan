package workspace_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
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

// foreignRenewals returns the claims that a store must never hand back for the
// renewal of claim clm-1 of workspace ws-1 by ada: a claim of another
// principal, a claim of a job with no lease, a claim of another workspace and
// another claim.
func foreignRenewals(now time.Time) map[string]workspace.Claim {
	own := workspace.Claim{
		ID: "clm-1", WorkspaceID: "ws-1", Base: "sv-1", Addresses: []string{"a"}, Principal: "ada", JobID: "",
		Trigger: "", State: workspace.ClaimAccepted, StartedAt: now, EndedAt: time.Time{},
		LeaseEndsAt: now.Add(time.Minute), RenewedAt: now,
	}

	other, job, elsewhere, another := own, own, own, own
	other.Principal = "bob"
	job.JobID, job.LeaseEndsAt = "job-1", time.Time{}
	elsewhere.WorkspaceID = "ws-2"
	another.ID = "clm-2"

	return map[string]workspace.Claim{
		"claim of the principal": own, "claim of another principal": other, "claim of a job": job,
		"claim of another workspace": elsewhere, "another claim": another,
	}
}

// TestCheckRenewedRefusesForeignRows sends each foreign row straight to the
// check that each layer applies to the row of the layer below.
func TestCheckRenewedRefusesForeignRows(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	renewal := workspace.ClaimRenewal{
		Workspace: "ws-1", ClaimID: "clm-1", Principal: "ada", Now: now, Lease: workspace.ClaimLease,
	}

	for name, claim := range foreignRenewals(now) {
		err := workspace.CheckRenewed(&claim, renewal)
		if name == "claim of the principal" {
			if err != nil {
				t.Errorf("%s: CheckRenewed = %v, want nil", name, err)
			}

			continue
		}

		if !errors.Is(err, workspace.ErrClaimNotFound) {
			t.Errorf("%s: CheckRenewed = %v, want ErrClaimNotFound", name, err)
		}
	}
}

// noWorkspaces and noVersions are the read sides of a registry that Renew never
// reads.
type noWorkspaces struct{}

func (noWorkspaces) GetWorkspaceByID(context.Context, workspace.ID) (workspace.Workspace, error) {
	var none workspace.Workspace

	return none, workspace.ErrNotFound
}

type noVersions struct{}

func (noVersions) GetWorkspaceStateVersion(
	context.Context, namespace.Name, workspace.ID, workspace.StateVersionID,
) (workspace.StateVersion, error) {
	var none workspace.StateVersion

	return none, workspace.ErrStateVersionNotFound
}

// foreignRenewStore is a claim store whose renewal hands back a fixed claim, as
// a store that failed to check its rules would.
type foreignRenewStore struct {
	workspace.ClaimStore

	claim workspace.Claim
}

func (f foreignRenewStore) RenewClaim(
	context.Context,
	namespace.Fence,
	workspace.ClaimRenewal,
) (workspace.Claim, error) {
	return f.claim, nil
}

// TestRegistryRenewRefusesForeignRowsOfTheStore sends each foreign claim to the
// registry through a store that returns it, as if every layer below failed.
func TestRegistryRenewRefusesForeignRowsOfTheStore(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_700_000_000_000).UTC()

	for name, claim := range foreignRenewals(now) {
		registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
			Workspaces: noWorkspaces{},
			Versions:   noVersions{},
			Deleted:    nil,
			Manual:     nil,
			Now:        func() time.Time { return now },
			Store:      foreignRenewStore{ClaimStore: nil, claim: claim},
			Lease:      0,
		})
		if err != nil {
			t.Fatalf("registry: %v", err)
		}

		_, renewErr := registry.Renew(
			t.Context(),
			"ws-1",
			"clm-1",
			"ada",
			namespace.Fence{Namespace: "acme", FailoverVersion: 1},
		)
		if name == "claim of the principal" {
			if renewErr != nil {
				t.Errorf("%s: Renew = %v, want nil", name, renewErr)
			}

			continue
		}

		if !errors.Is(renewErr, workspace.ErrClaimNotFound) {
			t.Errorf("%s: Renew = %v, want ErrClaimNotFound", name, renewErr)
		}
	}
}
