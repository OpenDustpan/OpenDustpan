package postgres

import (
	"testing"
	"time"
)

// SetStateLockWaitForTest sets the bound of the wait of an enter of
// maintenance mode, so that a test does not hold a write for the whole bound.
func SetStateLockWaitForTest(s *Store, wait time.Duration) {
	s.stateLockWait = wait
}

// HoldCheckedWriteForTest starts a write transaction that holds the shared
// write lock until the returned function runs. A test that ends while it holds
// the write releases it.
func HoldCheckedWriteForTest(t *testing.T, s *Store) func() {
	t.Helper()

	ctx := t.Context()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = tx.Exec(ctx, writeLockSQL, writeLockClass); err != nil {
		t.Fatalf("take the shared lock: %v", err)
	}

	var released bool

	release := func() {
		if released {
			return
		}

		released = true

		if err := tx.Rollback(ctx); err != nil && !t.Failed() {
			t.Errorf("release the held write = %v, want nil", err)
		}
	}

	t.Cleanup(release)

	return release
}
