package sqlite_test

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
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
			Age: func(t *testing.T, d time.Duration) {
				t.Helper()

				ageNameLocks(t, s, d)
			},
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
			Entries: func(t *testing.T) []audit.Entry {
				t.Helper()

				return serverEntries(t, s)
			},
			Notices: func(t *testing.T) []notify.Notification {
				t.Helper()

				return serverNotices(t, s.Notifications())
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
	if err != nil || !bytes.Equal(second, first) {
		t.Fatalf("second key differs or fails: %v", err)
	}
}

// serverEntries returns the audit entries of the installation that s holds.
func serverEntries(t *testing.T, s *sqlite.Store) []audit.Entry {
	t.Helper()

	page, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return page.Entries
}

// serverNotices returns the notifications of the server scope that n holds,
// in the order of the append.
func serverNotices(t *testing.T, n notify.Outbox) []notify.Notification {
	t.Helper()

	got, err := n.After(t.Context(), audit.ServerScope, 0, notify.MaxRecipients*1000)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}

	return got
}

// ageNameLocks moves every time column of every name count of s back by d,
// so a case reaches the age of a count without a wait. A time of 0, a count
// with no lock or no end, stays 0.
func ageNameLocks(t *testing.T, s *sqlite.Store, d time.Duration) {
	t.Helper()

	_, err := sqlite.DB(s).ExecContext(t.Context(), `UPDATE name_locks SET
		locked_until_ms  = CASE WHEN locked_until_ms <> 0 THEN locked_until_ms - ? ELSE 0 END,
		last_failure_ms  = CASE WHEN last_failure_ms <> 0 THEN last_failure_ms - ? ELSE 0 END,
		last_lock_end_ms = CASE WHEN last_lock_end_ms <> 0 THEN last_lock_end_ms - ? ELSE 0 END`,
		d.Milliseconds(), d.Milliseconds(), d.Milliseconds())
	if err != nil {
		t.Fatalf("age the name counts: %v", err)
	}
}
