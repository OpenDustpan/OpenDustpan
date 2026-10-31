// Package localuser holds the ports of the local users of an installation: the
// persons who sign in to the console without an identity provider, with a
// user name and a password, with a passkey, or with both.
//
// A local user is one person principal of package principal. The local user
// record adds the user name and the password hash. The passkeys of the person
// are in package mfa. So a person who signs in with a password and a person
// who signs in with a passkey are the same local user when the record and the
// passkeys name the same principal ID. The server never links two records by
// a user name or an email.
//
// The package holds types, defaults, sentinel errors and ports only. No
// adapter and no service implements them yet. The service lives in
// internal/localusersvc when it lands, and each store adapter runs the suite
// in package localusertest.
//
// Local users belong to the installation, not to a namespace, so no write
// carries a namespace fence. Each change takes its audit entry, in the scope
// audit.ServerScope, and appends it in the same transaction as the change:
// both commit, or neither.
package localuser

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ProviderID is the provider ID of every session that a local user sign-in
// makes. No configured identity provider can use it.
const ProviderID = "local"

// Rules of a user name: 1 to 64 characters that match
// ^[a-z0-9][a-z0-9._-]{0,63}$. The server compares user names after an ASCII
// lowercase fold, and it refuses every other character. It never changes a
// name to make it valid.
const (
	UserNameMaxLength = 64
	UserNamePattern   = `^[a-z0-9][a-z0-9._-]{0,63}$`
)

// Defaults and bounds of the password rules.
//
// A password is a sequence of Unicode code points. The server applies the
// Unicode normalization form NFC before it counts the length and before it
// hashes the password. It refuses a password that holds a control character.
// It never truncates a password.
const (
	// DefaultMinLength is the minimum length of a password, in code points,
	// for a password that can be the only factor of a person.
	DefaultMinLength = 15
	// MinLengthFloor is the lowest value of the minimum-length setting.
	MinLengthFloor = 12
	// MinLengthCeiling is the highest value of the minimum-length setting.
	MinLengthCeiling = 64
	// MaxLength is the maximum length of a password, in code points.
	MaxLength = 256
	// MaxBytes is the maximum size of a password in UTF-8, after NFC.
	MaxBytes = 1024
)

// Defaults of a password reset or a password set-up.
const (
	// DefaultResetLifetime is the lifetime of a reset code.
	DefaultResetLifetime = time.Hour
	// MinResetLifetime and MaxResetLifetime bound the reset-lifetime
	// setting.
	MinResetLifetime = 10 * time.Minute
	MaxResetLifetime = 24 * time.Hour
	// ResetSecretBytes is the count of random bytes of a reset code.
	ResetSecretBytes = 32
)

// Subjects and the user name of the demo person of a dev installation. The
// subject starts with principal.DevSubjectPrefix, so a server of the kind
// production refuses to authenticate the demo person.
const (
	DemoUserName = "demo"
	DemoSubject  = principal.DevSubjectPrefix + DemoUserName
)

// Sentinel errors of the ports and of the service. Adapters translate driver
// errors into these, so callers never see driver text.
var (
	// ErrNotFound reports a local user or a reset code that does not exist.
	// A reset code that expired does not exist.
	ErrNotFound = errors.New("local user not found")
	// ErrAlreadyExists reports a user name, a principal or a reset digest
	// that a local user record or a reset code holds.
	ErrAlreadyExists = errors.New("local user already exists")
	// ErrInvalid reports a value that fails a check of this package: a user
	// name that does not match UserNamePattern, an empty field, or a hash
	// that the PHC string format cannot hold.
	ErrInvalid = errors.New("invalid local user value")
	// ErrPasswordTooShort reports a password below the minimum length.
	ErrPasswordTooShort = errors.New("password is too short")
	// ErrPasswordTooLong reports a password above MaxLength or MaxBytes.
	ErrPasswordTooLong = errors.New("password is too long")
	// ErrPasswordForm reports a password that holds a control character or
	// that is not valid UTF-8.
	ErrPasswordForm = errors.New("password holds a character that is not allowed")
	// ErrPasswordBlocked reports a password that the blocklist holds, that
	// holds a context word (the user name, the name of the person, the local part
	// of the email, or the product name), or that is one character repeated
	// or a sequence.
	ErrPasswordBlocked = errors.New("password is too common")
	// ErrPasswordLettersAndDigits reports a password with no letter or no
	// digit, when the installation setting that asks for both is on. The
	// setting is off by default.
	ErrPasswordLettersAndDigits = errors.New("password needs a letter and a digit")
	// ErrWrongPassword reports a password that does not match the hash. The
	// service never sends it to a client: a client gets ErrSignInFailed.
	ErrWrongPassword = errors.New("password does not match")
	// ErrSignInFailed is the one answer of a password sign-in that fails:
	// for an unknown user name, a wrong password, a locked name, a disabled
	// principal and a user with no password. So the answer does not tell
	// that a user exists.
	ErrSignInFailed = errors.New("the user name or the password is wrong")
	// ErrCurrentPasswordRequired reports a password change with no current
	// password and no fresh authentication.
	ErrCurrentPasswordRequired = errors.New("the current password or a fresh authentication is required")
	// ErrLastCredential reports a change that removes the last sign-in
	// credential of a local user: the password when the person has no
	// passkey.
	ErrLastCredential = errors.New("the change removes the last sign-in credential")
	// ErrDemoOnly reports a demo sign-in on an installation that is not of
	// the kind dev, or a demo person on such an installation.
	ErrDemoOnly = errors.New("the demo user exists only on a dev installation")
	// ErrDemoFixed reports a change of the password, the user name or the
	// factors of the demo person. The server sets them.
	ErrDemoFixed = errors.New("the credentials of the demo user cannot change")
	// ErrUnavailable reports that the store or the hasher cannot answer.
	// The sign-in fails closed on it.
	ErrUnavailable = errors.New("local user store unavailable")
)

