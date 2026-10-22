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

	"opendustpan.org/opendustpan/internal/stepup"
	"opendustpan.org/opendustpan/pkg/audit"
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
	"an add with an empty field fails with ErrInvalid and stores nothing",
	"a second add of the same fingerprint for one person fails with ErrConflict",
	"the same fingerprint for two people is two keys",
	"an add above the key limit fails with ErrLimit and stores nothing",
	"keys read back in the order of the add",
	"a key of another person is ErrNotFound",
	"a revoke deletes the key, revokes its credentials and appends one entry",
	"a revoke of a missing key fails with ErrNotFound and appends nothing",
	"an add with an EnrolledBy fails with ErrInvalid",
	"a first-key enrollment stores the key with its administrator and appends one entry",
	"an admin enrollment for a person who has a key fails with ErrConflict and stores nothing",
	"an admin enrollment for a person with only a suspended key fails with ErrConflict",
	"an admin cannot enroll a key for themselves",
	"two first-key enrollments at the same time for one person: one wins",
	// ChallengeStore.
	"a created challenge reads back open with each bound value",
	"a challenge with an empty field or an empty holder fails with ErrInvalid",
	"a challenge above the open limit fails with ErrChallengeLimit",
	"a challenge of another person is ErrNotFound",
	"a challenge and a credential from a session bind the session ID",
	"a complete saves the credential and its entry and marks the challenge answered",
	"a second complete fails with ErrUsed and saves nothing",
	"a complete of a missing challenge fails with ErrNotFound",
	"a complete after the expiry fails with ErrExpired",
	"a complete with a credential of another person, holder, scope, key, job or plan digest fails with ErrInvalid",
	"two completes at the same time: one wins and one credential exists",
	"a complete with a higher counter advances the counter of the key",
	"a complete with a lower or equal counter fails with ErrCounterRegressed, suspends the key, revokes its credentials, saves no credential and appends one entry",
	"a complete with a suspended key fails with ErrFactorRefused",
	"a complete with a missing key or a key of another person fails with ErrNotFound",
	"a key that always sends a counter of 0 keeps working",
	"a second answer of one challenge with the same signature fails with ErrUsed and does not suspend the key",
	"two completes at the same time with one counter: one wins and the key is not suspended",
	"a credential reads back with the level, the job and the plan digest of the completion",
	"an accept moves an open challenge to accepted and appends one entry",
	"an accepted challenge completes only for the console factor",
	"an accept of a challenge that is not open fails with ErrUsed",
	"a recorded failure appends its entry",
	"the failure limit makes the challenge void",
	// CredentialStore.
	"a completed credential keeps the hash and never the secret",
	"a use of a missing credential fails with ErrNotFound",
	"a write credential works one time",
	"a use with another access token or session fails with ErrMismatch and makes the credential void",
	"a use by another person fails with ErrMismatch",
	"a use with another action, object or binding fails with ErrMismatch",
	"an apply credential refuses another job or plan digest",
	"a credential below the level of the write fails with ErrFactorRefused",
	"a mismatch or a refused level appends the failure entry and makes the credential void",
	"a use after the expiry fails with ErrExpired",
	"a window credential stops after its count of uses",
	"a window credential refuses another namespace",
	"a window credential refuses an action outside its list",
	"a window credential refuses an excluded action",
	"two uses at the same time on the last use: one wins",
	"credentials of a revoked key stop working",
	"a revoke of the credentials of a key leaves other credentials working",
	"a revoke of the credentials of a TOTP factor leaves key credentials working",
	"a revoke of the credentials of a person stops each of them",
	// TOTPStore.
	"an enrolled factor is pending and stores and returns the sealed bytes unchanged",
	"an enroll with an empty field or a factor that is not pending fails with ErrInvalid",
	"a pending factor does not replace the active factor",
	"a confirm makes the pending factor active, deletes the earlier factor and revokes its credentials",
	"a confirm after the pending expiry fails with ErrExpired",
	"a confirm with no pending factor fails with ErrNotFound",
	"a person with no active factor is ErrNotFound",
	"an accepted step cannot be accepted again",
	"an earlier step than the last accepted step fails with ErrUsed",
	"two accepts at the same time with one step: one wins",
	"each begun attempt counts as a failure and appends its entry",
	"wrong codes in a row lock the factor until the lockout ends",
	"the attempt at the lock limit accepts a good code",
	"an attempt after the lock limit fails with ErrLocked and counts nothing",
	"wrong codes up to the upper limit disable the factor",
	"the attempt at the upper limit accepts a good code",
	"an attempt after the upper limit fails with ErrDisabled and counts nothing",
	"concurrent attempts never pass the lock limit or the upper limit",
	"a good code sets the failure count to 0",
	"an accept after a reset or a new active factor fails with ErrNotFound",
	"a reset deletes the active and the pending factor, revokes the TOTP credentials and appends one entry",
}

// verifierCaseNames holds the name of each case of the Verifier suite, in
// the order of the run.
//
//nolint:gochecknoglobals // A fixed list of case names.
var verifierCaseNames = []string{
	"a valid SSH signature returns its assertion",
	"a valid sk- signature returns its user presence, user verification and counter",
	"an SSH signature with another namespace fails with ErrBadAnswer",
	"a key-add signature never verifies for a write or a window",
	"a write signature never verifies for a key add",
	"an SSH signature with a hash algorithm other than sha256 or sha512 fails with ErrBadAnswer",
	"an SSH signature whose embedded key is not k fails with ErrBadAnswer",
	"an SSH signature over another message fails with ErrBadAnswer",
	"a key type outside the allowlist fails with ErrInvalid",
	"an RSA key below 3072 bits fails with ErrInvalid",
	"an sk- signature without user presence fails with ErrBadAnswer",
	"a valid OpenPGP signature returns its assertion",
	"an OpenPGP signature without the notation fails with ErrBadAnswer",
	"an OpenPGP signature with the notation in the unhashed area fails with ErrBadAnswer",
	"a text-mode OpenPGP signature fails with ErrBadAnswer",
	"an OpenPGP signature whose issuer fingerprint is not k or a signing subkey of k fails with ErrBadAnswer",
	"an OpenPGP signature by an expired or revoked key or subkey fails with ErrBadAnswer",
	"a malformed signature fails with ErrBadAnswer and does not panic",
}

// CaseNames returns a copy of the names of the cases, in the order of the
// run.
func CaseNames() []string {
	return append([]string(nil), caseNames...)
}

// VerifierCaseNames returns a copy of the names of the cases of the
// Verifier suite, in the order of the run.
func VerifierCaseNames() []string {
	return append([]string(nil), verifierCaseNames...)
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

// VerifierFactory returns the verifier under test for one subtest.
type VerifierFactory func(t *testing.T) stepup.Verifier

// RunVerifier runs every case of the Verifier suite against the verifiers
// that newVerifier returns. Each Verifier adapter runs it:
//
//	func TestVerifierConformance(t *testing.T) {
//		t.Parallel()
//		stepuptest.RunVerifier(t, func(t *testing.T) stepup.Verifier { return sshsig.New() })
//	}
func RunVerifier(t *testing.T, newVerifier VerifierFactory) {
	t.Helper()

	for _, name := range verifierCaseNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if newVerifier == nil {
				t.Fatal("stepuptest.RunVerifier needs a verifier factory")
			}

			t.Skip("no adapter implements the Verifier port yet")
		})
	}
}
