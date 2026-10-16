package sqlite_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// inspectSnapshot reads the recorded versions (nil when schema_migrations is
// absent) and the table names of the database at path.
func inspectSnapshot(t *testing.T, path string) ([]int64, []string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	var count int64

	row := conn.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	)

	if err := row.Scan(&count); err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}

	var versions []int64

	if count > 0 {
		versions = readSchemaVersions(t, conn)
	}

	return versions, readSchemaTables(t, conn)
}

// TestAppliedMigrations proves the store reports the applied rows ordered by
// version without migrating: an empty schema_migrations table means no rows,
// and a missing table reports nothing while staying missing.
func TestAppliedMigrations(t *testing.T) {
	t.Parallel()

	t.Run("migrated database", func(t *testing.T) {
		t.Parallel()
		checkAppliedMigrated(t)
	})

	t.Run("missing table", func(t *testing.T) {
		t.Parallel()
		checkAppliedMissingTable(t)
	})
}

// checkAppliedMigrated proves AppliedMigrations on a migrated database
// reports the embedded rows ordered, with names matching their versions,
// and changes nothing.
func checkAppliedMigrated(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	beforeVersions, beforeTables := inspectSnapshot(t, path)

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}

	wantNames, wantNewest := schema.EmbeddedMigrationNames()

	if len(applied) != len(wantNames) {
		t.Fatalf("applied holds %d rows, want the embedded %d", len(applied), len(wantNames))
	}

	checkAppliedRows(t, applied, wantNames)

	if got := applied[len(applied)-1].Version; got != wantNewest {
		t.Fatalf("newest applied version = %d, want %d", got, wantNewest)
	}

	afterVersions, afterTables := inspectSnapshot(t, path)
	if !slices.Equal(afterVersions, beforeVersions) || !slices.Equal(afterTables, beforeTables) {
		t.Fatal("applied migrations changed the database, want a read-only report")
	}
}

// checkAppliedRows proves every applied row names the embedded name at its
// position with the version its name parses to.
func checkAppliedRows(t *testing.T, applied []schema.AppliedMigration, wantNames []string) {
	t.Helper()

	for i, migration := range applied {
		if migration.Name != wantNames[i] {
			t.Fatalf("applied row %d = %q, want %q", i, migration.Name, wantNames[i])
		}

		version, err := schema.ParseMigrationVersion(migration.Name)
		if err != nil {
			t.Fatalf("applied name %q: %v", migration.Name, err)
		}

		if version != migration.Version {
			t.Fatalf("applied row %q records version %d, want %d",
				migration.Name, migration.Version, version)
		}
	}
}

// checkAppliedMissingTable proves AppliedMigrations without the
// schema_migrations table reports nothing and creates no table.
func checkAppliedMissingTable(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, connErr := openRawDB(path)
	if connErr != nil {
		t.Fatalf("open raw database: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	if _, dropErr := conn.ExecContext(t.Context(), `DROP TABLE schema_migrations`); dropErr != nil {
		t.Fatalf("drop schema_migrations: %v", dropErr)
	}

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil {
		t.Fatalf("applied migrations without the table: %v", err)
	}

	if len(applied) != 0 {
		t.Fatalf("applied migrations = %v, want nothing without the table", applied)
	}

	if _, tables := inspectSnapshot(t, path); slices.Contains(tables, "schema_migrations") {
		t.Fatal("applied migrations created schema_migrations, want no such table")
	}
}

// TestEmbeddedMigrationsMatchSchema proves the adapter embeds exactly the
// shared schema list: the upgrade check verdict never depends on the
// dialect. Add a migration to both dialects and to the schema list together.
func TestEmbeddedMigrationsMatchSchema(t *testing.T) {
	t.Parallel()

	names, newest := schema.EmbeddedMigrationNames()

	got, gotNewest, err := sqlite.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}

	if !slices.Equal(got, names) || gotNewest != newest {
		t.Fatalf("embedded = %v/%d, want the schema list %v/%d", got, gotNewest, names, newest)
	}

	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	var files []string

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}

	slices.Sort(files)

	if !slices.Equal(files, names) {
		t.Fatalf("migration files = %v, want the schema list %v", files, names)
	}
}
