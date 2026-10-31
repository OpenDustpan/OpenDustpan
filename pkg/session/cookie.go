package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// This file holds the types and the ports of console sessions that the
// server does not store. A session lives in two sealed cookies: the access
// cookie, which authenticates each request for at most AccessLifetime, and
// the refresh cookie, which only the refresh route opens. A refresh needs a
// proof that the refresh key of the browser signs. One valid-after time for
// each principal ends every session of that principal.
//
// The types and the ports come first. The implementation replaces Store and
// its session rows: no adapter implements these ports yet, and the cases of
// sessiontest that test them are skipped.

// Cookie names and the path of the refresh route.
const (
	// AccessCookie is the name of the access cookie. The prefix __Host-
	// makes the browser refuse it without Secure, with a Domain, or with a
	// Path other than "/".
	AccessCookie = "__Host-dustpan_access"
	// RefreshCookie is the name of the refresh cookie. The prefix __Secure-
	// allows the narrow path RefreshPath, so that the browser sends the
	// cookie to the refresh route only.
	RefreshCookie = "__Secure-dustpan_refresh"
	// RefreshPath is the path of the refresh route and of the refresh cookie.
	RefreshPath = "/api/server/session/refresh"
	// ForgeCookiePrefix starts the name of each cookie that holds the token of
	// a Git host user. The connection ID follows it.
	ForgeCookiePrefix = "__Host-dustpan_forge_"
)

// Limits of the cookies and of the proof.
const (
	// AccessLifetime is the longest life of an access cookie.
	AccessLifetime = 5 * time.Minute
	// ProofMaxAge is how old the iat of a refresh proof can be.
	ProofMaxAge = 25 * time.Second
	// ProofMaxSkew is how far in the future the iat of a refresh proof can
	// be. ProofMaxAge plus ProofMaxSkew is the window of a proof: 30 seconds.
	ProofMaxSkew = 5 * time.Second
	// MaxCookieValue is the longest cookie value, in bytes, that an open
	// reads. A longer value is refused before the open.
	MaxCookieValue = 4096
	// MaxCookiePlaintext is the longest plaintext of a sealed cookie, in
	// bytes.
	MaxCookiePlaintext = 2048
	// MaxProof is the longest refresh proof, in bytes.
	MaxProof = 2048
	// MaxForgeCookies is the largest number of Git host token cookies that a
	// request can carry.
	MaxForgeCookies = 4
	// DefaultKeyRotation is the default period between two session key
	// versions.
	DefaultKeyRotation = 30 * 24 * time.Hour
)

// Prefixes of the IDs of this file. An ID is opaque: never parse it.
const (
	// SignInIDPrefix starts each sign-in ID.
	SignInIDPrefix = "sin-"
	// KeyIDPrefix starts the ID of each session key version.
	KeyIDPrefix = "sk-"
)

// Errors of a sealed cookie. Each one makes the request unauthenticated.
var (
	// ErrCookieMalformed reports a cookie value that is too long, or that
	// does not have the form "v1.<key ID>.<base64url>".
	ErrCookieMalformed = errors.New("session cookie is malformed")
	// ErrCookieKeyUnknown reports a cookie sealed with a key version that
	// the ring does not hold, or that it retired.
	ErrCookieKeyUnknown = errors.New("session cookie names an unknown key")
	// ErrCookieOpen reports a cookie that does not open: a changed value, a
	// value of another cookie, or a plaintext that the strict decoder
	// refuses.
	ErrCookieOpen = errors.New("session cookie does not open")
	// ErrCookieExpired reports an access cookie or a Git host token cookie
	// after its expiry.
	ErrCookieExpired = errors.New("session cookie expired")
)

// Errors of a refresh proof.
var (
	// ErrProofMalformed reports a proof that is missing, too long, not a
	// compact JWS, of another type, with an algorithm other than ES256, or
	// with a jwk header that holds a private part.
	ErrProofMalformed = errors.New("refresh proof is malformed")
	// ErrProofSignature reports a proof whose signature does not verify with
	// the key in its jwk header.
	ErrProofSignature = errors.New("refresh proof signature is not valid")
	// ErrProofStale reports a proof whose iat is outside the window.
	ErrProofStale = errors.New("refresh proof is outside its time window")
	// ErrProofTarget reports a proof whose htm or htu is not the method or
	// the public URL of the refresh route.
	ErrProofTarget = errors.New("refresh proof names another request")
	// ErrProofKey reports a proof whose key does not have the thumbprint of
	// the refresh cookie.
	ErrProofKey = errors.New("refresh proof key does not match the cookie")
)

