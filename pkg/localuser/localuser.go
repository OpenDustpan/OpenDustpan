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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// ID prefixes of the records of this package.
const (
	UserIDPrefix  = "lus-"
	ResetIDPrefix = "lpr-"
)

// maxIDLength is the length limit of a record ID and of a principal ID.
const maxIDLength = 128

// MaxReasonLength is the length limit of the reason of a reset, in bytes.
const MaxReasonLength = 1024

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
	// ErrBreakGlass reports a password, a set-up code or a reset for a
	// break-glass person. A break-glass person signs in with a passkey or a
	// hardware key only.
	ErrBreakGlass = errors.New("a break-glass account signs in with a passkey or a hardware key only")
	// ErrPasswordSignInOff reports a password sign-in while the installation
	// setting for local password sign-in is off.
	ErrPasswordSignInOff = errors.New("password sign-in is off on this installation")
	// ErrUnavailable reports that the store or the hasher cannot answer.
	// The sign-in fails closed on it.
	ErrUnavailable = errors.New("local user store unavailable")
	// ErrHashBusy reports a hash that found no room in the memory budget of
	// the instance within HashWait. The sign-in answers that the server is
	// busy, and counts no failure for the name or the person.
	ErrHashBusy = errors.New("the password hash budget of this server is full")
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

// ValidateUserName returns ErrInvalid when name does not match
// UserNamePattern: 1 to 64 characters, a lowercase ASCII letter or a digit
// first, then lowercase ASCII letters, digits, '.', '_' and '-'. It never
// changes a name to make it valid.
func ValidateUserName(name string) error {
	if name == "" || len(name) > UserNameMaxLength {
		return fmt.Errorf("%w: a user name of %d bytes, want 1 to %d", ErrInvalid, len(name), UserNameMaxLength)
	}

	for i := range len(name) {
		c := name[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')

		if !alnum && (i == 0 || (c != '.' && c != '_' && c != '-')) {
			return fmt.Errorf("%w: the user name holds a character that is not allowed at %d", ErrInvalid, i)
		}
	}

	return nil
}

// FoldUserName returns name with each ASCII upper-case letter in lower case,
// and each other byte as it is. A sign-in compares the folded name with the
// stored name.
func FoldUserName(name string) string {
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}

	return string(b)
}

// validID reports whether id has the prefix and 1 to maxIDLength characters
// of ASCII letters, digits, '-', '_' and '.' after it.
func validID(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" || len(id) > maxIDLength {
		return false
	}

	return validToken(id)
}

// validToken reports whether s is 1 to maxIDLength characters of ASCII
// letters, digits, '-', '_', '.' and ':'.
func validToken(s string) bool {
	if s == "" || len(s) > maxIDLength {
		return false
	}

	return !strings.ContainsFunc(s, func(r rune) bool { return !isTokenRune(r) })
}

// isTokenRune reports an ASCII letter, a digit, '-', '_', '.' or ':'.
func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.' || r == ':'
	}
}

// Validate returns ErrInvalid when u cannot be stored: an ID without the
// prefix UserIDPrefix, an empty or malformed principal ID, a user name that
// fails ValidateUserName, an empty creator or a zero creation time.
func (u User) Validate() error {
	switch {
	case !validID(u.ID, UserIDPrefix):
		return fmt.Errorf("%w: user ID %q", ErrInvalid, u.ID)
	case !validToken(u.PrincipalID):
		return fmt.Errorf("%w: principal ID %q", ErrInvalid, u.PrincipalID)
	case !validCreator(u.CreatedBy):
		return fmt.Errorf("%w: the creator of a local user", ErrInvalid)
	case u.CreatedAt.IsZero():
		return fmt.Errorf("%w: a local user with no creation time", ErrInvalid)
	default:
		return ValidateUserName(u.UserName)
	}
}

// validCreator reports whether s is a subject that a record can name as its
// creator: 1 to 2*maxIDLength bytes with no control character.
func validCreator(s string) bool {
	return s != "" && len(s) <= maxIDLength*2 && !strings.ContainsFunc(s, isControl)
}

// isControl reports a control character of ASCII or of Unicode.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// Credential is what a password sign-in reads in one query: the user, the
// password hash, and the session generation of the person at the time of
// the read. The sign-in seals this generation in its session, never a
// generation that it reads later.
type Credential struct {
	User              User
	Hash              PasswordHash
	SessionGeneration int64
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
	MaxMemoryKiB   = 65536
	MinIterations  = 1
	MaxIterations  = 16
	MinParallelism = 1
	MaxParallelism = 4
	MinSaltBytes   = 16
	MaxSaltBytes   = 64
	MinKeyBytes    = 32
	MaxKeyBytes    = 64
)

