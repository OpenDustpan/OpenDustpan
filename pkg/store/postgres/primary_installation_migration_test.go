package postgres_test

import (
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

// primaryInstallationVersion is the version of the migration that renames the
// home_region column of a namespace to primary_installation.
const primaryInstallationVersion = 145

// TestPrimaryInstallationMigration applies the migrations before the rename,
// plants a namespace with a value in home_region, then opens the store, which
// applies the rest. The namespace keeps the value as its primary installation.
func TestPrimaryInstallationMigration(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL primary installation migration test", envURL)
	}

	ctx := t.Context()

	all, err := migrate.Load(frozenMigrations, "migrations")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}

	before := slices.DeleteFunc(all, func(m migrate.Migration) bool { return m.Version >= primaryInstallationVersion })
	schemaURL := emptySchemaURL(t, base)

	applyTagMigrations(ctx, t, withControlSchema(t, schemaURL), before)

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	id := namespace.NewID()

	_, err = conn.Exec(ctx,
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
			VALUES ('shop', 'eu-west', 1, 0, $1)`, id.String())
	if err != nil {
		t.Fatalf("plant namespace: %v", err)
	}

	if closeErr := conn.Close(ctx); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	s := openURL(t, schemaURL)

	got, err := s.GetNamespace(ctx, "shop")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.PrimaryInstallation != "eu-west" || got.ID != id {
		t.Fatalf("namespace = %+v, want primary installation eu-west and ID %s", got, id)
	}
}
