// Package mfatest is the conformance suite of the mfa ports. A store adapter
// runs it:
//
//	func TestMFAConformance(t *testing.T) {
//		t.Parallel()
//		mfatest.Run(t, func(t *testing.T) mfatest.Store { return newMyStore(t) })
//	}
//
// The suite holds the names of its cases only. Each case skips until an
// adapter implements the ports, so that the names are the contract that the
// first adapter meets.
package mfatest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/mfa"
)

// Store is every persistence port the suite exercises.
type Store interface {
	mfa.PasskeyStore
	mfa.RecoveryCodeStore
	mfa.PendingStore
	audit.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// CaseNames returns the names of the cases, in the order of the run.
func CaseNames() []string {
	return []string{
		// PasskeyStore.
		"an added passkey reads back by person and by credential ID with its audit entry",
		"a passkey with an empty field or a name that the principal rules refuse fails with ErrInvalid",
		"a credential ID that another person holds fails with ErrAlreadyExists",
		"an add above MaxPasskeys fails with ErrLimit and stores nothing",
		"two adds at the same time at the limit: one wins",
		"a use with a higher counter records the counter and the time",
		"a use with a counter of 0 on a passkey with a counter of 0 is no regression",
		"a use with a lower or equal counter suspends the passkey, appends one entry and fails with ErrCounterRegressed",
		"a use of a suspended passkey fails with ErrSuspended",
		"a remove of the last passkey of a user with no password fails with ErrLastCredential",
		"a remove of a passkey of another person fails with ErrNotFound",
		"a remove of the last passkey and of the password at the same time: one wins and the person keeps a credential",
		"a challenge works once",
		"a challenge after its expiry fails with ErrNotFound",
		"two takes of one challenge at the same time: one wins",
		// RecoveryCodeStore.
		"a replace stores the digests and deletes the earlier set",
		"a replace with a count other than RecoveryCodeCount fails with ErrInvalid",
		"a code works once and the count of codes left goes down",
		"a code of another person fails with ErrNotFound",
		"two uses of one code at the same time: one wins",
		// PendingStore.
		"a created pending sign-in reads back by its digest",
		"a pending sign-in after its expiry fails with ErrNotFound",
		"each failure counts and the failure at the limit makes it void",
		"a pending sign-in that is void fails with ErrVoid and counts nothing",
		"concurrent failures never pass PendingMaxFailures",
		"a complete makes the session, deletes the pending sign-in and appends one entry in one transaction",
		"a second complete fails with ErrNotFound and makes no session",
		"a complete whose session write fails keeps the pending sign-in",
		"a complete for a session of another person fails with ErrInvalid",
		"a complete of a void pending sign-in fails with ErrVoid",
		"two completes at the same time: one wins and one session exists",
		"a prune deletes the expired pending sign-ins only",
		// All ports.
		"a change with an entry of another scope fails with audit.ErrInvalidEntry and changes nothing",
		"the store keeps the digest of a challenge, a code and a pending secret, never the secret",
	}
}

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	for _, name := range CaseNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if newStore == nil {
				t.Fatal("mfatest.Run needs a store factory")
			}

			t.Skip("no adapter implements the mfa ports yet")
		})
	}
}
