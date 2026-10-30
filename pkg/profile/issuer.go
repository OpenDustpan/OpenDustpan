package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The errors of the issuer of a namespace. A port wraps them with
// fmt.Errorf("...: %w", err).
var (
	// ErrIssuerNotConfigured reports a token, or a document of the issuer,
	// for a server that has neither the setting issuerBaseURL nor the
	// setting publicURL. The message names the two settings.
	ErrIssuerNotConfigured = errors.New(
		"the server has no issuer host: set issuerBaseURL or publicURL",
	)
	// ErrIssuerKeyMissing reports a token of a namespace with issuerURL whose
	// signing key is in no copied JWKS yet. Also a namespace with no key, a
	// manual rotation whose next key the copy check did not find, and an
	// export of a namespace with no key. The message names the route that
	// makes the keys or the route of the copy check.
	ErrIssuerKeyMissing = errors.New("the issuer has no signing key that a copied JWKS holds")
	// ErrIssuerKeyExpired reports a token of a namespace with issuerURL
	// whose active key signed for 180 days. The message names the export
	// route and the route of the copy check.
	ErrIssuerKeyExpired = errors.New("the active signing key of the issuer signed for 180 days")
)

// ErrSubjectTooLong reports a subject above MaxSubjectBytes.
var ErrSubjectTooLong = errors.New("the subject of the token is longer than 600 bytes")

// Limits and times of the issuer. A namespace with an issuerURL has the same
// rotation, with the copy check before each promotion.
const (
	// KeyBits is the size of a signing key.
	KeyBits = 2048
	// KeyAlgorithm is the only algorithm of the issuer. A relying party
	// that needs another algorithm gets no token.
	KeyAlgorithm = "RS256"
	// ActiveKeyLifetime is how long an active key signs.
	ActiveKeyLifetime = 90 * 24 * time.Hour
	// NextKeyLead is how long before the end of the active key the schedule
	// makes the next key.
	NextKeyLead = 7 * 24 * time.Hour
	// RetiredKeyKeep is how long a retired key stays in the JWKS.
	RetiredKeyKeep = 7 * 24 * time.Hour
	// JWKSMaxAge is the Cache-Control max-age of the discovery document and of
	// the JWKS. A verifier that caches the JWKS may not know a new key for
	// this long.
	JWKSMaxAge = 5 * time.Minute
	// MinNextKeyAge is how long a next key stays in the JWKS before a normal
	// rotation makes it active. It does not depend on JWKSMaxAge: some
	// verifiers keep a JWKS for up to 60 minutes, whatever the max-age header
	// says, so the wait is 2 hours. An emergency rotation does not wait.
	MinNextKeyAge = 2 * time.Hour
	// MaxActiveSigning is the longest time that an active key signs for a
	// namespace with an issuerURL, when the copy check fails.
	MaxActiveSigning = 180 * 24 * time.Hour
	// MaxRetiredKeys is the most retired keys in the JWKS.
	MaxRetiredKeys = 2
	// MaxPublishedKeys is the most keys in the JWKS: one active, one next
	// and the retired keys.
	MaxPublishedKeys = 2 + MaxRetiredKeys
	// MinTokenLifetime and MaxTokenLifetime bound exp - iat.
	MinTokenLifetime = time.Minute
	MaxTokenLifetime = 60 * time.Minute
	// DefaultTokenLifetime is the lifetime of a token that the server uses at
	// once in an exchange.
	DefaultTokenLifetime = 5 * time.Minute
	// FileTokenLifetime is the lifetime of a token in a file for a tool.
	FileTokenLifetime = 15 * time.Minute
	// MaxAudienceBytes is the longest audience.
	MaxAudienceBytes = 255
	// MaxSubjectBytes is the longest sub claim.
	MaxSubjectBytes = 600
	// MaxIssuerURLBytes is the longest issuerURL.
	MaxIssuerURLBytes = 2048
	// MaxEmergencyReason is the longest reason of an emergency rotation, in
	// characters.
	MaxEmergencyReason = audit.MaxReasonLen
)

