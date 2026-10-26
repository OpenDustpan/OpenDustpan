package runnerapi_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// postgresEnv names the variable with the URL of a PostgreSQL server for the
// tests. Each test uses a schema of its own.
const postgresEnv = "DUSTPAN_TEST_POSTGRES_URL"

const schemaSuffixBytes = 6

func openPostgres(t *testing.T) stores {
	t.Helper()

	return openPostgresAt(t, newSchema(t))
}

// newSchema makes a schema for one test and returns the URL that uses it.
func newSchema(t *testing.T) string {
	t.Helper()

	base := os.Getenv(postgresEnv)

	suffix := make([]byte, schemaSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	if err := execSQL(t.Context(), base, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", postgresEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	t.Cleanup(func() {
		// The store keeps its control tables in a second schema, <name>_control.
		for _, schema := range []string{name, name + "_control"} {
			drop := "DROP SCHEMA IF EXISTS " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
			if dropErr := execSQL(context.Background(), base, drop); dropErr != nil {
				t.Errorf("drop schema %s: %v", schema, dropErr)
			}
		}
	})

	return u.String()
}

// openPostgresAt opens a store on the schema at schemaURL. Two calls with
// one URL give two instances of one installation.
func openPostgresAt(t *testing.T, schemaURL string) stores {
	t.Helper()

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return stores{
		namespaces: s,
		operations: s.DurableOperations(),
		jobs:       s.Jobs(),
		units:      s,
		runners:    s.Runners(),
		filters:    s.SourceFilters(),
		audit:      s,
	}
}

func execSQL(ctx context.Context, dbURL, statement string) error {
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	_, execErr := conn.Exec(ctx, statement)
	if execErr != nil {
		execErr = fmt.Errorf("%s: %w", statement, execErr)
	}

	if closeErr := conn.Close(ctx); closeErr != nil {
		return errors.Join(execErr, fmt.Errorf("close: %w", closeErr))
	}

	return execErr
}
