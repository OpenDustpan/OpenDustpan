package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/manuallock/manuallocktest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

func TestManualLockConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	manuallocktest.Run(t, func(t *testing.T) manuallocktest.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestManualLockCompletion(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	manuallocktest.RunCompletion(t, func(t *testing.T) manuallocktest.Store {
		t.Helper()

		return open(t, base)
	}, func(t *testing.T, s manuallocktest.Store) idempotency.Store {
		t.Helper()

		st, ok := s.(*postgres.Store)
		if !ok {
			t.Fatalf("store is %T", s)
		}

		return st.Idempotency(idempotencytest.TestWrapper(t, 1))
	})
}
