package baseline_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// committedBaseline reads the baseline file of dialect from the repository
// and returns it split. It fails the test when the file does not start with
// the minimum binary version line.
func committedBaseline(t *testing.T, dialect string) baselineFile {
	t.Helper()

	path := filepath.Join("..", "..", dialect, "migrations", schema.BaselineName)

	//nolint:gosec // The path is the baseline file of a fixed dialect in this repository.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if !bytes.HasPrefix(data, []byte(minimumLine+"\n")) {
		t.Fatalf("%s does not start with %q", path, minimumLine)
	}

	return splitBaseline(t, string(data))
}

func TestSQLiteBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	// TODO: enable again at the first tag. Until then a change to the schema
	// edits the baseline by hand, so it no longer has the form of a dump.
	t.Skip("disabled until the first tag: the baseline is edited by hand until then")

	body := committedBaseline(t, "sqlite").body

	if !strings.HasPrefix(body, sqliteSchemaPrefix) {
		t.Fatalf("the committed baseline does not start with the version-table statements; " +
			"run make regenerate-baseline, do not edit the file")
	}

	want := strings.TrimPrefix(body, sqliteSchemaPrefix)
	ctx := t.Context()

	db := openSQLite(t)
	execSQLite(ctx, t, db, schema.BaselineName, body)

	if got := dumpSQLite(ctx, t, db); got != want {
		t.Fatalf("the committed baseline differs from the dump of its schema; "+
			"run make regenerate-baseline, do not edit the file:\n%s", firstDifference(want, got))
	}

	checkSQLiteVersionColumns(t, db)
}

func TestPostgresBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	// TODO: enable again at the first tag. Until then a change to the schema
	// edits the baseline by hand, so it no longer has the form of a dump.
	t.Skip("disabled until the first tag: the baseline is edited by hand until then")

	base := os.Getenv(envPostgres)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL round trip", envPostgres)
	}

	file := committedBaseline(t, "postgres")
	body := file.body

	if !strings.HasPrefix(body, postgresSchemaPrefix) {
		t.Fatalf("the committed baseline does not start with the version-table statements; " +
			"run make regenerate-baseline, do not edit the file")
	}

	want := strings.TrimPrefix(body, postgresSchemaPrefix)
	ctx := t.Context()

	fresh := newPostgresSchema(t, base)
	conn := connectPostgres(t, fresh.url)

	dumpMajor, server := pgDumpMajor(t), serverMajor(ctx, t, conn)
	if dumpMajor < server {
		t.Skipf("pg_dump %d cannot dump a PostgreSQL %d server: install postgresql-client-%d to run this test",
			dumpMajor, server, server)
	}

	if written := writtenDumpMajor(t, file.header); dumpMajor != written {
		t.Skipf("pg_dump %d wrote the baseline, and pg_dump is %d: install postgresql-client-%d to run this test",
			written, dumpMajor, written)
	}

	execPostgres(ctx, t, conn, schema.BaselineName, body)

	if got := postgresBody(t, base, fresh.name); got != want {
		t.Fatalf("the committed baseline differs from the dump of its schema; "+
			"run make regenerate-baseline, do not edit the file:\n%s", firstDifference(want, got))
	}

	checkPostgresVersionColumns(ctx, t, conn)
}

// checkSQLiteVersionColumns fails t unless the version table of db holds
// exactly the version, name, minimum and SHA-256 columns in order.
func checkSQLiteVersionColumns(t *testing.T, db *sql.DB) {
	t.Helper()

	rows, err := db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info('schema_migrations') ORDER BY cid`)
	if err != nil {
		t.Fatalf("read the version table columns: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var got []string

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("read the version table columns: %v", scanErr)
		}

		got = append(got, name)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the version table columns: %v", rowsErr)
	}

	want := []string{"version", "name", "min_binary_version", "sha256"}
	if !slices.Equal(got, want) {
		t.Fatalf("the version table columns are %v, want %v", got, want)
	}
}

// checkPostgresVersionColumns fails t unless the version table visible to
// conn holds exactly the version, name, minimum and SHA-256 columns in order.
func checkPostgresVersionColumns(ctx context.Context, t *testing.T, conn *pgx.Conn) {
	t.Helper()

	rows, err := conn.Query(ctx, `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("read the version table columns: %v", err)
	}

	defer rows.Close()

	var got []string

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("read the version table columns: %v", scanErr)
		}

		got = append(got, name)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the version table columns: %v", rowsErr)
	}

	want := []string{"version", "name", "min_binary_version", "sha256"}
	if !slices.Equal(got, want) {
		t.Fatalf("the version table columns are %v, want %v", got, want)
	}
}

// writtenDumpMajor returns the pg_dump major version that the header of
// the PostgreSQL baseline records.
func writtenDumpMajor(t *testing.T, header string) int {
	t.Helper()

	for line := range strings.SplitSeq(header, "\n") {
		if value, ok := strings.CutPrefix(line, pgDumpLine); ok {
			major, err := strconv.Atoi(value)
			if err != nil {
				t.Fatalf("parse the header line %q: %v", line, err)
			}

			return major
		}
	}

	t.Fatalf("the PostgreSQL baseline header has no %q line", pgDumpLine)

	return 0
}