// User is the stored record of one local user, without its password hash.
// ID is stable and opaque, with the prefix "lus-". PrincipalID names the
// person principal. UserName is in the stored form (lowercase). CreatedBy is
// the subject of the principal that made the record, or "system" for the
// demo seed. PasswordSetAt is zero for a user with no password.
// ChangeRequired marks a password that the person must change at the next
// sign-in. Demo marks the demo person of a dev installation.
type User struct {
	CreatedAt      time.Time
	PasswordSetAt  time.Time
	ID             string
	PrincipalID    string
	UserName       string
	CreatedBy      string
	ChangeRequired bool
	Demo           bool
}

// Algorithm names a password hash function.
type Algorithm string

// Algorithms of a password hash. Argon2id is the only one.
const (
	// AlgorithmArgon2id is Argon2id of RFC 9106, version 19, from
	// golang.org/x/crypto/argon2.
	AlgorithmArgon2id Algorithm = "argon2id"
)

// HashParams are the cost parameters of a password hash. The stored hash
// holds them, so a change of the defaults applies to each new hash, and the
// server rehashes an older hash at the next good sign-in.
type HashParams struct {
	Algorithm Algorithm
	// MemoryKiB is the memory cost in KiB.
	MemoryKiB uint32
	// Iterations is the time cost.
	Iterations uint32
	// Parallelism is the count of lanes.
	Parallelism uint8
	// SaltBytes is the length of the random salt.
	SaltBytes uint32
	// KeyBytes is the length of the derived key.
	KeyBytes uint32
}

// Default cost parameters of Argon2id: 19 MiB of memory, 2 iterations and 1
// lane, a 16-byte salt and a 32-byte key.
const (
	DefaultMemoryKiB   = 19456
	DefaultIterations  = 2
	DefaultParallelism = 1
	DefaultSaltBytes   = 16
	DefaultKeyBytes    = 32
)

// Bounds of the parameters that a stored hash can hold. A stored hash out of
// these bounds is not valid, so a database row cannot make the server spend
// unbounded memory or time.
const (
	MinMemoryKiB   = 7168
	MaxMemoryKiB   = 262144
	MinIterations  = 1
	MaxIterations  = 16
	MaxParallelism = 4
	MinSaltBytes   = 16
	MaxSaltBytes   = 64
	MinKeyBytes    = 32
	MaxKeyBytes    = 64
)

// DefaultHashParams returns the default cost parameters.
func DefaultHashParams() HashParams {
	return HashParams{
		Algorithm:   AlgorithmArgon2id,
		MemoryKiB:   DefaultMemoryKiB,
		Iterations:  DefaultIterations,
		Parallelism: DefaultParallelism,
		SaltBytes:   DefaultSaltBytes,
		KeyBytes:    DefaultKeyBytes,
	}
}

// PasswordHash is a stored password hash in the PHC string format, for
// example $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>, with the salt and the
// key in base64 without padding. The store keeps the string and returns it
// unchanged. Only a PasswordHasher reads it.
type PasswordHash string

// Verdict is the answer of a good password check.
type Verdict struct {
	// NeedsRehash reports a hash with other parameters than the current
	// ones. The service then stores a new hash with RehashPassword.
	NeedsRehash bool
}

// PasswordHasher hashes and checks passwords. The adapter on Argon2id lives
// outside the domain packages. The suite RunHasher in package localusertest
// states the rules.
type PasswordHasher interface {
	// Hash returns the hash of password with a new random salt from
	// crypto/rand and the parameters p. The password is the NFC form that
	// the service checked. It returns ErrInvalid for parameters out of the
	// bounds, and ErrPasswordTooLong above MaxBytes.
	Hash(ctx context.Context, password string, p HashParams) (PasswordHash, error)
	// Verify checks password against h in constant time over the derived
	// key, and compares the parameters of h with current. It returns
	// ErrWrongPassword for a password that does not match, and ErrInvalid
	// for a hash that does not parse or whose parameters are out of the
	// bounds. It never panics on a malformed hash.
	Verify(ctx context.Context, password string, h PasswordHash, current HashParams) (Verdict, error)
}

