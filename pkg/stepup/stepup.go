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
package stepup

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Sentinel errors of the ports. Adapters translate driver errors into these.
var (
	// ErrNotFound reports a key, a challenge, a credential or a TOTP factor
	// that does not exist.
	ErrNotFound = errors.New("step-up record not found")
	// ErrExpired reports a challenge or a credential after its expiry.
	ErrExpired = errors.New("step-up expired")
	// ErrUsed reports a challenge or a credential that was used up, or a
	// TOTP time step that the server accepted before.
	ErrUsed = errors.New("step-up already used")
	// ErrMismatch reports a request that does not match the values that its
	// challenge binds.
	ErrMismatch = errors.New("request does not match the step-up challenge")
	// ErrBadAnswer reports a signature or a code that does not verify.
	ErrBadAnswer = errors.New("step-up answer does not verify")
	// ErrFactorRefused reports a factor that the step-up factor level of the
	// namespace or the installation does not allow.
	ErrFactorRefused = errors.New("factor not allowed for this step-up")
	// ErrCLIStepUpOff reports a CLI step-up in a namespace or an
	// installation that turned it off.
	ErrCLIStepUpOff = errors.New("CLI step-up is off")
	// ErrLocked reports a TOTP factor after too many wrong codes.
	ErrLocked = errors.New("factor locked after failed attempts")
	// ErrNotPerson reports a step-up for a principal that is not a person,
	// or with a credential that is not a personal token or a session.
	ErrNotPerson = errors.New("only a person can do a step-up")
	// ErrInvalid reports a value that fails a check of this package.
	ErrInvalid = errors.New("invalid step-up value")
	// ErrLimit reports a person who already holds the maximum count of keys.
	ErrLimit = errors.New("step-up key limit reached")
	// ErrConflict reports a key that the person registered before, or a
	// counter that is not higher than the stored counter.
	ErrConflict = errors.New("step-up key conflict")
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
// strictest level that holds for a write applies.
type Level string

// The step-up factor levels, from the least strict to the strictest.
const (
	LevelAny         Level = "any"
	LevelKey         Level = "key"
	LevelHardwareKey Level = "hardware-key"
)

// Scope is what a credential allows.
type Scope string

// The scopes of a credential.
const (
	// ScopeWrite allows the one request that the challenge binds.
	ScopeWrite Scope = "write"
	// ScopeWindow allows the named actions in one namespace, for a short
	// time and a fixed count of writes.
	ScopeWindow Scope = "window"
)

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
	// CreatedAt is the time of the add.
	CreatedAt time.Time
}

// Binding is the request that a challenge of ScopeWrite binds.
type Binding struct {
	Method         string
	Path           string
	BodySHA256     [32]byte
	IfMatch        string
	IdempotencyKey string
}

// Challenge is the server record of one step-up.
type Challenge struct {
	// ID is the challenge ID, with the prefix "stc-".
	ID string
	// Nonce holds 32 random bytes.
	Nonce []byte
	// PersonID and TokenID name the person and the access token of the
	// request that made the challenge. TokenID is empty for a session.
	PersonID string
	TokenID  string
	// Origin is the public URL of the server.
	Origin string
	// Scope is ScopeWrite or ScopeWindow.
	Scope Scope
	// Action and Object are the action and the object of the write, for
	// ScopeWrite.
	Action identity.Action
	Object string
	// Binding is the request, for ScopeWrite.
	Binding Binding
	// Namespace and Actions are what a credential of ScopeWindow allows.
	Namespace string
	Actions   []identity.Action
	// Summary is the text that the CLI and the console show to the person.
	Summary string
	// CreatedAt and ExpiresAt bound the challenge.
	CreatedAt time.Time
	ExpiresAt time.Time
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
	// false for every other key.
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
	TokenID     string
	Factor      Factor
	// KeyID names the key, for a key factor.
	KeyID string
	Scope Scope
	// AuthTime is the time of the factor, for the fresh window of a write.
	AuthTime  time.Time
	ExpiresAt time.Time
	// UsesLeft is 1 for ScopeWrite, and at most the window limit for
	// ScopeWindow.
	UsesLeft int
}

// Use is one use of a credential by a request.
type Use struct {
	SecretHash [32]byte
	TokenID    string
	Action     identity.Action
	Object     string
	Namespace  string
	Binding    Binding
	At         time.Time
}