// Errors of the session key ring.
var (
	// ErrKeyNotFound reports a session key version that does not exist.
	ErrKeyNotFound = errors.New("session key not found")
	// ErrKeyRetired reports a change of a session key version that is
	// retired.
	ErrKeyRetired = errors.New("session key is retired")
)

// Thumbprint is the SHA-256 JWK thumbprint (RFC 7638) of the public part of
// a refresh key.
type Thumbprint [32]byte

// CSRF is the CSRF value of one sign-in: 256 bits from crypto/rand. Both
// cookies carry it, and a request that can change state sends it in the
// CSRF header. Its String and GoString methods redact it.
type CSRF [32]byte

// String redacts the CSRF value.
func (CSRF) String() string {
	return "[csrf value]"
}

// GoString redacts the CSRF value in %#v output too.
func (CSRF) GoString() string {
	return "[csrf value]"
}

// Claims are the fields that the access cookie and the refresh cookie share.
// A zero MFAAt or FreshAt is no MFA step or no fresh authentication.
type Claims struct {
	// SignInAt is the time of the sign-in, or of the last change that kept
	// the session (a password change by the person). A session
	// authenticates only while SignInAt is after the valid-after time.
	SignInAt time.Time
	// AuthTime is the time of the authentication at the identity provider.
	AuthTime time.Time
	// MFAAt is the time of the last MFA step.
	MFAAt time.Time
	// FreshAt is the time of the last fresh authentication.
	FreshAt time.Time
	// IdleDeadline is when the session ends with no refresh.
	IdleDeadline time.Time
	// AbsoluteDeadline is when the session ends in all. It never moves.
	AbsoluteDeadline time.Time
	// PrincipalID names the person.
	PrincipalID string
	// SignInID names the sign-in, with the prefix SignInIDPrefix.
	SignInID string
	// Provider names the sign-in method.
	Provider string
	// RefreshKey is the thumbprint of the refresh key of the browser.
	RefreshKey Thumbprint
	// CSRF is the CSRF value of the sign-in.
	CSRF CSRF
}

// AccessClaims are the fields of an access cookie.
type AccessClaims struct {
	Claims

	// IssuedAt is the time of issue.
	IssuedAt time.Time
	// ExpiresAt is at most IssuedAt plus AccessLifetime, and never after a
	// deadline.
	ExpiresAt time.Time
	// KeyID names the session key version that sealed the cookie. An open
	// sets it. A seal ignores it and uses the newest version.
	KeyID string
}

// RefreshClaims are the fields of a refresh cookie.
type RefreshClaims struct {
	Claims

	// KeyID names the session key version that sealed the cookie. An open
	// sets it. A seal ignores it and uses the newest version.
	KeyID string
}

// ForgeToken is the token of a Git host user. Its String and GoString
// methods redact it, so it cannot reach a log or an error by accident. Use
// Reveal only to call the Git host.
type ForgeToken struct {
	value string
}

// NewForgeToken wraps the token that a Git host returned.
func NewForgeToken(v string) ForgeToken {
	return ForgeToken{value: v}
}

// Reveal returns the plain token, to call the Git host.
func (t ForgeToken) Reveal() string {
	return t.value
}

// String redacts the token.
func (ForgeToken) String() string {
	return "[git host token]"
}

// GoString redacts the token in %#v output too.
func (ForgeToken) GoString() string {
	return "[git host token]"
}

// ForgeClaims are the fields of a Git host token cookie. The token works
// only in a request whose access cookie has the same sign-in ID and
// authenticates.
type ForgeClaims struct {
	// SignInAt is the sign-in time of the session that got the token.
	SignInAt time.Time
	// ExpiresAt is the earlier of the expiry of the token and the absolute
	// deadline of the session.
	ExpiresAt time.Time
	// ConnectionID names the Git host connection.
	ConnectionID string
	// ForgeUserID is the immutable user ID at the Git host.
	ForgeUserID string
	// SignInID names the sign-in that got the token.
	SignInID string
	// KeyID names the session key version that sealed the cookie.
	KeyID string
	// Token is the user token. It is never refreshed.
	Token ForgeToken
}

