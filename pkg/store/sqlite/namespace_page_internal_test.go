package sqlite

// TestNamespacePagePlan is an internal test: EXPLAIN QUERY PLAN must run on
// the open handle of the store, which the black-box test package cannot
// reach. It keeps the handle behind the read lock, as ListNamespaces does.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// planNamespaces is the row count the plan test seeds: large enough that a
// full scan is slower than the key lookup, small enough to seed fast.
const planNamespaces = 10000

// TestNamespacePagePlan seeds 10000 namespaces, then checks one page of 50
// comes back fast with the right rows, and both page statements use the
// primary key index instead of a full scan or a sort step.
func TestNamespacePagePlan(t *testing.T) {
	t.Parallel()

	s := openPlanStore(t)
	seedPlanNamespaces(t, s, planNamespaces)

	subset := planSubset(planNamespaces)

	checkPlanPage(t, s, subset)
	checkPlanCounts(t, s, subset)
	checkAllPlan(t, s)
	checkSubsetPlan(t, s, subset)
}

// openPlanStore opens a fresh store in a temporary directory.
func openPlanStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
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

	page, err := s.PageNamespaces(ctx, namespace.PageQuery{After: "", Limit: 50, Names: nil, All: true})
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

	sub, err := s.PageNamespaces(ctx, namespace.PageQuery{After: "", Limit: 100, Names: subset, All: false})
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

	total, err := s.CountNamespaces(ctx, true, nil)
	if err != nil {
		t.Fatalf("count namespaces: %v", err)
	}

	if total != planNamespaces {
		t.Fatalf("count = %d, want %d", total, planNamespaces)
	}

	sub, err := s.CountNamespaces(ctx, false, subset)
	if err != nil {
		t.Fatalf("count a subset: %v", err)
	}

	if sub != int64(len(subset)) {
		t.Fatalf("subset count = %d, want %d", sub, len(subset))
	}
}

// planLine holds one row of EXPLAIN QUERY PLAN: the detail is the last
// column, which names the access path of the step.
type planLine struct {
	id     int64
	parent int64
	serial int64
	detail string
}

// explainQueryPlan runs EXPLAIN QUERY PLAN for stmt with args on the store
// handle and returns the detail of every step.
func explainQueryPlan(t *testing.T, s *Store, stmt string, args ...any) []string {
	t.Helper()

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.conn.QueryContext(t.Context(), stmt, args...)
	if err != nil {
		t.Fatalf("explain the page statement: %v", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close the plan rows: %v", err)
		}
	}()

	var out []string

	for rows.Next() {
		var line planLine

		if err := rows.Scan(&line.id, &line.parent, &line.serial, &line.detail); err != nil {
			t.Fatalf("scan the plan row: %v", err)
		}

		t.Logf("plan: %s", line.detail)

		out = append(out, line.detail)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read the plan rows: %v", err)
	}

	return out
}

// indexSearch matches a key lookup of the namespaces table: a search through
// the primary key index, never a full scan.
var indexSearch = regexp.MustCompile(`SEARCH namespaces USING (INTEGER PRIMARY KEY|COVERING INDEX|INDEX)`)

// checkAllPlan checks the All page statement reads through the primary key
// index: no full scan and no sort step.
func checkAllPlan(t *testing.T, s *Store) {
	t.Helper()

	const stmt = `EXPLAIN QUERY PLAN SELECT name, home_region, failover_version,
		created_at_ms, allow_forced_writes, allow_unlocked_writes
		FROM namespaces WHERE name > ? ORDER BY name LIMIT ?`

	plan := strings.Join(explainQueryPlan(t, s, stmt, "", 50), "\n")

	if strings.Contains(plan, "SCAN namespaces") {
		t.Fatalf("all page plan scans the table:\n%s", plan)
	}

	if strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("all page plan sorts instead of using the index:\n%s", plan)
	}

	if !indexSearch.MatchString(plan) {
		t.Fatalf("all page plan uses no key lookup:\n%s", plan)
	}
}

// checkSubsetPlan checks the subset page statement looks every name up
// through the primary key index instead of scanning the table.
func checkSubsetPlan(t *testing.T, s *Store, subset []namespace.Name) {
	t.Helper()

	names := make([]string, 0, len(subset))
	for _, name := range subset {
		names = append(names, name.String())
	}

	raw, err := json.Marshal(names)
	if err != nil {
		t.Fatalf("encode subset names: %v", err)
	}

	const stmt = `EXPLAIN QUERY PLAN SELECT name, home_region, failover_version,
		created_at_ms, allow_forced_writes, allow_unlocked_writes
		FROM namespaces WHERE name > ? AND name IN (SELECT value FROM json_each(?)) ORDER BY name LIMIT ?`

	plan := strings.Join(explainQueryPlan(t, s, stmt, "", string(raw), 50), "\n")

	if strings.Contains(plan, "SCAN namespaces") {
		t.Fatalf("subset page plan scans the table:\n%s", plan)
	}

	if !indexSearch.MatchString(plan) {
		t.Fatalf("subset page plan uses no key lookup:\n%s", plan)
	}
}
