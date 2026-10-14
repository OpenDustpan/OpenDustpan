// Package accesstoken keeps the access tokens that OpenDustpan issues to its
// principals (ADR 6, ADR 53).
//
// An access token belongs to one principal of package principal. A token of
// a person is a personal token. A token of a service principal is a service
// token (ADR 53, decision 7). A workload proves its identity with a JWT or a
// client certificate (ADR 54), so it gets no access token here.
//
// The server shows the secret of a token once, when it creates the token.
// The store keeps only the SHA-256 digest of the secret, so a read of the
// database gives no usable token. Each token has an expiry and records the
// time of its last use (ADR 53, decision 1). A revoke ends a token at once.
// A rotation issues a new token and sets an end time for the old one
// (decision 8). Nothing is erased: a revoked or expired token keeps its row
// for the history.
//
// Each change writes its audit entry in the same transaction as the change
// (fail closed): both commit, or neither. The Service is also an
// identity.Authenticator: it resolves a secret to its principal and refuses
// a token that expired, that was revoked, or whose principal was deleted.
package accesstoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a token that does not exist.
	ErrNotFound = errors.New("access token not found")
	// ErrAlreadyExists reports a token ID or a digest that exists.
	ErrAlreadyExists = errors.New("access token already exists")
	// ErrInvalid reports a token, a lifetime or a page that fails
	// validation.
	ErrInvalid = errors.New("invalid access token")
	// ErrEnded reports a change of a token that was revoked, that expired,
	// or that a rotation replaced.
	ErrEnded = errors.New("access token ended")
	// ErrPrincipalNotFound reports a principal that does not exist or was
	// deleted.
	ErrPrincipalNotFound = errors.New("principal not found")
	// ErrNoTokenForKind reports a principal of a kind that gets no access
	// token: a workload proves its identity in another way (ADR 54).
	ErrNoTokenForKind = errors.New("principal kind gets no access token")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Kind names what a token is for.
type Kind string

// Kinds of access token (ADR 53, decisions 7 and 10).
const (
	// KindPersonal is a token of a person.
	KindPersonal Kind = "personal"
	// KindService is a token of a service principal, for a machine that
	// cannot sign in to the console.
	KindService Kind = "service"
)

// KindFor returns the kind of the token of a principal of kind k, or
// ErrNoTokenForKind.
func KindFor(k principal.Kind) (Kind, error) {
	switch k {
	case principal.KindPerson:
		return KindPersonal, nil
	case principal.KindService:
		return KindService, nil
	case principal.KindWorkload:
		return "", fmt.Errorf("%w: a workload uses a trust rule (ADR 54)", ErrNoTokenForKind)
	default:
		return "", fmt.Errorf("%w: unknown kind %q", ErrNoTokenForKind, k)
	}
}

// Status names where a token is in its life cycle at one time.
type Status string

// Statuses of a token.
const (
	// StatusActive is a token that authenticates.
	StatusActive Status = "active"
	// StatusExpired is a token whose expiry passed.
	StatusExpired Status = "expired"
	// StatusRevoked is a token that a revoke ended.
	StatusRevoked Status = "revoked"
)

// Token is the stored record of one access token, without its secret. ID is
// stable and opaque, with the prefix "tok-". PrincipalID names the owner.
// Name is a label for people to read. ExpiresAt is the end of the token: a
// rotation moves it to the end time of the old token. LastUsedAt is zero
// when no request used the token. RevokedAt is zero for a token that was not
// revoked. ReplacedBy names the token that a rotation issued for this one.
type Token struct {
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  time.Time
	RevokedAt   time.Time
	ID          string
	PrincipalID string
	Name        string
	Kind        Kind
	ReplacedBy  string
}

// StatusAt returns the status of t at the time now.
func (t Token) StatusAt(now time.Time) Status {
	switch {
	case !t.RevokedAt.IsZero():
		return StatusRevoked
	case !now.Before(t.ExpiresAt):
		return StatusExpired
	default:
		return StatusActive
	}
}

// Owner is the principal of a token, as the Authenticator answers it.
type Owner struct {
	ID          string
	Subject     string
	DisplayName string
	Kind        principal.Kind
}

