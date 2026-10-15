package postgres

// TestNamespacePagePlan is an internal test: EXPLAIN must run on the pool of
// the store, which the black-box test package cannot reach.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// planTestURL is the PostgreSQL URL the plan test reads, as the black-box
// tests do.
const planTestURL = "DUSTPAN_TEST_POSTGRES_URL"

// planNamespaces is the row count the plan test seeds: large enough that a
// sequential scan is slower than the key lookup, small enough to seed fast.
const planNamespaces = 10000

// TestNamespacePagePlan seeds 10000 namespaces, then checks one page of 50
// comes back fast with the right rows, and both page statements use the
// primary key index instead of a sequential scan or a sort step.
func TestNamespacePagePlan(t *testing.T) {
	t.Parallel()

	s := openPlanStore(t)
	seedPlanNamespaces(t, s, planNamespaces)
	analyzePlanNamespaces(t, s)

	subset := planSubset(planNamespaces)

	checkPlanPage(t, s, subset)
	checkPlanCounts(t, s, subset)
	checkAllPlan(t, s)
	checkSubsetPlan(t, s, subset)
}

// openPlanStore opens a store in a fresh schema of the test database.
func openPlanStore(t *testing.T) *Store {
	t.Helper()

	base := os.Getenv(planTestURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL plan test", planTestURL)
	}

	schemaURL := planSchemaURL(t, base)

	s, err := Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// planSchemaURL creates a fresh schema and returns base with search_path set
// to it, so the test owns its tables.
func planSchemaURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// seedPlanNamespaces creates n namespaces named ns-00000 and up. The names
// are zero-padded, so byte order is numeric order.
func seedPlanNamespaces(t *testing.T, s *Store, n int) {
	t.Helper()

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	for i := range n {
		ns := namespace.Namespace{
			CreatedAt:       now,
			Name:            namespace.Name(fmt.Sprintf("ns-%05d", i)),
			HomeRegion:      "eu-west",
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
			FailoverVersion: 1,
		}

		if err := s.CreateNamespace(t.Context(), ns); err != nil {
			t.Fatalf("create namespace %q: %v", ns.Name, err)
		}
	}
}

// analyzePlanNamespaces collects planner statistics for the seeded rows: a
// bulk load leaves none, and the plan check must see the table as the
// planner does in a running server.
func analyzePlanNamespaces(t *testing.T, s *Store) {
	t.Helper()

	if _, err := s.pool.Exec(t.Context(), `ANALYZE namespaces`); err != nil {
		t.Fatalf("analyze namespaces: %v", err)
	}
}

// planSubset returns 100 seeded names, every 100th, in name order.
func planSubset(n int) []namespace.Name {
	out := make([]namespace.Name, 0, 100)

	for i := 0; i < n; i += 100 {
		out = append(out, namespace.Name(fmt.Sprintf("ns-%05d", i)))
	}

	return out
}

// checkPlanPage reads one page of 50 through the adapter and checks the rows
// and the time, then reads the 100-name subset and checks its rows.
func checkPlanPage(t *testing.T, s *Store, subset []namespace.Name) {
	t.Helper()

	ctx := t.Context()

	start := time.Now()

	page, err := s.PageNamespaces(
		ctx,
		namespace.PageQuery{After: "", Limit: 50, Filter: namespace.Filter{All: true, Names: nil}},
	)
	if err != nil {
		t.Fatalf("page namespaces: %v", err)
	}

	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Fatalf("page of 50 took %v, want under 250 ms", elapsed)
	}

	if len(page) != 50 {
		t.Fatalf("page of 50 holds %d namespaces, want 50", len(page))
	}

	for i := range page {
		if want := namespace.Name(fmt.Sprintf("ns-%05d", i)); page[i].Name != want {
			t.Fatalf("page row %d = %q, want %q", i, page[i].Name, want)
		}
	}

	sub, err := s.PageNamespaces(
		ctx,
		namespace.PageQuery{After: "", Limit: 100, Filter: namespace.Filter{All: false, Names: subset}},
	)
	if err != nil {
		t.Fatalf("page a subset: %v", err)
	}

	if len(sub) != len(subset) {
		t.Fatalf("subset page holds %d namespaces, want %d", len(sub), len(subset))
	}

	for i := range sub {
		if sub[i].Name != subset[i] {
			t.Fatalf("subset row %d = %q, want %q", i, sub[i].Name, subset[i])
		}
	}
}