// Defaults of the limit on hash work. Each hash takes its MemoryKiB from a
// memory budget of the instance, so a stored hash with a high memory cost
// takes more of the budget than a default hash. The budget is never below
// MaxMemoryKiB, so one hash of any valid hash always fits.
const (
	// DefaultHashMemoryBudgetKiB is the memory that the password hashes of
	// one instance can use at the same time: 80 MiB, four default hashes.
	DefaultHashMemoryBudgetKiB = 81920
	// MinHashMemoryBudgetKiB is the lowest value of the budget setting.
	MinHashMemoryBudgetKiB = MaxMemoryKiB
	// HashWait is the longest time that a hash waits for the budget. After
	// it, the sign-in answers that the server is busy.
	HashWait = 2 * time.Second
)

// Demo password. The seed makes a new password at each start of the dev
// server, from crypto/rand, in groups of letters and digits joined by
// hyphens. The password stays in the memory of the process only.
const (
	// DemoPasswordGroups is the count of groups of the demo password.
	DemoPasswordGroups = 5
	// DemoPasswordGroupLength is the count of characters in each group.
	DemoPasswordGroupLength = 4
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

// Validate returns ErrInvalid when p names another algorithm than
// AlgorithmArgon2id or holds a value outside the bounds of this package.
func (p HashParams) Validate() error {
	if p.Algorithm != AlgorithmArgon2id {
		return fmt.Errorf("%w: hash algorithm %q", ErrInvalid, p.Algorithm)
	}

	bounds := []struct {
		name          string
		value, lo, hi uint32
	}{
		{name: "memory in KiB", value: p.MemoryKiB, lo: MinMemoryKiB, hi: MaxMemoryKiB},
		{name: "iterations", value: p.Iterations, lo: MinIterations, hi: MaxIterations},
		{name: "parallelism", value: uint32(p.Parallelism), lo: MinParallelism, hi: MaxParallelism},
		{name: "salt bytes", value: p.SaltBytes, lo: MinSaltBytes, hi: MaxSaltBytes},
		{name: "key bytes", value: p.KeyBytes, lo: MinKeyBytes, hi: MaxKeyBytes},
	}

	for _, b := range bounds {
		if b.value < b.lo || b.value > b.hi {
			return fmt.Errorf("%w: hash %s %d, want %d to %d", ErrInvalid, b.name, b.value, b.lo, b.hi)
		}
	}

	return nil
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

// PasswordHasher hashes and checks passwords. Package localuser/argon2id
// holds the adapter on Argon2id. The suite RunHasher in package
// localusertest states the rules.
type PasswordHasher interface {
	// Hash returns the hash of password with a new random salt from
	// crypto/rand and the parameters p. The password is the NFC form that
	// the service checked. It returns ErrInvalid for parameters out of the
	// bounds, ErrPasswordTooLong above MaxBytes, and ErrHashBusy when the
	// memory budget of the instance has no room within HashWait.
	Hash(ctx context.Context, password string, p HashParams) (PasswordHash, error)
	// Verify checks password against h in constant time over the derived
	// key, and compares the parameters of h with current. It returns
	// ErrWrongPassword for a password that does not match, and ErrInvalid
	// for a hash that does not parse or whose parameters are out of the
	// bounds (a parallelism of 0 included), before any work. It never
	// panics on a malformed hash. It returns ErrPasswordTooLong above
	// MaxBytes, and ErrHashBusy as Hash does.
	Verify(ctx context.Context, password string, h PasswordHash, current HashParams) (Verdict, error)
	// Reserve parses h and takes its memory cost from the budget of the
	// instance, and returns the reservation. A sign-in adds the failure of
	// the name after Reserve and before Reservation.Verify, so that a
	// request that finds no room counts no failure, and no more attempts
	// than the limit reach the compare. It returns ErrInvalid for a hash
	// that does not parse or whose parameters are out of the bounds, before
	// it takes any budget, and ErrHashBusy when the budget has no room
	// within HashWait. The caller calls Release once.
	Reserve(ctx context.Context, h PasswordHash) (*Reservation, error)
}

// Reservation is a stored hash that holds its memory in the budget of the
// instance until Release. An adapter makes it with NewReservation. It works
// once, whatever the adapter does: a second Verify, or a Verify after
// Release, returns ErrInvalid and calls the adapter no more.
type Reservation struct {
	verify   func(password string, current HashParams) (Verdict, error)
	release  func()
	mu       sync.Mutex
	used     bool
	released bool
}

// NewReservation returns a reservation that checks a password with verify
// and gives the memory back with release. It returns nil when either
// function is nil.
func NewReservation(
	verify func(password string, current HashParams) (Verdict, error), release func(),
) *Reservation {
	if verify == nil || release == nil {
		return nil
	}

	return &Reservation{verify: verify, release: release, mu: sync.Mutex{}, used: false, released: false}
}

// Verify checks password against the reserved hash, as
// PasswordHasher.Verify does. It works once: a second call, or a call after
// Release, returns ErrInvalid.
func (r *Reservation) Verify(password string, current HashParams) (Verdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.used || r.released {
		return Verdict{NeedsRehash: false}, fmt.Errorf("%w: the reservation was used or released", ErrInvalid)
	}

	r.used = true

	return r.verify(password, current)
}

// Release gives the memory back to the budget. A second call does nothing.
func (r *Reservation) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.released {
		return
	}

	r.released = true
	r.release()
}

// Blocklist holds the common and breached passwords that the binary ships.
// The list is a file in the binary: a check never calls a service outside
// the installation.
type Blocklist interface {
	// Blocked reports whether the list holds candidate, compared after NFC
	// and a Unicode lowercase fold. It returns ErrUnavailable when the list
	// cannot load, and ErrPasswordTooLong for a candidate above MaxBytes. The
	// service then refuses the password.
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

// Valid reports whether p is PurposeSetUp or PurposeReset.
func (p Purpose) Valid() bool {
	return p == PurposeSetUp || p == PurposeReset
}

// Validate returns ErrInvalid when r cannot be stored: an ID without the
// prefix ResetIDPrefix, an empty user ID, an unknown purpose, an empty
// creator, a reason above MaxReasonLength or with a control character, or
// an expiry that is not after the creation time by at most
// MaxResetLifetime.
func (r Reset) Validate() error {
	switch {
	case !validID(r.ID, ResetIDPrefix):
		return fmt.Errorf("%w: reset ID %q", ErrInvalid, r.ID)
	case !validID(r.UserID, UserIDPrefix):
		return fmt.Errorf("%w: user ID %q", ErrInvalid, r.UserID)
	case !r.Purpose.Valid():
		return fmt.Errorf("%w: reset purpose %q", ErrInvalid, r.Purpose)
	case !validCreator(r.CreatedBy):
		return fmt.Errorf("%w: the creator of a reset", ErrInvalid)
	case len(r.Reason) > MaxReasonLength || strings.ContainsFunc(r.Reason, isControl):
		return fmt.Errorf("%w: the reason of a reset", ErrInvalid)
	case r.CreatedAt.IsZero() || !r.ExpiresAt.After(r.CreatedAt):
		return fmt.Errorf("%w: a reset that expires before it starts", ErrInvalid)
	case r.ExpiresAt.Sub(r.CreatedAt) > MaxResetLifetime:
		return fmt.Errorf("%w: a reset that lives longer than %s", ErrInvalid, MaxResetLifetime)
	default:
		return nil
	}
}

// Digest is the SHA-256 digest of a reset code.
type Digest [32]byte

// Redeem is one use of a reset code: the digest of the code, the time of
// the use, the hash of the new password and the options of the write.
type Redeem struct {
	At     time.Time
	Hash   PasswordHash
	Change PasswordChange
	Digest Digest
}

// DigestOf returns the SHA-256 digest of the reset code code.
func DigestOf(code string) Digest {
	return sha256.Sum256([]byte(code))
}

// String returns the digest in lowercase hexadecimal, the form that the
// stores keep.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// PasswordChange holds the options of a password write.
//
// Each password write adds 1 to the session generation of the person, in
// its own transaction, so that each console session of the person ends. A
// change that keeps the session of the change gets new cookies with the new
// generation from the service.
type PasswordChange struct {
	// ChangeRequired sets the flag that asks the person for a new password
	// at the next sign-in.
	ChangeRequired bool
}

// Store is the persistence port of the records of local users.
//
// Each write that changes the credentials of a person locks the row of the
// person principal in its transaction, before it reads the passwords and the
// passkeys of the person. So two removals at the same time cannot leave a
// person with no credential. The passkey store takes the same lock.
//
// Each write that stores a password hash for a person reads the break-glass
// flag of the principal in its own transaction, and returns ErrBreakGlass for
// a break-glass person. The service checks this too. The reverse holds in
// the principal store: a write that sets the break-glass flag on a person
// with a password returns principal.ErrBreakGlassPassword.
type Store interface {
	// CreateUser stores u, with no password, and appends e, in one
	// transaction. A first password comes only from a set-up code
	// (ResetStore.RedeemReset), and the demo password only from
	// PasswordStore.SeedDemoPassword. It returns ErrInvalid for a value that
	// fails a check of this package, ErrAlreadyExists when the user name or
	// the principal has a record, and principal.ErrNotFound when the
	// principal does not exist or is not a person.
	CreateUser(ctx context.Context, u User, e audit.Entry) error
	// UserByName returns the credential of the user whose stored user name
	// is name, or ErrNotFound. The hash is empty for a user with no
	// password. The session generation comes from the same read as the
	// hash, so a sign-in that verifies this hash and issues its session with
	// this generation never carries a generation that a later password
	// write or reset made: such a session ends at its next request.
	UserByName(ctx context.Context, name string) (Credential, error)
	// UserByPrincipal returns the user of the principal principalID, or
	// ErrNotFound.
	UserByPrincipal(ctx context.Context, principalID string) (User, error)
	// RenameUser sets the user name of the user userID to name and appends
	// e, in one transaction. The subject of the principal does not change.
	// It returns ErrInvalid for a name that does not match UserNamePattern,
	// ErrAlreadyExists when another user has the name, ErrNotFound for no
	// such user, and ErrDemoFixed for the demo person.
	RenameUser(ctx context.Context, userID, name string, e audit.Entry) error
	// DeleteUser deletes the record of the user userID and every reset code
	// of the user, adds 1 to the session generation of the person, and
	// appends e, in one transaction. The principal stays. The delete removes
	// the password too, so it returns ErrLastCredential as RemovePassword
	// does when the person keeps no other credential. It returns ErrNotFound
	// for no such user.
	DeleteUser(ctx context.Context, userID string, e audit.Entry) error
}

// PasswordStore is the persistence port of the passwords of local users. It
// takes the principal lock and the break-glass check that Store describes.
type PasswordStore interface {
	// SetPassword stores h as the password of the user userID, sets
	// PasswordSetAt to e.At, applies c, adds 1 to the session generation of
	// the person, and appends e, in one transaction. It returns ErrInvalid
	// for a hash that the hasher does not read, ErrNotFound for no such
	// user, ErrDemoFixed for the demo person, and ErrBreakGlass for a
	// break-glass person.
	SetPassword(ctx context.Context, userID string, h PasswordHash, c PasswordChange, e audit.Entry) error
	// SetChangeRequired sets or clears the flag that asks the person for a
	// new password at the next sign-in, and appends e, in one transaction.
	// It returns ErrNotFound for no such user or a user with no password,
	// and ErrDemoFixed for the demo person.
	SetChangeRequired(ctx context.Context, userID string, required bool, e audit.Entry) error
	// RehashPassword replaces the hash old of the user userID with h and
	// appends e, in one transaction, only when the stored hash is still
	// old. It returns ErrNotFound when the user has no such hash, so that a
	// rehash never overwrites a password that changed in the meantime. The
	// password stays the same, so the session generation stays.
	RehashPassword(ctx context.Context, userID string, old, h PasswordHash, e audit.Entry) error
	// RemovePassword deletes the password of the user userID and appends e,
	// in one transaction. It returns ErrLastCredential when the person has
	// no passkey, ErrNotFound for no such user, and ErrDemoFixed for the
	// demo person.
	RemovePassword(ctx context.Context, userID string, e audit.Entry) error
	// SeedDemoPassword stores h as the password of the demo person, adds 1
	// to its session generation, and appends e, in one transaction. It
	// makes the demo user record when it does not exist. The demo person is
	// the principal with the subject DemoSubject, which the seed makes
	// first: it returns principal.ErrNotFound when no such person exists. The store reads the installation kind from
	// its own
	// database in the same transaction, and returns ErrDemoOnly when the
	// kind is not dev or cannot be read. It takes no kind from the caller.
	// Only the seed of the dev server calls it.
	SeedDemoPassword(ctx context.Context, h PasswordHash, e audit.Entry) error
}

// ResetStore is the persistence port of reset codes.
type ResetStore interface {
	// CreateReset stores r, keyed by the digest d of its secret, deletes
	// every earlier reset code of the same user, and appends e, in one
	// transaction. For PurposeReset, the same transaction adds 1 to the
	// session generation of the person, so that each session ends, and
	// revokes each access token of the person when r.RevokeAccessTokens is
	// set. It returns ErrInvalid for a value
	// that fails a check, ErrNotFound for no such user, ErrAlreadyExists
	// when the digest is taken, ErrDemoFixed for the demo person, and
	// ErrBreakGlass for a break-glass person.
	CreateReset(ctx context.Context, r Reset, d Digest, e audit.Entry) error
	// RedeemReset uses the reset code with the digest r.Digest, in one
	// transaction: it checks that the code exists and did not expire at or
	// before r.At, deletes it, stores r.Hash as the password of its user,
	// applies r.Change, adds 1 to the session generation of the person, and
	// appends e. The service checks
	// the password rules before it calls, so a refused password never uses
	// up a code. It returns ErrNotFound when no code has the digest or when
	// the code expired, and deletes an expired code then. It returns
	// ErrBreakGlass for a break-glass person and changes nothing. Of two
	// calls at the same time with one code, one wins.
	RedeemReset(ctx context.Context, r Redeem, e audit.Entry) (Reset, error)
	// PruneResets deletes the reset codes that expired at or before at, and
	// returns how many it deleted.
	PruneResets(ctx context.Context, at time.Time) (int64, error)
}
