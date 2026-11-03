package baseline_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

// committedMigrations loads the committed migrations of dialect.
func committedMigrations(t *testing.T, dialect string) []migrate.Migration {
	t.Helper()

	all, err := migrate.Load(os.DirFS(filepath.Join("..", "..", dialect)), "migrations")
	if err != nil {
		t.Fatalf("load the %s migrations: %v", dialect, err)
	}

	if len(all) == 0 {
		t.Fatalf("no %s migrations", dialect)
	}

	return all
}

// checkKeptName fails the test unless a regeneration from the committed
// migrations of both dialects keeps the name of the committed baseline of
// dialect, and answers that name.
func checkKeptName(t *testing.T, dialect string) (string, []migrate.Migration) {
	t.Helper()

	committed := committedMigrations(t, dialect)
	name := baselineName(versionOf([][]migrate.Migration{
		committedMigrations(t, "sqlite"), committedMigrations(t, "postgres"),
	}))

	if len(committed) != 1 || committed[0].Name != name {
		t.Fatalf("a regeneration from %v names the baseline %s, want the committed name", committed, name)
	}

	return name, committed
}

// asInput answers content as the one input migration name.
func asInput(name, content string, version int64) []migrate.Migration {
	return []migrate.Migration{{Name: name, SQL: content, MinBinaryVersion: "", SHA256: "", Version: version}}
}

// TestSQLiteRegenerationIsStable runs the generator two times in a row. The
// first run takes the committed baseline, which already has the sha256
// column, as its input, and the second run takes the output of the first.
// Both runs give the same bytes, so a regeneration with no change of the
// schema keeps the SHA-256 of the file and forces no reset.
func TestSQLiteRegenerationIsStable(t *testing.T) {
	t.Parallel()

	name, committed := checkKeptName(t, "sqlite")

	first := generateSQLite(t, committed, name)
	second := generateSQLite(t, asInput(name, first, committed[0].Version), name)

	if second != first {
		t.Fatalf("the second regeneration differs from the first:\n%s", firstDifference(first, second))
	}
}

// TestPostgresRegenerationIsStable is TestSQLiteRegenerationIsStable for
// PostgreSQL. It needs a database and a pg_dump of the major version of the
// server.
func TestPostgresRegenerationIsStable(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envPostgres)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL regeneration test", envPostgres)
	}

	conn := connectPostgres(t, base)

	dumpMajor, server := pgDumpMajor(t), serverMajor(context.WithoutCancel(t.Context()), t, conn)
	if dumpMajor != server {
		t.Skipf("pg_dump %d differs from the PostgreSQL %d server: install postgresql-client-%d to run this test",
			dumpMajor, server, server)
	}

	name, committed := checkKeptName(t, "postgres")

	first := generatePostgres(t, base, committed, name)
	second := generatePostgres(t, base, asInput(name, first, committed[0].Version), name)

	if second != first {
		t.Fatalf("the second regeneration differs from the first:\n%s", firstDifference(first, second))
	}
}
