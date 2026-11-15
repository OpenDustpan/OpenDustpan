// Package stepup holds the proposed ports of the step-up of a person in the
// CLI: a fresh authentication with a step-up key (an SSH or OpenPGP public
// key that the person registered) or a TOTP code, for one write or for a
// short window.
//
// The package holds types, sentinel errors and ports only. No adapter and no
// service implements them yet. The step-up credential is a separate
// credential, never an access token: alone it authenticates nothing, and an
// access token alone never gives a fresh authentication.
//
// Rows belong to a person, not to a namespace, so no write carries a fence.
//
// # Audit of refusals
//
// Exactly one layer appends the audit entry of each refusal, so that no
// refusal is recorded twice and none is recorded zero times:
//
//   - ErrMismatch and ErrFactorRefused of CredentialStore.UseCredential: the
//     store appends the failure entry of the Use, in the transaction that
//     makes the credential void. The service appends no entry for them.
//   - ErrNotFound, ErrExpired and ErrUsed of CredentialStore.UseCredential
//     and of ChallengeStore.RecordFailure: the store changes nothing and
//     appends nothing. The service appends one failure entry for each. When
//     the service cannot append it, the request still fails, and the
//     service never lets the write go on (fail closed).
//
// The implementation of the service and of the stores waits for the decision
// on the CLI step-up.
package stepup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Sentinel errors of the ports. Adapters translate driver errors into these.
var (
	// ErrNotFound reports a key, a challenge, a credential or a TOTP factor
	// that does not exist.
	ErrNotFound = errors.New("step-up record not found")
	// ErrExpired reports a challenge, a credential or a pending TOTP factor
	// after its expiry.
	ErrExpired = errors.New("step-up expired")
	// ErrUsed reports a challenge or a credential that was used up or is
	// void, or a TOTP time step that the store accepted before. The service
	// answers a used time step as ErrBadAnswer, so that the answer does not
	// tell that the code was valid.
	ErrUsed = errors.New("step-up already used")
	// ErrMismatch reports a request that does not match the values that its
	// challenge binds, or a window credential on a write that a window never
	// covers.
	ErrMismatch = errors.New("request does not match the step-up challenge")
	// ErrBadAnswer reports a signature or a code that does not verify.
	ErrBadAnswer = errors.New("step-up answer does not verify")
	// ErrFactorRefused reports a factor that the step-up factor level of the
	// namespace or the installation does not allow, a suspended key, or a
	// credential whose level is below the level of the write.
	ErrFactorRefused = errors.New("factor not allowed for this step-up")
	// ErrCLIStepUpOff reports a CLI step-up in a namespace or an
	// installation that turned it off.
	ErrCLIStepUpOff = errors.New("CLI step-up is off")
	// ErrLocked reports a TOTP factor that is locked for the step-up after
	// too many wrong codes in a row. The lock ends after a fixed time.
	ErrLocked = errors.New("factor locked after failed attempts")
	// ErrDisabled reports a TOTP factor that is disabled for the step-up
	// after the upper limit of wrong codes. Only a new enrollment ends it.
	ErrDisabled = errors.New("factor disabled after failed attempts")
	// ErrNotPerson reports a step-up for a principal that is not a person,
	// or with a credential that is not a console session or an access token
	// from the CLI sign-in.
	ErrNotPerson = errors.New("only a person can do a step-up")
	// ErrInvalid reports a value that fails a check of this package.
	ErrInvalid = errors.New("invalid step-up value")
	// ErrLimit reports a person who already holds the maximum count of keys.
	ErrLimit = errors.New("step-up key limit reached")
	// ErrChallengeLimit reports a person who already holds the maximum count
	// of open challenges.
	ErrChallengeLimit = errors.New("step-up challenge limit reached")
	// ErrConflict reports a key that the person registered before, or a
	// first-key enrollment for a person who already holds a key.
	ErrConflict = errors.New("step-up key conflict")
	// ErrCounterRegressed reports a signature counter of a FIDO key that is
	// not higher than the stored counter: a possible clone of the key.
	ErrCounterRegressed = errors.New("step-up key counter went back")
)

