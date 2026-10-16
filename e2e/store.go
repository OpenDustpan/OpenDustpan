//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	databasesEnv   = "E2E_DATABASES"
	postgresURLEnv = "DUSTPAN_TEST_POSTGRES_URL"
	schemaBytes    = 8
	dropTimeout    = 30 * time.Second
)

// Store names the database backend one scenario run uses.
type Store string

// Stores the end-to-end suite can run against.
const (
	StoreSQLite   Store = "sqlite"
	StorePostgres Store = "postgres"
)

// ErrUnknownStore reports an E2E_DATABASES entry that names no store.
var ErrUnknownStore = errors.New("unknown store")

// Databases returns the stores every scenario runs against: E2E_DATABASES as a
// comma-separated list (sqlite, postgres), or the default when it is unset.
// The default is SQLite, plus PostgreSQL when DUSTPAN_TEST_POSTGRES_URL is set.
func Databases() ([]Store, error) {
	if strings.TrimSpace(os.Getenv(databasesEnv)) == "" {
		stores := []Store{StoreSQLite}
		if PostgresURL() != "" {
			stores = append(stores, StorePostgres)
		}

		return stores, nil
	}

	return ParseDatabases(os.Getenv(databasesEnv))
}

// ParseDatabases parses raw as a comma-separated store list. "postgresql" is
// accepted for "postgres". Repeated names run once.
func ParseDatabases(raw string) ([]Store, error) {
	var stores []Store

	for part := range strings.SplitSeq(raw, ",") {
		var next Store

		switch name := strings.ToLower(strings.TrimSpace(part)); name {
		case "":
			continue
		case string(StoreSQLite):
			next = StoreSQLite
		case string(StorePostgres), "postgresql":
			next = StorePostgres
		default:
			return nil, fmt.Errorf("%w: %q (want sqlite or postgres)", ErrUnknownStore, strings.TrimSpace(part))
		}

		if !slices.Contains(stores, next) {
			stores = append(stores, next)
		}
	}

	if len(stores) == 0 {
		return nil, fmt.Errorf("%w: %q (want sqlite or postgres)", ErrUnknownStore, raw)
	}

	return stores, nil
}

// PostgresURL returns DUSTPAN_TEST_POSTGRES_URL, the PostgreSQL server the
// postgres store runs against, or "" when it is unset.
func PostgresURL() string {
	return os.Getenv(postgresURLEnv)
}

// newPostgresSchema creates a fresh schema on the PostgreSQL server at base
// and returns its URL. It opens no store: a server binary migrates the
// schema when it opens it. The schema is dropped when the test ends.
func newPostgresSchema(ctx context.Context, tb testing.TB, base string) string {
	tb.Helper()

	if base == "" {
		tb.Fatalf("set %s to run the PostgreSQL suite", postgresURLEnv)
	}

	schema := randomSchema(tb)

	createSchema(ctx, tb, base, schema)

	tb.Cleanup(func() {
		dropSchema(context.WithoutCancel(ctx), tb, base, schema)
	})

	return schemaURL(tb, base, schema)
}

func randomSchema(tb testing.TB) string {
	tb.Helper()

	raw := make([]byte, schemaBytes)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatalf("generate schema name: %v", err)
	}

	return "e2e_" + hex.EncodeToString(raw)
}

// schemaURL returns base with the search path set to schema.
func schemaURL(tb testing.TB, base, schema string) string {
	tb.Helper()

	u, err := url.Parse(base)
	if err != nil {
		tb.Fatalf("parse %s: %v", postgresURLEnv, err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}

func createSchema(ctx context.Context, tb testing.TB, base, schema string) {
	tb.Helper()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		tb.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if err := conn.Close(ctx); err != nil {
			tb.Errorf("close postgres connection: %v", err)
		}
	}()

	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		tb.Fatalf("create schema: %v", err)
	}
}

// dropSchema removes schema and everything in it. It runs in cleanups, after
// the test context is done, so callers pass a fresh context for cleanup.
func dropSchema(ctx context.Context, tb testing.TB, base, schema string) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(ctx, dropTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		tb.Errorf("connect to postgres: %v", err)

		return
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			tb.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	_, err = conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	if err != nil {
		tb.Errorf("drop schema: %v", err)
	}
}
