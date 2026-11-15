package postgres_test

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// removalRows returns every row of the member removal log of the schema at
// schemaURL, oldest first.
func removalRows(t *testing.T, schemaURL string) []principal.Removal {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	rows, err := conn.Query(t.Context(), `SELECT source_kind, source_id, group_id, removed, members_before, at_ms
		FROM member_removals ORDER BY at_ms, id`)
	if err != nil {
		t.Fatalf("select member removals: %v", err)
	}

	defer rows.Close()

	var out []principal.Removal

	for rows.Next() {
		var (
			kind, sourceID, groupID      string
			removed, membersBefore, atMs int64
		)

		if scanErr := rows.Scan(&kind, &sourceID, &groupID, &removed, &membersBefore, &atMs); scanErr != nil {
			t.Fatalf("scan member removal: %v", scanErr)
		}

		out = append(out, principal.Removal{
			Source:        principal.RemovalSource{Kind: principal.RemovalSourceKind(kind), ID: sourceID},
			GroupID:       groupID,
			Removed:       int(removed),
			MembersBefore: int(membersBefore),
			At:            codec.FromMillis(atMs),
		})
	}

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read member removals: %v", iterErr)
	}

	return out
}

// TestMemberRemovalsTableRefusesABadRow writes each row straight to the
// table, as a restore of a hostile backup does, and expects the schema to
// refuse it. Each bad row differs from the valid row in one field. The valid
// row proves that the table takes the statement, so a refusal names the
// CHECK constraint of that field.
func TestMemberRemovalsTableRefusesABadRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		kind          string
		sourceID      string
		groupID       string
		removed       int64
		membersBefore int64
		atMs          int64
		constraint    string
	}{
		{
			name: "an unknown source kind", kind: "other", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, constraint: "member_removals_source_kind_check",
		},
		{
			name: "an empty source ID", kind: "scim-token", sourceID: "", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, constraint: "member_removals_source_id_check",
		},
		{
			name: "an empty group ID", kind: "scim-token", sourceID: "s", groupID: "",
			removed: 1, membersBefore: 1, atMs: 1, constraint: "member_removals_group_id_check",
		},
		{
			name: "no member removed", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 0, membersBefore: 1, atMs: 1, constraint: "member_removals_removed_check",
		},
		{
			name: "a negative removal", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: -5, membersBefore: 1, atMs: 1, constraint: "member_removals_removed_check",
		},
		{
			name: "fewer members before than removed", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 0, atMs: 1, constraint: "member_removals_members_before_check",
		},
		{
			name: "no time", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 0, constraint: "member_removals_at_ms_check",
		},
		{
			name: "a valid row", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, constraint: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, schemaURL := openClaimsStore(t)

			conn, err := pgx.Connect(t.Context(), schemaURL)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}

			defer func() {
				if closeErr := conn.Close(t.Context()); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}()

			_, err = conn.Exec(t.Context(), `INSERT INTO member_removals
				(id, source_kind, source_id, group_id, removed, members_before, at_ms)
				VALUES ('rmv-1', $1, $2, $3, $4, $5, $6)`,
				tt.kind, tt.sourceID, tt.groupID, tt.removed, tt.membersBefore, tt.atMs)
			if tt.constraint == "" {
				requireRemovalAccepted(t, err)
			} else {
				requireRemovalRefused(t, err, tt.constraint)
			}
		})
	}
}

// requireRemovalAccepted fails the test unless the insert of the valid row
// passed.
func requireRemovalAccepted(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("the valid row: %v, want the insert to pass", err)
	}
}

// requireRemovalRefused fails the test unless the insert failed with the
// named CHECK constraint, with SQLSTATE 23514.
func requireRemovalRefused(t *testing.T, err error, constraint string) {
	t.Helper()

	if err == nil {
		t.Fatal("insert = <nil>, want a refused row")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("insert error = %v, want a *pgconn.PgError", err)
	}

	if pgErr.Code != "23514" {
		t.Fatalf("insert SQLSTATE = %s, want 23514", pgErr.Code)
	}

	if pgErr.ConstraintName != constraint {
		t.Fatalf("insert constraint = %q, want %q", pgErr.ConstraintName, constraint)
	}
}

// TestRemovalBackupRestore proves that a backup and a restore keep the rows
// of the removal log: after the restore, a removal counts the restored rows
// in its totals, as the log of the source store does.
func TestRemovalBackupRestore(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL removal log backup suite", envURL)
	}

	storetest.RunRemovalBackup(t, func(t *testing.T) storetest.RemovalBackupStore {
		t.Helper()

		return openBackup(t, base)
	})
}

// TestRemovalLogConformance runs the suite of the member removal log.
func TestRemovalLogConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunRemovalLog(t, func(t *testing.T) principaltest.RemovalLogStore {
		t.Helper()

		s, schemaURL := openClaimsStore(t)

		return principaltest.RemovalLogStore{Log: s, Rows: func(t *testing.T) []principal.Removal {
			t.Helper()

			return removalRows(t, schemaURL)
		}}
	})
}
