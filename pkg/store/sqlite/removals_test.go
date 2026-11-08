package sqlite_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// removalRows returns every row of the member removal log of the database at
// path, oldest first.
func removalRows(t *testing.T, path string) []principal.Removal {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	rows, err := conn.QueryContext(t.Context(), `SELECT source_kind, source_id, group_id, removed, members_before, at_ms
		FROM member_removals ORDER BY at_ms, id`)
	if err != nil {
		t.Fatalf("select member removals: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close member removals: %v", closeErr)
		}
	}()

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
		refuse        bool
	}{
		{
			name: "an unknown source kind", kind: "other", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, refuse: true,
		},
		{
			name: "an empty source ID", kind: "scim-token", sourceID: "", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, refuse: true,
		},
		{
			name: "an empty group ID", kind: "scim-token", sourceID: "s", groupID: "",
			removed: 1, membersBefore: 1, atMs: 1, refuse: true,
		},
		{
			name: "no member removed", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 0, membersBefore: 1, atMs: 1, refuse: true,
		},
		{
			name: "a negative removal", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: -5, membersBefore: 1, atMs: 1, refuse: true,
		},
		{
			name: "fewer members before than removed", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 0, atMs: 1, refuse: true,
		},
		{
			name: "no time", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 0, refuse: true,
		},
		{
			name: "a valid row", kind: "scim-token", sourceID: "s", groupID: "g",
			removed: 1, membersBefore: 1, atMs: 1, refuse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, path := openClaimsStore(t)

			conn, err := openRawDB(path)
			if err != nil {
				t.Fatalf("open raw database: %v", err)
			}

			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					t.Errorf("close raw database: %v", closeErr)
				}
			}()

			_, err = conn.ExecContext(t.Context(), `INSERT INTO member_removals
				(id, source_kind, source_id, group_id, removed, members_before, at_ms)
				VALUES ('rmv-1', ?, ?, ?, ?, ?, ?)`,
				tt.kind, tt.sourceID, tt.groupID, tt.removed, tt.membersBefore, tt.atMs)
			if tt.refuse {
				requireRemovalRefused(t, err)
			} else {
				requireRemovalAccepted(t, err)
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

// requireRemovalRefused fails the test unless the insert failed with a CHECK
// constraint failure.
func requireRemovalRefused(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("insert = <nil>, want a refused row")
	}

	if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("insert error = %v, want a CHECK constraint failure", err)
	}
}

// TestRemovalBackupRestore proves that a backup and a restore keep the rows
// of the removal log: after the restore, a removal counts the restored rows
// in its totals, as the log of the source store does.
func TestRemovalBackupRestore(t *testing.T) {
	t.Parallel()

	storetest.RunRemovalBackup(t, func(t *testing.T) storetest.RemovalBackupStore {
		t.Helper()

		st, ok := openBackup(t).(storetest.RemovalBackupStore)
		if !ok {
			t.Fatal("the backup store does not keep the removal log and the portable restore ports")
		}

		return st
	})
}

// TestRemovalLogConformance runs the suite of the member removal log.
func TestRemovalLogConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunRemovalLog(t, func(t *testing.T) principaltest.RemovalLogStore {
		t.Helper()

		s, path := openClaimsStore(t)

		return principaltest.RemovalLogStore{Log: s, Rows: func(t *testing.T) []principal.Removal {
			t.Helper()

			return removalRows(t, path)
		}}
	})
}
