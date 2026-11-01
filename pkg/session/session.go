// Package session keeps the console sessions that OpenDustpan issues after a
// person signs in.
//
// A sign-in method only proves who a person is. The server then issues its
// own session, and the console sends it back in a cookie. A session belongs
// to one person principal of package principal. It ends at a sign-out, when
// an administrator ends it, after a period with no use (the idle limit), and
// at a maximum length. The idle limit and the maximum length are 15 minutes
// and 12 hours by default.
//
// The server shows the secret of a session once, in the cookie. The store
// keeps only the SHA-256 digest of the secret, so a read of the database
// gives no usable session. The CSRF value of a session comes from the
// secret (CSRFValue), so the store keeps no CSRF value either.
//
// A sign-in through the browser runs in two requests: the start, which
// sends the browser to the identity provider, and the callback. A Request
// carries the state of the flow between the two. A callback takes its
// request once.
//
// Each change of a session writes its audit entry in the same transaction
// as the change (fail closed): both commit, or neither.
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrNotFound reports a session or a sign-in request that does not
	// exist. A sign-in request that expired does not exist.
	ErrNotFound = errors.New("session not found")
	// ErrAlreadyExists reports a session ID, a session digest or a state
	// digest that exists.
	ErrAlreadyExists = errors.New("session already exists")
	// ErrInvalid reports a session or a sign-in request that fails
	// validation.
	ErrInvalid = errors.New("invalid session")
	// ErrEnded reports a change of a session that ended.
	ErrEnded = errors.New("session ended")
	// ErrPrincipalNotFound reports a principal that does not exist, that
	// was deleted, or that is not a person.
	ErrPrincipalNotFound = errors.New("principal not found")
	// ErrPrincipalDisabled reports a principal that the inactivity disable
	// ended. It cannot sign in until an administrator enables it.
	ErrPrincipalDisabled = errors.New("principal is disabled")
	// ErrGenerationChanged reports a session whose generation is not the
	// stored session generation of its principal: a password write, a reset
	// or another change ended every session of the principal after the
	// sign-in read the generation.
	ErrGenerationChanged = errors.New("the session generation of the principal changed")
)

// Default limits of a console session: 15 minutes with no use, and 12 hours
// in all. PCI DSS v4.0.1 requirement 8.2.8 asks for a new authentication
// after 15 idle minutes. NIST SP 800-63B-4 asks for a new authentication
// after at most 12 hours at AAL3.
const (
	DefaultIdle      = 15 * time.Minute
	DefaultMaxLength = 12 * time.Hour
)

// Limits are the idle limit and the maximum length of a session.
type Limits struct {
	Idle      time.Duration
	MaxLength time.Duration
}

// DefaultLimits returns the default limits.
func DefaultLimits() Limits {
	return Limits{Idle: DefaultIdle, MaxLength: DefaultMaxLength}
}

// EndReason names why a session ended before its limits.
type EndReason string

// Reasons of an end.
const (
	// ReasonSignOut is a sign-out by the person.
	ReasonSignOut EndReason = "sign-out"
	// ReasonEnded is an end by an administrator.
	ReasonEnded EndReason = "ended"
)

// Status names where a session is at one time.
type Status string

// Statuses of a session.
const (
	// StatusActive is a session that authenticates.
	StatusActive Status = "active"
	// StatusEnded is a session that a sign-out or an administrator ended.
	StatusEnded Status = "ended"
	// StatusIdle is a session with no use for longer than the idle limit.
	StatusIdle Status = "idle"
	// StatusExpired is a session older than the maximum length.
	StatusExpired Status = "expired"
)

// Session is the stored record of one session, without its secret. ID is
// stable and opaque, with the prefix "ses-". PrincipalID names the person.
// Provider names the sign-in method that made the session. AuthTime is the
// time of the authentication at the identity provider: it can be before
// CreatedAt. LastSeenAt is the time of the last recorded use. EndedAt is
// zero for a session that no sign-out and no administrator ended, and
// EndReason is empty then. SourceIP and UserAgent describe the browser at
// the sign-in, for a person who reviews the sessions. Generation is the
// session generation of the principal that the sign-in read: the session
// authenticates only while it is the stored generation. A new session with
// the generation 0 takes the stored generation in the write that stores it.
type Session struct {
	CreatedAt   time.Time
	LastSeenAt  time.Time
	AuthTime    time.Time
	EndedAt     time.Time
	ID          string
	PrincipalID string
	Provider    string
	EndReason   EndReason
	SourceIP    string
	UserAgent   string
	Generation  int64
}

