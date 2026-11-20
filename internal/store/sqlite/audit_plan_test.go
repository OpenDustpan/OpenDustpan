package sqlite_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
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

// checkQueryGuards fails the test unless the query holds each guard: the
// scope, the start below a sequence, and the limit of the rows of one page.
func checkQueryGuards(t *testing.T, query string, guards ...string) {
	t.Helper()

	for _, guard := range guards {
		if !strings.Contains(query, guard) {
			t.Fatalf("query %q does not hold %q", query, guard)
		}
	}
}

// seedAuditRows opens a store at a copy of the template, so the schema
// exists, and adds 20,000 entries of the namespace acme through a second
// connection.
func seedAuditRows(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	_, err = conn.ExecContext(t.Context(), `
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 20000)
INSERT INTO audit_entries (namespace, sequence, subject, action, object, at_ms, channel, kind, outcome, hash)
SELECT 'acme', i, 'user', 'unit:decrypt', 'obj', i, 'api', 'reveal', 'success', 'h' FROM n`)
	if err != nil {
		t.Fatalf("seed audit entries: %v", err)
	}

	return conn
}

// explainPlan returns the lines of the plan of query for a page below 15000.
func explainPlan(t *testing.T, conn *sql.DB, query string) string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, "acme", 15000, 51)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close plan rows: %v", closeErr)
		}
	}()

	var plan strings.Builder

	for rows.Next() {
		var id, parent, unused int

		var detail string
		if scanErr := rows.Scan(&id, &parent, &unused, &detail); scanErr != nil {
			t.Fatalf("scan plan: %v", scanErr)
		}

		fmt.Fprintln(&plan, detail)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}

	return plan.String()
}

// primaryKeyIndexName returns the name of the index that SQLite made for the
// primary key of table, from PRAGMA index_list (origin pk).
func primaryKeyIndexName(t *testing.T, conn *sql.DB, table string) string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), "SELECT name FROM pragma_index_list(?) WHERE origin = 'pk'", table)
	if err != nil {
		t.Fatalf("index list of %s: %v", table, err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close index rows: %v", closeErr)
		}
	}()

	var names []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan index name: %v", scanErr)
		}

		names = append(names, name)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read index list: %v", err)
	}

	if len(names) != 1 {
		t.Fatalf("table %s has %d primary key indexes, want 1: %v", table, len(names), names)
	}

	return names[0]
}

// TestListPlanUsesThePrimaryKey proves that the page query reads the primary
// key (namespace, sequence) backwards: the plan searches by the key and holds
// no temporary B-tree for a sort.
func TestListPlanUsesThePrimaryKey(t *testing.T) {
	t.Parallel()

	conn := seedAuditRows(t)
	query := auditPageQuery(t)

	checkQueryGuards(t, query, "namespace = ?", "sequence < ?", "LIMIT ?")

	plan := explainPlan(t, conn, query)

	// SQLite names the index of the primary key (namespace, sequence) of a
	// table with a row ID. The test reads the name from the database.
	want := "SEARCH audit_entries USING INDEX " + primaryKeyIndexName(t, conn, "audit_entries") +
		" (namespace=? AND sequence<?)"

	if !strings.Contains(plan, want) || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("plan = %q, want a search by the primary key and no temporary B-tree", plan)
	}
}
