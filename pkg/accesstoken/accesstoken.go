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
	// KindTrust is a token that a workload got in a token exchange through
	// a trust rule. It belongs to a service principal, lives one exchange
	// lifetime, and no rotation renews it.
	KindTrust Kind = "trust"
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
	// StatusDisabled is a token that the inactivity disable ended (ADR 53,
	// decision 4).
	StatusDisabled Status = "disabled"
)

// Token is the stored record of one access token, without its secret. ID is
// stable and opaque, with the prefix "tok-". PrincipalID names the owner.
// Name is a label for people to read. ExpiresAt is the end of the token: a
// rotation moves it to the end time of the old token. LastUsedAt is zero
// when no request used the token. RevokedAt is zero for a token that was not
// revoked. DisabledAt is zero for a token that the inactivity disable did
// not end. ReplacedBy names the token that a rotation issued for this one.
// CreatedBy is the subject of the caller that made the token: by a create,
// or by the rotation that issued it.
type Token struct {
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  time.Time
	RevokedAt   time.Time
	DisabledAt  time.Time
	ID          string
	PrincipalID string
	Name        string
	Kind        Kind
	ReplacedBy  string
	CreatedBy   string
}

// StatusAt returns the status of t at the time now.
func (t Token) StatusAt(now time.Time) Status {
	switch {
	case !t.RevokedAt.IsZero():
		return StatusRevoked
	case !t.DisabledAt.IsZero():
		return StatusDisabled
	case !now.Before(t.ExpiresAt):
		return StatusExpired
	default:
		return StatusActive
	}
}

// day is one day, for the days left of a token.
const day = 24 * time.Hour

// ExpiringWithin is the period before its expiry in which an active token
// counts as expiring: the first expiry warning of the RFC 20 lead proposal
// (ADR 53, decision 11).
const ExpiringWithin = 14 * 24 * time.Hour

// Filter keeps the tokens of a list by their status at the time of the
// list.
type Filter string

// Filters of a token list.
const (
	// FilterAll keeps every token.
	FilterAll Filter = ""
	// FilterActive keeps the tokens that authenticate.
	FilterActive Filter = "active"
	// FilterExpiring keeps the active tokens that expire within
	// ExpiringWithin.
	FilterExpiring Filter = "expiring"
	// FilterEnded keeps the tokens that were revoked or that expired.
	FilterEnded Filter = "ended"
)

// ParseFilter returns the Filter that s names, or ErrInvalid.
func ParseFilter(s string) (Filter, error) {
	switch f := Filter(s); f {
	case FilterAll, FilterActive, FilterExpiring, FilterEnded:
		return f, nil
	default:
		return "", fmt.Errorf("%w: status %q: use active, expiring or ended", ErrInvalid, s)
	}
}

// DaysUntil answers the number of started days from now until expiresAt:
// the remaining days rounded up, at least 1 while the expiry is ahead and
// 0 once it passed. The CLI warning calls it, so the server and the CLI
// cannot disagree about the count.
func DaysUntil(expiresAt, now time.Time) int64 {
	d := expiresAt.Sub(now)
	if d <= 0 {
		return 0
	}

	return int64((d + day - 1) / day)
}

// DaysLeft returns the started days left until the expiry of t at the time
// now, at least 1 for an active token, or false when t is not active. The
// server answers it, so a client never needs its own clock to warn.
func (t Token) DaysLeft(now time.Time) (int64, bool) {
	if t.StatusAt(now) != StatusActive {
		return 0, false
	}

	return DaysUntil(t.ExpiresAt, now), true
}

// Owner is the principal of a token, as the Authenticator answers it.
// DisabledAt is zero for a principal that the inactivity disable did not
// end. ActiveAt is the latest activity of the principal by the account
// rule of the sweep: the latest of its creation, its activity baseline, its
// last sign-in, the creation and the last use of each of its tokens, and the
// last use of each of its console sessions. BreakGlass marks a
// break-glass account, which is always exempt from the inactivity disable.
type Owner struct {
	ActiveAt    time.Time
	DisabledAt  time.Time
	ID          string
	Subject     string
	DisplayName string
	Kind        principal.Kind
	BreakGlass  bool
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

// PolicyRef is the audit object of the token life cycle policy.
const PolicyRef = "token-policy"

// invalidRef names a malformed ID in an audit object.
const invalidRef = "invalid"

// TokenRef returns the audit object of a change of the token tokenID of the
// principal principalID: "principal:<id>#token:<id>", so the entry names the
// principal that the token acts as.
func TokenRef(principalID, tokenID string) string {
	return principal.Ref(principalID) + "#" + Ref(tokenID)
}

// Ref returns the audit object of the token id: "token:<id>". An id that is
// not a token ID gives "token:invalid", so request text never reaches the
// audit log.
func Ref(id string) string {
	if strings.HasPrefix(id, idPrefix) && ident.Valid(id, maxIDLength) {
		return "token:" + id
	}

	return "token:" + invalidRef
}

// Expiry warnings (ADR 53, decision 11). RFC 20 keeps the thresholds open:
// these follow the lead proposal of warnings 14 days and 3 days before the
// expiry.
const (
	// ExpiryWarningSoon is how long before its expiry a token warns soon.
	ExpiryWarningSoon = ExpiringWithin
	// ExpiryWarningImminent is how long before its expiry a token warns
	// imminent.
	ExpiryWarningImminent = 3 * 24 * time.Hour
	// ExpiryHeader is the response header that names the expiry of the
	// token of the caller, in RFC 3339, when the token warns. The server
	// sets it on each answer to such a token, and the opendustpan CLI
	// prints a warning when it reads it.
	ExpiryHeader = "Dustpan-Token-Expires-At"
)

// ExpiryWarning names how near the expiry of a token is.
type ExpiryWarning string

// Expiry warnings of a token.
const (
	// WarningNone is a token more than ExpiryWarningSoon before its expiry,
	// or a token that ended.
	WarningNone ExpiryWarning = "none"
	// WarningSoon is a token at most ExpiryWarningSoon before its expiry.
	WarningSoon ExpiryWarning = "soon"
	// WarningImminent is a token at most ExpiryWarningImminent before its
	// expiry.
	WarningImminent ExpiryWarning = "imminent"
)

// WarningFor returns the warning of a token that expires at expiresAt, at
// the time now. A token that expired has no warning: it no longer works.
func WarningFor(expiresAt, now time.Time) ExpiryWarning {
	left := expiresAt.Sub(now)

	switch {
	case left <= 0:
		return WarningNone
	case left <= ExpiryWarningImminent:
		return WarningImminent
	case left <= ExpiryWarningSoon:
		return WarningSoon
	default:
		return WarningNone
	}
}

// ExpiryWarningAt returns the warning of t at the time now: WarningNone for
// a token that is not active.
func (t Token) ExpiryWarningAt(now time.Time) ExpiryWarning {
	if t.StatusAt(now) != StatusActive {
		return WarningNone
	}

	return WarningFor(t.ExpiresAt, now)
}
