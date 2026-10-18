package sqlite_test

import (
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
	if _, err := conn.ExecContext(ctx, insert); err != nil {
		t.Fatalf("insert namespaces: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
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

		if _, err := namespace.ParseID(ns.ID.String()); err != nil {
			t.Fatalf("namespace %q has ID %q: %v", name, ns.ID, err)
		}

		if seen[ns.ID] {
			t.Fatalf("namespace %q has the ID %q of another namespace", name, ns.ID)
		}

		seen[ns.ID] = true

		got, err := s.GetNamespaceByID(t.Context(), ns.ID)
		if err != nil || got != ns {
			t.Fatalf("get namespace by ID %q = %+v, %v, want %+v", ns.ID, got, err, ns)
		}
	}
}
