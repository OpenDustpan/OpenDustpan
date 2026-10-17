// Package baseline_test writes and checks the baseline migration of each
// store dialect.
//
// The write tests run only when DUSTPAN_TEST_BASELINE_IN names a directory
// that holds the numbered migrations from before the baseline, in
// pkg/store/<dialect>/migrations/. scripts/regenerate-baseline.sh sets it up
// and runs them. For each dialect a write test applies every old migration
// in order to an empty database, then the statements of
// testdata/after-<dialect>.sql, and writes one baseline file: the
// version-table statements below, then the schema and the rows of that
// database without the version table. It then applies the baseline to a
// second empty database and fails when the schema or the rows of the two
// databases differ.
//
// The round-trip tests run in every test run. Each one applies the committed
// baseline to an empty database and fails when the dump of that database
// differs from the baseline, so the file stays what the generator writes.
package baseline_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite" // The driver of the SQLite adapter.

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// Variables that the regeneration script sets.
const (
	envIn       = "DUSTPAN_TEST_BASELINE_IN"
	envOut      = "DUSTPAN_TEST_BASELINE_OUT"
	envRef      = "DUSTPAN_TEST_BASELINE_REF"
	envPostgres = "DUSTPAN_TEST_POSTGRES_URL"
)

// schemaSuffixBytes is the number of random bytes in a schema name.
const schemaSuffixBytes = 8

// minimumLine is the first line of each baseline file. The migration
// loader reads the minimum binary version of the baseline from it.
const minimumLine = "-- minimum-binary-version: v0.1.0"

// pgDumpLine starts the header line of the PostgreSQL baseline that records
// the major version of the pg_dump that wrote it.
const pgDumpLine = "-- pg_dump major version: "

// versionTable is the table that records the applied migrations. The dumps
// leave it out, and each baseline starts with its own statements for it
// instead: the migration runner creates the table before it applies the
// baseline, so a dumped CREATE TABLE would fail there.
const versionTable = "schema_migrations"

// sqliteSchemaPrefix holds the statements that start the SQLite baseline
// body: the version-table statements of the folded migration that added the
// minimum binary version column.
const sqliteSchemaPrefix = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER NOT NULL PRIMARY KEY,
    name    TEXT    NOT NULL
) STRICT;

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

`

// postgresSchemaPrefix holds the statements that start the PostgreSQL
// baseline body: the version-table statements of the folded migration that
// added the minimum binary version column.
const postgresSchemaPrefix = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT NOT NULL PRIMARY KEY,
    name    TEXT   NOT NULL
);

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

`

// header returns the comment block that starts the baseline file of dialect,
// written from the migrations of ref. extra holds more comment lines, each
// with its line end, or nothing. The generator writes the rest.
func header(dialect, ref, extra string) string {
	return minimumLine + `
-- Generated file. Do not edit it by hand. make regenerate-baseline writes it
-- from the ` + dialect + ` migrations of ` + ref + `.
-- This baseline replaced every migration before it, as a one-time exception
-- to the rule that an applied migration never changes. No binary opens a
-- database or restores a backup from before it.
` + extra
}

// baselineFile is a baseline file split in its header and its body.
type baselineFile struct {
	header string
	body   string
}

// splitBaseline splits a baseline file. The header is every line up to the
// first empty line, and each of them is a comment. The body is the rest.
func splitBaseline(t *testing.T, file string) baselineFile {
	t.Helper()

	head, body, ok := strings.Cut(file, "\n\n")
	if !ok {
		t.Fatal("the baseline has no empty line after its header")
	}

	for line := range strings.SplitSeq(head, "\n") {
		if !strings.HasPrefix(line, "--") {
			t.Fatalf("header line %q is not a comment", line)
		}
	}

	return baselineFile{header: head + "\n", body: body}
}

// paths holds the directory of the old migrations and the root of the
// repository that gets the baseline files.
type paths struct {
	in  string
	out string
}

