package postgres_test

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/store/postgres"
)

func TestInspect(t *testing.T) {
	t.Parallel()

	t.Run("fresh database", func(t *testing.T) {
		t.Parallel()

		migrated := isolatedURL(t, baseURL(t))
		names, newest := listMigrationNames(t)

		checkInspectState(t, migrated, wantInspection{database: 0, binary: newest, pending: names, unknown: nil})
	})

	t.Run("old database", func(t *testing.T) {
		t.Parallel()

		migrated := isolatedURL(t, baseURL(t))
		seedAt0008WithStacks(t, migrated)

		names, newest := listMigrationNames(t)

		checkInspectState(t, migrated, wantInspection{database: 8, binary: newest, pending: names[8:], unknown: nil})
	})

	t.Run("up-to-date database", func(t *testing.T) {
		t.Parallel()

		migrated := isolatedURL(t, baseURL(t))

		s, err := postgres.Open(t.Context(), migrated)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		s.Close()

		_, newest := listMigrationNames(t)

		checkInspectState(t, migrated, wantInspection{database: newest, binary: newest, pending: nil, unknown: nil})
	})

	t.Run("newer database", func(t *testing.T) {
		t.Parallel()

		migrated := isolatedURL(t, baseURL(t))

		s, err := postgres.Open(t.Context(), migrated)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		s.Close()

		_, newest := listMigrationNames(t)

		insertFutureVersion(t, migrated, newest+1)
		checkInspectState(t, migrated, wantInspection{
			database: newest + 1,
			binary:   newest,
			pending:  nil,
			unknown:  []int64{newest + 1},
		})
	})
}

// baseURL returns the shared PostgreSQL URL or skips the test without it.
func baseURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	return base
}

// wantInspection holds the expected schema state of one database.
type wantInspection struct {
	database int64
	binary   int64
	pending  []string
	unknown  []int64
}

// checkInspectState runs Inspect on migrated, checks the four reported values
// and proves the call wrote nothing: versions and tables match the snapshot
// taken before the call.
func checkInspectState(t *testing.T, migrated string, want wantInspection) {
	t.Helper()

	beforeVersions, beforeTables := inspectSnapshot(t, migrated)

	got, err := postgres.Inspect(t.Context(), migrated)
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

	afterVersions, afterTables := inspectSnapshot(t, migrated)

	if !slices.Equal(afterVersions, beforeVersions) {
		t.Fatalf("versions after inspect = %v, want %v", afterVersions, beforeVersions)
	}

	if !slices.Equal(afterTables, beforeTables) {
		t.Fatalf("tables after inspect = %v, want %v", afterTables, beforeTables)
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