// Factor is the kind of factor that answered a challenge.
type Factor string

// The factors.
const (
	FactorSSH     Factor = "ssh"
	FactorOpenPGP Factor = "openpgp"
	FactorTOTP    Factor = "totp"
	FactorConsole Factor = "console"
)

// Level is a step-up factor level of a namespace or the installation. The
// strictest level that holds for a write applies. No level proves that a
// key is on hardware.
type Level string

// The step-up factor levels, from the least strict to the strictest.
const (
	// LevelAny allows a step-up key, a TOTP code and each console factor
	// that counts as a fresh authentication.
	LevelAny Level = "any"
	// LevelKey allows a step-up key, and the console path only after a
	// WebAuthn assertion. An add or a revoke of a key always needs this
	// level.
	LevelKey Level = "key"
)

// Scope is what a challenge is for, and what its credential allows.
type Scope string

// The scopes of a challenge.
const (
	// ScopeWrite allows the one request that the challenge binds.
	ScopeWrite Scope = "write"
	// ScopeWindow allows the named actions in one namespace, for a short
	// time and a fixed count of writes. It never allows an action of the
	// exclusion list of the service.
	ScopeWindow Scope = "window"
	// ScopeKeyAdd is the proof of possession of a new key. It makes no
	// credential, and its signature uses its own SSH namespace or OpenPGP
	// notation value, so that it is never a step-up signature.
	ScopeKeyAdd Scope = "key-add"
)

// State is the state of a challenge.
type State string

// The states of a challenge.
const (
	// StateOpen is a challenge that waits for an answer.
	StateOpen State = "open"
	// StateAccepted is a challenge that the person accepted in the console.
	// The next poll of the CLI completes it.
	StateAccepted State = "accepted"
	// StateAnswered is a challenge that made its credential.
	StateAnswered State = "answered"
	// StateVoid is a challenge after its failure limit, or a challenge that a
	// revoke or a reset ended.
	StateVoid State = "void"
)

// Holder names the credential of the request that made a challenge: a
// console session or an access token. ID is never empty, so that a challenge
// always binds one credential.
type Holder struct {
	// Kind is identity.CredentialSession or identity.CredentialAccessToken.
	Kind identity.Credential
	// ID is the session ID or the access token ID.
	ID string
}

// Key is a step-up key of a person.
type Key struct {
	// ID is the server ID of the key, with the prefix "stk-".
	ID string
	// PersonID is the principal ID of the person who owns the key.
	PersonID string
	// Factor is FactorSSH or FactorOpenPGP.
	Factor Factor
	// Type is the key type, for example "sk-ssh-ed25519@openssh.com".
	Type string
	// Fingerprint is "SHA256:<base64>" for an SSH key, or the OpenPGP
	// fingerprint in hex.
	Fingerprint string
	// PublicKey is the public key in its wire form.
	PublicKey []byte
	// Name is the name that the person gave the key.
	Name string
	// Counter is the signature counter of the last signature of a FIDO key.
	// It is 0 for every other key.
	Counter uint32
	// Suspended reports a key after a counter regression. A suspended key
	// answers no challenge.
	Suspended bool
	// EnrolledBy is the principal ID of the administrator who enrolled the
	// first key of the person, or empty for a key that the person added.
	EnrolledBy string
	// CreatedAt is the time of the add.
	CreatedAt time.Time
}

// Binding is the request that a challenge of ScopeWrite binds. Each value
// is in its canonical form, and the service checks it with an allowlist
// before it reaches a store.
type Binding struct {
	Method string
	// Target is the path and the raw query, in the canonical form under the
	// tool-neutral API prefix.
	Target string
	// ContentType is the media type of the body, in lowercase.
	ContentType string
	BodySHA256  [32]byte
	// IfMatch and IdempotencyKey are empty when the request has no such
	// header.
	IfMatch        string
	IdempotencyKey string
}