// KeyStore is the persistence port for step-up keys.
type KeyStore interface {
	// AddKey stores k and appends e in the same transaction. It returns
	// ErrConflict for a fingerprint that the person registered before,
	// ErrLimit when the person holds the maximum count of keys, and
	// ErrInvalid for a key that fails a check.
	AddKey(ctx context.Context, k Key, e audit.Entry) error
	// Keys returns the keys of the person personID in the order of the add.
	Keys(ctx context.Context, personID string) ([]Key, error)
	// Key returns the key id of the person personID, or ErrNotFound.
	Key(ctx context.Context, personID, id string) (Key, error)
	// RevokeKey deletes the key, revokes each credential that it made and
	// appends e, in one transaction. It returns ErrNotFound.
	RevokeKey(ctx context.Context, personID, id string, e audit.Entry) error
	// AdvanceCounter sets the counter of a FIDO key to counter when it is
	// higher than the stored counter, or when both are 0. Else it returns
	// ErrConflict and changes nothing.
	AdvanceCounter(ctx context.Context, personID, id string, counter uint32) error
}

// ChallengeStore is the persistence port for step-up challenges.
type ChallengeStore interface {
	// CreateChallenge stores c. It returns ErrInvalid for a challenge that
	// fails a check.
	CreateChallenge(ctx context.Context, c Challenge) error
	// Challenge returns the challenge id of the person personID, or
	// ErrNotFound. A challenge of another person is ErrNotFound.
	Challenge(ctx context.Context, personID, id string) (Challenge, error)
	// ConsumeChallenge marks the challenge as answered at the time at, in one
	// transaction. It returns ErrUsed for a challenge that was answered or
	// void, ErrExpired after its expiry and ErrNotFound. Of two calls at the
	// same time, one wins.
	ConsumeChallenge(ctx context.Context, personID, id string, at time.Time) error
	// RecordFailure counts one wrong answer and appends e. It returns ErrUsed
	// when the count reaches the limit, and then the challenge is void.
	RecordFailure(ctx context.Context, personID, id string, e audit.Entry) error
}

// CredentialStore is the persistence port for step-up credentials.
type CredentialStore interface {
	// SaveCredential stores c.
	SaveCredential(ctx context.Context, c Credential) error
	// UseCredential checks u against the credential with the same secret
	// hash and takes one use, in one transaction. It returns ErrNotFound,
	// ErrExpired, ErrUsed when no use is left, and ErrMismatch when the
	// token, the action, the object, the namespace or the binding differ. A
	// mismatch also takes the last use, so the credential stops working. Of
	// two calls at the same time on the last use, one wins.
	UseCredential(ctx context.Context, u Use) (Credential, error)
	// RevokeCredentialsOfKey revokes each credential that the key keyID made.
	RevokeCredentialsOfKey(ctx context.Context, personID, keyID string) error
}

// TOTP is the TOTP factor of a person. The store keeps the secret sealed.
type TOTP struct {
	// ID is the factor ID, with the prefix "tot-".
	ID       string
	PersonID string
	// Sealed is the sealed secret. The store never holds the plain secret.
	Sealed []byte
	// LastStep is the last time step that the server accepted.
	LastStep int64
	// Failures is the count of wrong codes in a row.
	Failures int
	// LockedUntil is the end of a lockout, or the zero time.
	LockedUntil time.Time
	// Disabled reports a factor after the upper limit of wrong codes.
	Disabled bool
}

// TOTPStore is the persistence port for TOTP factors.
type TOTPStore interface {
	// EnrollTOTP stores f and appends e, in one transaction. It replaces an
	// earlier factor of the person.
	EnrollTOTP(ctx context.Context, f TOTP, e audit.Entry) error
	// TOTP returns the factor of the person personID, or ErrNotFound.
	TOTP(ctx context.Context, personID string) (TOTP, error)
	// AcceptStep sets the last step to step and the failures to 0 when step
	// is higher than the stored step. Else it returns ErrUsed and changes
	// nothing. Of two calls at the same time with the same step, one wins.
	AcceptStep(ctx context.Context, personID string, step int64) error
	// RecordTOTPFailure counts one wrong code at the time at and appends e.
	// It returns ErrLocked when the count reaches a limit.
	RecordTOTPFailure(ctx context.Context, personID string, at time.Time, e audit.Entry) error
	// ResetTOTP deletes the factor and appends e, in one transaction.
	ResetTOTP(ctx context.Context, personID string, e audit.Entry) error
}

// Verifier checks a signature of a step-up key over a message. Each key
// format has its own adapter.
type Verifier interface {
	// Verify returns the assertion of a valid signature of k over message. It
	// returns ErrBadAnswer for a signature that does not verify, or that has
	// another SSH namespace or OpenPGP notation, and ErrInvalid for a key
	// type that the verifier does not allow.
	Verify(ctx context.Context, k Key, message, signature []byte) (Assertion, error)
}
