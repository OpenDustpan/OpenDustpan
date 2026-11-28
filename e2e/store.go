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
	instances, err := Instances()
	if err != nil {
		return nil, err
	}

	if instances > 1 {
		return haDatabases()
	}

	if strings.TrimSpace(os.Getenv(databasesEnv)) == "" {
		stores := []Store{StoreSQLite}
		if PostgresURL() != "" {
			stores = append(stores, StorePostgres)
		}

		return stores, nil
	}

	return ParseDatabases(os.Getenv(databasesEnv))
}

// haDatabases returns the stores of a run with several instances: PostgreSQL
// only, because SQLite holds one instance only. It fails when E2E_DATABASES
// names SQLite.
func haDatabases() ([]Store, error) {
	if strings.TrimSpace(os.Getenv(databasesEnv)) == "" {
		return []Store{StorePostgres}, nil
	}

	stores, err := ParseDatabases(os.Getenv(databasesEnv))
	if err != nil {
		return nil, err
	}

	if slices.Contains(stores, StoreSQLite) {
		return nil, fmt.Errorf(
			"%w: SQLite holds one instance, so %s above 1 runs on PostgreSQL only",
			ErrInstances,
			instancesEnv,
		)
	}

	return stores, nil
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

	// The store keeps its control tables in a second schema, <name>_control.
	for _, name := range []string{schema, schema + "_control"} {
		dropSchemaTables(ctx, tb, conn, name)

		_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{name}.Sanitize()+" CASCADE")
		if err != nil {
			tb.Errorf("drop schema %s: %v", name, err)
		}
	}
}

// dropSchemaTables drops each table of schema in a statement of its own.
// One DROP SCHEMA ... CASCADE takes a lock on each object in the schema in one
// transaction, about a thousand with the types of the tables. The server holds
// max_locks_per_transaction times max_connections locks for all sessions
// together (6,400 with the defaults), so parallel drops filled the lock table
// ("out of shared memory", SQLSTATE 53200). One table at a time holds only the
// locks of that table and its indexes. The drop of the schema that follows then
// has almost no object left to lock. A failure here is not an error of the
// test: the drop of the schema removes what is left and reports its own error.
func dropSchemaTables(ctx context.Context, tb testing.TB, conn *pgx.Conn, schema string) {
	tb.Helper()

	rows, err := conn.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname = $1", schema)
	if err != nil {
		tb.Logf("list tables of schema %s: %v", schema, err)

		return
	}

	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		tb.Logf("read tables of schema %s: %v", schema, err)

		return
	}

	for _, table := range tables {
		_, err = conn.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{schema, table}.Sanitize()+" CASCADE")
		if err != nil {
			tb.Logf("drop table %s.%s: %v", schema, table, err)
		}
	}
}
