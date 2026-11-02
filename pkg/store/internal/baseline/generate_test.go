// Package baseline_test writes and checks the baseline migration of each
// store dialect.
//
// The write tests run only when DUSTPAN_TEST_BASELINE_IN names a directory
// that holds the numbered migrations to collapse, in
// pkg/store/<dialect>/migrations/. scripts/regenerate-baseline.sh sets it up
// and runs them. For each dialect a write test applies every input
// migration in order to an empty database. When the input holds no baseline
// yet, it then applies the statements of testdata/after-<dialect>.sql. It
// writes one baseline file: the version-table statements below, then the
// schema and the rows of that database without the version table. The
// version of the file is above the version of each input migration of both
// dialects (migrate.CollapseVersion). It then applies the baseline to a
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
	"errors"
	"fmt"
	"io/fs"
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
// body: the version-table statements of the folded migrations that added the
// minimum binary version column and the SHA-256 column.
const sqliteSchemaPrefix = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER NOT NULL PRIMARY KEY,
    name    TEXT    NOT NULL
) STRICT;

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';

`

// postgresSchemaPrefix holds the statements that start the PostgreSQL
// baseline body: the version-table statements of the folded migrations that
// added the minimum binary version column and the SHA-256 column.
const postgresSchemaPrefix = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT NOT NULL PRIMARY KEY,
    name    TEXT   NOT NULL
);

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';

`

// addSumColumn adds the SHA-256 column to the version table of a database
// that the old migrations made, if it lacks it. The new baseline adds the
// column in its prefix, so the version tables of the two databases then
// compare equal. The column holds no data that a dump keeps.
const addSumColumn = `ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT ''`