// Sealer seals and opens the cookies of sessions with the session key ring.
// The associated data of each seal holds the format version, the cookie name
// and the key ID, and the connection ID for a Git host token cookie, so that
// a value of one cookie never opens as another.
//
// Each Open method returns ErrCookieMalformed for a value longer than
// MaxCookieValue or of the wrong form, ErrCookieKeyUnknown for a key
// version that the ring does not hold, and ErrCookieOpen for a value that
// does not open or decode. OpenAccess and OpenForge return ErrCookieExpired
// after the expiry. An Open method checks no principal and no deadline of
// the session: the caller does.
type Sealer interface {
	// SealAccess returns the value of an access cookie with c, sealed with
	// the newest key version.
	SealAccess(ctx context.Context, c AccessClaims) (string, error)
	// OpenAccess returns the claims of the access cookie raw at now.
	OpenAccess(ctx context.Context, raw string, now time.Time) (AccessClaims, error)
	// SealRefresh returns the value of a refresh cookie with c.
	SealRefresh(ctx context.Context, c RefreshClaims) (string, error)
	// OpenRefresh returns the claims of the refresh cookie raw.
	OpenRefresh(ctx context.Context, raw string) (RefreshClaims, error)
	// SealForge returns the value of a Git host token cookie with c.
	SealForge(ctx context.Context, c ForgeClaims) (string, error)
	// OpenForge returns the claims of the Git host token cookie raw of the
	// connection connectionID at now.
	OpenForge(ctx context.Context, raw, connectionID string, now time.Time) (ForgeClaims, error)
}

// WrappedKey is one session key version as SQL keeps it: wrapped by the
// key-encryption key, never in plain form. SealsUntil is zero while the
// version is the newest. RetiredAt is zero while the version can open.
type WrappedKey struct {
	CreatedAt  time.Time
	SealsUntil time.Time
	RetiredAt  time.Time
	// ID has the prefix KeyIDPrefix.
	ID string
	// WrapperID names the key-encryption key that wrapped the version.
	WrapperID  string
	Ciphertext []byte
}

// KeyStore is the persistence port of the session key ring. The ring
// belongs to the installation, so no write carries a namespace fence. A
// backup never holds a key version, and a restore makes a new ring.
//
// Each change appends its audit entry, in the scope audit.ServerScope, in the
// same transaction: both commit, or neither. A change returns
// audit.ErrInvalidEntry, and changes nothing, when the entry fails
// validation.
type KeyStore interface {
	// AddKey stores k as the newest version, sets SealsUntil of the version
	// that was the newest to k.CreatedAt, and appends e. It returns
	// ErrInvalid when k fails validation, and ErrAlreadyExists when the ID
	// is taken.
	AddKey(ctx context.Context, k WrappedKey, e audit.Entry) error
	// ListKeys returns each version that is not retired, newest first.
	ListKeys(ctx context.Context) ([]WrappedKey, error)
	// RetireKeys sets RetiredAt of each version in ids to at, and appends e.
	// It returns ErrKeyNotFound when a version does not exist, ErrKeyRetired
	// when one is retired, and ErrInvalid when ids holds the newest version.
	// It changes nothing on an error.
	RetireKeys(ctx context.Context, ids []string, at time.Time, e audit.Entry) error
}

// BumpCause names why the valid-after time of a principal moved.
type BumpCause string

// Causes of a bump.
const (
	// BumpSignOutEverywhere is a sign-out everywhere by the person.
	BumpSignOutEverywhere BumpCause = "sign-out-everywhere"
	// BumpAdministrator is an end of the sessions by an administrator.
	BumpAdministrator BumpCause = "administrator"
	// BumpDisabled is a disable of the principal.
	BumpDisabled BumpCause = "disabled"
	// BumpPassword is a set, a change or a reset of the password.
	BumpPassword BumpCause = "password"
	// BumpFactor is an add, a delete or a reset of an MFA factor.
	BumpFactor BumpCause = "factor"
	// BumpDirectory is a disable by SCIM or LDAP.
	BumpDirectory BumpCause = "directory"
)

// PrincipalState is what each request with an access cookie, and each
// refresh, reads about the person of the session: the owner, its
// valid-after time, and the policies of the installation, from one read.
// ValidAfter is zero when no bump happened.
type PrincipalState struct {
	ValidAfter time.Time
	Owner      Owner
	Policy     Policy
	Accounts   AccountPolicy
}

