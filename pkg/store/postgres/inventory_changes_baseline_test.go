package postgres_test

import (
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL change log baseline test", envURL)
	}

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= changeLogVersion })
	schemaURL := isolatedURL(t, base)

	applyTagMigrations(ctx, t, schemaURL, before)

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
		VALUES ('acme', 'eu-west', 1, 0, 'ns-acme');` + storetest.BaselineSeed

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	if _, err = conn.Exec(ctx, seed); err != nil {
		t.Fatalf("seed inventory: %v", err)
	}

	if err = conn.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	s := openURL(t, schemaURL)

	storetest.CheckBaseline(t, s)
}
