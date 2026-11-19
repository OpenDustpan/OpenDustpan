package postgres_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres"
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
// key backwards on a table of 20,000 rows, with no sort.
func TestListPlanUsesThePrimaryKey(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL plan test", envURL)
	}

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
		if err := conn.Close(t.Context()); err != nil {
			t.Errorf("close connection: %v", err)
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

	// The query holds placeholders. A plan with literal values shows the
	// plan of a first page and of a deep page alike: the key and the
	// direction decide it.
	query := auditPageQuery(t)

	// The three guards of the query, each by its own check: the scope, the
	// start below a sequence, and the limit of the rows that one page reads.
	for _, guard := range []string{"namespace = $1", "sequence < $2", "LIMIT $3"} {
		if !strings.Contains(query, guard) {
			t.Fatalf("query %q does not hold %q", query, guard)
		}
	}

	query = strings.NewReplacer("$1", "'acme'", "$2", "15000", "$3", "51").Replace(query)

	rows, err := conn.Query(t.Context(), "EXPLAIN "+query)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	var plan strings.Builder

	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}

		fmt.Fprintln(&plan, line)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}

	if !strings.Contains(plan.String(), "Index Scan Backward using audit_entries_pkey") ||
		strings.Contains(plan.String(), "Sort") || !strings.Contains(plan.String(), "Limit") {
		t.Fatalf("plan = %q, want a backward scan of the primary key and no sort", plan.String())
	}
}
