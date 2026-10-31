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
		"a lock time of 0 holds until a clear",
		"a scope with a character other than a to z, an empty key, a limit below 1 or a negative lock time fails with ErrInvalidLock",
		"a good password with a wrong factor does not clear the name count",
		"factor failures across pending sign-ins lock the person",
		"the lock of one name and one source prefix does not lock the name from another prefix",
		"the global count of a name locks the name from every prefix",
		"two IPv6 addresses in one /64 share one count",
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
