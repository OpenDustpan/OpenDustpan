package postgres_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// TestOpenRefusesSchemaBeforeBaseline proves that a database that records a
// migration from before the baseline stops open with the named error and
// stays unchanged: the baseline replaced those migrations, so no binary can
// upgrade the database.
func TestOpenRefusesSchemaBeforeBaseline(t *testing.T) {
	t.Parallel()

	migrated := isolatedURL(t, baseURL(t))

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	const create = `CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, name TEXT NOT NULL)`
	if _, execErr := conn.Exec(t.Context(), create); execErr != nil {
		t.Fatalf("create schema_migrations: %v", execErr)
	}

	if closeErr := conn.Close(t.Context()); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	const old = 76

	insertFutureVersion(t, migrated, old)

	beforeVersions, beforeTables := newerSchemaSnapshot(t, migrated)

	_, err = postgres.Open(t.Context(), migrated)
	if !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("open a database from before the baseline: err = %v, want ErrSchemaBeforeBaseline", err)
	}

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || before.DatabaseVersion != old || before.BaselineVersion != schema.BaselineVersion {
		t.Fatalf("refusal = %+v, want database version %d and baseline version %d", before, old, schema.BaselineVersion)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, migrated)

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