// KeyState is the state of a signing key.
type KeyState string

// The states of a key. Only active signs. A key of the states next, active
// and retired is in the JWKS.
const (
	KeyNext    KeyState = "next"
	KeyActive  KeyState = "active"
	KeyRetired KeyState = "retired"
	KeyDeleted KeyState = "deleted"
)

// Valid reports whether s is a state.
func (s KeyState) Valid() bool {
	switch s {
	case KeyNext, KeyActive, KeyRetired, KeyDeleted:
		return true
	default:
		return false
	}
}

// Published reports whether a key of the state is in the JWKS.
func (s KeyState) Published() bool {
	return s == KeyNext || s == KeyActive || s == KeyRetired
}

// HoldsPrivate reports whether a key of the state keeps its sealed private
// key. A retired or deleted key never signs again, so the store drops it.
func (s KeyState) HoldsPrivate() bool {
	return s == KeyNext || s == KeyActive
}

const keyIDPrefix = "oik-"

// KeyID is the kid of a key: the prefix "oik-" and 20 random characters.
type KeyID string

// NewKeyID returns a new key ID.
func NewKeyID() KeyID {
	return KeyID(ident.NewID(keyIDPrefix))
}

// ParseKeyID validates s as a key ID.
func ParseKeyID(s string) (KeyID, error) {
	if !strings.HasPrefix(s, keyIDPrefix) || len(s) != len(keyIDPrefix)+20 || !ident.Valid(s, len(s)) {
		return "", fmt.Errorf("%w: key ID %s", ErrInvalid, ident.Echo(s))
	}

	return KeyID(s), nil
}

// Key is a signing key of the issuer of one namespace.
//
// Public is the PKIX DER of the public key. Sealed is the PKCS #8 DER of the
// private key, sealed under the data key of the namespace scope "issuer". A
// key of the state retired or deleted has no Sealed. No route answers
// Sealed.
type Key struct {
	CreatedAt   time.Time
	ActivatedAt time.Time
	RetiredAt   time.Time
	DeletedAt   time.Time
	ID          KeyID
	Namespace   namespace.Name
	State       KeyState
	Public      []byte
	Sealed      []byte
}

// KeyInfo is the metadata of a key for an answer. It holds no key material.
type KeyInfo struct {
	CreatedAt   time.Time
	ActivatedAt time.Time
	RetiredAt   time.Time
	DeletedAt   time.Time
	// ExpiresAt is when the active key stops signing by the schedule, and
	// zero for another state.
	ExpiresAt time.Time
	ID        KeyID
	State     KeyState
	Algorithm string
}

// Info returns the metadata of k.
func (k Key) Info() KeyInfo {
	info := KeyInfo{
		CreatedAt: k.CreatedAt, ActivatedAt: k.ActivatedAt, RetiredAt: k.RetiredAt, DeletedAt: k.DeletedAt,
		ExpiresAt: time.Time{}, ID: k.ID, State: k.State, Algorithm: KeyAlgorithm,
	}

	if k.State == KeyActive {
		info.ExpiresAt = k.ActivatedAt.Add(ActiveKeyLifetime)
	}

	return info
}

// KeyMove changes the state of one key. The store applies it only when the
// key is in the state From.
type KeyMove struct {
	At   time.Time
	ID   KeyID
	From KeyState
	To   KeyState
}

// KeyChange is one atomic change of the keys of a namespace: the new keys,
// and the moves of the keys that exist.
//
// Initial says that the namespace must have no key that is not deleted, as
// the first make of keys needs.
type KeyChange struct {
	Add     []Key
	Move    []KeyMove
	Initial bool
}

