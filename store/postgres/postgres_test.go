package postgres_test

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/postgres"
	"opendustpan.org/opendustpan/store/storetest"
)

// envURL names the variable holding a PostgreSQL URL for integration tests.
// Each subtest gets its own schema, so the database can be shared.
const envURL = "DUSTPAN_TEST_POSTGRES_URL"

func TestConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return open(t, base)
	})
}

// open returns a store in a fresh schema of the database at base.
func open(t *testing.T, base string) *postgres.Store {
	t.Helper()

	s, err := postgres.Open(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

func TestMigration0003FillsIDs(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0002(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: "acme", Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("migrated stacks = %d, want 2", len(list))
	}

	seen := map[stack.ID]bool{}

	for _, st := range list {
		if st.ID == "" || !strings.HasPrefix(st.ID.String(), "stk-") {
			t.Fatalf("migrated stack %s ID = %q, want stk- prefix", st.Ref, st.ID)
		}

		if seen[st.ID] {
			t.Fatalf("duplicate migrated ID %q", st.ID)
		}

		seen[st.ID] = true
	}
}

// seedAt0002 creates a database at migration 0002 with two stacks and no IDs.
func seedAt0002(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyBaseMigrations(t, conn)
	recordBaseMigrations(t, conn)
	seedBaseStacks(t, conn)
}

func applyBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for _, m := range []struct {
		version int64
		name    string
	}{{version: 1, name: "0001_init.sql"}, {version: 2, name: "0002_updates.sql"}} {
		if _, err := conn.Exec(t.Context(), record, m.version, m.name); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedBaseStacks(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(
		t.Context(),
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms) VALUES ('acme', 'eu-west', 1, 0)`,
	)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks
		(namespace, project, name, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', $1, $2, '{}', 0, 0, 0)`

	for _, s := range [][2]string{{"web", "prod"}, {"web", "dev"}} {
		if _, err := conn.Exec(t.Context(), seedStack, s[0], s[1]); err != nil {
			t.Fatalf("seed stack %s: %v", s[1], err)
		}
	}
}

// isolatedURL creates a fresh schema and returns base with search_path set to it.
func isolatedURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	schema := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	_, err = conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", envURL, err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}
