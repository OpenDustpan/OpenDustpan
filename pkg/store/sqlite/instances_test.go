package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The store meets installation.MinimumChecker, so the start check of a restore
// and the readiness probe of the server never skip it.
var _ installation.MinimumChecker = (*sqlite.Store)(nil)

func TestInstanceConformance(t *testing.T) {
	t.Parallel()

	storetest.RunInstances(t, func(t *testing.T) installation.InstanceStore {
		t.Helper()

		return open(t)
	})
}

func TestStoreCheckConformance(t *testing.T) {
	t.Parallel()

	storetest.RunStoreChecks(t, func(t *testing.T) storetest.StoreCheckStore {
		t.Helper()

		st := openBackup(t)

		records, ok := st.(storecheck.Records)
		if !ok {
			t.Fatal("the store keeps no store checks")
		}

		witness, ok := st.(storetest.NullEndedWitness)
		if !ok {
			t.Fatal("the store has no raw end-time reader")
		}

		return storetest.StoreCheckStore{Records: records, Ops: st.DurableOperationsPort(), Witness: witness}
	})
}
