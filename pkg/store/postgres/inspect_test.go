package postgres_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// baseURL returns the shared PostgreSQL URL or skips the test without it.
func baseURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	return base
}

// inspectSnapshot reads the recorded versions (nil when schema_migrations is
// absent) and the table names of the database at migrated.
func inspectSnapshot(t *testing.T, migrated string) ([]int64, []string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var exists bool

	err = conn.QueryRow(t.Context(), `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}

	var versions []int64

	if exists {
		versions = readSchemaVersions(t, conn)
	}

	return versions, readSchemaTables(t, conn)
}

// TestAppliedMigrations proves the store reports the applied rows ordered by
// version without migrating: an empty schema_migrations table means no rows,
// and a missing table reports nothing while staying missing.
func TestAppliedMigrations(t *testing.T) {
	t.Parallel()

	t.Run("migrated schema", func(t *testing.T) {
		t.Parallel()
		checkAppliedMigrated(t)
	})

	t.Run("missing table", func(t *testing.T) {
		t.Parallel()
		checkAppliedMissingTable(t)
	})
}

// checkAppliedMigrated proves AppliedMigrations on a migrated schema reports
// the embedded rows ordered, with names matching their versions, and changes
// nothing.
func checkAppliedMigrated(t *testing.T) {
	t.Helper()

	migrated := emptySchemaURL(t, baseURL(t))

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	beforeVersions, beforeTables := inspectSnapshot(t, migrated)

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

	afterVersions, afterTables := inspectSnapshot(t, migrated)
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

	migrated := emptySchemaURL(t, baseURL(t))

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	conn, connErr := pgx.Connect(t.Context(), migrated)
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, dropErr := conn.Exec(t.Context(), `DROP TABLE schema_migrations`); dropErr != nil {
		t.Fatalf("drop schema_migrations: %v", dropErr)
	}

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil {
		t.Fatalf("applied migrations without the table: %v", err)
	}

	if len(applied) != 0 {
		t.Fatalf("applied migrations = %v, want nothing without the table", applied)
	}

	if _, tables := inspectSnapshot(t, migrated); slices.Contains(tables, "schema_migrations") {
		t.Fatal("applied migrations created schema_migrations, want no such table")
	}
}

// TestEmbeddedMigrationsMatchSchema proves the adapter embeds exactly the
// shared schema list: the upgrade check verdict never depends on the
// dialect. Until the first tag, the list holds the name of the schema file
// only, and the adapter has no migrations directory.
func TestEmbeddedMigrationsMatchSchema(t *testing.T) {
	t.Parallel()

	names, newest := schema.EmbeddedMigrationNames()

	got, gotNewest, err := postgres.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}

	if !slices.Equal(got, names) || gotNewest != newest {
		t.Fatalf("embedded = %v/%d, want the schema list %v/%d", got, gotNewest, names, newest)
	}

	if !slices.Equal(names, []string{schema.BaselineName}) || newest != schema.BaselineVersion {
		t.Fatalf("schema list = %v/%d, want [%s]/%d", names, newest, schema.BaselineName, schema.BaselineVersion)
	}

	if _, err := os.Stat("migrations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat migrations = %v, want no migrations directory before the first tag", err)
	}
}