// Blocklist holds the common and breached passwords that the binary ships.
// The list is a file in the binary: a check never calls a service outside
// the installation.
type Blocklist interface {
	// Blocked reports whether the list holds candidate, compared after NFC
	// and a Unicode lowercase fold. It returns ErrUnavailable when the list
	// cannot load. The service then refuses the password.
	Blocked(ctx context.Context, candidate string) (bool, error)
}

// Purpose names why a reset code exists.
type Purpose string

// Purposes of a reset code.
const (
	// PurposeSetUp is the first password of a new local user. The
	// administrator who makes the user never knows the password.
	PurposeSetUp Purpose = "set-up"
	// PurposeReset is a reset of a password that an administrator starts.
	PurposeReset Purpose = "reset"
)

// Reset is one stored reset code, without its secret. ID has the prefix
// "lpr-". The store keeps only the SHA-256 digest of the secret. CreatedBy
// is the subject of the administrator, and Reason is the reason that the
// administrator typed. RevokeAccessTokens is the choice of the
// administrator to revoke the access tokens of the person too. It is false
// by default, and the audit entry of the reset records it.
type Reset struct {
	CreatedAt          time.Time
	ExpiresAt          time.Time
	ID                 string
	UserID             string
	CreatedBy          string
	Reason             string
	Purpose            Purpose
	RevokeAccessTokens bool
}

// Digest is the SHA-256 digest of a reset code.
type Digest [32]byte

// PasswordChange holds the options of a password write.
type PasswordChange struct {
	// ChangeRequired sets the flag that asks the person for a new password
	// at the next sign-in.
	ChangeRequired bool
	// KeepSession is the ID of the one session that stays when the person
	// changes the own password. Every other session of the person ends in
	// the same transaction. Empty ends every session.
	KeepSession string
}

// Store is the persistence port of local users.
type Store interface {
	// CreateUser stores u and appends e, in one transaction. h is empty for
	// a user with no password yet. It returns ErrInvalid for a value that
	// fails a check of this package, ErrAlreadyExists when the user name
	// or the principal has a record, and principal.ErrNotFound when the
	// principal does not exist or is not a person.
	CreateUser(ctx context.Context, u User, h PasswordHash, e audit.Entry) error
	// UserByName returns the user whose stored user name is name, and its
	// hash, or ErrNotFound. The hash is empty for a user with no password.
	UserByName(ctx context.Context, name string) (User, PasswordHash, error)
	// UserByPrincipal returns the user of the principal principalID, or
	// ErrNotFound.
	UserByPrincipal(ctx context.Context, principalID string) (User, error)
	// SetPassword stores h as the password of the user userID, sets
	// PasswordSetAt to e.At, applies c, ends the sessions of the person that
	// c names, and appends e, in one transaction. It returns ErrNotFound for
	// no such user, and ErrDemoFixed for the demo person.
	SetPassword(ctx context.Context, userID string, h PasswordHash, c PasswordChange, e audit.Entry) error
	// RehashPassword replaces the hash old of the user userID with h and
	// appends e, in one transaction, only when the stored hash is still
	// old. It returns ErrNotFound when the user has no such hash, so that a
	// rehash never overwrites a password that changed in the meantime.
	RehashPassword(ctx context.Context, userID string, old, h PasswordHash, e audit.Entry) error
	// RemovePassword deletes the password of the user userID and appends e,
	// in one transaction. It returns ErrLastCredential when the person has
	// no passkey, ErrNotFound for no such user, and ErrDemoFixed for the
	// demo person.
	RemovePassword(ctx context.Context, userID string, e audit.Entry) error
	// DeleteUser deletes the record of the user userID and every reset code
	// of the user, and appends e, in one transaction. The principal stays.
	// It returns ErrNotFound for no such user.
	DeleteUser(ctx context.Context, userID string, e audit.Entry) error
}

// ResetStore is the persistence port of reset codes.
type ResetStore interface {
	// CreateReset stores r, keyed by the digest d of its secret, deletes
	// every earlier reset code of the same user, and appends e, in one
	// transaction. For PurposeReset, the same transaction ends every
	// session of the person, and revokes each access token of the person
	// when r.RevokeAccessTokens is set. It returns ErrInvalid for a value that fails a check,
	// ErrNotFound for no such user, ErrAlreadyExists when the digest is
	// taken, and ErrDemoFixed for the demo person.
	CreateReset(ctx context.Context, r Reset, d Digest, e audit.Entry) error
	// TakeReset deletes the reset code with the digest d and returns it, so
	// that a code works once. It returns ErrNotFound when no code has the
	// digest, or when the code expired at or before at. It deletes an
	// expired code too.
	TakeReset(ctx context.Context, d Digest, at time.Time) (Reset, error)
	// PruneResets deletes the reset codes that expired at or before at, and
	// returns how many it deleted.
	PruneResets(ctx context.Context, at time.Time) (int64, error)
}
