package sqlite_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestOpenRefusesSchemaBeforeBaseline proves that a database that records a
// migration from before the baseline stops open with the named error and
// stays unchanged: the baseline replaced those migrations, so no binary can
// upgrade the database.
func TestOpenRefusesSchemaBeforeBaseline(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	const create = `CREATE TABLE schema_migrations (version INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL) STRICT`
	if _, execErr := conn.ExecContext(t.Context(), create); execErr != nil {
		t.Fatalf("create schema_migrations: %v", execErr)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	const old = 76

	insertFutureVersion(t, path, old)

	beforeVersions, beforeTables := newerSchemaSnapshot(t, path)

	_, err = sqlite.Open(t.Context(), path)
	if !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("open a database from before the baseline: err = %v, want ErrSchemaBeforeBaseline", err)
	}

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || before.DatabaseVersion != old || before.BaselineVersion != schema.BaselineVersion {
		t.Fatalf("refusal = %+v, want database version %d and baseline version %d", before, old, schema.BaselineVersion)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, path)

	if !slices.Equal(afterVersions, beforeVersions) || !slices.Equal(afterTables, beforeTables) {
		t.Fatalf(
			"after refusal: versions %v, tables %v, want %v, %v",
			afterVersions,
			afterTables,
			beforeVersions,
			beforeTables,
		)
	}
}
