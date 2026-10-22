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
// the job. A clone is always dropped when its test ends, because the files of
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
// connection after the call, so it can be the template of a clone.
func BuildTemplate(ctx context.Context, base, name string, migrate Migrate) error {
	if err := exec(ctx, base, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("create the template database: %w", err)
	}

	u, err := databaseURL(base, name)
	if err != nil {
		return err
	}

	create := "CREATE SCHEMA " + pgx.Identifier{templateSchema}.Sanitize()
	if schemaErr := exec(ctx, u.String(), create); schemaErr != nil {
		return fmt.Errorf("create the template schema: %w", schemaErr)
	}

	if migrateErr := migrate(ctx, withSearchPath(u).String()); migrateErr != nil {
		return fmt.Errorf("migrate the template database: %w", migrateErr)
	}

	return nil
}

// MigratedURL clones the template database and returns a URL for the migrated
// schema of the clone. The clone is dropped when the test ends.
func MigratedURL(tb testing.TB, base string) string {
	tb.Helper()

	template := os.Getenv(TemplateEnv)
	if template == "" {
		tb.Fatalf("%s is not set: TestMain makes the template database", TemplateEnv)
	}

	suffix := make([]byte, suffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatalf("random database name: %v", err)
	}

	name := template + "_c" + hex.EncodeToString(suffix)
	clone := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() +
		" TEMPLATE " + pgx.Identifier{template}.Sanitize() + " STRATEGY " + cloneStrategy

	if err := exec(tb.Context(), base, clone); err != nil {
		tb.Fatalf("clone the template database: %v", err)
	}

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
		defer cancel()

		drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"
		if err := exec(ctx, base, drop); err != nil {
			tb.Errorf("drop the clone %s: %v", name, err)
		}
	})

	u, err := databaseURL(base, name)
	if err != nil {
		tb.Fatalf("%v", err)
	}

	return withSearchPath(u).String()
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
