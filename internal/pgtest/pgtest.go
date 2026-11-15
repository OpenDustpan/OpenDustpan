// Package pgtest holds the test helper that gives each PostgreSQL test a
// migrated database. A test binary migrates one template database, and each
// test clones it.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TemplateEnv names the variable that holds the name of the migrated template
// database. TestMain sets it once for the test binary, and every package of
// the binary reads it.
const TemplateEnv = "DUSTPAN_TEST_POSTGRES_TEMPLATE"

// SkipFinalDropEnv names the variable that keeps the test database after the
// tests. CI sets it to "1" for a PostgreSQL server that it throws away after
// the job. The variable also keeps the test database and the template of a
// run that fails, so set it only for a server that is thrown away after the
// run. A clone is always dropped when its test ends, because the files of
// about 4,000 clones do not fit on a CI disk.
const SkipFinalDropEnv = "DUSTPAN_TEST_SKIP_FINAL_DROP"

// templateSchema is the schema that the template database migrates into. Each
// clone keeps the name, so a test reads it from the search path of its URL.
const templateSchema = "tpl"

// cloneStrategy is the strategy of CREATE DATABASE for a clone. WAL_LOG does
// not force a checkpoint for each clone.
const cloneStrategy = "WAL_LOG"

// objectBusy is the SQLSTATE of "source database is being accessed by other
// users". A clone gets it while a backend of the template is still ending.
const objectBusy = "55006"

const (
	busyWait     = 50 * time.Millisecond
	busyDeadline = 30 * time.Second
	dropTimeout  = time.Minute
	suffixBytes  = 6
)

// Migrate opens the store at dbURL, applies the migrations and closes it.
type Migrate func(ctx context.Context, dbURL string) error

// BuildTemplate makes the database name on the server of base and migrates
// the schema templateSchema in it with migrate. The database has no
// connection after the call, so it can be the template of a clone. A step
// that fails after CREATE DATABASE drops the database.
func BuildTemplate(ctx context.Context, base, name string, migrate Migrate) error {
	if err := exec(ctx, base, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("create the template database: %w", err)
	}

	// A template that a failed build leaves behind fills the disk of the
	// server. The drop ignores the end of ctx, because a cleanup must run.
	fail := func(err error) error {
		return errors.Join(err, dropDatabase(context.WithoutCancel(ctx), base, name))
	}

	u, err := databaseURL(base, name)
	if err != nil {
		return fail(err)
	}

	create := "CREATE SCHEMA " + pgx.Identifier{templateSchema}.Sanitize()
	if schemaErr := exec(ctx, u.String(), create); schemaErr != nil {
		return fail(fmt.Errorf("create the template schema: %w", schemaErr))
	}

	if migrateErr := migrate(ctx, withSearchPath(u).String()); migrateErr != nil {
		return fail(fmt.Errorf("migrate the template database: %w", migrateErr))
	}

	return nil
}

// MigratedURL clones the template database and returns a URL for the migrated
// schema of the clone. The clone is dropped when the test ends. A drop that
// fails is only logged, because a slow checkpoint of the server is no failure
// of the test. DropClones removes the clones that stay.
func MigratedURL(tb testing.TB, base string) string {
	tb.Helper()

	template := os.Getenv(TemplateEnv)
	if template == "" {
		tb.Fatalf("%s is not set: TestMain makes the template database", TemplateEnv)
	}

	return MigratedURLWithTimeout(tb, base, template, dropTimeout)
}

// cloneSuffix is the text between the name of the template and the random
// part of the name of a clone.
const cloneSuffix = "_c"

// maxCloneNameBytes is the longest database name that PostgreSQL keeps in
// full. A longer name loses its end, and two clones can then share a name.
const maxCloneNameBytes = 63

// errCloneNameTooLong is the error of CloneName for a name longer than
// maxCloneNameBytes bytes.
var errCloneNameTooLong = errors.New("the clone name is too long")

// CloneName returns the name of a new clone of the template database, with a
// random suffix. It returns an error when the name is longer than
// maxCloneNameBytes bytes.
func CloneName(template string) (string, error) {
	suffix := make([]byte, suffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("random database name: %w", err)
	}

	name := template + cloneSuffix + hex.EncodeToString(suffix)
	if len(name) > maxCloneNameBytes {
		return "", fmt.Errorf("%w: %d bytes, the limit is %d", errCloneNameTooLong, len(name), maxCloneNameBytes)
	}

	return name, nil
}

