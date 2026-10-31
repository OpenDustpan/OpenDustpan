// Package localusertest is the conformance suite of the localuser ports. A
// store adapter runs it:
//
//	func TestLocalUserConformance(t *testing.T) {
//		t.Parallel()
//		localusertest.Run(t, func(t *testing.T) localusertest.Store { return newMyStore(t) })
//	}
//
// The suite holds the names of its cases only. Each case skips until an
// adapter implements the ports, so that the names are the contract that the
// first adapter meets.
package localusertest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
)

// Store is every persistence port the suite exercises.
type Store interface {
	localuser.Store
	localuser.ResetStore
	audit.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// HasherFactory returns the hasher under test for one subtest.
type HasherFactory func(t *testing.T) localuser.PasswordHasher

// BlocklistFactory returns the blocklist under test for one subtest.
type BlocklistFactory func(t *testing.T) localuser.Blocklist

// CaseNames returns the names of the cases of the store suite, in the order
// of the run.
func CaseNames() []string {
	return []string{
		// Store.
		"a created user reads back by name and by principal with its audit entry",
		"a created user with no password reads back with an empty hash",
		"a user name that does not match the pattern fails with ErrInvalid and stores nothing",
		"a second user with the same name fails with ErrAlreadyExists",
		"a second user for the same principal fails with ErrAlreadyExists",
		"a user for a principal that is not a person fails with principal.ErrNotFound",
		"an unknown name is ErrNotFound",
		"a password set stores the hash, sets the time and appends one entry",
		"a password set ends every other session of the person in the same transaction",
		"a password set keeps the session that the change names",
		"a password set with ChangeRequired reads back with the flag",
		"a rehash replaces the hash only while the stored hash is the old one",
		"a rehash after a password change fails with ErrNotFound and changes nothing",
		"a password remove for a person with no passkey fails with ErrLastCredential",
		"a password remove for a person with a passkey deletes the hash and appends one entry",
		"a password set, a remove and a reset code for the demo person fail with ErrDemoFixed",
		"a user delete keeps the principal and deletes the reset codes",
		"a change with an entry of another scope fails with audit.ErrInvalidEntry and changes nothing",
		"a change whose audit append fails changes nothing",
		// ResetStore.
		"a created reset code reads back once by its digest",
		"a second take of a reset code fails with ErrNotFound",
		"a take after the expiry fails with ErrNotFound and deletes the code",
		"a new reset code deletes the earlier code of the same user",
		"a reset code for an unknown user fails with ErrNotFound",
		"two takes at the same time: one wins",
		"a prune deletes the expired codes only",
		"the store keeps the digest of a reset code, never the code",
	}
}

// HasherCaseNames returns the names of the cases of the PasswordHasher
// suite, in the order of the run.
func HasherCaseNames() []string {
	return []string{
		"a hash verifies with the same password",
		"a hash does not verify with another password: ErrWrongPassword",
		"two hashes of one password have different salts",
		"a hash holds its algorithm and its parameters in the PHC string",
		"a hash with other parameters than the current ones verifies with NeedsRehash",
		"a hash with parameters out of the bounds fails with ErrInvalid before any work",
		"parameters out of the bounds for a new hash fail with ErrInvalid",
		"a password above MaxBytes fails with ErrPasswordTooLong",
		"a password that differs in its last byte does not verify",
		"a malformed hash fails with ErrInvalid and does not panic",
		"a hash of another algorithm fails with ErrInvalid",
	}
}

// BlocklistCaseNames returns the names of the cases of the Blocklist suite,
// in the order of the run.
func BlocklistCaseNames() []string {
	return []string{
		"a password of the list is blocked",
		"a password of the list in another case is blocked",
		"a password of the list in another Unicode normalization form is blocked",
		"a long random password is not blocked",
		"the list loads from the binary and opens no network connection",
	}
}

// Run runs every case of the store suite against the stores that newStore
// returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	if newStore == nil {
		t.Fatal("localusertest.Run needs a store factory")
	}

	skipCases(t, CaseNames())
}

// RunHasher runs every case of the PasswordHasher suite against the hashers
// that newHasher returns.
func RunHasher(t *testing.T, newHasher HasherFactory) {
	t.Helper()

	if newHasher == nil {
		t.Fatal("localusertest.RunHasher needs a hasher factory")
	}

	skipCases(t, HasherCaseNames())
}

// RunBlocklist runs every case of the Blocklist suite against the lists that
// newList returns.
func RunBlocklist(t *testing.T, newList BlocklistFactory) {
	t.Helper()

	if newList == nil {
		t.Fatal("localusertest.RunBlocklist needs a blocklist factory")
	}

	skipCases(t, BlocklistCaseNames())
}

// skipCases runs one subtest for each name. Each case skips until an
// adapter implements the ports.
func skipCases(t *testing.T, names []string) {
	t.Helper()

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			t.Skip("no adapter implements the localuser ports yet")
		})
	}
}
