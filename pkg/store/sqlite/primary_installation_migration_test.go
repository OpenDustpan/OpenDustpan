package sqlite_test

import (
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// primaryInstallationVersion is the version of the migration that renames the
// home_region column of a namespace to primary_installation.
const primaryInstallationVersion = 145

// TestPrimaryInstallationMigration applies the migrations before the rename,
// plants a namespace with a value in home_region, then opens the store, which
// applies the rest. The namespace keeps the value as its primary installation.
func TestPrimaryInstallationMigration(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= primaryInstallationVersion })
	path := filepath.Join(t.TempDir(), "primaryinstallation.db")

	applyTagMigrations(ctx, t, path, before)

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	id := namespace.NewID()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
			VALUES ('shop', 'eu-west', 1, 0, ?)`, id.String())
	if err != nil {
		t.Fatalf("plant namespace: %v", err)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
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

	got, err := s.GetNamespace(ctx, "shop")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.PrimaryInstallation != "eu-west" || got.ID != id {
		t.Fatalf("namespace = %+v, want primary installation eu-west and ID %s", got, id)
	}
}