// dirs returns the input and output directories, or skips the test.
func dirs(t *testing.T) paths {
	t.Helper()

	in := os.Getenv(envIn)
	if in == "" {
		t.Skipf("set %s to write the baseline migrations", envIn)
	}

	out := os.Getenv(envOut)
	if out == "" {
		t.Fatalf("set %s to the root of the repository", envOut)
	}

	return paths{in: in, out: out}
}

// oldMigrations returns the migrations of dialect in the input directory,
// ordered by version, without a baseline that the input may already hold.
func oldMigrations(t *testing.T, in, dialect string) []migrate.Migration {
	t.Helper()

	all, err := migrate.Load(os.DirFS(filepath.Join(in, "pkg", "store", dialect)), "migrations")
	if err != nil {
		t.Fatalf("load %s migrations: %v", dialect, err)
	}

	all = slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= schema.BaselineVersion })
	if len(all) == 0 {
		t.Fatalf("no %s migrations before the baseline in %s", dialect, in)
	}

	return all
}

// after returns the statements that run after the old migrations.
func after(t *testing.T, dialect string) string {
	t.Helper()

	//nolint:gosec // The path is a testdata file of this package, named by a fixed dialect.
	data, err := os.ReadFile(filepath.Join("testdata", "after-"+dialect+".sql"))
	if err != nil {
		t.Fatalf("read the after statements of %s: %v", dialect, err)
	}

	return string(data)
}

// writeBaseline writes body as the baseline file of dialect, after the
// header with the extra header lines.
func writeBaseline(t *testing.T, out, dialect, extra, body string) {
	t.Helper()

	ref := os.Getenv(envRef)
	if ref == "" {
		t.Fatalf("set %s to the Git ref of the old migrations", envRef)
	}

	path := filepath.Join(out, "pkg", "store", dialect, "migrations", schema.BaselineName)

	content := header(dialect, ref, extra) + "\n" + body

	//nolint:gosec // out is the repository root that the regeneration script passes; the test writes only there.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	sum := sha256.Sum256([]byte(content))

	//nolint:gosec // The hash file belongs next to the baseline file the test writes.
	if err := os.WriteFile(path+".sha256", []byte(hex.EncodeToString(sum[:])+"\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path+".sha256", err)
	}
}

func TestWriteSQLiteBaseline(t *testing.T) {
	t.Parallel()

	dir := dirs(t)
	ctx := t.Context()

	old := openSQLite(t)

	for _, m := range oldMigrations(t, dir.in, "sqlite") {
		execSQLite(ctx, t, old, m.Name, m.SQL)
	}

	execSQLite(ctx, t, old, "after-sqlite.sql", after(t, "sqlite"))

	dumped := dumpSQLite(ctx, t, old)
	body := sqliteSchemaPrefix + dumped

	fresh := openSQLite(t)
	execSQLite(ctx, t, fresh, schema.BaselineName, body)

	if got := dumpSQLite(ctx, t, fresh); got != dumped {
		t.Fatalf("the baseline gives another schema than the migrations:\n%s", firstDifference(dumped, got))
	}

	checkSQLiteVersionTable(t, old, fresh)

	writeBaseline(t, dir.out, "sqlite", "", body)
}

func TestWritePostgresBaseline(t *testing.T) {
	t.Parallel()

	dir := dirs(t)

	base := os.Getenv(envPostgres)
	if base == "" {
		t.Fatalf("set %s to write the PostgreSQL baseline", envPostgres)
	}

	ctx := t.Context()

	oldSchema := newPostgresSchema(t, base)
	old := connectPostgres(t, oldSchema.url)

	// The output of pg_dump can change between major versions, so the
	// baseline records the version, and only a pg_dump of the server's
	// major version writes it.
	dumpMajor, server := pgDumpMajor(t), serverMajor(ctx, t, old)
	if dumpMajor != server {
		t.Fatalf("pg_dump %d differs from the PostgreSQL %d server: "+
			"install postgresql-client-%d and put its pg_dump first in PATH", dumpMajor, server, server)
	}

	for _, m := range oldMigrations(t, dir.in, "postgres") {
		execPostgres(ctx, t, old, m.Name, m.SQL)
	}

	execPostgres(ctx, t, old, "after-postgres.sql", after(t, "postgres"))

	dumped := dumpPostgres(t, base, oldSchema.name)
	body := postgresSchemaPrefix + dumped

	freshSchema := newPostgresSchema(t, base)
	fresh := connectPostgres(t, freshSchema.url)
	execPostgres(ctx, t, fresh, schema.BaselineName, body)

	if got := dumpPostgres(t, base, freshSchema.name); got != dumped {
		t.Fatalf("the baseline gives another schema than the migrations:\n%s", firstDifference(dumped, got))
	}

	checkPostgresVersionTable(ctx, t, old, fresh)

	writeBaseline(t, dir.out, "postgres", pgDumpLine+strconv.Itoa(dumpMajor)+"\n", body)
}

// serverMajor returns the major version of the PostgreSQL server of conn.
func serverMajor(ctx context.Context, t *testing.T, conn *pgx.Conn) int {
	t.Helper()

	var num int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&num); err != nil {
		t.Fatalf("read the server version: %v", err)
	}

	return num / serverVersionDivisor
}

