package postgres_test

import (
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestKindConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunKind(t, func(t *testing.T) installation.KindStore {
		t.Helper()

		return open(t, base)
	})
}

// execInSchema runs statements on the live and control schema that
// schemaURL names, on a connection of its own.
func execInSchema(t *testing.T, schemaURL string, statements ...string) {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	parsed, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse schema URL: %v", err)
	}

	live := parsed.Query().Get("search_path")
	setPath := "SET search_path TO " + pgx.Identifier{live}.Sanitize() + ", " +
		pgx.Identifier{live + "_control"}.Sanitize()

	if _, execErr := conn.Exec(ctx, setPath); execErr != nil {
		t.Fatalf("set search path: %v", execErr)
	}

	for _, stmt := range statements {
		if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
			t.Fatalf("run %q: %v", stmt, execErr)
		}
	}
}

// openKindFixture opens a new schema, claims claimed (when not empty) and
// closes the store. It returns the URL of the schema.
func openKindFixture(t *testing.T, claimed installation.Kind) string {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL integration tests", envURL)
	}

	schemaURL := isolatedURL(t, base)

	first, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer first.Close()

	if claimed != "" {
		if _, claimErr := first.ClaimKind(t.Context(), claimed); claimErr != nil {
			t.Fatalf("claim %q: %v", claimed, claimErr)
		}
	}

	return schemaURL
}

// TestOpenKindCheck proves that Open compares the kind of the database with
// the kind of the command before it applies a migration.
func TestOpenKindCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		claimed installation.Kind
		open    installation.Kind
		wantErr error
	}{
		{name: "dev database, dev command", claimed: installation.KindDev, open: installation.KindDev, wantErr: nil},
		{
			name: "production database, production command", claimed: installation.KindProduction,
			open: installation.KindProduction, wantErr: nil,
		},
		{
			name: "dev database, production command", claimed: installation.KindDev,
			open: installation.KindProduction, wantErr: installation.ErrKindMismatch,
		},
		{
			name: "production database, dev command", claimed: installation.KindProduction,
			open: installation.KindDev, wantErr: installation.ErrKindMismatch,
		},
		{name: "database with no kind, dev command", claimed: "", open: installation.KindDev, wantErr: nil},
		{name: "dev database, command with no kind", claimed: installation.KindDev, open: "", wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			schemaURL := openKindFixture(t, tt.claimed)

			second, err := postgres.Open(t.Context(), schemaURL, postgres.WithInstallationKind(tt.open))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("open as %q: err = %v, want %v", tt.open, err, tt.wantErr)
			}

			if err == nil {
				second.Close()
			}
		})
	}
}

// TestOpenKindMismatchAppliesNoMigration proves that a command of another
// kind stops before any migration. The test removes the record of the kind
// migration while its column stays: a migration that ran would fail with a
// duplicate column.
func TestOpenKindMismatchAppliesNoMigration(t *testing.T) {
	t.Parallel()

	schemaURL := openKindFixture(t, installation.KindDev)

	execInSchema(t, schemaURL, `DELETE FROM schema_migrations WHERE name = '0118_installation_kind.sql'`)

	_, err := postgres.Open(t.Context(), schemaURL, postgres.WithInstallationKind(installation.KindProduction))

	var mismatch *installation.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("open as production: err = %v, want a *MismatchError", err)
	}

	if mismatch.Stored != installation.KindDev || mismatch.Want != installation.KindProduction {
		t.Fatalf("mismatch = %+v, want stored dev and want production", *mismatch)
	}
}

// TestOpenKindOnDatabaseWithoutColumn proves that a database from before the
// kind column opens with a command kind: the migration adds the column, and
// the claim decides.
func TestOpenKindOnDatabaseWithoutColumn(t *testing.T) {
	t.Parallel()

	schemaURL := openKindFixture(t, "")

	execInSchema(t, schemaURL,
		`ALTER TABLE installation DROP COLUMN kind`,
		`DELETE FROM schema_migrations WHERE name = '0118_installation_kind.sql'`,
	)

	second, err := postgres.Open(t.Context(), schemaURL, postgres.WithInstallationKind(installation.KindDev))
	if err != nil {
		t.Fatalf("open as dev: %v", err)
	}

	defer second.Close()

	got, err := second.ClaimKind(t.Context(), installation.KindDev)
	if err != nil || got != installation.KindDev {
		t.Fatalf("claim dev = %q, %v, want dev without error", got, err)
	}
}

// TestKindUnknownStoredValue proves that a stored value outside the known
// kinds is a server fault: the claim reports ErrUnavailable, and so does the
// check at Open. The test drops the CHECK constraint to plant the value.
func TestKindUnknownStoredValue(t *testing.T) {
	t.Parallel()

	schemaURL := openKindFixture(t, "")

	execInSchema(t, schemaURL,
		`ALTER TABLE installation DROP CONSTRAINT installation_kind_check`,
		`UPDATE installation SET kind = 'staging'`,
	)

	_, err := postgres.Open(t.Context(), schemaURL, postgres.WithInstallationKind(installation.KindDev))
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("open with an unknown stored kind: err = %v, want ErrUnavailable", err)
	}

	store, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open with no command kind: %v", err)
	}

	defer store.Close()

	_, err = store.ClaimKind(t.Context(), installation.KindDev)
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("claim with an unknown stored kind: err = %v, want ErrUnavailable", err)
	}
}
