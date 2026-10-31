package authgatetest

import (
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
)

// NameLockFactory returns a fresh, empty counter for one subtest.
type NameLockFactory func(t *testing.T) authgate.NameLockCounter

// NameLockCaseNames returns the names of the cases of the suite of
// authgate.NameLockCounter, in the order of the run.
func NameLockCaseNames() []string {
	return []string{
		"a first failure counts 1 and sets no lock",
		"the failure that reaches the limit passes and sets the lock",
		"a failure while the lock holds fails with ErrNameLocked and counts nothing",
		"a failure after the lock ends starts a new count at 1",
		"a clear sets the count to 0 and ends the lock",
		"a clear of a key with no count is no error",
		"two scopes keep separate counts for one key",
		"concurrent failures never pass the limit",
		"the lock time comes from the clock of the store",
		"every instance on one database shares one count",
		"a prune deletes old counts and keeps a lock that holds",
		"a write that fails is an error that is not ErrNameLocked",
	}
}

// RunNameLocks runs every case of the suite against the counters that
// newCounter returns. Each case skips until a store adapter implements the
// port, so that the names are the contract that the first adapter meets.
func RunNameLocks(t *testing.T, newCounter NameLockFactory) {
	t.Helper()

	for _, name := range NameLockCaseNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if newCounter == nil {
				t.Fatal("authgatetest.RunNameLocks needs a counter factory")
			}

			t.Skip("no adapter implements authgate.NameLockCounter yet")
		})
	}
}
