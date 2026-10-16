package baseline_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// committedBaseline reads the baseline file of dialect from the repository
// and returns its body. It fails the test when the file does not start with
// the minimum binary version line.
func committedBaseline(t *testing.T, dialect string) string {
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

	return baselineBody(t, string(data))
}

func TestSQLiteBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	body := committedBaseline(t, "sqlite")
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

	body := committedBaseline(t, "postgres")
	ctx := t.Context()

	fresh := newPostgresSchema(t, base)
	conn := connectPostgres(t, fresh.url)

	var serverNum int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&serverNum); err != nil {
		t.Fatalf("read the server version: %v", err)
	}

	if dumpMajor := pgDumpMajor(t); dumpMajor < serverNum/10000 {
		t.Skipf("pg_dump %d cannot dump a PostgreSQL %d server; install pg_dump %d or later",
			dumpMajor, serverNum/10000, serverNum/10000)
	}

	execPostgres(ctx, t, conn, schema.BaselineName, body)

	if got := dumpPostgres(t, base, fresh.name); got != body {
		t.Fatalf("the committed baseline differs from the dump of its schema; "+
			"run make regenerate-baseline, do not edit the file:\n%s", firstDifference(body, got))
	}
}

// pgDumpVersion matches the major version in the output of pg_dump --version.
var pgDumpVersion = regexp.MustCompile(`\(PostgreSQL\) (\d+)`)

// pgDumpMajor returns the major version of pg_dump, or fails the test when
// pg_dump is not installed.
func pgDumpMajor(t *testing.T) int {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "pg_dump", "--version").Output()
	if err != nil {
		t.Fatalf("run pg_dump --version: %v; install the PostgreSQL client", err)
	}

	match := pgDumpVersion.FindSubmatch(out)
	if match == nil {
		t.Fatalf("pg_dump --version answered %q, want a PostgreSQL version", out)
	}

	major, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatalf("parse the pg_dump version %q: %v", match[1], err)
	}

	return major
}
