package postgres_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// execSchema runs the statements on the schema of migrated, outside the
// store.
func execSchema(t *testing.T, migrated string, statements ...string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	for _, stmt := range statements {
		if _, execErr := conn.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("run %q: %v", stmt, execErr)
		}
	}

	if closeErr := conn.Close(t.Context()); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

// openAndClose opens the store at migrated once, so that it applies the
// schema file, and closes it.
func openAndClose(t *testing.T, migrated string) {
	t.Helper()

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	s.Close()
}

// TestOpenRecordsTheSchemaFile proves that a new database records the schema
// file under its version and its name, with the SHA-256 of the file.
func TestOpenRecordsTheSchemaFile(t *testing.T) {
	t.Parallel()

	migrated := emptySchemaURL(t, baseURL(t))
	openAndClose(t, migrated)

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var name, digest string

	err = conn.QueryRow(t.Context(), `SELECT name, sha256 FROM schema_migrations WHERE version = $1`,
		schema.BaselineVersion).Scan(&name, &digest)
	if err != nil {
		t.Fatalf("read the schema file row: %v", err)
	}

	if name != schema.BaselineName || !schema.ValidSHA256(digest) {
		t.Fatalf("schema file row = %s %q, want the name %s and a SHA-256", name, digest, schema.BaselineName)
	}

	if versions := readSchemaVersions(t, conn); !slices.Equal(versions, []int64{schema.BaselineVersion}) {
		t.Fatalf("recorded versions = %v, want [%d]", versions, schema.BaselineVersion)
	}
}

// TestOpenRefusesAnotherSchemaFile changes the row of the schema file as
// another build of the schema file would have written it, and proves that
// open refuses the database with schema.ErrSchemaChanged and changes nothing.
func TestOpenRefusesAnotherSchemaFile(t *testing.T) {
	t.Parallel()

	other := sha256.Sum256([]byte("CREATE TABLE other (id bigint);"))

	cases := map[string]string{
		"another sum":         `UPDATE schema_migrations SET sha256 = '` + hex.EncodeToString(other[:]) + `'`,
		"another name":        `UPDATE schema_migrations SET name = '1000_other.sql'`,
		"no sum":              `UPDATE schema_migrations SET sha256 = ''`,
		"upper-case sum":      `UPDATE schema_migrations SET sha256 = upper(sha256)`,
		"sum with a line end": `UPDATE schema_migrations SET sha256 = left(sha256, 62) || E'\r\n'`,
		"no sum column":       `ALTER TABLE schema_migrations DROP COLUMN sha256`,
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			migrated := emptySchemaURL(t, baseURL(t))
			openAndClose(t, migrated)
			execSchema(t, migrated, change)

			beforeVersions, beforeTables := newerSchemaSnapshot(t, migrated)

			_, err := postgres.Open(t.Context(), migrated)
			if !errors.Is(err, schema.ErrSchemaChanged) {
				t.Fatalf("open a database of another schema file: err = %v, want ErrSchemaChanged", err)
			}

			if !strings.Contains(err.Error(), "start the server on an empty database") {
				t.Fatalf("refusal %q does not name the action", err)
			}

			afterVersions, afterTables := newerSchemaSnapshot(t, migrated)
			if !slices.Equal(afterVersions, beforeVersions) || !slices.Equal(afterTables, beforeTables) {
				t.Fatalf("after refusal: versions %v, tables %v, want %v, %v",
					afterVersions, afterTables, beforeVersions, beforeTables)
			}
		})
	}
}

// TestOpenRefusesTheMigrationChain builds the version table of a database
// that the numbered migrations made before the schema file, and proves that
// open refuses it with schema.ErrSchemaBeforeBaseline and changes nothing.
func TestOpenRefusesTheMigrationChain(t *testing.T) {
	t.Parallel()

	migrated := emptySchemaURL(t, baseURL(t))

	execSchema(t, migrated,
		`CREATE TABLE schema_migrations (version BIGINT NOT NULL PRIMARY KEY, name TEXT NOT NULL,
			min_binary_version TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO schema_migrations (version, name, min_binary_version)
			VALUES (100, '0100_baseline.sql', 'v0.1.0'), (165, '0165_name_locks.sql', '')`,
		`CREATE TABLE namespaces (name text PRIMARY KEY)`,
	)

	beforeVersions, beforeTables := newerSchemaSnapshot(t, migrated)

	_, err := postgres.Open(t.Context(), migrated)

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || before.DatabaseVersion != 165 || before.BaselineVersion != schema.BaselineVersion {
		t.Fatalf("open a database of the migration chain: err = %v, want a refusal at version 165", err)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, migrated)
	if !slices.Equal(afterVersions, beforeVersions) || !slices.Equal(afterTables, beforeTables) {
		t.Fatalf("after refusal: versions %v, tables %v, want %v, %v",
			afterVersions, afterTables, beforeVersions, beforeTables)
	}
}