// Limits of the text fields, in bytes, and of the IDs.
const (
	maxNameLength = 100
	maxIDLength   = 64
)

// idPrefix starts each token ID. An ID is opaque: never parse it.
const idPrefix = "tok-"

// SecretPrefix starts each secret, so that secret scanners can find a
// token in code, logs and chat. The rest is 43 characters of unpadded
// base64url: 256 bits from crypto/rand.
const SecretPrefix = "odp_"

// Secret sizes.
const (
	secretEntropyBytes = 32
	secretEncodedLen   = 43
	// SecretLength is the length of a secret in bytes.
	SecretLength = len(SecretPrefix) + secretEncodedLen
)

// redacted replaces a secret in every formatted output.
const redacted = "[access token]"

// Secret is the plain value of an access token. Its String and GoString
// methods redact it, so it cannot leak through a log or an error by
// accident. Use Reveal only to send the secret to its owner.
type Secret struct {
	value string
}

// newSecret returns a secret with 256 bits from crypto/rand.
func newSecret() (Secret, error) {
	b := make([]byte, secretEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return Secret{}, fmt.Errorf("generate access token: %w", err)
	}

	return Secret{value: SecretPrefix + base64.RawURLEncoding.EncodeToString(b)}, nil
}

// Reveal returns the plain value of s, to show it once to its owner.
func (s Secret) Reveal() string {
	return s.value
}

// String redacts the secret.
func (Secret) String() string {
	return redacted
}

// GoString redacts the secret in %#v output too.
func (Secret) GoString() string {
	return redacted
}

// Digest is what a store keeps in place of a secret: the SHA-256 of the
// secret.
type Digest [sha256.Size]byte

// DigestOf returns the digest of the secret s.
func DigestOf(s Secret) Digest {
	return sha256.Sum256([]byte(s.value))
}

// digestOfRaw returns the digest of a secret as a client sent it.
func digestOfRaw(raw string) Digest {
	return sha256.Sum256([]byte(raw))
}

// String returns the digest in lower-case hex, the form a store keeps.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ParseDigest reads the hex form of String.
func ParseDigest(s string) (Digest, error) {
	var d Digest

	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != len(d) {
		return Digest{}, fmt.Errorf("%w: digest", ErrInvalid)
	}

	copy(d[:], raw)

	return d, nil
}

// Equal reports, in constant time, whether d and other are the same digest.
func (d Digest) Equal(other Digest) bool {
	return subtle.ConstantTimeCompare(d[:], other[:]) == 1
}

// LooksLikeSecret reports whether raw has the form of a secret: the prefix
// and 43 base64url characters. A check of the form needs no store read, so
// a bootstrap admin token or a random value never reaches the store.
func LooksLikeSecret(raw string) bool {
	rest, ok := strings.CutPrefix(raw, SecretPrefix)
	if !ok || len(rest) != secretEncodedLen {
		return false
	}

	_, err := base64.RawURLEncoding.DecodeString(rest)

	return err == nil
}

// ValidateName reports whether s is a usable token name: 1 to 100 bytes of
// UTF-8 with no control or format character and no white space at the
// start or the end.
func ValidateName(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%w: missing name", ErrInvalid)
	case len(s) > maxNameLength:
		return fmt.Errorf("%w: name longer than %d bytes", ErrInvalid, maxNameLength)
	case !utf8.ValidString(s):
		return fmt.Errorf("%w: name is not UTF-8", ErrInvalid)
	case strings.TrimSpace(s) != s:
		return fmt.Errorf("%w: name starts or ends with white space", ErrInvalid)
	}

	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return fmt.Errorf("%w: name holds the control or format character %U", ErrInvalid, c)
		}
	}

	return nil
}

// invalidRef names a malformed ID in an audit object.
const invalidRef = "invalid"

// Ref returns the audit object of the token id: "token:<id>". An id that is
// not a token ID gives "token:invalid", so request text never reaches the
// audit log.
func Ref(id string) string {
	if strings.HasPrefix(id, idPrefix) && ident.Valid(id, maxIDLength) {
		return "token:" + id
	}

	return "token:" + invalidRef
}
