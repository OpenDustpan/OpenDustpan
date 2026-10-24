package sqlite

import (
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// SetEnterWaitForTest sets the bound of the wait of an enter of maintenance
// mode, so that a test does not hold a write for the whole bound.
func SetEnterWaitForTest(s *Store, wait time.Duration) {
	s.enterWait = wait
}

// HoldCheckedWriteForTest starts a checked write that stays in flight until
// the returned function runs, and returns when the body of the write runs. A
// test that ends while it holds the write releases it.
func HoldCheckedWriteForTest(t *testing.T, s *Store) func() {
	t.Helper()

	entered := make(chan struct{})
	free := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- s.inTx(t.Context(), func(*db.Queries) error {
			close(entered)
			<-free

			return nil
		})
	}()

	var (
		once   sync.Once
		endErr error
	)

	end := func() error {
		once.Do(func() {
			close(free)

			endErr = <-done
		})

		return endErr
	}

	// The context of a failed test has ended, so the commit may fail too: the
	// cleanup does not report that second failure.
	t.Cleanup(func() {
		if err := end(); err != nil && !t.Failed() {
			t.Errorf("held write = %v, want nil", err)
		}
	})

	select {
	case <-entered:
	case <-time.After(gateWait):
		t.Fatal("timed out waiting for the held write to run")
	}

	return func() {
		if err := end(); err != nil {
			t.Errorf("held write = %v, want nil", err)
		}
	}
}
