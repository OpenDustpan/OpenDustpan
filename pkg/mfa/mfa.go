// Package mfa holds the ports of the built-in factors of a person: passkeys
// (WebAuthn credentials), recovery codes, the pending sign-in between a
// first method and a second factor, and the MFA policy of the installation.
//
// A passkey is a sign-in of its own for a local user, and a second factor
// after a password. The TOTP factor is the one factor that the CLI step-up
// shares, so its port stays in the step-up package. A recovery code is a
// one-use code for a person who lost each factor.
//
// The package holds types, defaults, checks, sentinel errors and ports. The
// SQL adapters implement PasskeyStore, the adapter in package gowebauthn
// implements RelyingParty, and the service in internal/mfasvc runs the
// passkey ceremonies. Each store adapter runs the suites in package mfatest.
//
// Factors belong to a person, not to a namespace, so no write carries a
// namespace fence. Each change takes its audit entry, in the scope
// audit.ServerScope, and appends it in the same transaction as the change.
package mfa

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

// Defaults and limits of the factors.
const (
	// MaxPasskeys is the most passkeys that one person can hold.
	MaxPasskeys = 20
	// ChallengeBytes is the count of random bytes of a WebAuthn challenge.
	ChallengeBytes = 32
	// ChallengeLifetime is the lifetime of a WebAuthn ceremony.
	ChallengeLifetime = 300 * time.Second
	// RecoveryCodeCount is the count of recovery codes in a set.
	RecoveryCodeCount = 10
	// RecoveryCodeLength is the count of base32 characters of a recovery
	// code: 80 bits.
	RecoveryCodeLength = 16
	// PendingLifetime is the lifetime of a pending sign-in.
	PendingLifetime = 300 * time.Second
	// PendingMaxFailures is the count of wrong factors that makes a pending
	// sign-in void.
	PendingMaxFailures = 5
	// MaxEnrollmentGraceDays is the highest value of the grace setting.
	MaxEnrollmentGraceDays = 14
)

// Sentinel errors of the ports. Adapters translate driver errors into these.
var (
	// ErrNotFound reports a passkey, a ceremony, a recovery code or a
	// pending sign-in that does not exist. An expired record does not
	// exist.
	ErrNotFound = errors.New("factor record not found")
	// ErrAlreadyExists reports a credential ID or a digest that a record
	// holds.
	ErrAlreadyExists = errors.New("factor record already exists")
	// ErrInvalid reports a value that fails a check of this package.
	ErrInvalid = errors.New("invalid factor value")
	// ErrLimit reports a person who holds MaxPasskeys passkeys.
	ErrLimit = errors.New("too many passkeys")
	// ErrCounterRegressed reports a signature counter that is not higher
	// than the stored counter, when either one is not 0. The store then
	// suspends the passkey.
	ErrCounterRegressed = errors.New("passkey signature counter went back")
	// ErrSuspended reports a passkey that a counter regression suspended.
	ErrSuspended = errors.New("passkey is suspended")
	// ErrVoid reports a pending sign-in after PendingMaxFailures wrong
	// factors.
	ErrVoid = errors.New("pending sign-in is void")
	// ErrEnrollmentRequired reports a person with no factor when the policy
	// requires one and the grace ended. The session can only enroll a
	// factor and sign out.
	ErrEnrollmentRequired = errors.New("enroll a factor to continue")
	// ErrLastCredential reports the removal of the last passkey of a local
	// user who has no password.
	ErrLastCredential = errors.New("the change removes the last sign-in credential")
	// ErrUnavailable reports that the store cannot answer. Each sign-in
	// fails closed on it.
	ErrUnavailable = errors.New("factor store unavailable")
)

// Passkey is one stored WebAuthn credential of a person. ID has the prefix
// "pky-". CredentialID is the credential ID of the authenticator.
// PublicKey is the COSE key. AAGUID names the authenticator model, and is
// zero with attestation "none". BackupEligible marks a synced passkey.
type Passkey struct {
	CreatedAt      time.Time
	LastUsedAt     time.Time
	SuspendedAt    time.Time
	ID             string
	PersonID       string
	Name           string
	CredentialID   []byte
	PublicKey      []byte
	Transports     []string
	AAGUID         [16]byte
	SignCount      uint32
	BackupEligible bool
}

// Ceremony names the kind of a WebAuthn ceremony.
type Ceremony string

// Kinds of a ceremony.
const (
	// CeremonyRegister adds a passkey.
	CeremonyRegister Ceremony = "register"
	// CeremonySignIn is a passkey sign-in with a discoverable credential
	// and user verification required.
	CeremonySignIn Ceremony = "sign-in"
	// CeremonySecondFactor is a passkey after a first method, with user
	// verification preferred.
	CeremonySecondFactor Ceremony = "second-factor"
	// CeremonyFresh is a fresh authentication in a session.
	CeremonyFresh Ceremony = "fresh"
)