// serverVersionDivisor turns server_version_num, for example 180006, into
// the major version.
const serverVersionDivisor = 10000

// pgDumpVersion matches the major version in the output of pg_dump --version.
var pgDumpVersion = regexp.MustCompile(`\(PostgreSQL\) (\d+)`)

// pgDumpMajor returns the major version of the pg_dump first in PATH, or
// fails the test when pg_dump is not installed.
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

// firstDifference returns the first line where want and got differ.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	for i := range max(len(wantLines), len(gotLines)) {
		var w, g string

		if i < len(wantLines) {
			w = wantLines[i]
		}

		if i < len(gotLines) {
			g = gotLines[i]
		}

		if w != g {
			return fmt.Sprintf("line %d: want %q, got %q", i+1, w, g)
		}
	}

	return "no line differs"
}

// openSQLite opens an empty SQLite database with the foreign keys on, as
// the adapter opens it.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()

	query := url.Values{}
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{
		Scheme:   "file",
		Path:     filepath.Join(t.TempDir(), "baseline.db"),
		RawQuery: query.Encode(),
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close sqlite: %v", closeErr)
		}
	})

	return db
}

// execSQLite runs the statements of one file in one transaction, as the
// adapter applies a migration.
func execSQLite(ctx context.Context, t *testing.T, db *sql.DB, name, statements string) {
	t.Helper()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin %s: %v", name, err)
	}

	if _, execErr := tx.ExecContext(ctx, statements); execErr != nil {
		_ = tx.Rollback() //nolint:errcheck // The test fails on the statement error.

		t.Fatalf("apply %s: %v", name, execErr)
	}

	if commitErr := tx.Commit(); commitErr != nil {
		t.Fatalf("commit %s: %v", name, commitErr)
	}
}

// sqliteObject is one schema object that sqlite_master records.
type sqliteObject struct {
	kind  string
	name  string
	table string
	sql   string
}

