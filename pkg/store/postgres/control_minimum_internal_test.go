package postgres

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// controlHandle is the handle of a store in the control minimum suites.
type controlHandle struct {
	store *Store
	url   string
}

// controlOpen opens the schema at url as a binary of version binary.
func controlOpen(t *testing.T, url, binary string) storetest.ControlStore {
	t.Helper()

	s := openAt(t, url, binary)

	return storetest.ControlStore{
		Instances:   s,
		Maintenance: s,
		Backups:     s,
		Durable:     s.DurableOperations(),
		Audit:       s,
		Failures:    s,
		Handle:      &controlHandle{store: s, url: url},
	}
}

func controlHandleOf(t *testing.T, s storetest.ControlStore) *controlHandle {
	t.Helper()

	h, ok := s.Handle.(*controlHandle)
	if !ok {
		t.Fatalf("handle = %T, want *controlHandle", s.Handle)
	}

	return h
}

// controlHarness is the harness of the control minimum suites. A second
// handle on the same schema stands for the new binary of a rolling deploy.
func controlHarness() storetest.ControlMinimumHarness {
	return storetest.ControlMinimumHarness{
		Open: func(t *testing.T, binary string) storetest.ControlStore {
			t.Helper()

			return controlOpen(t, membershipURL(t), binary)
		},
		Raise: func(t *testing.T, s storetest.ControlStore) {
			t.Helper()

			applyContract(t, controlHandleOf(t, s).store)
		},
		Successor: func(t *testing.T, old storetest.ControlStore) storetest.ControlStore {
			t.Helper()

			return controlOpen(t, controlHandleOf(t, old).url, "v0.6.0")
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