// Challenge is one open WebAuthn ceremony. The store keys it by the SHA-256
// digest of the challenge bytes. PersonID is empty for CeremonySignIn,
// which starts with no user name. PendingID names the pending sign-in of a
// CeremonySecondFactor, and SessionID the session of a CeremonyRegister or a
// CeremonyFresh.
type Challenge struct {
	CreatedAt time.Time
	ExpiresAt time.Time
	Kind      Ceremony
	PersonID  string
	PendingID string
	SessionID string
}

// Digest is a SHA-256 digest of a secret: a challenge, a recovery code or
// the secret of a pending sign-in.
type Digest [32]byte

// PasskeyUse is the result of a good assertion that the store records.
type PasskeyUse struct {
	At        time.Time
	PasskeyID string
	SignCount uint32
}

// PasskeyStore is the persistence port of passkeys and ceremonies.
//
// AddPasskey and RemovePasskey lock the row of the person principal in their
// transaction, before they read the passkeys and the password of the
// person, as the local user store does. So a removal of the password and a
// removal of the last passkey at the same time cannot both pass.
type PasskeyStore interface {
	// AddPasskey stores p and appends e, in one transaction. It returns
	// ErrInvalid for a value that Passkey.Validate refuses, a use or a
	// suspension already set, or a principal that is not a person that
	// exists, ErrAlreadyExists for a passkey ID or a credential ID that any
	// person holds, ErrLimit when the person holds MaxPasskeys passkeys, and
	// localuser.ErrDemoFixed for the demo person.
	AddPasskey(ctx context.Context, p Passkey, e audit.Entry) error
	// Passkeys returns the passkeys of the person personID in the order of
	// the add.
	Passkeys(ctx context.Context, personID string) ([]Passkey, error)
	// PasskeyByCredentialID returns the passkey with the credential ID id,
	// or ErrNotFound.
	PasskeyByCredentialID(ctx context.Context, id []byte) (Passkey, error)
	// RecordUse sets the last use and the counter of the passkey u.PasskeyID.
	// When the new counter is not higher than the stored counter and either
	// one is not 0, it suspends the passkey, appends regressed, and returns
	// ErrCounterRegressed, in one transaction. It returns ErrSuspended for a
	// suspended passkey and ErrNotFound for no such passkey.
	RecordUse(ctx context.Context, u PasskeyUse, regressed audit.Entry) error
	// RemovePasskey deletes the passkey id of the person personID and
	// appends e, in one transaction. A local user with no password, and a
	// break-glass person, keep at least one passkey that is not suspended:
	// the store returns ErrLastCredential for a removal that leaves none, and
	// for each removal when none is left. It returns ErrNotFound for a
	// passkey of another person, and localuser.ErrDemoFixed for the demo
	// person.
	RemovePasskey(ctx context.Context, personID, id string, e audit.Entry) error
	// CreateChallenge deletes the challenges that expired at c.CreatedAt and
	// stores c keyed by d. It returns ErrInvalid for a value that
	// Challenge.Validate refuses, and ErrAlreadyExists when d is taken.
	CreateChallenge(ctx context.Context, c Challenge, d Digest) error
	// TakeChallenge deletes the challenge d and returns it, so that a
	// challenge works once. It returns ErrNotFound when no challenge has the
	// digest or when it expired at or before at, and deletes it then too.
	TakeChallenge(ctx context.Context, d Digest, at time.Time) (Challenge, error)
}

// RecoveryCodeStore is the persistence port of recovery codes. The store
// keeps the SHA-256 digest of each code, never the code.
type RecoveryCodeStore interface {
	// ReplaceRecoveryCodes deletes every code of the person personID,
	// stores the digests ds, and appends e, in one transaction. It returns
	// ErrInvalid unless ds holds RecoveryCodeCount distinct digests.
	ReplaceRecoveryCodes(ctx context.Context, personID string, ds []Digest, e audit.Entry) error
	// UseRecoveryCode deletes the code d of the person personID and appends
	// e, in one transaction, and returns the count of codes that remain. It
	// returns ErrNotFound when the person has no such code. Of two uses at
	// the same time, one wins.
	UseRecoveryCode(ctx context.Context, personID string, d Digest, e audit.Entry) (int, error)
	// RecoveryCodesLeft returns the count of codes of the person personID.
	RecoveryCodesLeft(ctx context.Context, personID string) (int, error)
}