// Requester is what the console accept page shows about the request that
// made a challenge, so that the person can see a challenge that is not
// theirs.
type Requester struct {
	// TokenName is the name of the access token, or empty for a session.
	TokenName string
	// ClientIP is the client IP address of the challenge create.
	ClientIP string
}

// KeyAddProof is what a challenge of ScopeKeyAdd binds: the person who gets
// the new key and the key itself, so that a proof for one person or one key
// never adds another.
type KeyAddProof struct {
	// Type is the key type of the new key.
	Type string
	// Fingerprint is the fingerprint of the new key, in the form of Key.
	Fingerprint string
}

// Challenge is the server record of one step-up.
type Challenge struct {
	// ID is the challenge ID, with the prefix "stc-".
	ID string
	// Nonce holds 32 random bytes.
	Nonce []byte
	// PersonID names the person of the request that made the challenge. For
	// ScopeKeyAdd it names the person who gets the key, which is another
	// person only for a first-key enrollment by an administrator.
	PersonID string
	// Holder names the session or the access token of that request.
	Holder Holder
	// CreatedBy is the principal ID of the person who made the request that
	// made the challenge: the person for their own add, and the administrator
	// for a first-key enrollment. It is never empty. The store of a key add
	// refuses a challenge that another principal made. The service sets it
	// from the authenticated principal of the request, never from a request
	// field, because the store cannot tie it to Holder.
	CreatedBy string
	// Requester is what the console accept page shows.
	Requester Requester
	// Origin is the normalized public URL of the server.
	Origin string
	// Scope is ScopeWrite, ScopeWindow or ScopeKeyAdd.
	Scope Scope
	// State is the state of the challenge.
	State State
	// Action and Object are the action and the object of the write, for
	// ScopeWrite.
	Action identity.Action
	Object string
	// Binding is the request, for ScopeWrite.
	Binding Binding
	// JobID and PlanDigest bind the apply of a job, for the action
	// "job:apply". Both are empty for every other write.
	JobID      string
	PlanDigest string
	// Namespace and Actions are what a credential of ScopeWindow allows.
	Namespace string
	Actions   []identity.Action
	// KeyAdd is the new key, for ScopeKeyAdd.
	KeyAdd KeyAddProof
	// PlanSummary is the list of the changes of the plan of a window, as
	// information only. The server does not limit the writes to it.
	PlanSummary string
	// Summary is the text that the CLI and the console show to the person,
	// and SummarySHA256 is the SHA-256 of its bytes, which the signed text
	// holds.
	Summary       string
	SummarySHA256 [32]byte
	// Failures is the count of wrong answers.
	Failures int
	// CreatedAt and ExpiresAt bound the challenge. AcceptedAt is the time of
	// the accept in the console, or the zero time.
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt time.Time
}

// KeyAddition is the add of a step-up key under the challenge that proved
// possession of it. The store binds the key to the challenge, so that one
// proof adds one key, once.
type KeyAddition struct {
	// Key is the new key.
	Key Key
	// ChallengeID names the challenge of ScopeKeyAdd that the new key
	// answered. The service verified the signature before the call.
	ChallengeID string
	// At is the time of the add. A challenge that expired at or before At is
	// expired.
	At time.Time
}

// CheckKeyAdditionArguments applies step 1 of AddKey and EnrollFirstKey to
// the arguments alone, and returns ErrInvalid for the first rule that fails.
// An adapter calls it before it reads a challenge. For AddKey, adminID is
// empty and enroll is false. For EnrollFirstKey, enroll is true.
func CheckKeyAdditionArguments(a KeyAddition, adminID string, enroll bool) error {
	switch {
	case a.ChallengeID == "":
		return fmt.Errorf("%w: the challenge ID is empty", ErrInvalid)
	case a.At.IsZero():
		return fmt.Errorf("%w: the time of the add is zero", ErrInvalid)
	}

	if err := checkNewKey(a.Key); err != nil {
		return err
	}

	switch {
	case !enroll && (adminID != "" || a.Key.EnrolledBy != ""):
		return fmt.Errorf("%w: a key that the person adds has no administrator", ErrInvalid)
	case enroll && (adminID == "" || adminID == a.Key.PersonID || a.Key.EnrolledBy != adminID):
		return fmt.Errorf("%w: an enrollment needs an administrator other than the person, who enrolls the key",
			ErrInvalid)
	}

	return nil
}

