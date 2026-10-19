package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestGateConformance(t *testing.T) {
	t.Parallel()

	storetest.RunGate(t, func(t *testing.T) storetest.GateStore {
		t.Helper()

		return open(t)
	})
}