// FirstMethod names the method before the second factor of a pending
// sign-in.
type FirstMethod string

// First methods.
const (
	// FirstPassword is the password of a local user.
	FirstPassword FirstMethod = "password"
	// FirstLDAP is a bind at an LDAP directory.
	FirstLDAP FirstMethod = "ldap"
)

// Pending is one pending sign-in: the state after the first method and
// before the second factor. It holds no rights. ID has the prefix "psi-".
// The store keys it by the SHA-256 digest of its secret, which the browser
// holds in a cookie.
type Pending struct {
	CreatedAt   time.Time
	ExpiresAt   time.Time
	FirstAt     time.Time
	ID          string
	PersonID    string
	Method      FirstMethod
	SourceIP    string
	UserAgent   string
	Failures    int
	EnrollsOnly bool
}

// PendingStore is the persistence port of pending sign-ins.
type PendingStore interface {
	// CreatePending stores p keyed by d. It returns ErrInvalid for a value
	// that fails a check, and ErrAlreadyExists when d is taken.
	CreatePending(ctx context.Context, p Pending, d Digest) error
	// PendingByDigest returns the pending sign-in d. It returns ErrNotFound
	// when none has the digest or when it expired at or before at, and
	// ErrVoid after PendingMaxFailures failures.
	PendingByDigest(ctx context.Context, d Digest, at time.Time) (Pending, error)
	// FailPending counts one wrong factor on the pending sign-in d before
	// the service checks the factor, and returns the count after the add.
	// The add that reaches PendingMaxFailures makes it void. Of calls at
	// the same time, no more than PendingMaxFailures pass. It returns
	// ErrNotFound and ErrVoid as PendingByDigest does, and counts nothing
	// then.
	FailPending(ctx context.Context, d Digest, at time.Time) (int, error)
	// CompletePending turns the pending sign-in d into the session s, in
	// one transaction: it checks that the pending sign-in exists, did not
	// expire at or before at and is not void, deletes it, stores s with the
	// digest sd of its secret, sets the last sign-in time of the person, and
	// appends e. So a good factor makes one session only, and a failed
	// session write leaves the pending sign-in as it was. The adapter writes
	// the session with the same code as session.Store.CreateSession, so the
	// session meets each check of that method. The caller clears the
	// lockout counts only after the commit. It returns the
	// errors of PendingByDigest, ErrInvalid when s names another person than
	// the pending sign-in, and the errors of session.Store.CreateSession.
	CompletePending(
		ctx context.Context,
		d Digest,
		at time.Time,
		s session.Session,
		sd session.Digest,
		e audit.Entry,
	) (Pending, error)
	// PrunePending deletes the pending sign-ins that expired at or before
	// at, and returns how many it deleted.
	PrunePending(ctx context.Context, at time.Time) (int64, error)
}

// AAGUIDScope names to whom the allowlist of authenticator models applies.
type AAGUIDScope string

// Scopes of the allowlist of authenticator models.
const (
	// AAGUIDScopeNone applies the allowlist to no one.
	AAGUIDScopeNone AAGUIDScope = "none"
	// AAGUIDScopeBreakGlass applies it to break-glass accounts only.
	AAGUIDScopeBreakGlass AAGUIDScope = "break-glass"
	// AAGUIDScopeAll applies it to every person.
	AAGUIDScopeAll AAGUIDScope = "all"
)

// Policy is the MFA policy of the installation. The settings resource holds
// it, and the API shows each effective value.
type Policy struct {
	// Enabled turns built-in MFA on. Off by default.
	Enabled bool
	// RequireForAll requires a second factor after a password, and a
	// factor for each person. Off by default.
	RequireForAll bool
	// RequireForSecretReveal requires a fresh factor for a secret reveal.
	// Off by default.
	RequireForSecretReveal bool
	// EnrollmentGraceDays lets a person with no factor skip the enrollment
	// for this many days after RequireForAll turns on. 0 by default, at most
	// MaxEnrollmentGraceDays.
	EnrollmentGraceDays int
	// AllowedAAGUIDs is the allowlist of authenticator models. Empty means
	// any model, with attestation "none".
	AllowedAAGUIDs [][16]byte
	// AllowedAAGUIDsScope says to whom AllowedAAGUIDs applies.
	AllowedAAGUIDsScope AAGUIDScope
}

// DefaultPolicy returns the default policy: everything off.
func DefaultPolicy() Policy {
	return Policy{
		Enabled:                false,
		RequireForAll:          false,
		RequireForSecretReveal: false,
		EnrollmentGraceDays:    0,
		AllowedAAGUIDs:         nil,
		AllowedAAGUIDsScope:    AAGUIDScopeNone,
	}
}
