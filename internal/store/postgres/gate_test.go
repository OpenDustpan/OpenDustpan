package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestGateConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunGate(t, func(t *testing.T) storetest.GateStore {
		t.Helper()

		return open(t, base)
	})
}