// PrincipalStateStore reads and bumps the valid-after time of principals.
type PrincipalStateStore interface {
	// PrincipalState returns the state of the principal id. It returns
	// ErrPrincipalNotFound when the principal does not exist, was deleted,
	// or is not a person. A disabled principal comes back with its disable
	// time: the caller decides.
	PrincipalState(ctx context.Context, id string) (PrincipalState, error)
	// BumpValidAfter sets the valid-after time of the principal id to at,
	// unless it holds a later time, and appends e, in one transaction. It
	// returns the stored time. It returns ErrInvalid for a cause that
	// ValidBumpCause refuses, ErrPrincipalNotFound as PrincipalState does,
	// and audit.ErrInvalidEntry for an entry that fails validation.
	BumpValidAfter(ctx context.Context, id string, at time.Time, cause BumpCause, e audit.Entry) (time.Time, error)
}

// ProofTarget is the request that a refresh proof must name: the method and
// the public URL of the refresh route, with no query and no fragment.
type ProofTarget struct {
	Method string
	URL    string
}

// ProofVerifier checks a refresh proof. It keeps no list of used proofs: the
// window of ProofMaxAge and ProofMaxSkew limits a replay.
type ProofVerifier interface {
	// Verify checks the proof raw for the request want at now, and returns
	// the thumbprint of its key. It returns ErrProofMalformed,
	// ErrProofSignature, ErrProofStale or ErrProofTarget. The caller
	// compares the thumbprint with the cookie, in constant time, and
	// returns ErrProofKey on a difference.
	Verify(ctx context.Context, raw string, want ProofTarget, now time.Time) (Thumbprint, error)
}

// Refusal names the step of a refresh that refused it. The audit entry of
// the refusal records it.
type Refusal string

// Steps that refuse a refresh, in the order that the refresh checks them.
const (
	RefusalOrigin     Refusal = "origin"
	RefusalCookie     Refusal = "cookie"
	RefusalProof      Refusal = "proof"
	RefusalPrincipal  Refusal = "principal"
	RefusalValidAfter Refusal = "valid-after"
	RefusalIdle       Refusal = "idle"
	RefusalAbsolute   Refusal = "absolute"
	RefusalSubject    Refusal = "subject"
)

// RefreshRequest is what the refresh route reads from the request. The
// HTTP adapter copies each value and checks none of them: the service
// checks each one.
type RefreshRequest struct {
	Now          time.Time
	Cookie       string
	Proof        string
	Origin       string
	SecFetchSite string
	SourceIP     string
	UserAgent    string
}

// Issued is the answer of a good refresh or sign-in: the values of the two
// cookies and the claims of the access cookie.
type Issued struct {
	Access       string
	Refresh      string
	AccessClaims AccessClaims
}

// AccessRequest is what the check of the access cookie reads from a
// request. The HTTP adapter copies each value and checks none of them.
type AccessRequest struct {
	Now          time.Time
	Cookie       string
	Method       string
	CSRFHeader   string
	Origin       string
	SecFetchSite string
}

// CookieSessions is the port that the HTTP adapters call to check and to
// refresh sessions. internal/sessionauth uses it.
type CookieSessions interface {
	// Authenticate checks the access cookie of r: the origin, the seal, the
	// expiry, the CSRF value for a method that can change state, the
	// principal state, the valid-after time and the deadlines under the
	// current policy. It returns ErrUnauthenticated, wrapped with the cause,
	// on any refusal.
	Authenticate(ctx context.Context, r AccessRequest) (AccessClaims, PrincipalState, error)
	// Refresh runs the steps of a refresh in the order of the Refusal
	// constants. A refusal returns ErrUnauthenticated, wrapped with the
	// cause, sets no cookie, and appends a refusal entry.
	Refresh(ctx context.Context, r RefreshRequest) (Issued, error)
}

// ValidBumpCause returns ErrInvalid when c is not a cause of a bump.
func ValidBumpCause(c BumpCause) error {
	switch c {
	case BumpSignOutEverywhere, BumpAdministrator, BumpDisabled, BumpPassword, BumpFactor, BumpDirectory:
		return nil
	default:
		return fmt.Errorf("%w: bump cause %q", ErrInvalid, c)
	}
}
