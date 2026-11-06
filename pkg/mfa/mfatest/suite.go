// Package mfatest is the conformance suite of the mfa ports. A store adapter
// runs it:
//
//	func TestPasskeyConformance(t *testing.T) {
//		t.Parallel()
//		mfatest.RunPasskeys(t, func(t *testing.T) mfatest.PasskeyStore { return newMyStore(t) })
//	}
//
//	func TestMFAConformance(t *testing.T) {
//		t.Parallel()
//		mfatest.Run(t, func(t *testing.T) mfatest.Store { return newMyStore(t) })
//	}
//
// RunPasskeys runs the cases of PasskeyStore. Run holds the names of the
// cases of the other ports. Each of them skips until an adapter implements
// its port, so that the names are the contract that the first adapter meets.
package mfatest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Store is every persistence port the suite of Run exercises.
type Store interface {
	mfa.PasskeyStore
	mfa.RecoveryCodeStore
	mfa.PendingStore
	audit.Store
}

// PasskeyStore is every persistence port the suite of RunPasskeys
// exercises: the passkey port, and the ports that make the persons, the
// local users and the installation kind of the cases and read the audit log.
type PasskeyStore interface {
	mfa.PasskeyStore
	audit.Store
	principal.RecordStore
	localuser.Store
	localuser.PasswordStore
	installation.KindStore
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// PasskeyFactory returns a fresh, empty store for one subtest of
// RunPasskeys.
type PasskeyFactory func(t *testing.T) PasskeyStore

// CaseNames returns the names of the cases of Run, in the order of the run.
func CaseNames() []string {
	return []string{
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
		"a session from CompletePending reads back through session.Store and meets each check of CreateSession",
		"a complete of a void pending sign-in fails with ErrVoid",
		"two completes at the same time: one wins and one session exists",
		"a prune deletes the expired pending sign-ins only",
		// All ports.
		"a change with an entry of another scope fails with audit.ErrInvalidEntry and changes nothing",
		"the store keeps the digest of a code and a pending secret, never the secret",
	}
}

// PasskeyCaseNames returns the names of the cases of RunPasskeys, in the
// order of the run.
func PasskeyCaseNames() []string {
	cases := passkeyCases()
	names := make([]string, 0, len(cases))

	for _, c := range cases {
		names = append(names, c.name)
	}

	return names
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

			t.Skip("no adapter implements the recovery code and pending sign-in ports yet")
		})
	}
}

// RunPasskeys runs every case of the PasskeyStore suite against the stores
// that newStore returns.
func RunPasskeys(t *testing.T, newStore PasskeyFactory) {
	t.Helper()

	if newStore == nil {
		t.Fatal("mfatest.RunPasskeys needs a store factory")
	}

	for _, c := range passkeyCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.test(t, newStore(t))
		})
	}
}
