package sqlite_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// auditPageQuery returns the text of the query ListAuditEntriesBefore from
// queries.sql, so the plan test reads the query that the store runs.
func auditPageQuery(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	_, block, ok := strings.Cut(string(raw), "-- name: ListAuditEntriesBefore :many\n")
	if !ok {
		t.Fatal("queries.sql has no ListAuditEntriesBefore")
	}

	block, _, _ = strings.Cut(block, "\n\n")

	var lines []string

	for line := range strings.SplitSeq(block, "\n") {
		if !strings.HasPrefix(line, "--") {
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
}

// TestListPlanUsesThePrimaryKey proves that the page query reads the primary
// key (namespace, sequence) backwards: the plan searches by the key and holds
// no temporary B-tree for a sort.
func TestListPlanUsesThePrimaryKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close raw database: %v", err)
		}
	})

	_, err = conn.ExecContext(t.Context(), `
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 20000)
INSERT INTO audit_entries (namespace, sequence, subject, action, object, at_ms, channel, kind, outcome, hash)
SELECT 'acme', i, 'user', 'unit:decrypt', 'obj', i, 'api', 'reveal', 'success', 'h' FROM n`)
	if err != nil {
		t.Fatalf("seed audit entries: %v", err)
	}

	query := auditPageQuery(t)

	// The three guards of the query, each by its own check: the scope, the
	// start below a sequence, and the limit of the rows that one page reads.
	for _, guard := range []string{"namespace = ?", "sequence < ?", "LIMIT ?"} {
		if !strings.Contains(query, guard) {
			t.Fatalf("query %q does not hold %q", query, guard)
		}
	}

	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, "acme", 15000, 51)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close plan rows: %v", err)
		}
	}()

	var plan strings.Builder

	for rows.Next() {
		var id, parent, unused int

		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}

		fmt.Fprintln(&plan, detail)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}

	// SQLite names the index of the primary key (namespace, sequence) of a
	// table with a row ID sqlite_autoindex_audit_entries_1.
	if !strings.Contains(plan.String(), "SEARCH audit_entries USING INDEX sqlite_autoindex_audit_entries_1 (namespace=? AND sequence<?)") ||
		strings.Contains(plan.String(), "USE TEMP B-TREE") {
		t.Fatalf("plan = %q, want a search by the primary key and no temporary B-tree", plan.String())
	}
}
