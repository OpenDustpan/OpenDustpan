package postgres_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestPortableStageKeepsHousekeepingRows proves that a staged restore holds
// the housekeeping principals and grants of the backup, not the seeded rows of
// the new baseline: one row for each purpose, no row twice, and a grant that
// was revoked before the export stays revoked after the stage.
func TestPortableStageKeepsHousekeepingRows(t *testing.T) {
	t.Parallel()

	s := openStagingTestStore(t)
	ctx := t.Context()

	purposes := housekeepingServerPurposes(t)

	const revokedAt = int64(1_700_000_000_000)

	revokedID, err := s.RevokeSeededHousekeepingGrantForTest(ctx, revokedAt)
	if err != nil {
		t.Fatalf("revoke the seeded grant: %v", err)
	}

	files, sum, descs := storetest.PortableFiles(t, s)

	_, stageErr := s.StagePortable(
		ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, s),
	)
	if stageErr != nil {
		t.Fatalf("stage backup: %v", stageErr)
	}

	staging := s.StagingSchemaForTest()
	if staging == "" {
		t.Fatal("stage kept no staging schema, want the staging area")
	}

	conn := openStagingConn(t, staging)

	checkStagedHousekeepingPrincipals(t, conn)
	checkStagedHousekeepingGrants(t, conn, purposes, revokedID, revokedAt)
}

// housekeepingServerPurposes returns the purposes whose role holds a server
// action, so the install writes one grant on @server for each of them.
func housekeepingServerPurposes(t *testing.T) []principal.HousekeepingPurpose {
	t.Helper()

	var out []principal.HousekeepingPurpose

	for _, purpose := range principal.HousekeepingPurposes() {
		role, ok := access.BuiltInRole(purpose.Role())
		if !ok {
			t.Fatalf("purpose %q has no built-in role %q", purpose.Name(), purpose.Role())
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

// openStagingConn connects to the staging schema. The search path makes every
// query read the staged tables, and never the live ones.
func openStagingConn(t *testing.T, staging string) *pgx.Conn {
	t.Helper()

	base := os.Getenv(stagingTestURL)
	if base == "" {
		t.Skipf("set %s to run the staging tests", stagingTestURL)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", stagingTestURL, err)
	}

	query := u.Query()
	query.Set("search_path", staging)
	u.RawQuery = query.Encode()

	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatalf("connect to the staging schema: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(t.Context())); closeErr != nil {
			t.Errorf("close the staging connection: %v", closeErr)
		}
	})

	return conn
}

// checkStagedHousekeepingPrincipals checks that the staged schema holds one
// builtin principal for each purpose, and no row twice.
func checkStagedHousekeepingPrincipals(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	rows, err := conn.Query(
		t.Context(),
		`SELECT id, subject FROM principals WHERE source = $1`,
		principal.SourceBuiltin,
	)
	if err != nil {
		t.Fatalf("read the staged principals: %v", err)
	}

	defer rows.Close()

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
	revokedAt pgtype.Int8
}

// checkStagedHousekeepingGrants checks that the staged schema holds one grant
// on @server for each purpose that acts on the installation, and that the
// grant revoked before the export stayed revoked.
func checkStagedHousekeepingGrants(
	t *testing.T,
	conn *pgx.Conn,
	purposes []principal.HousekeepingPurpose,
	revokedID string,
	revokedAt int64,
) {
	t.Helper()

	rows, err := conn.Query(
		t.Context(),
		`SELECT g.id, g.role, g.object, g.revoked_at_ms
		FROM grants g JOIN principals p ON p.id = g.subject_id
		WHERE p.source = $1`,
		principal.SourceBuiltin,
	)
	if err != nil {
		t.Fatalf("read the staged grants: %v", err)
	}

	defer rows.Close()

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
func scanStagedGrants(t *testing.T, rows pgx.Rows) stagedGrants {
	t.Helper()

	grants := stagedGrants{byRole: map[string]stagedGrant{}, byID: map[string]stagedGrant{}}

	for rows.Next() {
		var (
			id, role, object string
			revoked          pgtype.Int8
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
		grant, ok := byRole[purpose.Role()]
		if !ok {
			t.Errorf("staged grants with role %q = 0, want 1", purpose.Role())

			continue
		}

		if grant.object != wantObject {
			t.Errorf("staged grant of role %q names object %q, want %q", purpose.Role(), grant.object, wantObject)
		}
	}
}
