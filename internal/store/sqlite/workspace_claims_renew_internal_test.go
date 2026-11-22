package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
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

// TestRenewStatementHoldsItsRules sends hostile parameters straight to the
// renew statement, below every check of the store, the registry and the
// handler: another principal, another workspace, an unknown claim and a lease
// that ended each match no row and change nothing.
func TestRenewStatementHoldsItsRules(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	fence, ws := workspacetest.SeedClaimWorkspace(t, s)

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: s, Versions: s, Deleted: s, Manual: nil, Store: s, Now: nil, Lease: 0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	entry := manualEntry(fence, manuallock.NamespaceTarget(fence.Namespace))
	entry.Action = string(identity.ActionUnitClaimRegister)

	claim, err := registry.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false, WorkspaceID: ws, Base: "sv-acme", Addresses: []string{"aws_instance.a"}, Replace: false,
		Principal: "ada", JobID: "", Trigger: "", Audit: entry,
	}, fence)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	ok := db.RenewWorkspaceClaimParams{
		LeaseEndsAtMs: claim.LeaseEndsAt.Add(time.Hour).UnixMilli(), NowMs: claim.StartedAt.UnixMilli(), ID: claim.ID,
		WorkspaceID: ws.String(), Principal: "ada",
	}

	for name, mutate := range map[string]func(p *db.RenewWorkspaceClaimParams){
		"another principal": func(p *db.RenewWorkspaceClaimParams) { p.Principal = "bob" },
		"another workspace": func(p *db.RenewWorkspaceClaimParams) { p.WorkspaceID = ws.String() + "-2" },
		"unknown claim":     func(p *db.RenewWorkspaceClaimParams) { p.ID = "clm-unknown" },
		"lease that ended":  func(p *db.RenewWorkspaceClaimParams) { p.NowMs = claim.LeaseEndsAt.UnixMilli() },
	} {
		p := ok
		mutate(&p)

		if _, renewErr := s.queries.RenewWorkspaceClaim(t.Context(), p); !errors.Is(renewErr, sql.ErrNoRows) {
			t.Errorf("%s: statement = %v, want no rows", name, renewErr)
		}
	}

	got, err := registry.List(t.Context(), ws)
	if err != nil || len(got) != 1 || !got[0].LeaseEndsAt.Equal(claim.LeaseEndsAt) || !got[0].RenewedAt.IsZero() {
		t.Fatalf("claims after the hostile renewals = %+v, %v, want the claim as registered", got, err)
	}

	if _, renewErr := s.queries.RenewWorkspaceClaim(t.Context(), ok); renewErr != nil {
		t.Fatalf("the statement with the rules met = %v, want a row", renewErr)
	}
}

// TestClaimTableRefusesALeaseThatDisagreesWithItsJob sends rows that break the
// CHECK of the table straight to the database: a claim of a job with a lease,
// and a claim with no job and no lease. A good row of each kind goes in.
func TestClaimTableRefusesALeaseThatDisagreesWithItsJob(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	_, ws := workspacetest.SeedClaimWorkspace(t, s)

	const insert = `INSERT INTO workspace_claims
(id, workspace_id, seq, base, addresses, principal, job_id, trigger_name, state, started_at_ms, ended_at_ms,
 lease_ends_at_ms, renewed_at_ms)
VALUES (?, ?, ?, 'sv-acme', '["a"]', 'ada', ?, '', 'accepted', 1, 0, ?, 0)`

	for i, c := range []struct {
		name  string
		job   string
		lease int64
		ok    bool
	}{
		{"claim of a job with a lease", "job-1", 90, false},
		{"claim with no job and no lease", "", 0, false},
		{"claim of a job", "job-1", 0, true},
		{"claim with no job and a lease", "", 90, true},
	} {
		_, err := s.conn.ExecContext(t.Context(), insert, "clm-"+c.name, ws.String(), i+1, c.job, c.lease)
		if (err == nil) != c.ok {
			t.Errorf("%s: insert = %v, want ok %v", c.name, err, c.ok)
		}
	}
}