// checkNewKey returns ErrInvalid for a key with an empty field or a factor
// that is not a key factor.
func checkNewKey(k Key) error {
	if k.ID == "" || k.PersonID == "" || k.Type == "" || k.Fingerprint == "" || len(k.PublicKey) == 0 ||
		k.Name == "" || k.CreatedAt.IsZero() {
		return fmt.Errorf("%w: a key field is empty", ErrInvalid)
	}

	if k.Factor != FactorSSH && k.Factor != FactorOpenPGP {
		return fmt.Errorf("%w: the factor of a key is ssh or openpgp", ErrInvalid)
	}

	return nil
}

// CheckKeyAdditionChallenge applies steps 2 to 5 of AddKey and
// EnrollFirstKey to the stored challenge ch, in this order, and returns the
// first error. creator is the principal who must have made the challenge: the
// person for AddKey and the administrator for EnrollFirstKey. An adapter calls
// it with the challenge that it read in the transaction of the add, after
// CheckKeyAdditionArguments, and it passes nil for a challenge that does not
// exist, and the function returns ErrNotFound.
func CheckKeyAdditionChallenge(a KeyAddition, creator string, ch *Challenge) error {
	switch {
	case ch == nil || ch.PersonID != a.Key.PersonID || creator == "" || ch.CreatedBy != creator:
		return ErrNotFound
	case ch.Scope != ScopeKeyAdd || ch.KeyAdd.Type != a.Key.Type || ch.KeyAdd.Fingerprint != a.Key.Fingerprint:
		return fmt.Errorf("%w: the challenge does not hold this key", ErrInvalid)
	case ch.State != StateOpen:
		return ErrUsed
	case !ch.ExpiresAt.After(a.At):
		return ErrExpired
	}

	return nil
}

// Answer is the answer of a challenge.
type Answer struct {
	Factor Factor
	// KeyID names the step-up key, for a key factor.
	KeyID string
	// Signature is the signature, for a key factor.
	Signature []byte
	// Code is the TOTP code, for FactorTOTP.
	Code string
}

// Assertion is what a verified signature says about the key.
type Assertion struct {
	// UserPresent and UserVerified are the flags of a FIDO key. Both are
	// false for every other key. A software FIDO middleware can set them, so
	// they prove no hardware.
	UserPresent  bool
	UserVerified bool
	// Counter is the signature counter of a FIDO key.
	Counter uint32
}

// Credential is a step-up credential. The store keeps a hash of its secret,
// never the secret.
type Credential struct {
	// ID is the credential ID, with the prefix "stp-".
	ID string
	// SecretHash is the hash of the secret that the client sends.
	SecretHash [32]byte
	// ChallengeID names the challenge that the credential answers.
	ChallengeID string
	PersonID    string
	// Holder is the session or the access token that the credential needs
	// in each request.
	Holder Holder
	Factor Factor
	// KeyID names the key, for a key factor, and TOTPID names the TOTP
	// factor, for FactorTOTP.
	KeyID  string
	TOTPID string
	Scope  Scope
	// Level is the strictest step-up factor level that the factor met. A
	// TOTP code and each console factor except a WebAuthn assertion meet
	// LevelAny only. The audit entry of the step-up names the console method.
	Level Level
	// JobID and PlanDigest are those of the challenge, for the action
	// "job:apply", and empty for every other write.
	JobID      string
	PlanDigest string
	// AuthTime is the time of the factor, for the fresh window of a write.
	AuthTime  time.Time
	ExpiresAt time.Time
	// UsesLeft is 1 for ScopeWrite, and at most the window limit for
	// ScopeWindow.
	UsesLeft int
}

