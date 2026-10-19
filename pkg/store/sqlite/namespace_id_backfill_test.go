package sqlite_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// namespaceIDVersion is the version of the migration that adds namespace IDs.
const namespaceIDVersion = 105

// TestNamespaceIDBackfill applies the migrations before namespace IDs, adds
// namespaces, then opens the store, which applies the rest. Each namespace
// gets its own valid ID, and the ID finds the namespace.
func TestNamespaceIDBackfill(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= namespaceIDVersion })
	path := filepath.Join(t.TempDir(), "backfill.db")

	applyTagMigrations(ctx, t, path, before)

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	const insert = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0), ('globex', 'eu-west', 1, 0)`
	if _, execErr := conn.ExecContext(ctx, insert); execErr != nil {
		t.Fatalf("insert namespaces: %v", execErr)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	checkBackfilledIDs(t, s, "acme", "globex")
}

// checkBackfilledIDs fails unless each namespace of names has a valid ID,
// no two IDs are equal, and each ID finds its namespace.
func checkBackfilledIDs(t *testing.T, s *sqlite.Store, names ...namespace.Name) {
	t.Helper()

	seen := make(map[namespace.ID]bool, len(names))

	for _, name := range names {
		ns, err := s.GetNamespace(t.Context(), name)
		if err != nil {
			t.Fatalf("get namespace %q: %v", name, err)
		}

		if _, parseErr := namespace.ParseID(ns.ID.String()); parseErr != nil {
			t.Fatalf("namespace %q has ID %q: %v", name, ns.ID, parseErr)
		}

		if seen[ns.ID] {
			t.Fatalf("namespace %q has the ID %q of another namespace", name, ns.ID)
		}

		seen[ns.ID] = true
	}
}

// TestNamespaceIDTriggers proves that the database itself refuses a namespace
// ID without the ns- prefix and one more character, on insert and on update.
func TestNamespaceIDTriggers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "triggers.db")

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	const insert = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
		VALUES (?, 'eu-west', 1, 0, ?)`

	if _, execErr := conn.ExecContext(ctx, insert, "good", "ns-abc"); execErr != nil {
		t.Fatalf("insert namespace with a valid ID: %v", execErr)
	}

	for i, id := range []string{"", "acme", "ns-", "NS-abc"} {
		if _, execErr := conn.ExecContext(ctx, insert, fmt.Sprintf("bad%d", i), id); execErr == nil {
			t.Fatalf("insert namespace with ID %q: err = nil, want a refusal", id)
		}

		const update = `UPDATE namespaces SET id = ? WHERE name = 'good'`
		if _, execErr := conn.ExecContext(ctx, update, id); execErr == nil {
			t.Fatalf("update namespace to ID %q: err = nil, want a refusal", id)
		}
	}
}
