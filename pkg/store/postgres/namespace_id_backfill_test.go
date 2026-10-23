package postgres_test

import (
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// namespaceIDVersion is the version of the migration that adds namespace IDs.
const namespaceIDVersion = 105

// TestNamespaceIDBackfill applies the migrations before namespace IDs, adds
// namespaces, then opens the store, which applies the rest. Each namespace
// gets its own valid ID, and the ID finds the namespace.
func TestNamespaceIDBackfill(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL namespace ID backfill test", envURL)
	}

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= namespaceIDVersion })
	schemaURL := emptySchemaURL(t, base)

	applyTagMigrations(ctx, t, schemaURL, before)

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	const insert = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0), ('globex', 'eu-west', 1, 0)`
	if _, err := conn.Exec(ctx, insert); err != nil {
		t.Fatalf("insert namespaces: %v", err)
	}

	if err := conn.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	checkBackfilledIDs(t, openURL(t, schemaURL), "acme", "globex")
}

// checkBackfilledIDs fails unless each namespace of names has a valid ID,
// no two IDs are equal, and each ID finds its namespace.
func checkBackfilledIDs(t *testing.T, s *postgres.Store, names ...namespace.Name) {
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