// dumpSQLite returns the schema of db as its CREATE statements, tables
// first in the order of their creation, then the indexes, triggers and
// views, and then the rows of each table as INSERT statements.
func dumpSQLite(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()

	objects := sqliteObjects(ctx, t, db)

	rank := map[string]int{"table": 0, "index": 1, "trigger": 2, "view": 3}

	slices.SortStableFunc(objects, func(a, b sqliteObject) int { return rank[a.kind] - rank[b.kind] })

	var b strings.Builder

	var tables []string

	for _, o := range objects {
		if o.table == versionTable {
			continue // The baseline starts with its own version-table statements.
		}

		b.WriteString(strings.TrimSpace(o.sql))
		b.WriteString(";\n\n")

		if o.kind == "table" {
			tables = append(tables, o.name)
		}
	}

	for _, table := range tables {
		b.WriteString(sqliteRows(ctx, t, db, table))
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// sqliteObjects returns the schema objects of db in rowid order, without the
// internal objects of SQLite.
func sqliteObjects(ctx context.Context, t *testing.T, db *sql.DB) []sqliteObject {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var out []sqliteObject

	for rows.Next() {
		var o sqliteObject
		if scanErr := rows.Scan(&o.kind, &o.name, &o.table, &o.sql); scanErr != nil {
			t.Fatalf("read sqlite_master: %v", scanErr)
		}

		out = append(out, o)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read sqlite_master: %v", rowsErr)
	}

	return out
}

// sqliteRows returns the rows of table as INSERT statements, ordered by
// rowid, or an empty string for an empty table.
func sqliteRows(ctx context.Context, t *testing.T, db *sql.DB, table string) string {
	t.Helper()

	columns := sqliteColumns(ctx, t, db, table)

	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = sqliteIdent(c)
	}

	//nolint:gosec // The names come from the schema of a database the test made.
	query := "SELECT " + strings.Join(quoted, ", ") + " FROM " + sqliteIdent(table) + " ORDER BY rowid"

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("read rows of %s: %v", table, err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var b strings.Builder

	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))

		for i := range values {
			targets[i] = &values[i]
		}

		if scanErr := rows.Scan(targets...); scanErr != nil {
			t.Fatalf("read a row of %s: %v", table, scanErr)
		}

		literals := make([]string, len(values))
		for i, v := range values {
			literals[i] = sqliteLiteral(t, v)
		}

		fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s);\n\n",
			table, strings.Join(columns, ", "), strings.Join(literals, ", "))
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read rows of %s: %v", table, rowsErr)
	}

	return b.String()
}

