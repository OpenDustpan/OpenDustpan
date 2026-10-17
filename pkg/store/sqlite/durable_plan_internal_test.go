package sqlite

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
)

// TestDurableClaimUsesOpenIndex proves the claim query reads the partial
// index of the open operations. SQLite uses a partial index only when the
// query holds the WHERE term of the index, and a claim runs under the write
// lock, so a scan of every ended operation would hold every writer.
func TestDurableClaimUsesOpenIndex(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	query, args := durableClaimQuery(durable.ClaimRequest{
		Owner:       "instance-a",
		Kinds:       []durable.Kind{"kind-a", "kind-b"},
		MaxVersions: map[durable.Kind]int{"kind-b": 1},
		Lease:       time.Minute,
	}, 0)

	rows, err := s.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain the claim query: %v", err)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // rows.Err below reports a read failure.
	}()

	var plan []string

	for rows.Next() {
		var (
			id, parent, unused int
			detail             string
		)

		if scanErr := rows.Scan(&id, &parent, &unused, &detail); scanErr != nil {
			t.Fatalf("read the plan: %v", scanErr)
		}

		plan = append(plan, detail)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the plan: %v", rowsErr)
	}

	if !slices.ContainsFunc(plan, func(d string) bool { return strings.Contains(d, "durable_operations_open") }) {
		t.Fatalf("claim query plan = %q, want the index durable_operations_open", plan)
	}
}

// TestKeepControlStatementsCoverControlTables proves the swap of a restore
// clears and copies every table of backup.ControlTables.
func TestKeepControlStatementsCoverControlTables(t *testing.T) {
	t.Parallel()

	for _, table := range backup.ControlTables {
		clears := slices.Contains(keepControlStatements, "DELETE FROM main."+table)
		copies := slices.ContainsFunc(keepControlStatements, func(stmt string) bool {
			return strings.HasPrefix(stmt, "INSERT INTO main."+table+" ") &&
				strings.Contains(stmt, " FROM live."+table)
		})

		if !clears || !copies {
			t.Errorf("control table %s: cleared %v, copied %v, want both", table, clears, copies)
		}
	}
}
