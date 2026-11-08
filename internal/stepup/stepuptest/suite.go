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
	stepup.ResetStore
	audit.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// caseNames holds the name of each case, in the order of the run.
//
//nolint:gochecknoglobals // A fixed list of case names.
var caseNames = []string{
	// KeyStore.
	"an added key reads back with its fingerprint and its audit entry, and its challenge is answered",
	"an add with an empty field fails with ErrInvalid, stores nothing and leaves the challenge open",
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
	// KeyStore: the binding of an add and of an enrollment to its key-add challenge.
	"an add with a missing challenge, a challenge of another person or a challenge that the person did not make fails with ErrNotFound, also when the challenge has another scope or another key, and stores nothing",
	"an add with a challenge of the person of another scope, or whose key type or fingerprint differs from the key, fails with ErrInvalid and stores nothing",
	"an add with a challenge that is answered, void or accepted fails with ErrUsed and stores nothing",
	"an add with an expired challenge fails with ErrExpired and stores nothing",
	"a second add under one challenge fails with ErrUsed, also after the revoke of the first key and before the expiry of the challenge",
	"an add that fails with ErrConflict or ErrLimit leaves the challenge open, and the challenge answers a later good add",
	"an add whose audit append fails stores no key and leaves the challenge open",
	"two adds at the same time under one challenge: one wins and one key exists",
	"a first-key enrollment marks its challenge answered in the same transaction as the key and its entry",
	"an enrollment with a missing challenge, a challenge of another person or a challenge that the administrator did not make fails with ErrNotFound and stores nothing",
	"an enrollment with a challenge that the person made for their own add fails with ErrNotFound and stores nothing",
	"an enrollment with a challenge of another scope or with another key fails with ErrInvalid and stores nothing",
	"a second enrollment under one challenge fails with ErrUsed, also after the revoke of the first key and before the expiry of the challenge",
	"an enrollment that fails with ErrConflict or ErrInvalid leaves the challenge open",
	"an enrollment whose audit append fails stores no key and leaves the challenge open",
	"two enrollments at the same time under one challenge: one wins and one key exists",
	// ChallengeStore.
	"a created challenge reads back open with each bound value",
	"a challenge with an empty field, an empty holder or an empty CreatedBy fails with ErrInvalid",
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
	"a recorded failure of an open challenge counts it, appends its entry and returns nil",
	"the recorded failure that reaches the limit makes the challenge void, appends its entry and one entry of the void, and returns nil",
	"a recorded failure after the limit fails with ErrUsed, counts nothing and appends nothing",
	"a recorded failure of a missing challenge or of another person fails with ErrNotFound, counts nothing and appends nothing",
	"a recorded failure after the expiry fails with ErrExpired, counts nothing and appends nothing",
	"a recorded failure of an accepted, answered or void challenge fails with ErrUsed, counts nothing and appends nothing",
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
	"a use that fails with ErrNotFound, ErrExpired or ErrUsed changes nothing and appends nothing",
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
	"a begun attempt records a reservation with an ID and an expiry",
	"an accept with a fabricated reservation, or one whose person, factor, count or times differ from the record, fails with ErrNotFound and changes nothing",
	"an accept of a consumed reservation fails with ErrUsed, also with a higher step, and changes nothing",
	"an accept after the expiry of the reservation fails with ErrExpired and changes nothing",
	"an accept with a step that is not higher consumes the reservation, keeps the failure count and a retry fails",
	"a valid reservation still passes after a lock or a disable that the same attempt set",
	"a valid reservation still passes after a lock or a disable that a concurrent attempt set",
	"a reset or a new active factor ends the reservations of the earlier factor",
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
	// ResetStore.
	"a factor reset deletes each key and both TOTP factors, revokes each credential, voids each open or accepted challenge and appends exactly one entry",
	"an accepted challenge does not complete after a factor reset",
	"a factor reset changes nothing for another person",
	"a factor reset of a person with no key and no factor appends one entry",
	"a factor reset with an empty person ID fails with ErrInvalid and appends nothing",
	"a factor reset that fails in its transaction leaves the keys, the factors, the credentials, the challenges and the audit entries unchanged",
	"a credential of a person after a factor reset stops working",
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
