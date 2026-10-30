package sqlite

import (
	"path/filepath"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// controlHandle is the handle of a store in the control minimum suites. It
// closes the store once, whether the suite or the cleanup of the test asks
// first.
type controlHandle struct {
	store *Store
	path  string
	once  sync.Once
}

func (h *controlHandle) close(t *testing.T) {
	t.Helper()

	h.once.Do(func() {
		if err := h.store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
}

// controlStoreOf returns the ports of s for the control minimum suites.
func controlStoreOf(s *Store, h *controlHandle) storetest.ControlStore {
	return storetest.ControlStore{
		Instances:   s,
		Maintenance: s,
		Backups:     s,
		Durable:     s.DurableOperations(),
		Audit:       s,
		Failures:    s,
		Handle:      h,
	}
}

// controlOpen opens the database at path as a binary of version binary.
func controlOpen(t *testing.T, path, binary string) storetest.ControlStore {
	t.Helper()

	s, err := Open(t.Context(), path, WithBinaryVersion(binary))
	if err != nil {
		t.Fatalf("open the binary %q: %v", binary, err)
	}

	h := &controlHandle{store: s, path: path, once: sync.Once{}}

	t.Cleanup(func() { h.close(t) })

	return controlStoreOf(s, h)
}

// controlHarness is the harness of the control minimum suites. SQLite allows
// one handle on a file, so the same handle raises the minimum, and the
// successor replaces the old handle.
func controlHarness() storetest.ControlMinimumHarness {
	return storetest.ControlMinimumHarness{
		Open: func(t *testing.T, binary string) storetest.ControlStore {
			t.Helper()

			path := filepath.Join(t.TempDir(), "dustpan.db")
			copyInternalTemplate(t, path)

			return controlOpen(t, path, binary)
		},
		Raise: func(t *testing.T, s storetest.ControlStore) {
			t.Helper()

			h, ok := s.Handle.(*controlHandle)
			if !ok {
				t.Fatalf("handle = %T, want *controlHandle", s.Handle)
			}

			if err := h.store.apply(t.Context(), contractMigration(storetest.ControlMinimum)); err != nil {
				t.Fatalf("apply the contract migration: %v", err)
			}
		},
		Successor: func(t *testing.T, old storetest.ControlStore) storetest.ControlStore {
			t.Helper()

			h, ok := old.Handle.(*controlHandle)
			if !ok {
				t.Fatalf("handle = %T, want *controlHandle", old.Handle)
			}

			h.close(t)

			return controlOpen(t, h.path, "v0.6.0")
		},
	}
}

// TestControlMinimum proves the decision about each control path: a binary
// below the minimum is refused, or allowed with a reason.
func TestControlMinimum(t *testing.T) {
	t.Parallel()

	storetest.RunControlMinimum(t, controlHarness())
}

// TestControlMinimumRunner proves that a durable runner of a binary below the
// minimum never claims an operation, and that a runner of a new binary takes
// it.
func TestControlMinimumRunner(t *testing.T) {
	t.Parallel()

	storetest.RunControlMinimumRunner(t, controlHarness())
}
