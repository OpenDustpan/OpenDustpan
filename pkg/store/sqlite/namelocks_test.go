package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestNameLockConformance runs the suite of the lockout by name. One SQLite
// file allows one store, so the suite gets that store for every handle. The
// broken handle is a store that is closed.
func TestNameLockConformance(t *testing.T) {
	t.Parallel()

	authgatetest.RunNameLocks(t, func(t *testing.T) authgatetest.NameLockHarness {
		t.Helper()

		s := open(t)

		return authgatetest.NameLockHarness{
			Open: func(*testing.T) authgate.NameLockCounter { return s },
			Broken: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				closed, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "closed.db"))
				if err != nil {
					t.Fatalf("open store: %v", err)
				}

				if err := closed.Close(); err != nil {
					t.Fatalf("close store: %v", err)
				}

				return closed
			},
		}
	})
}

// TestNameLockKey proves that the key of the digests of the names is 32
// bytes, and that a second read returns the same key.
func TestNameLockKey(t *testing.T) {
	t.Parallel()

	s := open(t)

	first, err := s.NameLockKey(t.Context())
	if err != nil || len(first) != 32 {
		t.Fatalf("key = %d bytes, %v; want 32", len(first), err)
	}

	second, err := s.NameLockKey(t.Context())
	if err != nil || string(second) != string(first) {
		t.Fatalf("second key differs or fails: %v", err)
	}
}
