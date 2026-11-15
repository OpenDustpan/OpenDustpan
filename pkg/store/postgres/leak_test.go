package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(ownDatabase{m: m})
}

// ownDatabase runs the tests of the package in a database of their own. A
// test that needs an empty schema makes its own schema there, and about 1,000
// schemas stay after one run. One DROP DATABASE at the end removes them as
// files, which costs much less server time than one DROP SCHEMA for each
// test. A test that needs a migrated schema gets a clone of a template
// database that this binary migrates one time.
type ownDatabase struct {
	m *testing.M
}

// Run implements goleak.TestingM. It points envURL at a new database, runs
// the tests and drops the database. Without envURL it only runs the tests,
// which then skip. A database that it cannot make or drop fails the package.
func (o ownDatabase) Run() int {
	base := os.Getenv(envURL)
	if base == "" {
		return o.m.Run()
	}

	ctx := context.Background()

	dropStaleTestDatabases(ctx, base, time.Now())

	db, err := createTestDatabase(ctx, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "make the test database: %v\n", err)

		return 1
	}

	if setErr := os.Setenv(envURL, db.url); setErr != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", envURL, setErr)

		return 1
	}

	template := db.name + "_tpl"
	if tplErr := pgtest.BuildTemplate(ctx, base, template, migrateTemplate); tplErr != nil {
		fmt.Fprintf(os.Stderr, "make the template database: %v\n", tplErr)

		// The build drops the template database when it fails. The database
		// of the test binary stays, so drop it here too.
		dropTestDatabase(ctx, base, db.name)

		return 1
	}

	if setErr := os.Setenv(pgtest.TemplateEnv, template); setErr != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", pgtest.TemplateEnv, setErr)

		return 1
	}

	code := o.m.Run()

	// A CI service container is thrown away after the job, with its
	// databases. The drop then only costs time: about 150 s to delete the
	// files of about 1,000 schemas. Any other value than "1" drops.
	if os.Getenv(pgtest.SkipFinalDropEnv) == "1" {
		return code
	}

	// A clone that its test could not drop stays. A failure here does not
	// fail the package: the clones are only disk, and the next run removes
	// the stale ones.
	failed, sweepErr := pgtest.DropClones(ctx, base, template, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	})
	if sweepErr != nil {
		fmt.Fprintf(os.Stderr, "drop the remaining clones: %v\n", sweepErr)
	}

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "could not drop %d clones\n", failed)
	}

	for _, name := range []string{template, db.name} {
		drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"
		if dropErr := execOn(ctx, base, drop); dropErr != nil {
			fmt.Fprintf(os.Stderr, "drop the test database %s: %v\n", name, dropErr)

			if code == 0 {
				code = 1
			}
		}
	}

	return code
}

// testDatabasePrefix starts the name of each test database, the template and
// the clones. The Unix time of its creation and a random suffix follow.
const testDatabasePrefix = "dustpan_test_"

// staleTestDatabaseAge is the age after which a test database counts as left
// by a test binary that crashed. It is far above the time limit of a test run.
const staleTestDatabaseAge = 2 * time.Hour

// dropStaleTestDatabases drops the test databases older than
// staleTestDatabaseAge that have no connection. A test binary that crashed
// does not drop its database, and the next run removes it here. A database
// with a connection or a young age can belong to a run on the same server
// now, so it stays. A failure here does not fail the package: the stale
// database is only disk.
func dropStaleTestDatabases(ctx context.Context, base string, now time.Time) {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list stale test databases: %v\n", err)

		return
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Cleanup of stale data only.
	}()

	rows, err := conn.Query(ctx, `SELECT d.datname FROM pg_database AS d
		WHERE starts_with(d.datname, $1)
		AND NOT EXISTS (SELECT 1 FROM pg_stat_activity AS a WHERE a.datname = d.datname)`, testDatabasePrefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list stale test databases: %v\n", err)

		return
	}

	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		fmt.Fprintf(os.Stderr, "list stale test databases: %v\n", err)

		return
	}

	for _, name := range names {
		if !staleTestDatabase(name, now) {
			continue
		}

		// No FORCE: a run that connected after the list keeps its database.
		if _, dropErr := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()); dropErr != nil {
			fmt.Fprintf(os.Stderr, "drop stale test database %s: %v\n", name, dropErr)
		}
	}
}

// staleTestDatabase reports whether name is a test database made more than
// staleTestDatabaseAge before now. A name that does not parse is not stale.
func staleTestDatabase(name string, now time.Time) bool {
	rest, ok := strings.CutPrefix(name, testDatabasePrefix)
	if !ok {
		return false
	}

	created, _, ok := strings.Cut(rest, "_")
	if !ok {
		return false
	}

	unix, err := strconv.ParseInt(created, 10, 64)
	if err != nil {
		return false
	}

	return now.Sub(time.Unix(unix, 0)) > staleTestDatabaseAge
}

// testDatabase is the database of the test binary: its name, and a URL of
// the server that names it.
type testDatabase struct {
	url  string
	name string
}

// createTestDatabase makes a database with a random name on the server of
// base.
func createTestDatabase(ctx context.Context, base string) (testDatabase, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return testDatabase{}, fmt.Errorf("random database name: %w", err)
	}

	name := testDatabasePrefix + strconv.FormatInt(time.Now().Unix(), 10) + "_" + hex.EncodeToString(suffix)

	u, err := url.Parse(base)
	if err != nil {
		return testDatabase{}, fmt.Errorf("parse %s: %w", envURL, err)
	}

	if execErr := execOn(ctx, base, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		return testDatabase{}, execErr
	}

	u.Path = "/" + name

	return testDatabase{url: u.String(), name: name}, nil
}

// execOn runs one statement on a new connection to dbURL.
func execOn(ctx context.Context, dbURL, stmt string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // The statement result decides the outcome.
	}()

	if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
		return fmt.Errorf("exec: %w", execErr)
	}

	return nil
}

// dropTestDatabase drops the test database name on the server of base. A
// failure only reaches stderr: the caller decides the result. The drop ignores
// the end of ctx, because a cleanup must run even when the build was canceled.
func dropTestDatabase(ctx context.Context, base, name string) {
	drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"
	if dropErr := execOn(context.WithoutCancel(ctx), base, drop); dropErr != nil {
		fmt.Fprintf(os.Stderr, "drop the test database %s: %v\n", name, dropErr)
	}
}

// TestDropTestDatabaseIgnoresACanceledContext checks that the drop of the test
// database still runs when the context of the build is canceled. A canceled
// context must not keep the database.
func TestDropTestDatabaseIgnoresACanceledContext(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	name := "dustpan_test_drop" + hex.EncodeToString(suffix)

	if err := execOn(t.Context(), base, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}

	t.Cleanup(func() { dropTestDatabase(context.Background(), base, name) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dropTestDatabase(ctx, base, name)

	if testDatabaseExists(t, base, name) {
		t.Fatalf("database %s stays after a drop with a canceled context", name)
	}
}

// testDatabaseExists reports whether the database name exists on the server of
// base.
func testDatabaseExists(t *testing.T, base, name string) bool {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var n int

	query := "SELECT count(*) FROM pg_database WHERE datname = $1"
	if err = conn.QueryRow(t.Context(), query, name).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}

	return n > 0
}

// migrateTemplate opens the store at dbURL, which applies the migrations, and closes
// it.
func migrateTemplate(ctx context.Context, dbURL string) error {
	s, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	s.Close() //nolint:contextcheck // Close takes no context, and the store has no other work.

	return nil
}