// ActiveBounds are the times after which the last use and the creation of
// a session must be for the session to be active. A zero time is no bound.
type ActiveBounds struct {
	// LastSeen: a session whose last use is at or before it is idle.
	LastSeen time.Time
	// Created: a session whose creation is at or before it is expired.
	Created time.Time
}

// ActiveAfter returns the bounds of an active session for a use at the
// time when, under l. A limit of zero does not apply and gives no bound.
func (l Limits) ActiveAfter(when time.Time) ActiveBounds {
	var b ActiveBounds

	if l.Idle > 0 {
		b.LastSeen = when.Add(-l.Idle)
	}

	if l.MaxLength > 0 {
		b.Created = when.Add(-l.MaxLength)
	}

	return b
}

// StatusAt returns the status of s at the time now under the limits l. A
// limit of zero does not apply.
func (s Session) StatusAt(now time.Time, l Limits) Status {
	switch {
	case !s.EndedAt.IsZero():
		return StatusEnded
	case l.MaxLength > 0 && !now.Before(s.CreatedAt.Add(l.MaxLength)):
		return StatusExpired
	case l.Idle > 0 && !now.Before(s.LastSeenAt.Add(l.Idle)):
		return StatusIdle
	default:
		return StatusActive
	}
}

// Owner is the principal of a session, as a lookup answers it. DisabledAt is
// zero for a principal that the inactivity disable did not end. ActiveAt is
// the latest activity of the principal by the account rule of the
// inactivity disable: the latest of its creation, its activity baseline,
// its last sign-in, the creation and the last use of each of its access
// tokens, and the last use of each of its sessions. BreakGlass marks a
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

// Match is the answer of a lookup by digest: the session, its owner, the
// digest that the store keeps, and the policies of the installation at the
// time of the lookup. The policies come from the same read as the session,
// so a change applies to the next lookup on every instance. Generation is
// the stored session generation of the owner, from the same read.
type Match struct {
	Session    Session
	Owner      Owner
	Digest     Digest
	Policy     Policy
	Accounts   AccountPolicy
	Generation int64
}

// AccountPolicy is the part of the access token policy that a console
// session applies too: the account disable and its inactivity period.
type AccountPolicy struct {
	Inactivity      time.Duration
	DisableAccounts bool
}

// Request is the state of one sign-in between its start and its callback.
// Provider names the sign-in method. Nonce goes into the ID token, and
// Verifier is the PKCE code verifier (RFC 7636): both are secret until the
// callback. ReturnPath is where the browser goes after the sign-in: a path
// of this server. MaxAge is the max_age that the start sent to the provider,
// when HasMaxAge is true: the callback refuses an authentication older
// than it. A request expires at ExpiresAt. A stored request works once. A
// request in a sealed sign-in request cookie works until ExpiresAt, and the
// identity provider gives each code once.
type Request struct {
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Provider   string
	Nonce      string
	Verifier   string
	ReturnPath string
	MaxAge     time.Duration
	HasMaxAge  bool
}

// RequestLifetime is how long a sign-in can take from its start to its
// callback.
const RequestLifetime = 10 * time.Minute

// Limits of the text fields, in bytes, and of the IDs.
const (
	maxIDLength         = 64
	maxProviderLength   = 64
	maxSourceIPLength   = 64
	maxUserAgentLength  = 512
	maxReturnPathLength = 2048
	minVerifierLength   = 43
	maxVerifierLength   = 128
	minNonceLength      = 16
	maxNonceLength      = 128
)

// idPrefix starts each session ID. An ID is opaque: never parse it.
const idPrefix = "ses-"

// NewID returns a new session ID.
func NewID() string {
	return ident.NewID(idPrefix)
}

// Validate checks the fields of s that a store keeps.
func (s Session) Validate() error {
	if err := s.validateNames(); err != nil {
		return err
	}

	if err := s.validateTimes(); err != nil {
		return err
	}

	return s.validateBrowser()
}

