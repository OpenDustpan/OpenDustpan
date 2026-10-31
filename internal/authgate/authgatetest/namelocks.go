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
		"a second lock within the reset time lasts twice the first",
		"a lock never lasts longer than the maximum",
		"a lock after the reset time with no lock lasts the first lock time again",
		"a rule whose maximum equals its lock time never doubles",
		"a clear sets the count to 0, ends the lock and keeps the level",
		"a clear of a count that does not exist is no error",
		"a clear of a name ends the count of each sub-key and returns the count",
		"the list of a name returns one entry for each sub-key in the order of the sub-key",
		"the count of one sub-key does not lock another sub-key of the same name",
		"two scopes keep separate counts for one name",
		"concurrent failures never pass the limit",
		"the lock time comes from the clock of the store",
		"every instance on one database shares one count",
		"a prune deletes old counts and keeps a lock that holds and a level that has not reset",
		"a write that fails is an error that is not ErrNameLocked",
		"a scope with a character other than a to z, an empty name, a limit below 1, a lock time of 0, a maximum below the lock time or a negative reset fails with ErrInvalidLock",
		"the store holds the name as the caller gives it and no user name",
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
