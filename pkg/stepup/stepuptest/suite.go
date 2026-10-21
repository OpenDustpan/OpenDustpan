// Package stepuptest is the conformance suite for the stepup ports. A store
// adapter runs it:
//
//	func TestStepUpConformance(t *testing.T) {
//		t.Parallel()
//		stepuptest.Run(t, func(t *testing.T) stepuptest.Store { return newMyStore(t) })
//	}
//
// The suite holds the names of its cases only. Each case skips until an
// adapter implements the ports, so that the names are the contract that the
// first adapter meets.
package stepuptest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/stepup"
)

// Store is every persistence port the suite exercises.
type Store interface {
	stepup.KeyStore
	stepup.ChallengeStore
	stepup.CredentialStore
	stepup.TOTPStore
	audit.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// caseNames holds the name of each case, in the order of the run.
//
//nolint:gochecknoglobals // A fixed list of case names.
var caseNames = []string{
	// KeyStore.
	"an added key reads back with its fingerprint and its audit entry",
	"a second add of the same fingerprint for one person fails with ErrConflict",
	"the same fingerprint for two people is two keys",
	"an add above the key limit fails with ErrLimit and stores nothing",
	"a key of another person is ErrNotFound",
	"a revoke deletes the key, revokes its credentials and appends one entry",
	"a revoke of a missing key fails with ErrNotFound and appends nothing",
	"a higher counter advances and a lower or equal counter fails with ErrConflict",
	"a key that always sends a counter of 0 keeps working",
	"two counter advances at the same time with one value: one wins",
	// ChallengeStore.
	"a created challenge reads back with each bound value",
	"a challenge of another person is ErrNotFound",
	"a consume marks the challenge used, and a second consume fails with ErrUsed",
	"a consume after the expiry fails with ErrExpired",
	"two consumes at the same time: one wins",
	"the failure limit makes the challenge void",
	// CredentialStore.
	"a saved credential keeps the hash and never the secret",
	"a write credential works one time",
	"a use with another access token fails with ErrMismatch and uses the credential up",
	"a use with another action, object or binding fails with ErrMismatch",
	"a use after the expiry fails with ErrExpired",
	"a window credential stops after its count of uses",
	"two uses at the same time on the last use: one wins",
	"credentials of a revoked key stop working",
	// TOTPStore.
	"an enrolled factor keeps the sealed secret only",
	"an accepted step cannot be accepted again",
	"an earlier step than the last accepted step fails with ErrUsed",
	"two accepts at the same time with one step: one wins",
	"wrong codes in a row lock the factor until the lockout ends",
	"wrong codes up to the upper limit disable the factor",
	"a good code sets the failure count to 0",
	"a reset deletes the factor and appends one entry",
}

// CaseNames returns a copy of the names of the cases, in the order of the
// run.
func CaseNames() []string {
	return append([]string(nil), caseNames...)
}

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	for _, name := range caseNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if newStore == nil {
				t.Fatal("stepuptest.Run needs a store factory")
			}

			t.Skip("no adapter implements the stepup ports yet")
		})
	}
}
