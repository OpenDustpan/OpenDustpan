package sqlite

import (
	"fmt"
	"path/filepath"
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

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "bench",
		PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(b.Context(), ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	id := ns.ID.String()

	stmts := []struct {
		sql  string
		args []any
	}{
		{
			`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		  INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status,
		                         resource_count)
		  SELECT 'bench', 'pulumi', 'u' || i, 'unit-' || printf('%08d', i), 'u' || i, 1000 + i, 'ok', 1 FROM n`,
			[]any{count},
		},
		{
			`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?1),
		       c(j) AS (SELECT 1 UNION ALL SELECT j + 1 FROM c WHERE j < ?2)
		  INSERT INTO inventory_changes (namespace_id, seq, at_ms, tool, unit_id, name, display_name,
		                               status, collection, kind, resource_count)
		  SELECT ?3, (i - 1) * ?2 + j, 1000 + (i - 1) * ?2 + j, 'pulumi', 'unit-' || printf('%08d', i),
		         'u' || i, 'u' || i, 'ok', '', 'update', 1 FROM n, c`,
			[]any{count, changes, id},
		},
		{`INSERT INTO inventory_change_heads (namespace_id, last_seq) VALUES (?3, ?1 * ?2)`, []any{count, changes, id}},
		{`ANALYZE`, nil},
	}

	for _, stmt := range stmts {
		if _, err := s.conn.ExecContext(b.Context(), stmt.sql, stmt.args...); err != nil {
			b.Fatalf("seed: %v", err)
		}
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

	s, err := Open(b.Context(), filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	ns := seedUnitFilterBench(b, s, units, changes)
	name := fmt.Sprintf("%dx%d", units, changes)
	at := time.UnixMilli(1000 + int64(units*changes)/2)

	q := inventory.UnitQuery{
		Namespace: ns.Name, After: "", Labels: nil, Units: nil, RestrictUnits: false,
		RestrictStates: true, States: []inventory.State{inventory.StateLocked}, Limit: 100,
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
