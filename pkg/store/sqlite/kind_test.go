package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestKindConformance(t *testing.T) {
	t.Parallel()

	storetest.RunKind(t, func(t *testing.T) installation.KindStore {
		t.Helper()

		return open(t)
	})
}

// claimKindFixture makes a database, claims claimed on it when it is not
// empty, closes it and answers its path.
func claimKindFixture(t *testing.T, claimed installation.Kind) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	if claimed != "" {
		if _, claimErr := first.ClaimKind(t.Context(), claimed); claimErr != nil {
			t.Fatalf("claim %q: %v", claimed, claimErr)
		}
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}

	return path
}

// TestOpenKindCheck proves that Open compares the kind of the database with
// the kind of the command before it applies a migration.
func TestOpenKindCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// claimed is the kind that the first command stores. Empty means no
		// command claims.
		claimed installation.Kind
		// open is the kind of the second command.
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

			path := claimKindFixture(t, tt.claimed)

			second, err := sqlite.Open(t.Context(), path, sqlite.WithInstallationKind(tt.open))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("open as %q: err = %v, want %v", tt.open, err, tt.wantErr)
			}

			if err == nil {
				if closeErr := second.Close(); closeErr != nil {
					t.Fatalf("close the database: %v", closeErr)
				}
			}
		})
	}
}

// TestOpenKindMismatchAppliesNoMigration proves that a command of another
// kind stops before any migration. The test removes the record of the kind
// migration while its column stays: a migration that ran would fail with a
// duplicate column, and the record would come back.
func TestOpenKindMismatchAppliesNoMigration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	if _, claimErr := first.ClaimKind(t.Context(), installation.KindDev); claimErr != nil {
		t.Fatalf("claim dev: %v", claimErr)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	const forget = `DELETE FROM schema_migrations WHERE name = '0118_installation_kind.sql'`
	if _, execErr := raw.ExecContext(t.Context(), forget); execErr != nil {
		t.Fatalf("remove the record of the kind migration: %v", execErr)
	}

	if closeErr := raw.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	_, err = sqlite.Open(t.Context(), path, sqlite.WithInstallationKind(installation.KindProduction))

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

	path := filepath.Join(t.TempDir(), "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	for _, stmt := range []string{
		`ALTER TABLE installation DROP COLUMN kind`,
		`DELETE FROM schema_migrations WHERE name = '0118_installation_kind.sql'`,
	} {
		if _, execErr := raw.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("make a database from before the kind column: %v", execErr)
		}
	}

	if closeErr := raw.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	second, err := sqlite.Open(t.Context(), path, sqlite.WithInstallationKind(installation.KindDev))
	if err != nil {
		t.Fatalf("open as dev: %v", err)
	}

	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	got, err := second.ClaimKind(t.Context(), installation.KindDev)
	if err != nil || got != installation.KindDev {
		t.Fatalf("claim dev = %q, %v, want dev without error", got, err)
	}
}

// TestKindUnknownStoredValue proves that a stored value outside the known
// kinds is a server fault: the claim reports ErrUnavailable, and so does the
// check at Open. The test turns the CHECK constraint off for one connection
// to plant the value.
func TestKindUnknownStoredValue(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	first, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close the database: %v", closeErr)
	}

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	raw.SetMaxOpenConns(1)

	for _, stmt := range []string{
		`PRAGMA ignore_check_constraints = ON`,
		`UPDATE installation SET kind = 'staging'`,
	} {
		if _, execErr := raw.ExecContext(t.Context(), stmt); execErr != nil {
			t.Fatalf("plant an unknown kind: %v", execErr)
		}
	}

	if closeErr := raw.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	_, err = sqlite.Open(t.Context(), path, sqlite.WithInstallationKind(installation.KindDev))
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("open with an unknown stored kind: err = %v, want ErrUnavailable", err)
	}

	store, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open with no command kind: %v", err)
	}

	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	_, err = store.ClaimKind(t.Context(), installation.KindDev)
	if !errors.Is(err, installation.ErrUnavailable) {
		t.Fatalf("claim with an unknown stored kind: err = %v, want ErrUnavailable", err)
	}
}
