package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/manuallock/manuallocktest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func TestManualLockConformance(t *testing.T) {
	t.Parallel()

	manuallocktest.Run(t, func(t *testing.T) manuallocktest.Store {
		t.Helper()

		return open(t)
	})
}

func TestManualLockCompletion(t *testing.T) {
	t.Parallel()

	manuallocktest.RunCompletion(t, func(t *testing.T) manuallocktest.Store {
		t.Helper()

		return open(t)
	}, func(t *testing.T, s manuallocktest.Store) idempotency.Store {
		t.Helper()

		st, ok := s.(*sqlite.Store)
		if !ok {
			t.Fatalf("store is %T", s)
		}

		return st.Idempotency(idempotencytest.TestWrapper(t, 1))
	})
}
