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
	"opendustpan.org/opendustpan/internal/store/sqlite"
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
			Place: func(t *testing.T, scope, name, sub string, offset time.Duration) {
				t.Helper()

				placeNameLocks(t, s, nameCount{scope: scope, name: name, sub: sub}, offset)
			},
			SetTimes: func(t *testing.T, scope, name, sub string, lastFailure, lockedUntil, lastLockEnd time.Duration) {
				t.Helper()

				setNameLockTimes(t, s, nameCount{scope: scope, name: name, sub: sub},
					[3]time.Duration{lastFailure, lockedUntil, lastLockEnd})
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

	page, err := s.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.DefaultPageSize, Before: 0})
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

// nameCount names one stored name count: its scope, its name and its
// sub-key.
type nameCount struct {
	scope, name, sub string
}

// placeNameLocks moves each time column of one name count of s, the count
// that k names, to the time of the clock of the store plus offset, so a case
// fixes the time of a count without a wait. A time of 0, a count with no
// lock or no end, stays 0.
func placeNameLocks(t *testing.T, s *sqlite.Store, k nameCount, offset time.Duration) {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the clock of the store: %v", err)
	}

	at := now.UnixMilli() + offset.Milliseconds()

	res, err := sqlite.DB(s).ExecContext(t.Context(), `UPDATE name_locks SET
		locked_until_ms  = CASE WHEN locked_until_ms <> 0 THEN ? ELSE 0 END,
		last_failure_ms  = CASE WHEN last_failure_ms <> 0 THEN ? ELSE 0 END,
		last_lock_end_ms = CASE WHEN last_lock_end_ms <> 0 THEN ? ELSE 0 END
		WHERE scope = ? AND name = ? AND sub = ?`,
		at, at, at, k.scope, k.name, k.sub)
	if err != nil {
		t.Fatalf("place the name count: %v", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("count the changed rows of the name count %s, %s, %s: %v", k.scope, k.name, k.sub, err)
	}

	if rows != 1 {
		t.Fatalf("the update of the name count %s, %s, %s changed %d rows; want 1", k.scope, k.name, k.sub, rows)
	}
}

// setNameLockTimes sets the three time columns of one name count of s, the
// count that k names, each to the time of the clock of the store plus its
// offset: the last failure, the end of the lock and the end of the last
// lock. An offset of authgatetest.NoTime stores 0.
func setNameLockTimes(t *testing.T, s *sqlite.Store, k nameCount, offsets [3]time.Duration) {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the clock of the store: %v", err)
	}

	var at [3]int64

	for i, o := range offsets {
		if o != authgatetest.NoTime {
			at[i] = now.UnixMilli() + o.Milliseconds()
		}
	}

	res, err := sqlite.DB(s).ExecContext(t.Context(), `UPDATE name_locks SET
		last_failure_ms = ?, locked_until_ms = ?, last_lock_end_ms = ?
		WHERE scope = ? AND name = ? AND sub = ?`,
		at[0], at[1], at[2], k.scope, k.name, k.sub)
	if err != nil {
		t.Fatalf("set the times of the name count: %v", err)
	}

	if rows, rowsErr := res.RowsAffected(); rowsErr != nil || rows != 1 {
		t.Fatalf("the update of the name count %s, %s, %s changed %d rows, %v; want 1",
			k.scope, k.name, k.sub, rows, rowsErr)
	}
}