// Keys holds the signing keys of the issuer of each namespace. The store
// keeps no private key in plain text: Key.Sealed is a sealed value.
//
// Apply takes the fence of the namespace and the audit entries of the
// change. The store checks the fence and appends each entry in the transaction of
// the change: all commit, or none. It returns namespace.ErrFenced for a stale
// fence, namespace.ErrNotFound for an unknown namespace, audit.ErrInvalidEntry
// for an entry that fails validation, and changes nothing on an error.
type Keys interface {
	// Published reads the keys of the states next, active and retired, with
	// no Sealed. An unknown namespace reads as no key.
	Published(ctx context.Context, ns namespace.Name) ([]Key, error)
	// Live reads the keys of the states next and active, with Sealed, and the
	// retired keys. A namespace with no key reads as an empty slice.
	Live(ctx context.Context, ns namespace.Name) ([]Key, error)
	// History reads the metadata of every key of the namespace, also the
	// deleted keys, oldest first. The slice holds no key material.
	History(ctx context.Context, ns namespace.Name) ([]KeyInfo, error)
	// Apply makes ch. ErrConflict when a key is not in the state of its
	// move, when Initial is set and the namespace has a key, or when the
	// result has a second active key, a second next key, more than
	// MaxRetiredKeys retired keys, a key of the state next or active with no
	// Sealed, or a key of another state with a Sealed. ErrInvalid for a key
	// that fails CheckKey or a move that fails CheckMove.
	Apply(ctx context.Context, ns namespace.Name, ch KeyChange, f namespace.Fence, e []audit.Entry) error
}

// NamespaceResolver finds a namespace by its stable ID. The public routes
// of the issuer name a namespace by ID.
type NamespaceResolver interface {
	// NamespaceByID reads the namespace with the stable ID id, or
	// namespace.ErrNotFound.
	NamespaceByID(ctx context.Context, id namespace.ID) (namespace.Namespace, error)
}

// IssuerStore is what an adapter of the store gives the issuer.
type IssuerStore interface {
	Keys
	IssuerSettingsStore
	NamespaceResolver
}

// OpenContext holds the facts that the server gives the issuer for one
// token: the namespace, the profile, the subject and the claims of the
// open. The server builds them from its own records. No route takes a claim
// from a caller, and a claim with the name of a registered claim, of the
// claims that the issuer sets, or with a name that is not snake_case is
// refused with ErrInvalid.
//
// By is the template of the audit entry: the caller fills who and where, and
// the issuer fills the time, the action, the object and the changes.
type OpenContext struct {
	// Claims holds the claims of the open, except the registered claims and
	// namespace, namespace_id and purpose.
	Claims map[string]any
	By     audit.Entry
	// Namespace and NamespaceID name the namespace as the open read it.
	// FailoverVersion is the failover version that the open read: the fence of
	// the open.
	Namespace       namespace.Name
	NamespaceID     namespace.ID
	FailoverVersion int64
	// Subject is the sub claim.
	Subject string
	// Profile and ProfileID name the profile of the block, for the audit
	// entry.
	Profile   Name
	ProfileID ID
}

// Token is a signed token. Value is the compact JWT. The caller commits
// Entry in the transaction that records the open, and answers Value only
// after that commit.
type Token struct {
	IssuedAt  time.Time
	ExpiresAt time.Time
	Entry     audit.Entry
	Value     string
	// ID is the jti.
	ID       string
	Issuer   string
	Subject  string
	Audience string
	KeyID    KeyID
	// Warnings are texts for a person: the namespace has an issuerURL and
	// its copy check failed, with the days left before the key stops signing.
	Warnings []string
}

// Issuer signs tokens for one namespace. It is the only code that signs.
type Issuer interface {
	// Login signs a token with purpose "login" for one block of one open.
	// ErrIssuerNotConfigured, ErrIssuerKeyMissing, ErrIssuerKeyExpired,
	// ErrInvalid for an audience or a lifetime that the rules refuse,
	// namespace.ErrFenced for a stale fence.
	Login(ctx context.Context, o OpenContext, block, aud string, lifetime time.Duration) (Token, error)
	// Adapter signs a token with purpose "adapter" and the hash of body for
	// one call of an external adapter. The lifetime is DefaultTokenLifetime.
	// It returns the errors of Login.
	Adapter(ctx context.Context, o OpenContext, block, aud string, body []byte) (Token, error)
}
