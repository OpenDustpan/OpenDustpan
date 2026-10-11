package sqlite_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/sqlite"
	"opendustpan.org/opendustpan/store/storetest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return open(t)
	})
}

// open returns a fresh store in a temporary directory.
func open(t *testing.T) *sqlite.Store {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return s
}

func TestMigration0003FillsIDs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	seedAt0002(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

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
func seedAt0002(t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	applyBaseMigrations(t, conn)
	recordBaseMigrations(t, conn)
	seedBaseStacks(t, conn)
}

func applyBaseMigrations(t *testing.T, conn *sql.DB) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.ExecContext(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordBaseMigrations(t *testing.T, conn *sql.DB) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	for _, m := range [][2]any{{1, "0001_init.sql"}, {2, "0002_updates.sql"}} {
		if _, err := conn.ExecContext(t.Context(), record, m[0], m[1]); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedBaseStacks(t *testing.T, conn *sql.DB) {
	t.Helper()

	_, err := conn.ExecContext(
		t.Context(),
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms) VALUES ('acme', 'eu-west', 1, 0)`,
	)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks
		(namespace, project, name, tags_json, version, created_at_ms, updated_at_ms)
		VALUES ('acme', ?, ?, '{}', 0, 0, 0)`

	for _, s := range [][2]string{{"web", "prod"}, {"web", "dev"}} {
		if _, err := conn.ExecContext(t.Context(), seedStack, s[0], s[1]); err != nil {
			t.Fatalf("seed stack %s: %v", s[1], err)
		}
	}
}
