package postgres_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The size of the namespace that the plan test seeds: units times changes.
const (
	planUnits   = 2000
	planChanges = 50
)

// boundsQuery returns the SQL of the named query of queries.sql, with the
// two named arguments of ListInventoryChangeBounds as $1 and $2.
func boundsQuery(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	_, rest, found := strings.Cut(string(data), "-- name: "+name+" :many\n")
	if !found {
		t.Fatalf("query %s not found", name)
	}

	body, _, _ := strings.Cut(rest, "\n-- name:")
	body = regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(body, "")
	body = strings.ReplaceAll(body, "sqlc.arg(namespace_id)", "$1")
	body = strings.ReplaceAll(body, "sqlc.arg(at_ms)", "$2")

	return strings.TrimSuffix(strings.TrimSpace(body), ";")
}

// seedChangeLog creates the namespace acme with planUnits units of
// planChanges change log rows each, and analyzes the table.
func seedChangeLog(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	statements := []string{
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
			VALUES ('acme', 'eu-west', 1, 0, 'ns-plan')`,
		`INSERT INTO inventory_changes
			(namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count)
			SELECT 'ns-plan', (u - 1) * $2 + k, 1000 + (u - 1) * $2 + k,
				CASE WHEN u % 2 = 0 THEN 'pulumi' ELSE 'opentofu' END,
				'unit-' || lpad(u::text, 5, '0'), 'unit-' || u, 'unit-' || u, '', '',
				CASE WHEN k = 1 THEN 'created' ELSE 'changed' END, NULL
			FROM generate_series(1, $1::int) AS u, generate_series(1, $2::int) AS k`,
		`INSERT INTO inventory_change_heads (namespace_id, last_seq) VALUES ('ns-plan', $1::bigint * $2::bigint)`,
		`ANALYZE inventory_changes`,
	}

	for n, statement := range statements {
		args := []any{}
		if n == 1 || n == 2 {
			args = append(args, planUnits, planChanges)
		}

		if _, err := conn.Exec(t.Context(), statement, args...); err != nil {
			t.Fatalf("seed change log, statement %d: %v", n, err)
		}
	}
}

// explain returns the plan of query with args.
func explain(t *testing.T, conn *pgx.Conn, query string, args ...any) string {
	t.Helper()

	rows, err := conn.Query(t.Context(), "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer rows.Close()

	var plan strings.Builder

	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}

		plan.WriteString(line + "\n")
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}

	return plan.String()
}

// TestInventoryChangeBoundsPlan seeds a namespace with 2,000 units of 50
// change log rows each, checks that the plan of the point-in-time query reads
// no table by a sequential scan, and checks that ViewAt at the middle of the
// log lists the half of the units that exist then.
func TestInventoryChangeBoundsPlan(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL plan test", envURL)
	}

	ctx := t.Context()
	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	seedChangeLog(t, conn)

	middle := int64(1000 + planUnits*planChanges/2)

	plan := explain(t, conn, boundsQuery(t, "ListInventoryChangeBounds"), "ns-plan", middle)
	if strings.Contains(plan, "Seq Scan") || !strings.Contains(plan, "inventory_changes_unit") {
		t.Fatalf("plan reads the change table without the index:\n%s", plan)
	}

	start := time.Now()

	view, err := s.ViewAt(ctx, namespace.Name("acme"), time.UnixMilli(middle))
	if err != nil || len(view.Units) != planUnits/2 {
		t.Fatalf("view: %d units, err %v, want %d units", len(view.Units), err, planUnits/2)
	}

	t.Logf("ViewAt at the middle over %d units of %d changes: %v", planUnits, planChanges, time.Since(start))

	start = time.Now()

	if _, err = s.ViewAt(ctx, namespace.Name("acme"), time.UnixMilli(1000+1)); err != nil {
		t.Fatalf("view at the earliest time: %v", err)
	}

	t.Logf("ViewAt at the earliest time over %d units of %d changes: %v", planUnits, planChanges, time.Since(start))
}
