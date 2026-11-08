package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
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
// refuse it.
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
	}{
		{
			name:          "an unknown source kind",
			kind:          "other",
			sourceID:      "s",
			groupID:       "g",
			removed:       1,
			membersBefore: 1,
			atMs:          1,
		},
		{
			name:          "an empty source ID",
			kind:          "scim-token",
			sourceID:      "",
			groupID:       "g",
			removed:       1,
			membersBefore: 1,
			atMs:          1,
		},
		{
			name:          "an empty group ID",
			kind:          "scim-token",
			sourceID:      "s",
			groupID:       "",
			removed:       1,
			membersBefore: 1,
			atMs:          1,
		},
		{
			name:          "no member removed",
			kind:          "scim-token",
			sourceID:      "s",
			groupID:       "g",
			removed:       0,
			membersBefore: 1,
			atMs:          1,
		},
		{
			name:          "a negative removal",
			kind:          "scim-token",
			sourceID:      "s",
			groupID:       "g",
			removed:       -5,
			membersBefore: 1,
			atMs:          1,
		},
		{
			name:          "fewer members than removed",
			kind:          "scim-token",
			sourceID:      "s",
			groupID:       "g",
			removed:       3,
			membersBefore: 2,
			atMs:          1,
		},
		{name: "no time", kind: "scim-token", sourceID: "s", groupID: "g", removed: 1, membersBefore: 1, atMs: 0},
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
			if err == nil {
				t.Fatal("insert = <nil>, want a refused row")
			}
		})
	}
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
