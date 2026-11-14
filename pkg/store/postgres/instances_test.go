package postgres_test

import (
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The store meets installation.MinimumChecker, so the start check of a restore
// and the readiness probe of the server never skip it.
var _ installation.MinimumChecker = (*postgres.Store)(nil)

func TestInstanceConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunInstances(t, func(t *testing.T) installation.InstanceStore {
		t.Helper()

		return open(t, base)
	})
}

func TestStoreCheckConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunStoreChecks(t, func(t *testing.T) storetest.StoreCheckStore {
		t.Helper()

		st := openBackup(t, base)

		return storetest.StoreCheckStore{Records: st, Ops: st.DurableOperationsPort(), Witness: st}
	})
}
