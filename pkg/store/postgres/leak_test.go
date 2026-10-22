package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(ownDatabase{m: m})
}

// ownDatabase runs the tests of the package in a database of their own. Each
// test makes its own schema, and about 4,000 schemas stay after one run. One
// DROP DATABASE at the end removes them as files, which costs much less
// server time than one DROP SCHEMA for each test.
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

	db, err := createTestDatabase(ctx, base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "make the test database: %v; the role of %s needs CREATEDB\n", err, envURL)

		return 1
	}

	if setErr := os.Setenv(envURL, db.url); setErr != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", envURL, setErr)

		return 1
	}

	code := o.m.Run()

	drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{db.name}.Sanitize() + " WITH (FORCE)"
	if dropErr := execOn(ctx, base, drop); dropErr != nil {
		fmt.Fprintf(os.Stderr, "drop the test database %s: %v\n", db.name, dropErr)

		if code == 0 {
			code = 1
		}
	}

	return code
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

	name := "dustpan_test_" + hex.EncodeToString(suffix)

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
