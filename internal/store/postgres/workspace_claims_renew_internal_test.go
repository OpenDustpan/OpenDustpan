package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// fixedRenewer is a layer below the store that returns one row for each
// renewal, as a statement that failed to hold its rules would.
type fixedRenewer struct {
	row db.RenewWorkspaceClaimRow
}

func (f fixedRenewer) RenewWorkspaceClaim(
	context.Context, db.RenewWorkspaceClaimParams,
) (db.RenewWorkspaceClaimRow, error) {
	return f.row, nil
}

// renewRow returns the row of claim clm-1 of workspace ws-1 for ada, a wrapper
// claim with a lease.
func renewRow() db.RenewWorkspaceClaimRow {
	return db.RenewWorkspaceClaimRow{
		ID: "clm-1", WorkspaceID: "ws-1", Base: "sv-1", Addresses: `["a"]`, Principal: "ada", JobID: "",
		TriggerName: "", State: "accepted", StartedAtMs: 1_700_000_000_000, EndedAtMs: 0,
		LeaseEndsAtMs: 1_700_000_090_000, RenewedAtMs: 1_700_000_000_000,
	}
}

// TestRenewClaimRowRefusesForeignRows sends the row of another principal, of a
// job claim with no lease, of another workspace and of another claim to the
// store layer straight, through a layer below that returns them.
func TestRenewClaimRowRefusesForeignRows(t *testing.T) {
	t.Parallel()

	renewal := workspace.ClaimRenewal{
		Workspace: "ws-1", ClaimID: "clm-1", Principal: "ada", Now: time.UnixMilli(1_700_000_000_000).UTC(),
		Lease: workspace.ClaimLease,
	}

	if _, err := renewClaimRow(t.Context(), fixedRenewer{row: renewRow()}, renewal); err != nil {
		t.Fatalf("the row of the principal = %v, want nil", err)
	}

	for name, mutate := range map[string]func(r *db.RenewWorkspaceClaimRow){
		"another principal": func(r *db.RenewWorkspaceClaimRow) { r.Principal = "bob" },
		"claim of a job":    func(r *db.RenewWorkspaceClaimRow) { r.JobID, r.LeaseEndsAtMs, r.RenewedAtMs = "job-1", 0, 0 },
		"another workspace": func(r *db.RenewWorkspaceClaimRow) { r.WorkspaceID = "ws-2" },
		"another claim":     func(r *db.RenewWorkspaceClaimRow) { r.ID = "clm-2" },
	} {
		row := renewRow()
		mutate(&row)

		_, err := renewClaimRow(t.Context(), fixedRenewer{row: row}, renewal)
		if !errors.Is(err, workspace.ErrClaimNotFound) {
			t.Errorf("%s: renewClaimRow = %v, want ErrClaimNotFound", name, err)
		}
	}
}
