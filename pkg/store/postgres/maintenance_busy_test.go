package postgres_test

import (
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestEnterBusyConformance runs the busy case of an enter: a write in flight
// that outlasts the lock timeout makes the enter report ErrBusy.
func TestEnterBusyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunEnterBusy(t, func(t *testing.T) (storetest.Store, storetest.BusyHarness) {
		t.Helper()

		s := open(t, base)
		postgres.SetStateLockWaitForTest(s, 200*time.Millisecond)

		return s, storetest.BusyHarness{
			Hold: func(t *testing.T) func() {
				t.Helper()

				return postgres.HoldCheckedWriteForTest(t, s)
			},
		}
	})
}
