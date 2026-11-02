package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// storedKindDB makes a database at a new path. A kind that is not empty
// claims it.
func storedKindDB(t *testing.T, kind installation.Kind) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if kind != "" {
		if _, claimErr := s.ClaimKind(t.Context(), kind); claimErr != nil {
			t.Fatalf("claim: %v", claimErr)
		}
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	return path
}

// TestStoredKindReadsTheClaimedKind proves that StoredKind answers the kind
// that a command claimed.
func TestStoredKindReadsTheClaimedKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{installation.KindDev, installation.KindProduction} {
		got, err := sqlite.StoredKind(t.Context(), storedKindDB(t, kind))
		if err != nil || got != kind {
			t.Fatalf("StoredKind = %q, %v; want %q", got, err, kind)
		}
	}
}

// TestStoredKindFailsClosed sends databases with no kind, with a kind that is
// not known, and with no installation row directly to StoredKind. Each one
// fails with installation.ErrUnavailable.
func TestStoredKindFailsClosed(t *testing.T) {
	t.Parallel()

	for name, change := range map[string][]string{
		"no kind":             nil,
		"a space after dev":   {`PRAGMA ignore_check_constraints = ON`, `UPDATE installation SET kind = 'dev ' WHERE id = 1`},
		"an unknown kind":     {`PRAGMA ignore_check_constraints = ON`, `UPDATE installation SET kind = 'staging' WHERE id = 1`},
		"an upper-case kind":  {`PRAGMA ignore_check_constraints = ON`, `UPDATE installation SET kind = 'DEV' WHERE id = 1`},
		"no installation row": {`PRAGMA foreign_keys = OFF`, `DELETE FROM installation`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kind := installation.KindDev
			if change == nil {
				kind = ""
			}

			path := storedKindDB(t, kind)
			execRaw(t, path, change...)

			if got, err := sqlite.StoredKind(t.Context(), path); !errors.Is(err, installation.ErrUnavailable) {
				t.Fatalf("StoredKind = %q, %v; want %v", got, err, installation.ErrUnavailable)
			}
		})
	}
}

// TestStoredKindRefusesAFileInUse proves that StoredKind takes the guard of
// Open: it refuses a file that a store holds, and a file that does not exist.
func TestStoredKindRefusesAFileInUse(t *testing.T) {
	t.Parallel()

	path := storedKindDB(t, installation.KindDev)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := sqlite.StoredKind(t.Context(), path); !errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("StoredKind of a file in use = %v, want %v", err, sqlite.ErrInUse)
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	if _, err := sqlite.StoredKind(t.Context(), filepath.Join(t.TempDir(), "none.db")); err == nil {
		t.Fatal("StoredKind of a missing file succeeded, want an error")
	}
}
