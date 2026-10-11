package sqlite_test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/store/sqlite"
)

func TestInspect(t *testing.T) {
	t.Parallel()

	t.Run("fresh database", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("write empty database: %v", err)
		}

		names, newest := listMigrationNames(t)

		checkInspectState(t, path, wantInspection{database: 0, binary: newest, pending: names, unknown: nil})
	})

	t.Run("old database", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		seedAt0008WithStacks(t, path)

		names, newest := listMigrationNames(t)

		checkInspectState(t, path, wantInspection{database: 8, binary: newest, pending: names[8:], unknown: nil})
	})

	t.Run("up-to-date database", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		_, newest := listMigrationNames(t)

		checkInspectState(t, path, wantInspection{database: newest, binary: newest, pending: nil, unknown: nil})
	})

	t.Run("newer database", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		_, newest := listMigrationNames(t)

		insertFutureVersion(t, path, newest+1)
		checkInspectState(t, path, wantInspection{
			database: newest + 1,
			binary:   newest,
			pending:  nil,
			unknown:  []int64{newest + 1},
		})
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "missing.db")

		if _, err := sqlite.Inspect(t.Context(), path); err == nil {
			t.Fatal("inspect missing file: err = nil, want an error")
		} else if !strings.Contains(err.Error(), path) {
			t.Fatalf("inspect missing file error = %q, want it to name %q", err, path)
		}

		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("stat missing file: err = %v, want file to stay absent", statErr)
		}
	})
}

// wantInspection holds the expected schema state of one database.
type wantInspection struct {
	database int64
	binary   int64
	pending  []string
	unknown  []int64
}

// checkInspectState runs Inspect on path, checks the four reported values
// and proves the call wrote nothing: versions, tables and file bytes match
// the snapshot taken before the call.
func checkInspectState(t *testing.T, path string, want wantInspection) {
	t.Helper()

	beforeVersions, beforeTables := inspectSnapshot(t, path)
	beforeBytes := readDatabaseFile(t, path)

	got, err := sqlite.Inspect(t.Context(), path)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	if got.DatabaseVersion != want.database || got.BinaryVersion != want.binary {
		t.Fatalf(
			"versions = %d/%d, want %d/%d",
			got.DatabaseVersion,
			got.BinaryVersion,
			want.database,
			want.binary,
		)
	}

	if !slices.Equal(got.Pending, want.pending) {
		t.Fatalf("pending = %v, want %v", got.Pending, want.pending)
	}

	if !slices.Equal(got.Unknown, want.unknown) {
		t.Fatalf("unknown = %v, want %v", got.Unknown, want.unknown)
	}

	afterVersions, afterTables := inspectSnapshot(t, path)
	afterBytes := readDatabaseFile(t, path)

	if !slices.Equal(afterVersions, beforeVersions) {
		t.Fatalf("versions after inspect = %v, want %v", afterVersions, beforeVersions)
	}

	if !slices.Equal(afterTables, beforeTables) {
		t.Fatalf("tables after inspect = %v, want %v", afterTables, beforeTables)
	}

	if !bytes.Equal(afterBytes, beforeBytes) {
		t.Fatal("database file changed after inspect, want it unchanged")
	}

	if want.database == 0 && slices.Contains(afterTables, "schema_migrations") {
		t.Fatal("inspect created schema_migrations, want no such table")
	}
}

// listMigrationNames returns the embedded migration file names in version
// order with the newest version.
func listMigrationNames(t *testing.T) ([]string, int64) {
	t.Helper()

	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	var names []string

	var newest int64

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		version, parseErr := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		if parseErr != nil {
			t.Fatalf("parse migration version %q: %v", entry.Name(), parseErr)
		}

		names = append(names, entry.Name())

		if version > newest {
			newest = version
		}
	}

	slices.Sort(names)

	return names, newest
}

// inspectSnapshot reads the recorded versions (nil when schema_migrations is
// absent) and the table names of the database at path.
func inspectSnapshot(t *testing.T, path string) ([]int64, []string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
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

// readDatabaseFile returns the raw bytes of the database file at path.
func readDatabaseFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	return data
}
