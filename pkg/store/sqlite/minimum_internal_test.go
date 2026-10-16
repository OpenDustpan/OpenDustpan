package sqlite

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// TestContractMigrationRaisesMinimum applies a migration that declares a
// minimum binary version. The schema then records it, and a binary below it
// cannot open the database.
func TestContractMigrationRaisesMinimum(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyInternalTemplate(t, path)

	s, err := Open(t.Context(), path, WithBinaryVersion("v0.5.0"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	contract := migrate.Migration{
		Name:             "9999_contract.sql",
		SQL:              "SELECT 1",
		MinBinaryVersion: "v0.5.0",
		Version:          9999,
	}

	applyErr := s.apply(t.Context(), contract)
	applied, readErr := s.AppliedMigrations(t.Context())

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	if applyErr != nil || readErr != nil {
		t.Fatalf("apply contract migration: %v, %v", applyErr, readErr)
	}

	if len(applied) == 0 {
		t.Fatal("applied migrations are empty, want the recorded migrations")
	}

	if last := applied[len(applied)-1]; last.Version != 9999 || last.MinBinaryVersion != "v0.5.0" {
		t.Fatalf("newest applied migration = %+v, want 9999 with minimum v0.5.0", last)
	}

	_, belowErr := Open(t.Context(), path, WithBinaryVersion("v0.4.9"))
	if !errors.Is(belowErr, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("open below the raised minimum: err = %v, want ErrBinaryBelowMinimum", belowErr)
	}

	again, err := Open(t.Context(), path, WithBinaryVersion("v0.5.0"))
	if err != nil {
		t.Fatalf("open at the raised minimum: %v", err)
	}

	if err := again.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}