// checkPlanCounts checks the counts of all namespaces and of the subset.
func checkPlanCounts(t *testing.T, s *Store, subset []namespace.Name) {
	t.Helper()

	ctx := t.Context()

	total, err := s.CountNamespaces(ctx, namespace.Filter{All: true, Names: nil})
	if err != nil {
		t.Fatalf("count namespaces: %v", err)
	}

	if total != planNamespaces {
		t.Fatalf("count = %d, want %d", total, planNamespaces)
	}

	sub, err := s.CountNamespaces(ctx, namespace.Filter{All: false, Names: subset})
	if err != nil {
		t.Fatalf("count a subset: %v", err)
	}

	if sub != int64(len(subset)) {
		t.Fatalf("subset count = %d, want %d", sub, len(subset))
	}
}

// explain runs EXPLAIN for stmt on the pool of the store and returns the
// plan lines. The values are inlined, so the planner sees the same shapes
// and constants as the adapter statements.
func explain(t *testing.T, s *Store, stmt string) string {
	t.Helper()

	rows, err := s.pool.Query(t.Context(), stmt)
	if err != nil {
		t.Fatalf("explain the page statement: %v", err)
	}

	defer rows.Close()

	var lines []string

	for rows.Next() {
		var line string

		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan the plan row: %v", err)
		}

		t.Logf("plan: %s", line)

		lines = append(lines, line)
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		t.Fatalf("read the plan rows: %v", err)
	}

	return strings.Join(lines, "\n")
}

// checkAllPlan checks the All page statement reads through the primary key
// index: no sequential scan and no sort step.
func checkAllPlan(t *testing.T, s *Store) {
	t.Helper()

	const stmt = `EXPLAIN SELECT name, home_region, failover_version,
		created_at_ms, allow_forced_writes, allow_unlocked_writes
		FROM namespaces WHERE name > '' ORDER BY name LIMIT 50`

	plan := explain(t, s, stmt)

	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("all page plan scans the table:\n%s", plan)
	}

	if strings.Contains(plan, "Sort") {
		t.Fatalf("all page plan sorts instead of using the index:\n%s", plan)
	}

	if !strings.Contains(plan, "Index Scan") && !strings.Contains(plan, "Index Only Scan") {
		t.Fatalf("all page plan uses no index scan:\n%s", plan)
	}
}

// subsetArray renders names as a text array literal. The names hold ASCII
// letters, digits and hyphens only, so quoting each value is enough.
func subsetArray(names []namespace.Name) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, `"`+name.String()+`"`)
	}

	return "{" + strings.Join(quoted, ",") + "}"
}

// checkSubsetPlan checks the subset page statement looks every name up
// through the primary key index instead of scanning the table.
func checkSubsetPlan(t *testing.T, s *Store, subset []namespace.Name) {
	t.Helper()

	stmt := `EXPLAIN SELECT name, home_region, failover_version,
		created_at_ms, allow_forced_writes, allow_unlocked_writes
		FROM namespaces WHERE name > '' AND name = ANY('` + subsetArray(subset) + `'::text[])
		ORDER BY name LIMIT 50`

	plan := explain(t, s, stmt)

	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("subset page plan scans the table:\n%s", plan)
	}

	if !strings.Contains(plan, "Index Scan") && !strings.Contains(plan, "Index Only Scan") {
		t.Fatalf("subset page plan uses no index scan:\n%s", plan)
	}
}