// Completion is the success of one challenge: the credential that it makes
// and the audit entry of the step-up.
type Completion struct {
	PersonID    string
	ChallengeID string
	// KeyID and Counter are the key and the signature counter of a key
	// answer. KeyID is empty for FactorTOTP and FactorConsole, and equals
	// Credential.KeyID otherwise.
	KeyID      string
	Counter    uint32
	Credential Credential
	Entry      audit.Entry
	// Regression is the audit entry of the kind security that the store
	// appends when the counter went back.
	Regression audit.Entry
	At         time.Time
}

// Use is one use of a credential by a request.
type Use struct {
	SecretHash [32]byte
	// PersonID and Holder name the person and the credential of the
	// request. The store checks each of them on its own.
	PersonID  string
	Holder    Holder
	Action    identity.Action
	Object    string
	Namespace string
	Binding   Binding
	// Level is the step-up factor level that holds for the write. The store
	// refuses a credential whose level is below it.
	Level Level
	// JobID and PlanDigest are those of the apply, for the action
	// "job:apply". The store refuses a credential of another job or plan.
	JobID      string
	PlanDigest string
	// Excluded reports an action that a window credential never covers.
	// The service sets it from its own list. The store refuses a window
	// credential when it is true.
	Excluded bool
	// Failure is the audit entry that the store appends when the use
	// fails with ErrMismatch or ErrFactorRefused.
	Failure audit.Entry
	At      time.Time
}

// KeyStore is the persistence port for step-up keys.
type KeyStore interface {
	// AddKey stores a.Key, marks the challenge a.ChallengeID answered and
	// appends e, in one transaction. The challenge must be a challenge of
	// ScopeKeyAdd that the person a.Key.PersonID made for themselves, in
	// StateOpen, not expired at a.At, and its KeyAdd must hold the type and
	// the fingerprint of a.Key. An adapter applies the rules of
	// CheckKeyAdditionArguments and CheckKeyAdditionChallenge, in this order,
	// and the first failure decides:
	//
	//  1. ErrInvalid for an argument that fails CheckKeyAdditionArguments. It
	//     needs no stored challenge.
	//  2. ErrNotFound for a missing challenge, a challenge of another person,
	//     and a challenge that the person did not make. The three answers are
	//     the same, so that a caller learns nothing about the challenge of
	//     another person.
	//  3. ErrInvalid for a challenge of another scope or whose KeyAdd differs
	//     from the key.
	//  4. ErrUsed for a challenge that is not open: answered, void or
	//     accepted. A challenge that made a key is answered, so the proof
	//     adds no second key, also after the revoke of the first key and
	//     before the expiry of the challenge.
	//  5. ErrExpired for a challenge that expired at or before a.At.
	//  6. ErrConflict for a fingerprint that the person registered before,
	//     and ErrLimit when the person holds the maximum count of keys.
	//
	// A failed add stores nothing, appends nothing and leaves the challenge
	// open. The store appends no entry for a refusal: the service appends
	// the one failure entry and fails closed. Of two calls at the same time
	// for one challenge, one wins, and the other returns ErrUsed.
	AddKey(ctx context.Context, a KeyAddition, e audit.Entry) error
	// Keys returns the keys of the person personID in the order of the add.
	Keys(ctx context.Context, personID string) ([]Key, error)
	// Key returns the key id of the person personID, or ErrNotFound.
	Key(ctx context.Context, personID, id string) (Key, error)
	// EnrollFirstKey stores a.Key, the first key of the person
	// a.Key.PersonID, for the administrator adminID, marks the challenge
	// a.ChallengeID answered and appends e, in one transaction. The rules
	// and the order of the checks are those of AddKey, with these changes:
	// step 1 also returns ErrInvalid when adminID is empty or names the
	// person, or when the EnrolledBy of the key is not adminID; step 2 also
	// returns ErrNotFound for a challenge that the administrator adminID did
	// not make, so a challenge that the person made for their own add never
	// enrolls a key for an administrator; and step 6 returns ErrConflict when
	// the person holds a key, also a suspended one. The store checks that
	// the challenge was made by adminID, and the service checks that adminID
	// holds the right to enroll.
	// Of two calls at the same time for one person, or for one challenge,
	// one wins. A failed enrollment stores nothing, appends nothing and
	// leaves the challenge open. The store appends no entry for a refusal.
	EnrollFirstKey(ctx context.Context, adminID string, a KeyAddition, e audit.Entry) error
	// RevokeKey deletes the key, revokes each credential that it made and
	// appends e, in one transaction. It returns ErrNotFound.
	RevokeKey(ctx context.Context, personID, id string, e audit.Entry) error
}

