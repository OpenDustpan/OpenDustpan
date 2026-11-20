package postgres_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// TestNameLockConformance runs the suite of the lockout by name: each handle
// opens one schema, as two servers open one database. The broken handle is a
// store that is closed.
func TestNameLockConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	authgatetest.RunNameLocks(t, func(t *testing.T) authgatetest.NameLockHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return authgatetest.NameLockHarness{
			Open: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				return openURL(t, schemaURL)
			},
			Age: func(t *testing.T, d time.Duration) {
				t.Helper()

				ageNameLocks(t, openURL(t, schemaURL), d)
			},
			Place: func(t *testing.T, scope, name, sub string, offset time.Duration) {
				t.Helper()

				placeNameLocks(t, openURL(t, schemaURL), nameCount{scope: scope, name: name, sub: sub}, offset)
			},
			SetTimes: func(t *testing.T, scope, name, sub string, lastFailure, lockedUntil, lastLockEnd time.Duration) {
				t.Helper()

				setNameLockTimes(t, openURL(t, schemaURL), nameCount{scope: scope, name: name, sub: sub},
					[3]time.Duration{lastFailure, lockedUntil, lastLockEnd})
			},
			Broken: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				closed := openURL(t, schemaURL)
				closed.Close()

				return closed
			},
			Entries: func(t *testing.T) []audit.Entry {
				t.Helper()

				return serverEntries(t, openURL(t, schemaURL))
			},
			Notices: func(t *testing.T) []notify.Notification {
				t.Helper()

				return serverNotices(t, openURL(t, schemaURL).Notifications())
			},
		}
	})
}

// TestNameLockKey proves that two handles on one database read one key of 32
// bytes.
func TestNameLockKey(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)

	first, err := openURL(t, schemaURL).NameLockKey(t.Context())
	if err != nil || len(first) != 32 {
		t.Fatalf("key = %d bytes, %v; want 32", len(first), err)
	}

	second, err := openURL(t, schemaURL).NameLockKey(t.Context())
	if err != nil || !bytes.Equal(second, first) {
		t.Fatalf("second handle reads another key or fails: %v", err)
	}
}

// serverEntries returns the audit entries of the installation that s holds.
func serverEntries(t *testing.T, s *postgres.Store) []audit.Entry {
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
func ageNameLocks(t *testing.T, s *postgres.Store, d time.Duration) {
	t.Helper()

	_, err := postgres.Pool(s).Exec(t.Context(), `UPDATE name_locks SET
		locked_until_ms  = CASE WHEN locked_until_ms <> 0 THEN locked_until_ms - $1 ELSE 0 END,
		last_failure_ms  = CASE WHEN last_failure_ms <> 0 THEN last_failure_ms - $1 ELSE 0 END,
		last_lock_end_ms = CASE WHEN last_lock_end_ms <> 0 THEN last_lock_end_ms - $1 ELSE 0 END`,
		d.Milliseconds())
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
// that k names, to the time of the clock of the database plus offset, so a
// case fixes the time of a count without a wait. A time of 0, a count with
// no lock or no end, stays 0.
func placeNameLocks(t *testing.T, s *postgres.Store, k nameCount, offset time.Duration) {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	at := now.UnixMilli() + offset.Milliseconds()

	// The cast keeps each placeholder a bigint: the CASE branch of 0 alone
	// types it an int4, and a time in milliseconds does not fit an int4.
	tag, err := postgres.Pool(s).Exec(t.Context(), `UPDATE name_locks SET
		locked_until_ms  = CASE WHEN locked_until_ms <> 0 THEN $1::bigint ELSE 0 END,
		last_failure_ms  = CASE WHEN last_failure_ms <> 0 THEN $1::bigint ELSE 0 END,
		last_lock_end_ms = CASE WHEN last_lock_end_ms <> 0 THEN $1::bigint ELSE 0 END
		WHERE scope = $2 AND name = $3 AND sub = $4`,
		at, k.scope, k.name, k.sub)
	if err != nil {
		t.Fatalf("place the name count: %v", err)
	}

	if rows := tag.RowsAffected(); rows != 1 {
		t.Fatalf("the update of the name count %s, %s, %s changed %d rows; want 1", k.scope, k.name, k.sub, rows)
	}
}

// setNameLockTimes sets the three time columns of one name count of s, the
// count that k names, each to the time of the clock of the database plus its
// offset: the last failure, the end of the lock and the end of the last
// lock. An offset of authgatetest.NoTime stores 0.
func setNameLockTimes(t *testing.T, s *postgres.Store, k nameCount, offsets [3]time.Duration) {
	t.Helper()

	now, err := s.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	var at [3]int64

	for i, o := range offsets {
		if o != authgatetest.NoTime {
			at[i] = now.UnixMilli() + o.Milliseconds()
		}
	}

	tag, err := postgres.Pool(s).Exec(t.Context(), `UPDATE name_locks SET
		last_failure_ms = $1::bigint, locked_until_ms = $2::bigint, last_lock_end_ms = $3::bigint
		WHERE scope = $4 AND name = $5 AND sub = $6`,
		at[0], at[1], at[2], k.scope, k.name, k.sub)
	if err != nil {
		t.Fatalf("set the times of the name count: %v", err)
	}

	if rows := tag.RowsAffected(); rows != 1 {
		t.Fatalf("the update of the name count %s, %s, %s changed %d rows; want 1", k.scope, k.name, k.sub, rows)
	}
}
