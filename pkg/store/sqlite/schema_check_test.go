package sqlite_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// recordRow is the row of the schema file in schema_migrations.
type recordRow struct {
	name   string
	digest string
}

// readRecordRow reads the name and the SHA-256 of the schema file row of the
// database at path.
func readRecordRow(t *testing.T, path string) recordRow {
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

	var row recordRow

	err = conn.QueryRowContext(t.Context(), `SELECT name, sha256 FROM schema_migrations WHERE version = ?`,
		schema.BaselineVersion).Scan(&row.name, &row.digest)
	if err != nil {
		t.Fatalf("read the schema file row: %v", err)
	}

	return row
}

// TestOpenRecordsTheSchemaFile proves that a new database records the schema
// file under its version and its name, with the SHA-256 of the file.
func TestOpenRecordsTheSchemaFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	row := readRecordRow(t, path)
	if row.name != schema.BaselineName || !schema.ValidSHA256(row.digest) {
		t.Fatalf("schema file row = %+v, want the name %s and a SHA-256", row, schema.BaselineName)
	}

	versions, _ := newerSchemaSnapshot(t, path)
	if !slices.Equal(versions, []int64{schema.BaselineVersion}) {
		t.Fatalf("recorded versions = %v, want [%d]", versions, schema.BaselineVersion)
	}
}

// TestOpenRefusesAnotherSchemaFile changes the row of the schema file as
// another build of the schema file would have written it, and proves that
// open refuses the database with schema.ErrSchemaChanged and changes nothing.
func TestOpenRefusesAnotherSchemaFile(t *testing.T) {
	t.Parallel()

	other := sha256.Sum256([]byte("CREATE TABLE other (id INTEGER);"))

	cases := map[string]string{
		"another sum":         `UPDATE schema_migrations SET sha256 = '` + hex.EncodeToString(other[:]) + `'`,
		"another name":        `UPDATE schema_migrations SET name = '1000_other.sql'`,
		"no sum":              `UPDATE schema_migrations SET sha256 = ''`,
		"upper-case sum":      `UPDATE schema_migrations SET sha256 = upper(sha256)`,
		"sum with a line end": `UPDATE schema_migrations SET sha256 = substr(sha256, 1, 62) || char(13, 10)`,
		"no sum column":       `ALTER TABLE schema_migrations DROP COLUMN sha256`,
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "dustpan.db")

			s, err := sqlite.Open(t.Context(), path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}

			if closeErr := s.Close(); closeErr != nil {
				t.Fatalf("close: %v", closeErr)
			}

			execRaw(t, path, change)

			beforeVersions, beforeTables := newerSchemaSnapshot(t, path)

			_, err = sqlite.Open(t.Context(), path)
			if !errors.Is(err, schema.ErrSchemaChanged) {
				t.Fatalf("open a database of another schema file: err = %v, want ErrSchemaChanged", err)
			}

			if !strings.Contains(err.Error(), "start the server on an empty database") {
				t.Fatalf("refusal %q does not name the action", err)
			}

			afterVersions, afterTables := newerSchemaSnapshot(t, path)
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

	path := filepath.Join(t.TempDir(), "dustpan.db")

	execRaw(t, path,
		`CREATE TABLE schema_migrations (version INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL,
			min_binary_version TEXT NOT NULL DEFAULT '') STRICT`,
		`INSERT INTO schema_migrations (version, name, min_binary_version)
			VALUES (100, '0100_baseline.sql', 'v0.1.0'), (165, '0165_name_locks.sql', '')`,
		`CREATE TABLE namespaces (name TEXT NOT NULL PRIMARY KEY) STRICT`,
	)

	beforeVersions, beforeTables := newerSchemaSnapshot(t, path)

	_, err := sqlite.Open(t.Context(), path)

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || before.DatabaseVersion != 165 || before.BaselineVersion != schema.BaselineVersion {
		t.Fatalf("open a database of the migration chain: err = %v, want a refusal at version 165", err)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, path)
	if !slices.Equal(afterVersions, beforeVersions) || !slices.Equal(afterTables, beforeTables) {
		t.Fatalf("after refusal: versions %v, tables %v, want %v, %v",
			afterVersions, afterTables, beforeVersions, beforeTables)
	}
}

// execRaw runs the statements on the database at path, outside the store.
func execRaw(t *testing.T, path string, statements ...string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	for _, stmt := range statements {
		if _, execErr := conn.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("run %q: %v", stmt, execErr)
		}
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}
}