// ChallengeStore is the persistence port for step-up challenges.
type ChallengeStore interface {
	// CreateChallenge stores c in the state StateOpen. It returns ErrInvalid
	// for a challenge with an empty field, an empty holder or an empty
	// CreatedBy, and
	// ErrChallengeLimit when the person holds the maximum count of open
	// challenges.
	CreateChallenge(ctx context.Context, c Challenge) error
	// Challenge returns the challenge id of the person personID, or
	// ErrNotFound. A challenge of another person is ErrNotFound. The read
	// does not check the holder, because the console accept page reads it
	// from another session. The service answers a poll from another holder
	// with ErrNotFound, and CompleteChallenge refuses its credential.
	Challenge(ctx context.Context, personID, id string) (Challenge, error)
	// AcceptChallenge moves an open challenge to StateAccepted at the time
	// at and appends e, in one transaction, for the console path. It returns
	// ErrNotFound, ErrExpired after the expiry, and ErrUsed for a challenge
	// that is not open.
	AcceptChallenge(ctx context.Context, personID, id string, at time.Time, e audit.Entry) error
	// CompleteChallenge completes one challenge in one transaction:
	//
	//  1. It checks the state first. It takes an open challenge, or an
	//     accepted one for FactorConsole. It returns ErrNotFound, ErrExpired
	//     after the expiry, and ErrUsed for a challenge that was answered or
	//     is void. On these errors it changes nothing, also not the counter
	//     of the key, so that a retry of the same answer never suspends a
	//     good key.
	//  2. For a key answer, it reads the key c.KeyID of the person. It
	//     returns ErrNotFound for a missing key or a key of another person,
	//     and ErrFactorRefused for a suspended key. It sets the counter to
	//     c.Counter when it is higher than the stored counter, or when both
	//     are 0. Else it suspends the key, revokes each credential that the
	//     key made, makes the challenge void, appends c.Regression, saves no
	//     credential and returns ErrCounterRegressed.
	//  3. It moves the challenge to StateAnswered, saves the credential and
	//     appends c.Entry, so that a credential never exists without its
	//     entry.
	//
	// It returns ErrInvalid for a credential whose person, holder,
	// challenge, scope, key, job or plan digest differ from the challenge or
	// from c. Of two calls at the same time, one wins, and the other changes
	// nothing.
	CompleteChallenge(ctx context.Context, c Completion) error
	// RecordFailure counts one wrong answer of the challenge id of the
	// person personID and appends e, in one transaction. Its result for each
	// state of the challenge:
	//
	//   - Open, below the failure limit: it counts the answer, appends e and
	//     returns nil. The challenge stays open.
	//   - Open, and this answer reaches the failure limit: it counts the
	//     answer, makes the challenge void, appends e, and appends a second
	//     entry that records the void of the challenge at the limit, and
	//     returns nil. The void is an automatic action, so it has its own
	//     entry, and e stays the record of the wrong answer. The next call
	//     returns ErrUsed.
	//   - Missing, or a challenge of another person: it returns ErrNotFound.
	//   - After the expiry: it returns ErrExpired.
	//   - Accepted, answered or void: it returns ErrUsed.
	//
	// On ErrNotFound, ErrExpired and ErrUsed it counts nothing, changes no
	// state and appends nothing. The service appends the entry of these
	// refusals (see the package documentation).
	RecordFailure(ctx context.Context, personID, id string, e audit.Entry) error
}

