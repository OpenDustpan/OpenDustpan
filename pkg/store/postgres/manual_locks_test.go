package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/manuallock/manuallocktest"
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
