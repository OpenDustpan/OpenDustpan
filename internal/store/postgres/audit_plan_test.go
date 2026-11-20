package postgres_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/postgres"
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

// seedAuditRows opens a store on a fresh schema, so the tables exist, and
// adds 20,000 entries of the namespace acme through a second connection.
func seedAuditRows(t *testing.T, base string) *pgx.Conn {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close connection: %v", closeErr)
		}
	})

	_, err = conn.Exec(t.Context(), `
INSERT INTO audit_entries (namespace, sequence, subject, action, object, at_ms, channel, kind, outcome, hash)
SELECT 'acme', i, 'user', 'unit:decrypt', 'obj', i, 'api', 'reveal', 'success', 'h' FROM generate_series(1, 20000) AS i`)
	if err != nil {
		t.Fatalf("seed audit entries: %v", err)
	}

	if _, err = conn.Exec(t.Context(), "ANALYZE audit_entries"); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	return conn
}

// explainPlan returns the lines of the plan of query. The query holds
// placeholders, so the plan runs with literal values: the key and the
// direction decide the plan of a first page and of a deep page alike.
func explainPlan(t *testing.T, conn *pgx.Conn, query string) string {
	t.Helper()

	literal := strings.NewReplacer("$1", "'acme'", "$2", "15000", "$3", "51").Replace(query)

	rows, err := conn.Query(t.Context(), "EXPLAIN "+literal)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer rows.Close()

	var plan strings.Builder

	for rows.Next() {
		var line string
		if scanErr := rows.Scan(&line); scanErr != nil {
			t.Fatalf("scan plan: %v", scanErr)
		}

		fmt.Fprintln(&plan, line)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}

	return plan.String()
}

// TestListPlanUsesThePrimaryKey proves that the page query reads the primary
// key backwards on a table of 20,000 rows, with no sort and with a limit.
func TestListPlanUsesThePrimaryKey(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL plan test", envURL)
	}

	conn := seedAuditRows(t, base)
	query := auditPageQuery(t)

	checkQueryGuards(t, query, "namespace = $1", "sequence < $2", "LIMIT $3")

	plan := explainPlan(t, conn, query)

	if !strings.Contains(plan, "Index Scan Backward using audit_entries_pkey") ||
		strings.Contains(plan, "Sort") || !strings.Contains(plan, "Limit") {
		t.Fatalf("plan = %q, want a backward scan of the primary key, a limit and no sort", plan)
	}
}
