package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// TestPortableStageKeepsHousekeepingRows proves that a staged restore holds
// the housekeeping principals and grants of the backup, not the seeded rows of
// the new baseline: one row for each purpose, no row twice, and a grant that
// was revoked before the export stays revoked after the stage.
func TestPortableStageKeepsHousekeepingRows(t *testing.T) {
	t.Parallel()

	s := openTestStore(t, filepath.Join(t.TempDir(), "dustpan.db"))
	ctx := t.Context()

	serverPurposes := housekeepingServerPurposes(t)
	revokedID := seededHousekeepingGrantID(t, s, serverPurposes[0].InstallationRole())

	const revokedAt = int64(1_700_000_000_000)

	_, execErr := s.conn.ExecContext(
		ctx,
		`UPDATE grants SET revoked_at_ms = ?, revoked_by = ? WHERE id = ?`,
		revokedAt, "alice", revokedID,
	)
	if execErr != nil {
		t.Fatalf("revoke the seeded grant: %v", execErr)
	}

	stageExport(t, s)

	staged, err := openStagedAccess(ctx, s.StagingPathForTest())
	if err != nil {
		t.Fatalf("open the staged file: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := staged.Close(); closeErr != nil {
			t.Errorf("close the staged file: %v", closeErr)
		}
	})

	checkStagedHousekeepingPrincipals(t, staged)
	checkStagedHousekeepingGrants(t, staged, serverPurposes, revokedID, revokedAt)
}

// housekeepingServerPurposes returns the purposes whose installation role
// holds a server action, so the install writes one grant on @server for each
// of them.
func housekeepingServerPurposes(t *testing.T) []principal.HousekeepingPurpose {
	t.Helper()

	var out []principal.HousekeepingPurpose

	for _, purpose := range principal.HousekeepingPurposes() {
		if !purpose.ActsOnInstallation() {
			continue
		}

		role, ok := access.BuiltInRole(purpose.InstallationRole())
		if !ok {
			t.Fatalf("purpose %q has no built-in role %q", purpose.Name(), purpose.InstallationRole())
		}

		for _, action := range role.Actions {
			if strings.HasPrefix(string(action), "server:") {
				out = append(out, purpose)

				break
			}
		}
	}

	if len(out) == 0 {
		t.Fatal("no purpose holds a server action, want the install grants")
	}

	return out
}

// seededHousekeepingGrantID returns the ID of the current seeded grant of role.
func seededHousekeepingGrantID(t *testing.T, s *Store, role string) string {
	t.Helper()

	var id string

	err := s.conn.QueryRowContext(
		t.Context(),
		`SELECT id FROM grants WHERE role = ? AND revoked_at_ms IS NULL`,
		role,
	).Scan(&id)
	if err != nil {
		t.Fatalf("read the seeded grant of role %q: %v", role, err)
	}

	return id
}

// checkStagedHousekeepingPrincipals checks that the staged file holds one
// builtin principal for each purpose, and no row twice.
func checkStagedHousekeepingPrincipals(t *testing.T, staged *sql.DB) {
	t.Helper()

	rows, err := staged.QueryContext(
		t.Context(),
		`SELECT id, subject FROM principals WHERE source = ?`,
		principal.SourceBuiltin,
	)
	if err != nil {
		t.Fatalf("read the staged principals: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close the staged principals: %v", closeErr)
		}
	}()

	subjects := map[string]int{}
	ids := map[string]int{}

	for rows.Next() {
		var id, subject string
		if scanErr := rows.Scan(&id, &subject); scanErr != nil {
			t.Fatalf("scan the staged principal: %v", scanErr)
		}

		subjects[subject]++
		ids[id]++
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the staged principals: %v", rowsErr)
	}

	purposes := principal.HousekeepingPurposes()
	if len(subjects) != len(purposes) || len(ids) != len(purposes) {
		t.Fatalf("staged builtin principals = %d subjects and %d IDs, want %d", len(subjects), len(ids), len(purposes))
	}

	for _, purpose := range purposes {
		if subjects[purpose.Subject()] != 1 {
			t.Errorf("staged principals with subject %q = %d, want 1", purpose.Subject(), subjects[purpose.Subject()])
		}
	}
}

// stagedGrant is the part of a staged grant that the restore test checks.
type stagedGrant struct {
	object    string
	revokedAt sql.NullInt64
}

// checkStagedHousekeepingGrants checks that the staged file holds one grant on
// @server for each purpose that acts on the installation, and that the grant
// revoked before the export stayed revoked.
func checkStagedHousekeepingGrants(
	t *testing.T,
	staged *sql.DB,
	purposes []principal.HousekeepingPurpose,
	revokedID string,
	revokedAt int64,
) {
	t.Helper()

	rows, err := staged.QueryContext(
		t.Context(),
		`SELECT g.id, g.role, g.object, g.revoked_at_ms
		FROM grants g JOIN principals p ON p.id = g.subject_id
		WHERE p.source = ?`,
		principal.SourceBuiltin,
	)
	if err != nil {
		t.Fatalf("read the staged grants: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close the staged grants: %v", closeErr)
		}
	}()

	grants := scanStagedGrants(t, rows)

	if len(grants.byRole) != len(purposes) || len(grants.byID) != len(purposes) {
		t.Fatalf("staged grants = %d roles and %d IDs, want %d",
			len(grants.byRole), len(grants.byID), len(purposes))
	}

	checkStagedGrantsOfPurposes(t, purposes, grants.byRole)

	revoked, ok := grants.byID[revokedID]
	if !ok {
		t.Fatalf("the staged grants hold no grant %q, want the revoked grant of the backup", revokedID)
	}

	if !revoked.revokedAt.Valid || revoked.revokedAt.Int64 != revokedAt {
		t.Fatalf("staged grant %q revoked at %v, want %d", revokedID, revoked.revokedAt, revokedAt)
	}
}

// stagedGrants keys the staged grants by role and by ID.
type stagedGrants struct {
	byRole map[string]stagedGrant
	byID   map[string]stagedGrant
}

// scanStagedGrants keys the staged grants by role and by ID.
func scanStagedGrants(t *testing.T, rows *sql.Rows) stagedGrants {
	t.Helper()

	grants := stagedGrants{byRole: map[string]stagedGrant{}, byID: map[string]stagedGrant{}}

	for rows.Next() {
		var (
			id, role, object string
			revoked          sql.NullInt64
		)

		if scanErr := rows.Scan(&id, &role, &object, &revoked); scanErr != nil {
			t.Fatalf("scan the staged grant: %v", scanErr)
		}

		grant := stagedGrant{object: object, revokedAt: revoked}
		grants.byRole[role] = grant
		grants.byID[id] = grant
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the staged grants: %v", rowsErr)
	}

	return grants
}

// checkStagedGrantsOfPurposes checks that each purpose has a grant on @server.
func checkStagedGrantsOfPurposes(
	t *testing.T, purposes []principal.HousekeepingPurpose, byRole map[string]stagedGrant,
) {
	t.Helper()

	wantObject := identity.ServerObject().String()

	for _, purpose := range purposes {
		role := purpose.InstallationRole()

		grant, ok := byRole[role]
		if !ok {
			t.Errorf("staged grants with role %q = 0, want 1", role)

			continue
		}

		if grant.object != wantObject {
			t.Errorf("staged grant of role %q names object %q, want %q", role, grant.object, wantObject)
		}
	}
}
