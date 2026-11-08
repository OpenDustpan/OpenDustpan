package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
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
