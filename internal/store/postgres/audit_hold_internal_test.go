package postgres

import (
	"context"
	"sync"
	"testing"
	"time"
)

// lockWaitPoll is the interval between two reads of the lock waiters.
const lockWaitPoll = 10 * time.Millisecond

// lockWaitLimit bounds the wait for the lock waiters, so that a case that
// never gets them fails and does not hang.
const lockWaitLimit = time.Minute

// HoldAuditWrites implements storetest.AuditWriteHolder. It takes the lock
// EXCLUSIVE on audit_entries in a transaction of its own. That lock lets a
// read of the table go on and makes an insert wait. release rolls the
// transaction back. The cleanup of t runs release too.
func (s *Store) HoldAuditWrites(t *testing.T) func() {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the hold of the audit writes: %v", err)
	}

	var once sync.Once

	release := func() {
		once.Do(func() {
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
				t.Errorf("end the hold of the audit writes: %v", rollbackErr)
			}
		})
	}

	t.Cleanup(release)

	if _, err := tx.Exec(ctx, "LOCK TABLE audit_entries IN EXCLUSIVE MODE"); err != nil {
		t.Fatalf("hold the audit writes: %v", err)
	}

	return release
}

// WaitForLockWaiters implements storetest.AuditWriteHolder. It reads
// pg_stat_activity until n sessions of the database of the store wait for a
// lock. Each test has a database of its own, so the count holds only the
// sessions of the test.
func (s *Store) WaitForLockWaiters(t *testing.T, n int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), lockWaitLimit)
	defer cancel()

	const waiters = "SELECT count(*) FROM pg_stat_activity" +
		" WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()"

	ticker := time.NewTicker(lockWaitPoll)
	defer ticker.Stop()

	for {
		var got int

		if err := s.pool.QueryRow(ctx, waiters).Scan(&got); err != nil {
			t.Fatalf("read the lock waiters (want %d): %v", n, err)
		}

		if got >= n {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("lock waiters = %d, want %d: %v", got, n, ctx.Err())
		case <-ticker.C:
		}
	}
}