// MigratedURLWithTimeout is MigratedURL for the template database template,
// with timeout as the time limit of the drop of the clone.
func MigratedURLWithTimeout(tb testing.TB, base, template string, timeout time.Duration) string {
	tb.Helper()

	name, nameErr := CloneName(template)
	if nameErr != nil {
		tb.Fatalf("clone name: %v", nameErr)
	}

	clone := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() +
		" TEMPLATE " + pgx.Identifier{template}.Sanitize() + " STRATEGY " + cloneStrategy

	if err := exec(tb.Context(), base, clone); err != nil {
		tb.Fatalf("clone the template database: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := dropDatabase(ctx, base, name); err != nil {
			tb.Logf("drop the clone %s: %v: DropClones tries again at the end of the run", name, err)
		}
	})

	u, err := databaseURL(base, name)
	if err != nil {
		tb.Fatalf("%v", err)
	}

	return withSearchPath(u).String()
}

// DropSchemaAtCleanup drops the schema name and its control schema on the
// server of base when tb ends. Each test makes its own schema, and a schema
// that stays fills the disk of the database server.
// The cleanup runs after the cleanups that close the stores of the schema,
// because it is registered before them.
func DropSchemaAtCleanup(tb testing.TB, base, name string) {
	tb.Helper()

	tb.Cleanup(func() {
		ctx := context.WithoutCancel(tb.Context())

		conn, err := pgx.Connect(ctx, base)
		if err != nil {
			tb.Errorf("connect to drop schema %s: %v", name, err)

			return
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				tb.Errorf("close: %v", closeErr)
			}
		}()

		for _, schema := range []string{name, name + "_control"} {
			drop := "DROP SCHEMA IF EXISTS " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
			if _, execErr := conn.Exec(ctx, drop); execErr != nil {
				tb.Errorf("drop schema %s: %v", schema, execErr)
			}
		}
	})
}

// Logf writes one line of a report.
type Logf func(format string, args ...any)

// DropClones drops each remaining clone of template on the server of base.
// Clones of other runs have other template names, so they stay. It returns
// the number of clones that it could not drop. It writes each failure to
// logf. The error is not nil when the list of clones fails.
func DropClones(ctx context.Context, base, template string, logf Logf) (int, error) {
	return DropClonesWithTimeout(ctx, base, template, dropTimeout, logf)
}

// DropClonesWithTimeout is DropClones with timeout as the time limit of each
// drop.
func DropClonesWithTimeout(ctx context.Context, base, template string, timeout time.Duration, logf Logf) (int, error) {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return 0, fmt.Errorf("list the clones: connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // The list decides the outcome.
	}()

	rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE starts_with(datname, $1)", template+cloneSuffix)
	if err != nil {
		return 0, fmt.Errorf("list the clones: %w", err)
	}

	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("list the clones: %w", err)
	}

	failed := 0

	for _, name := range names {
		dropCtx, cancel := context.WithTimeout(ctx, timeout)
		dropErr := dropDatabase(dropCtx, base, name)

		cancel()

		if dropErr != nil {
			failed++

			logf("drop the clone %s: %v", name, dropErr)
		}
	}

	return failed, nil
}

func dropDatabase(ctx context.Context, base, name string) error {
	return exec(ctx, base, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// databaseURL returns base with the database name replaced by name.
func databaseURL(base, name string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse test URL: %w", err)
	}

	u.Path = "/" + name

	return u, nil
}

// withSearchPath sets the search path of u to the template schema.
func withSearchPath(u *url.URL) *url.URL {
	next := *u
	q := next.Query()
	q.Set("search_path", templateSchema)
	next.RawQuery = q.Encode()

	return &next
}

// exec runs one statement on a new connection to dbURL. It tries again while
// the template database is busy.
func exec(ctx context.Context, dbURL, stmt string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // The statement result decides the outcome.
	}()

	deadline := time.Now().Add(busyDeadline)

	for {
		_, execErr := conn.Exec(ctx, stmt)
		if execErr == nil {
			return nil
		}

		var pgErr *pgconn.PgError
		if !errors.As(execErr, &pgErr) || pgErr.Code != objectBusy || time.Now().After(deadline) {
			return fmt.Errorf("exec: %w", execErr)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the template: %w", ctx.Err())
		case <-time.After(busyWait):
		}
	}
}
