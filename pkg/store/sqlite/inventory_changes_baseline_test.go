package sqlite_test

import (
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// changeLogVersion is the version of the migration that adds the inventory
// change log.
const changeLogVersion = 108

// TestInventoryChangeLogBaseline applies the migrations before the change
// log, adds a live unit and a soft-deleted unit, then opens the store, which
// applies the rest. Each unit gets one baseline row, the counter continues
// after the last baseline row, and the view at a time after a baseline lists
// the unit.
func TestInventoryChangeLogBaseline(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= changeLogVersion })
	path := filepath.Join(t.TempDir(), "baseline.db")

	applyTagMigrations(ctx, t, path, before)

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
		VALUES ('acme', 'eu-west', 1, 0, 'ns-acme');` + storetest.BaselineSeed

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	if _, err = conn.ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}

	if err = conn.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	storetest.CheckBaseline(t, s)
}