// CredentialStore is the persistence port for step-up credentials. The
// store saves a credential only in ChallengeStore.CompleteChallenge.
type CredentialStore interface {
	// UseCredential checks u against the credential with the same secret
	// hash and takes one use, in its own transaction, before the write. It
	// returns ErrNotFound, ErrExpired, ErrUsed when no use is left or the
	// credential is void, ErrMismatch when the person, the holder, the
	// action, the object, the namespace, the binding, the job or the plan
	// digest differ, or when u is excluded for a window credential, and
	// ErrFactorRefused when the level of the credential is below u.Level. A
	// mismatch or a refused level makes the credential void and appends the
	// failure entry of u. On ErrNotFound, ErrExpired and ErrUsed it changes
	// nothing and appends nothing: the service appends the entry of these
	// refusals (see the package documentation). Of two calls at the same
	// time on the last use, one wins.
	UseCredential(ctx context.Context, u Use) (Credential, error)
	// RevokeCredentialsOfKey revokes each credential that the key keyID of
	// the person personID made.
	RevokeCredentialsOfKey(ctx context.Context, personID, keyID string) error
	// RevokeCredentialsOfTOTP revokes each credential that the TOTP factor
	// totpID of the person personID made.
	RevokeCredentialsOfTOTP(ctx context.Context, personID, totpID string) error
	// RevokeCredentialsOfPerson revokes each credential of the person
	// personID, for an admin reset and a deprovision.
	RevokeCredentialsOfPerson(ctx context.Context, personID string) error
}

// ResetStore is the persistence port for the admin reset of the factors of a
// person.
type ResetStore interface {
	// ResetFactors deletes each step-up key of the person personID and the
	// active and the pending TOTP factor, revokes each step-up credential of
	// the person, makes each open or accepted challenge of the person void,
	// and appends e, which is the one entry of the reset, in one
	// transaction. It returns ErrInvalid for an empty personID. A person who
	// holds no key and no factor still gets the entry, so that the reset has
	// a record. It changes nothing for another person. A failed reset
	// changes nothing and appends nothing. Two resets at the same time each
	// append their entry.
	ResetFactors(ctx context.Context, personID string, e audit.Entry) error
}

// TOTP is the TOTP factor of a person. The store keeps the secret sealed.
// The counters are those of the step-up only. The console sign-in has its
// own counters, so a step-up lockout never blocks a sign-in.
type TOTP struct {
	// ID is the factor ID, with the prefix "tot-".
	ID       string
	PersonID string
	// Sealed is the sealed secret. The service seals and opens it. The
	// store keeps the bytes and returns them unchanged.
	Sealed []byte
	// Pending reports a factor that waits for its first valid code.
	// PendingUntil is the end of that wait.
	Pending      bool
	PendingUntil time.Time
	// LastStep is the last time step that the server accepted.
	LastStep int64
	// Failures is the count of wrong codes in a row on the step-up.
	Failures int
	// LockedUntil is the end of a step-up lockout, or the zero time.
	LockedUntil time.Time
	// Disabled reports a factor after the upper limit of wrong codes on the
	// step-up.
	Disabled bool
}

// ReservationTTL is how long the reservation of a TOTP attempt lasts. The
// service checks a code at once after BeginTOTPAttempt, so a reservation that
// is older than this is not a live attempt.
const ReservationTTL = 5 * time.Minute

// Attempt is one reserved TOTP attempt. BeginTOTPAttempt counts it as a
// failure and records the reservation in the store, and AcceptStep consumes
// the reservation and cancels that count for a good code. A value that the
// store did not return from BeginTOTPAttempt is not a reservation.
type Attempt struct {
	// ID is the reservation ID, with the prefix "tra-". The store makes it.
	ID       string
	PersonID string
	TOTPID   string
	// Count is the failure count after the reservation of this attempt.
	Count int
	At    time.Time
	// ExpiresAt is At plus ReservationTTL.
	ExpiresAt time.Time
}

