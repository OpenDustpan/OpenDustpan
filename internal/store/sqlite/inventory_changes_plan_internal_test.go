package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// The size of the namespace that the plan test seeds: units times changes.
const (
	planUnits   = 2000
	planChanges = 50
)

// TestInventoryChangeBoundsPlan seeds a namespace with 2,000 units of 50
// change log rows each, checks that the plan of the point-in-time query steps
// from unit to unit by a range on the index, and logs the time of ViewAt. A
// step by a row value is not a range in SQLite and makes the query scan the
// index of the tool for each unit.
func TestInventoryChangeBoundsPlan(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	statements := []string{
		`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
			VALUES ('acme', 'eu-west', 1, 0, 'ns-plan')`,
		fmt.Sprintf(`WITH RECURSIVE u(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM u WHERE n < %[1]d),
			k(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM k WHERE n < %[2]d)
			INSERT INTO inventory_changes
				(namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count)
			SELECT 'ns-plan', (u.n - 1) * %[2]d + k.n, 1000 + (u.n - 1) * %[2]d + k.n,
				CASE WHEN u.n %% 2 = 0 THEN 'pulumi' ELSE 'opentofu' END,
				'unit-' || printf('%%05d', u.n), 'unit-' || u.n, 'unit-' || u.n, '', '',
				CASE WHEN k.n = 1 THEN 'created' ELSE 'changed' END, NULL
			FROM u, k`, planUnits, planChanges),
		fmt.Sprintf(`INSERT INTO inventory_change_heads (namespace_id, last_seq) VALUES ('ns-plan', %d)`,
			planUnits*planChanges),
		`ANALYZE`,
	}

	for n, statement := range statements {
		if _, err = s.conn.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed change log, statement %d: %v", n, err)
		}
	}

	middle := int64(1000 + planUnits*planChanges/2)

	plan := explainBounds(t, s, middle)
	if !strings.Contains(plan, "tool=? AND unit_id>?") {
		t.Fatalf("plan does not step to the next unit by a range on the index:\n%s", plan)
	}

	start := time.Now()

	view, err := s.ViewAt(ctx, namespace.Name("acme"), time.UnixMilli(middle))
	if err != nil || len(view.Units) != planUnits/2 {
		t.Fatalf("view: %d units, err %v, want %d units", len(view.Units), err, planUnits/2)
	}

	t.Logf("ViewAt over %d units of %d changes: %v", planUnits, planChanges, time.Since(start))
}

// explainBounds returns the plan of ListInventoryChangeBounds.
func explainBounds(t *testing.T, s *Store, at int64) string {
	t.Helper()

	data, err := os.ReadFile("queries.sql")
	if err != nil {
		t.Fatalf("read queries.sql: %v", err)
	}

	_, rest, found := strings.Cut(string(data), "-- name: ListInventoryChangeBounds :many\n")
	if !found {
		t.Fatal("query ListInventoryChangeBounds not found")
	}

	body, _, _ := strings.Cut(rest, "\n-- name:")
	body = regexp.MustCompile(`(?m)^--.*$`).ReplaceAllString(body, "")
	body = strings.ReplaceAll(body, "sqlc.arg(namespace_id)", "?1")
	body = strings.ReplaceAll(body, "sqlc.arg(at_ms)", "?2")
	body = strings.TrimSuffix(strings.TrimSpace(body), ";")

	rows, err := s.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+body, "ns-plan", at)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // rows.Err below reports a read failure.
	}()

	var plan strings.Builder

	for rows.Next() {
		var (
			id, parent, unused int
			detail             string
		)

		if scanErr := rows.Scan(&id, &parent, &unused, &detail); scanErr != nil {
			t.Fatalf("read the plan: %v", scanErr)
		}

		plan.WriteString(detail + "\n")
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read the plan: %v", err)
	}

	return plan.String()
}