// validateNames checks the ID, the principal and the provider of s.
func (s Session) validateNames() error {
	switch {
	case !strings.HasPrefix(s.ID, idPrefix) || !ident.Valid(s.ID, maxIDLength):
		return fmt.Errorf("%w: ID %q", ErrInvalid, s.ID)
	case s.PrincipalID == "":
		return fmt.Errorf("%w: no principal", ErrInvalid)
	case !ident.Valid(s.Provider, maxProviderLength):
		return fmt.Errorf("%w: provider %q", ErrInvalid, s.Provider)
	case s.Generation < 0:
		return fmt.Errorf("%w: generation %d", ErrInvalid, s.Generation)
	default:
		return nil
	}
}

// validateTimes checks that s is a new session: it has a creation time and
// an authentication time no later, its last use is its creation, and it
// has not ended.
func (s Session) validateTimes() error {
	switch {
	case s.CreatedAt.IsZero() || s.AuthTime.IsZero():
		return fmt.Errorf("%w: no creation time or authentication time", ErrInvalid)
	case s.AuthTime.After(s.CreatedAt):
		return fmt.Errorf("%w: the authentication time is after the creation time", ErrInvalid)
	case !s.LastSeenAt.Equal(s.CreatedAt):
		return fmt.Errorf("%w: a new session has its creation time as the last use", ErrInvalid)
	case !s.EndedAt.IsZero() || s.EndReason != "":
		return fmt.Errorf("%w: a new session has not ended", ErrInvalid)
	default:
		return nil
	}
}

// validateBrowser checks the source IP and the user agent of s.
func (s Session) validateBrowser() error {
	switch {
	case len(s.SourceIP) > maxSourceIPLength || !utf8.ValidString(s.SourceIP):
		return fmt.Errorf("%w: source IP is not valid UTF-8 of at most %d bytes", ErrInvalid, maxSourceIPLength)
	case len(s.UserAgent) > maxUserAgentLength || !utf8.ValidString(s.UserAgent):
		return fmt.Errorf("%w: user agent is not valid UTF-8 of at most %d bytes", ErrInvalid, maxUserAgentLength)
	default:
		return nil
	}
}

// ValidEndReason returns ErrInvalid when r is not a reason of an end.
func ValidEndReason(r EndReason) error {
	switch r {
	case ReasonSignOut, ReasonEnded:
		return nil
	default:
		return fmt.Errorf("%w: end reason %q", ErrInvalid, r)
	}
}

// Validate checks the fields of r that a store keeps.
func (r Request) Validate() error {
	switch {
	case !ident.Valid(r.Provider, maxProviderLength):
		return fmt.Errorf("%w: provider %q", ErrInvalid, r.Provider)
	case !unreserved(r.Nonce, minNonceLength, maxNonceLength):
		return fmt.Errorf("%w: the nonce is not %d to %d unreserved characters", ErrInvalid,
			minNonceLength, maxNonceLength)
	case !unreserved(r.Verifier, minVerifierLength, maxVerifierLength):
		return fmt.Errorf("%w: the PKCE verifier is not %d to %d unreserved characters (RFC 7636)", ErrInvalid,
			minVerifierLength, maxVerifierLength)
	case !ValidReturnPath(r.ReturnPath):
		return fmt.Errorf("%w: return path %q is not a path of this server", ErrInvalid, r.ReturnPath)
	case r.CreatedAt.IsZero() || !r.ExpiresAt.After(r.CreatedAt):
		return fmt.Errorf("%w: the expiry is not after the creation time", ErrInvalid)
	case r.MaxAge < 0 || r.MaxAge%time.Second != 0 || (!r.HasMaxAge && r.MaxAge != 0):
		return fmt.Errorf("%w: max_age %s is not a count of whole seconds that the request sent", ErrInvalid,
			r.MaxAge)
	default:
		return nil
	}
}

// unreserved reports whether v holds minLen to maxLen of the unreserved
// characters of RFC 3986, the alphabet of a PKCE code verifier (RFC 7636
// section 4.1). Unpadded base64url uses only these characters.
func unreserved(v string, minLen, maxLen int) bool {
	if len(v) < minLen || len(v) > maxLen {
		return false
	}

	for _, c := range []byte(v) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}

	return true
}

// ValidReturnPath reports whether p is a path of this server that a sign-in
// can send the browser to: it starts with one slash, it is not a
// protocol-relative URL, and it holds no backslash and no control
// character. So a callback can never send the browser to another host.
func ValidReturnPath(p string) bool {
	if len(p) > maxReturnPathLength || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}

	for _, c := range []byte(p) {
		if c == '\\' || c < 0x20 || c == 0x7f {
			return false
		}
	}

	return true
}