// sqliteVersionShape returns one line per column of the version table of
// db: the position, name, type, nullability, default and key position.
func sqliteVersionShape(ctx context.Context, t *testing.T, db *sql.DB) []string {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT cid, name, type, "notnull", dflt_value, pk
		FROM pragma_table_info('schema_migrations') ORDER BY cid`)
	if err != nil {
		t.Fatalf("read the version table shape: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var out []string

	for rows.Next() {
		var cid, notnull, pk int64

		var name, colType string

		var dflt any

		if scanErr := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); scanErr != nil {
			t.Fatalf("read the version table shape: %v", scanErr)
		}

		out = append(out, fmt.Sprintf("%d|%s|%s|%d|%v|%d", cid, name, colType, notnull, dflt, pk))
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the version table shape: %v", rowsErr)
	}

	return out
}

// checkSQLiteVersionTable fails t unless the baseline prefix builds the same
// version table that the old migrations built.
func checkSQLiteVersionTable(t *testing.T, old, fresh *sql.DB) {
	t.Helper()

	ctx := t.Context()
	want := sqliteVersionShape(ctx, t, old)

	if got := sqliteVersionShape(ctx, t, fresh); !slices.Equal(got, want) {
		t.Fatalf("the version table is %v, want the table of the old migrations %v", got, want)
	}
}

// sqliteIdent quotes name as a SQLite identifier.
func sqliteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// sqliteColumns returns the column names of table in their order.
func sqliteColumns(ctx context.Context, t *testing.T, db *sql.DB, table string) []string {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	var out []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("read columns of %s: %v", table, scanErr)
		}

		out = append(out, name)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read columns of %s: %v", table, rowsErr)
	}

	return out
}

// sqliteLiteral returns v as a SQLite literal.
func sqliteLiteral(t *testing.T, v any) string {
	t.Helper()

	switch value := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case string:
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	case []byte:
		return "X'" + hex.EncodeToString(value) + "'"
	default:
		t.Fatalf("no SQLite literal for %T", v)

		return ""
	}
}

// pgSchema is a schema of the test: a URL whose search path names it, and
// its name.
type pgSchema struct {
	url  string
	name string
}

// newPostgresSchema creates a schema with a fresh name.
func newPostgresSchema(t *testing.T, base string) pgSchema {
	t.Helper()

	suffix := make([]byte, schemaSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "baseline_" + hex.EncodeToString(suffix)

	conn := connectPostgres(t, base)

	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())

		dropper, err := pgx.Connect(ctx, base)
		if err != nil {
			t.Errorf("connect: %v", err)

			return
		}

		if _, dropErr := dropper.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); dropErr != nil {
			t.Errorf("drop schema: %v", dropErr)
		}

		if closeErr := dropper.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", envPostgres, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return pgSchema{url: u.String(), name: name}
}

// connectPostgres opens one connection to dbURL for the test.
func connectPostgres(t *testing.T, dbURL string) *pgx.Conn {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(t.Context())); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return conn
}

// execPostgres runs the statements of one file in one transaction, as the
// adapter applies a migration.
func execPostgres(ctx context.Context, t *testing.T, conn *pgx.Conn, name, statements string) {
	t.Helper()

	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// No arguments, so pgx uses the simple protocol, which allows several statements.
		_, execErr := tx.Exec(ctx, statements)

		return execErr //nolint:wrapcheck // The test names the file in its failure.
	})
	if err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// dumpPostgres returns the schema and the rows of schema name in the
// database at base, as pg_dump writes them, without the schema name, the
// session settings, the comments and the meta-commands of pg_dump.
func dumpPostgres(t *testing.T, base, name string) string {
	t.Helper()

	dbURL, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", envPostgres, err)
	}

	q := dbURL.Query()
	q.Del("search_path")
	dbURL.RawQuery = q.Encode()

	//nolint:gosec // pg_dump runs with the URL of the test database and a schema name the test made.
	cmd := exec.CommandContext(t.Context(), "pg_dump",
		"--no-owner", "--no-privileges", "--no-comments", "--inserts", "--column-inserts",
		"--exclude-table=*."+versionTable,
		"--schema="+name, "--dbname="+dbURL.String())

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("pg_dump: %v", err)
	}

	return cleanPostgresDump(string(output), name)
}

// cleanPostgresDump keeps the statements of a pg_dump output that create the
// objects and the rows of schema name, without its name.
func cleanPostgresDump(dump, name string) string {
	var out []string

	blank := true

	for line := range strings.SplitSeq(dump, "\n") {
		switch {
		case strings.HasPrefix(line, "--"),
			strings.HasPrefix(line, `\`),
			strings.HasPrefix(line, "SET "),
			strings.HasPrefix(line, "SELECT pg_catalog."),
			strings.HasPrefix(line, "CREATE SCHEMA "):
			continue
		case strings.TrimSpace(line) == "":
			if !blank {
				out = append(out, "")
			}

			blank = true

			continue
		default:
		}

		line = strings.ReplaceAll(line, pgx.Identifier{name}.Sanitize()+".", "")
		line = strings.ReplaceAll(line, name+".", "")
		out = append(out, line)
		blank = false
	}

	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

// postgresVersionShape returns one line per column of the version table
// visible to conn: the name, type, nullability and default, in column order.
func postgresVersionShape(ctx context.Context, t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, err := conn.Query(ctx, `SELECT column_name, data_type, is_nullable, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatalf("read the version table shape: %v", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var name, dataType, nullable, dflt string

		if scanErr := rows.Scan(&name, &dataType, &nullable, &dflt); scanErr != nil {
			t.Fatalf("read the version table shape: %v", scanErr)
		}

		out = append(out, name+"|"+dataType+"|"+nullable+"|"+dflt)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read the version table shape: %v", rowsErr)
	}

	return out
}

// checkPostgresVersionTable fails t unless the baseline prefix builds the
// same version table that the old migrations built.
func checkPostgresVersionTable(ctx context.Context, t *testing.T, old, fresh *pgx.Conn) {
	t.Helper()

	want := postgresVersionShape(ctx, t, old)

	if got := postgresVersionShape(ctx, t, fresh); !slices.Equal(got, want) {
		t.Fatalf("the version table is %v, want the table of the old migrations %v", got, want)
	}
}