// header returns the comment block that starts the baseline file of dialect,
// written from the migrations of ref. extra holds more comment lines, each
// with its line end, or nothing. The generator writes the rest.
func header(dialect, ref, extra string) string {
	return minimumLine + `
-- This baseline replaced every migration before it. No binary opens a
-- database or restores a backup from before it. Until the first tag, a
-- change to the schema edits this file in place and adds no numbered
-- migration. The server refuses a database that another version of this
-- file made. make regenerate-baseline wrote the first version of this file
-- from the ` + dialect + ` migrations of ` + ref + `.
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

	if tagged := taggedDirs(t, out); len(tagged) > 0 {
		t.Fatalf("tagged migrations exist (%s); collapse only above the last tag is not implemented",
			strings.Join(tagged, ", "))
	}

	return paths{in: in, out: out}
}

// taggedDirs returns the tag directories under
// pkg/store/<dialect>/testdata/tag-migrations of the repository at root. A
// released tag has one, and a collapse of the migrations would break the
// upgrade from it.
func taggedDirs(t *testing.T, root string) []string {
	t.Helper()

	var found []string

	for _, dialect := range dialects {
		dir := filepath.Join(root, "pkg", "store", dialect, "testdata", "tag-migrations")

		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				found = append(found, filepath.Join(dir, entry.Name()))
			}
		}
	}

	return found
}

// dialects are the store dialects that each get a baseline.
var dialects = []string{"sqlite", "postgres"} //nolint:gochecknoglobals // A fixed list, read only.

// inputMigrations returns the migrations of dialect in the input directory,
// ordered by version.
func inputMigrations(t *testing.T, in, dialect string) []migrate.Migration {
	t.Helper()

	all, err := migrate.Load(os.DirFS(filepath.Join(in, "pkg", "store", dialect)), "migrations")
	if err != nil {
		t.Fatalf("load %s migrations: %v", dialect, err)
	}

	if len(all) == 0 {
		t.Fatalf("no %s migrations in %s", dialect, in)
	}

	return all
}

// collapseVersion returns the version of the new baseline: above every
// input migration of every dialect, so both dialects get one version.
func collapseVersion(t *testing.T, in string) int64 {
	t.Helper()

	var versions []int64

	for _, dialect := range dialects {
		for _, m := range inputMigrations(t, in, dialect) {
			versions = append(versions, m.Version)
		}
	}

	return migrate.CollapseVersion(versions)
}

// baselineName returns the file name of the baseline with version v.
func baselineName(v int64) string {
	return fmt.Sprintf("%04d_baseline.sql", v)
}

// holdsBaseline reports whether migrations hold a baseline already. Then
// the statements of testdata/after-<dialect>.sql are in it, and the test
// does not run them again.
func holdsBaseline(migrations []migrate.Migration) bool {
	return slices.ContainsFunc(migrations, func(m migrate.Migration) bool {
		return strings.HasSuffix(m.Name, "_baseline.sql")
	})
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

// target names the baseline file that a write test writes: its dialect,
// its file name and the extra header lines.
type target struct {
	dialect string
	name    string
	extra   string
}

// writeBaseline writes body as the baseline file of to, after the header
// with the extra header lines.
func writeBaseline(t *testing.T, out string, to target, body string) {
	t.Helper()

	ref := os.Getenv(envRef)
	if ref == "" {
		t.Fatalf("set %s to the Git ref of the old migrations", envRef)
	}

	path := filepath.Join(out, "pkg", "store", to.dialect, "migrations", to.name)

	content := header(to.dialect, ref, to.extra) + "\n" + body

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

	name := baselineName(collapseVersion(t, dir.in))
	input := inputMigrations(t, dir.in, "sqlite")
	old := openSQLite(t)

	for _, m := range input {
		execSQLite(ctx, t, old, m.Name, m.SQL)
	}

	if !holdsBaseline(input) {
		execSQLite(ctx, t, old, "after-sqlite.sql", after(t, "sqlite"))
	}

	execSQLite(ctx, t, old, "the SHA-256 column", addSumColumn)

	dumped := dumpSQLite(ctx, t, old)
	body := sqliteSchemaPrefix + dumped

	fresh := openSQLite(t)
	execSQLite(ctx, t, fresh, name, body)

	if got := dumpSQLite(ctx, t, fresh); got != dumped {
		t.Fatalf("the baseline gives another schema than the migrations:\n%s", firstDifference(dumped, got))
	}

	checkSQLiteVersionTable(t, old, fresh)

	writeBaseline(t, dir.out, target{dialect: "sqlite", name: name, extra: ""}, body)
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

	name := baselineName(collapseVersion(t, dir.in))
	input := inputMigrations(t, dir.in, "postgres")

	for _, m := range input {
		execPostgres(ctx, t, old, m.Name, m.SQL)
	}

	if !holdsBaseline(input) {
		execPostgres(ctx, t, old, "after-postgres.sql", after(t, "postgres"))
	}

	execPostgres(ctx, t, old, "the SHA-256 column", addSumColumn)

	dumped := postgresBody(t, base, oldSchema.name)
	body := postgresSchemaPrefix + dumped

	freshSchema := newPostgresSchema(t, base)
	fresh := connectPostgres(t, freshSchema.url)
	execPostgres(ctx, t, fresh, name, body)

	if got := postgresBody(t, base, freshSchema.name); got != dumped {
		t.Fatalf("the baseline gives another schema than the migrations:\n%s", firstDifference(dumped, got))
	}

	checkPostgresTables(ctx, t, old, fresh)
	checkPostgresVersionTable(ctx, t, old, fresh)

	writeBaseline(
		t,
		dir.out,
		target{dialect: "postgres", name: name, extra: pgDumpLine + strconv.Itoa(dumpMajor) + "\n"},
		body,
	)
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

		for _, drop := range []string{name, name + controlSuffix} {
			stmt := "DROP SCHEMA IF EXISTS " + pgx.Identifier{drop}.Sanitize() + " CASCADE"
			if _, dropErr := dropper.Exec(ctx, stmt); dropErr != nil {
				t.Errorf("drop schema: %v", dropErr)
			}
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
	// The search path of the adapter holds the live schema and its control
	// schema, and the migrations depend on it. No space: the URL form of the
	// value keeps a plus sign as it is.
	q.Set("search_path", name+","+name+controlSuffix)
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

// controlSuffix is appended to the name of the live schema to name the
// control schema, which holds the tables that a restore never renames.
const controlSuffix = "_control"

// controlTables returns the tables of schema name in the database at base,
// ordered by name. The migrations move a table to the control schema in
// more than one place, so the list comes from the database.
func controlTables(t *testing.T, base, name string) []string {
	t.Helper()

	conn := connectPostgres(t, base)

	rows, err := conn.Query(t.Context(), `SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE' ORDER BY table_name`, name)
	if err != nil {
		t.Fatalf("list the control tables: %v", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var table string

		if scanErr := rows.Scan(&table); scanErr != nil {
			t.Fatalf("list the control tables: %v", scanErr)
		}

		out = append(out, table)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("list the control tables: %v", rowsErr)
	}

	return out
}

// postgresBody returns the baseline text of the live schema name and of its
// control schema. The statements create the control tables in the live
// schema, where sqlc reads them, and a DO block then moves them to the schema
// that appends _control to the current schema, as the migration that moved
// them did.
func postgresBody(t *testing.T, base, name string) string {
	t.Helper()

	live := dumpPostgres(t, base, name)

	// A database built from the baseline before the migration that moved
	// the tables has no control schema.
	if !postgresSchemaExists(t, base, name+controlSuffix) {
		return live
	}

	control := dumpPostgres(t, base, name+controlSuffix)

	var move strings.Builder

	for _, table := range controlTables(t, base, name+controlSuffix) {
		move.WriteString("  EXECUTE format('ALTER TABLE %I." + table + " SET SCHEMA %I', live, ctl);\n")
	}

	return live + "\n" + control + `
DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '` + controlSuffix + `';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
` + move.String() + `END $$;
`
}

// postgresSchemaExists reports whether the database at base has schema name.
func postgresSchemaExists(t *testing.T, base, name string) bool {
	t.Helper()

	conn := connectPostgres(t, base)

	const query = `SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)`

	var found bool

	err := conn.QueryRow(t.Context(), query, name).Scan(&found)
	if err != nil {
		t.Fatalf("look for schema %s: %v", name, err)
	}

	return found
}

// missingTables returns the entries of want that got lacks.
func missingTables(want, got []string) []string {
	var out []string

	for _, name := range want {
		if !slices.Contains(got, name) {
			out = append(out, name)
		}
	}

	return out
}

// postgresTables returns schema.table for each table in the current schema
// of conn and in its control schema.
func postgresTables(ctx context.Context, t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, err := conn.Query(ctx, `SELECT 'live.' || table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'
		UNION ALL
		SELECT 'control.' || table_name FROM information_schema.tables
		WHERE table_schema = current_schema() || $1 AND table_type = 'BASE TABLE'
		ORDER BY 1`, controlSuffix)
	if err != nil {
		t.Fatalf("list the tables: %v", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("list the tables: %v", scanErr)
		}

		out = append(out, name)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("list the tables: %v", rowsErr)
	}

	return out
}

// checkPostgresTables fails t unless the baseline builds each table, in the
// live schema and in the control schema, that the old migrations built.
func checkPostgresTables(ctx context.Context, t *testing.T, old, fresh *pgx.Conn) {
	t.Helper()

	want := postgresTables(ctx, t, old)

	if len(want) == 0 {
		t.Fatal("the old migrations built no table")
	}

	if lost := missingTables(want, postgresTables(ctx, t, fresh)); len(lost) > 0 {
		t.Fatalf("the baseline lacks the tables %v that the old migrations built", lost)
	}
}

// TestMissingTables proves that the table check names a table that the
// baseline lacks, also in the control schema.
func TestMissingTables(t *testing.T) {
	t.Parallel()

	want := []string{"live.stacks", "control.backups", "control.installation"}

	if got := missingTables(want, want); len(got) != 0 {
		t.Fatalf("missing tables of equal lists = %v, want none", got)
	}

	got := missingTables(want, []string{"live.stacks"})
	if !slices.Equal(got, []string{"control.backups", "control.installation"}) {
		t.Fatalf("missing tables = %v, want the two control tables", got)
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

// TestTaggedDirsRefuseTheCollapse proves that the write tests find a tag
// directory of either dialect, and ignore the README file next to them.
func TestTaggedDirsRefuseTheCollapse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if got := taggedDirs(t, root); len(got) != 0 {
		t.Fatalf("tag directories of an empty repository = %v, want none", got)
	}

	base := filepath.Join(root, "pkg", "store", "postgres", "testdata", "tag-migrations")
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatalf("make %s: %v", base, err)
	}

	if err := os.WriteFile(filepath.Join(base, "README.md"), []byte("notes\n"), 0o600); err != nil {
		t.Fatalf("write the README: %v", err)
	}

	if got := taggedDirs(t, root); len(got) != 0 {
		t.Fatalf("tag directories with a README only = %v, want none", got)
	}

	tag := filepath.Join(base, "v0.1.0")
	if err := os.Mkdir(tag, 0o750); err != nil {
		t.Fatalf("make %s: %v", tag, err)
	}

	if got := taggedDirs(t, root); !slices.Equal(got, []string{tag}) {
		t.Fatalf("tag directories = %v, want [%s]", got, tag)
	}
}