// SecretPrefix starts each session secret, so that secret scanners can find
// one in logs and chat. The rest is 43 characters of unpadded base64url: 256
// bits from crypto/rand.
const SecretPrefix = "odps_"

// Secret sizes.
const (
	secretEntropyBytes = 32
	secretEncodedLen   = 43
	// SecretLength is the length of a secret in bytes.
	SecretLength = len(SecretPrefix) + secretEncodedLen
)

// redacted replaces a secret in every formatted output.
const redacted = "[session secret]"

// Secret is the plain value of a session or of the state of a sign-in
// request. Its String and GoString methods redact it, so it cannot leak
// through a log or an error by accident. Use Reveal only to send it to the
// browser.
type Secret struct {
	value string
}

// NewSecret returns a secret with 256 bits from crypto/rand.
func NewSecret() (Secret, error) {
	b := make([]byte, secretEntropyBytes)
	if _, err := rand.Read(b); err != nil {
		return Secret{}, fmt.Errorf("generate session secret: %w", err)
	}

	return Secret{value: SecretPrefix + base64.RawURLEncoding.EncodeToString(b)}, nil
}

// ParseSecret returns the secret that a browser sent, or ErrInvalid when raw
// does not have the form of a secret. A secret of the right form can still
// match no session.
func ParseSecret(raw string) (Secret, error) {
	rest, ok := strings.CutPrefix(raw, SecretPrefix)
	if !ok || len(rest) != secretEncodedLen {
		return Secret{}, fmt.Errorf("%w: not a session secret", ErrInvalid)
	}

	if _, err := base64.RawURLEncoding.DecodeString(rest); err != nil {
		return Secret{}, fmt.Errorf("%w: not a session secret", ErrInvalid)
	}

	return Secret{value: raw}, nil
}

// Reveal returns the plain value of s, to send it to the browser.
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

// csrfLabel separates the CSRF value from any other value that the server
// could derive from a secret.
const csrfLabel = "opendustpan session csrf v1"

// CSRFValue returns the CSRF value of the session with the secret s: the
// HMAC-SHA256 of a fixed label, keyed with the secret, in unpadded
// base64url. A page that can read the session through the API gets it, and
// sends it back in a header with each change. Another site cannot compute
// it, because the cookie is HttpOnly and the browser sends it only with the
// requests of this server.
func CSRFValue(s Secret) string {
	mac := hmac.New(sha256.New, []byte(s.value))
	mac.Write([]byte(csrfLabel))

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CheckCSRF reports whether value is the CSRF value of the session with the
// secret s. It compares in constant time.
func CheckCSRF(s Secret, value string) bool {
	return hmac.Equal([]byte(CSRFValue(s)), []byte(value))
}

// Digest is what a store keeps in place of a secret: the SHA-256 of the
// secret.
type Digest [sha256.Size]byte

// DigestOf returns the digest of the secret s.
func DigestOf(s Secret) Digest {
	return sha256.Sum256([]byte(s.value))
}

// String returns the digest in lower-case hex, the form a store keeps.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ParseDigest parses a digest in the form that a store keeps.
func ParseDigest(s string) (Digest, error) {
	var d Digest

	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(d) || hex.EncodeToString(b) != s {
		return Digest{}, fmt.Errorf("%w: digest %q is not %d bytes of lower-case hex", ErrInvalid, s, len(d))
	}

	copy(d[:], b)

	return d, nil
}

// invalidRef stands for an ID that is not a session ID in an audit object.
const invalidRef = "invalid"

// Ref returns the audit object of the session id: "session:<id>". An id
// that is not a session ID gives "session:invalid", so request text never
// reaches the audit log.
func Ref(id string) string {
	if strings.HasPrefix(id, idPrefix) && ident.Valid(id, maxIDLength) {
		return "session:" + id
	}

	return "session:" + invalidRef
}

// FullRef returns the audit object of the session id of the principal
// principalID: "principal:<principal>#session:<id>", so a reader of the
// audit log finds the sessions of one person.
func FullRef(principalID, id string) string {
	return principal.Ref(principalID) + "#" + Ref(id)
}
