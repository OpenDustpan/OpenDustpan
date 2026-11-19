package postgres

import (
	"fmt"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// seedUnitFilterBench fills a new namespace with count units and changes
// change rows for each unit, with plain SQL, so that a benchmark at 100,000
// units does not wait for 100,000 store writes. The change rows of unit n are
// at 1,000 + n*changes + c milliseconds.
func seedUnitFilterBench(b *testing.B, s *Store, count, changes int) namespace.Namespace {
	b.Helper()

	ns := newLockNamespace()
	ns.Name = "bench"

	if err := s.CreateNamespace(b.Context(), ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status,
		                         resource_count)
		 SELECT 'bench', 'pulumi', 'u' || n, 'unit-' || lpad(n::text, 8, '0'), 'u' || n, 1000 + n, 'ok', 1
		 FROM generate_series(1, $1::int) AS n`, []any{count}},
		{`INSERT INTO inventory_changes (namespace_id, seq, at_ms, tool, unit_id, name, display_name,
		                               status, collection, kind, resource_count)
		 SELECT $3, (n - 1) * $2::int + c, 1000 + (n - 1) * $2::int + c, 'pulumi', 'unit-' || lpad(n::text, 8, '0'),
		        'u' || n, 'u' || n, 'ok', '', 'update', 1
		 FROM generate_series(1, $1::int) AS n, generate_series(1, $2::int) AS c`, []any{count, changes, ns.ID.String()}},
		{
			`INSERT INTO inventory_change_heads (namespace_id, last_seq) VALUES ($3, $1::int * $2::int)`,
			[]any{count, changes, ns.ID.String()},
		},
	}

	for _, stmt := range stmts {
		if _, err := s.pool.Exec(b.Context(), stmt.sql, stmt.args...); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	if _, err := s.pool.Exec(b.Context(), "VACUUM ANALYZE"); err != nil {
		b.Fatalf("analyze: %v", err)
	}

	return ns
}

// BenchmarkUnitFilters measures the unit list at a past time and the locked
// filter, on a namespace where no unit is locked. Each case is the count of
// units and the count of change rows for each unit.
func BenchmarkUnitFilters(b *testing.B) {
	for _, shape := range []struct{ units, changes int }{
		{1_000, 1}, {10_000, 1}, {100_000, 1}, {2_000, 1}, {2_000, 50}, {2_000, 200},
	} {
		benchUnitFilterShape(b, shape.units, shape.changes)
	}
}

// benchUnitFilterShape runs the cases of BenchmarkUnitFilters on a new store
// with count units and changes change rows for each unit.
func benchUnitFilterShape(b *testing.B, units, changes int) {
	b.Helper()

	s := benchStore(b)

	ns := seedUnitFilterBench(b, s, units, changes)
	name := fmt.Sprintf("%dx%d", units, changes)
	at := time.UnixMilli(1000 + int64(units*changes)/2)

	q := inventory.UnitQuery{
		Namespace: ns.Name, After: "", Labels: nil, Units: nil, RestrictUnits: false,
		RestrictStates: true, States: []inventory.State{inventory.StateLocked}, Limit: 100,
	}

	if os.Getenv("DUSTPAN_BENCH_EXPLAIN") != "" {
		explainUnitFilters(b, s, q, name)
	}

	b.Run("at/"+name, func(b *testing.B) {
		for b.Loop() {
			if _, err := s.ViewAt(b.Context(), ns.Name, at); err != nil {
				b.Fatalf("ViewAt: %v", err)
			}
		}
	})

	b.Run("locked/"+name, func(b *testing.B) {
		for b.Loop() {
			if _, err := s.QueryUnits(b.Context(), q); err != nil {
				b.Fatalf("QueryUnits: %v", err)
			}
		}
	})
}

// explainUnitFilters logs the plan of the locked filter. Set
// DUSTPAN_BENCH_EXPLAIN to read it.
func explainUnitFilters(b *testing.B, s *Store, q inventory.UnitQuery, name string) {
	b.Helper()

	query, args := unitPageQuery(q)

	rows, err := s.pool.Query(b.Context(), "EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) "+query, args...)
	if err != nil {
		b.Fatalf("explain: %v", err)
	}

	defer rows.Close()

	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			b.Fatalf("scan plan: %v", err)
		}

		b.Logf("locked/%s plan: %s", name, line)
	}
}