// TOTPStore is the persistence port for TOTP factors.
type TOTPStore interface {
	// EnrollTOTP stores f as the pending factor of the person and appends
	// e, in one transaction. It never changes the active factor. A pending
	// factor that came before is replaced. It returns ErrInvalid for a
	// factor that is not pending or has an empty field.
	EnrollTOTP(ctx context.Context, f TOTP, e audit.Entry) error
	// ConfirmTOTP makes the pending factor id active at the time at:
	// it deletes the earlier active factor, revokes each credential that
	// the earlier factor made and appends e, in one transaction. It
	// returns ErrNotFound for no pending factor with that id, and ErrExpired
	// after its PendingUntil.
	ConfirmTOTP(ctx context.Context, personID, id string, at time.Time, e audit.Entry) error
	// TOTP returns the active factor of the person personID, or
	// ErrNotFound.
	TOTP(ctx context.Context, personID string) (TOTP, error)
	// BeginTOTPAttempt checks the lock and the disable of the active factor
	// and counts one failure, in one transaction, before the service
	// checks the code. It appends e. It returns ErrNotFound, ErrLocked
	// before LockedUntil, and ErrDisabled, and then counts nothing. The
	// lock and the disable come only from earlier attempts: the attempt
	// that takes the count to a multiple of the lock limit passes, sets the
	// lock and appends lockout in the same transaction, and the attempt that
	// takes the count to the upper limit passes and sets the disable. So the
	// 10th wrong code locks, and the 10th attempt can still pass. Of calls
	// at the same time, no more than the limit pass.
	BeginTOTPAttempt(ctx context.Context, personID string, at time.Time, e, lockout audit.Entry) (Attempt, error)
	// AcceptStep checks the reservation a, consumes it and, for a step that is
	// higher than the stored step, sets the last step to step, the failures
	// to 0 and ends the lock and the disable, in one transaction. It
	// validates the reservation against the record that BeginTOTPAttempt
	// stored: the ID, the person, the factor, the count and the times must
	// equal that record. It returns, in this order:
	//
	//  1. ErrNotFound for a reservation that the store did not record or
	//     whose values differ from the record, and when the factor a.TOTPID
	//     is not the active factor. It changes nothing.
	//  2. ErrUsed for a reservation that was consumed. It changes nothing,
	//     also with a higher step.
	//  3. ErrExpired for a reservation that expired at or before at. It
	//     changes nothing.
	//  4. ErrUsed for a step that is not higher than the stored step. It
	//     consumes the reservation and keeps the failure count of the
	//     attempt, so that a retry of the reservation with another step fails.
	//
	// A lock or a disable that a or a concurrent attempt set does not refuse
	// a, because BeginTOTPAttempt passed it. Of two calls at the same time
	// with one reservation, one wins. A reset or a new active factor ends the
	// reservations of the earlier factor.
	AcceptStep(ctx context.Context, a Attempt, step int64, at time.Time) error
	// ResetTOTP deletes the active and the pending factor, revokes each
	// credential that TOTP made and appends e, in one transaction.
	ResetTOTP(ctx context.Context, personID string, e audit.Entry) error
}

// Verifier checks a signature of a step-up key over a message. Each key
// format has its own adapter. The suite RunVerifier in package stepuptest
// states the rules.
type Verifier interface {
	// Verify returns the assertion of a valid signature of k over message
	// for the scope scope. It verifies with k only, never with a key that
	// the signature holds. It returns ErrBadAnswer for a signature that does
	// not verify, an SSH signature whose embedded public key is not k, an
	// OpenPGP signature whose issuer fingerprint is not k or a signing
	// subkey of k, a signature that has another SSH namespace or OpenPGP
	// notation than the scope needs, an SSH hash algorithm other than
	// sha256 or sha512, or an OpenPGP signature that is not binary or whose
	// notation is not in the hashed area. It returns ErrInvalid for a key
	// type or a key size that the verifier does not allow.
	Verify(ctx context.Context, k Key, scope Scope, message, signature []byte) (Assertion, error)
}
