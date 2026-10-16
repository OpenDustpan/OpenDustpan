package baseline_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

	body := committedBaseline(t, "sqlite").body
	ctx := t.Context()

	db := openSQLite(t)
	execSQLite(ctx, t, db, schema.BaselineName, body)

	if got := dumpSQLite(ctx, t, db); got != body {
		t.Fatalf("the committed baseline differs from the dump of its schema; "+
			"run make regenerate-baseline, do not edit the file:\n%s", firstDifference(body, got))
	}
}

func TestPostgresBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envPostgres)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL round trip", envPostgres)
	}

	file := committedBaseline(t, "postgres")
	body := file.body
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

	if got := dumpPostgres(t, base, fresh.name); got != body {
		t.Fatalf("the committed baseline differs from the dump of its schema; "+
			"run make regenerate-baseline, do not edit the file:\n%s", firstDifference(body, got))
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
