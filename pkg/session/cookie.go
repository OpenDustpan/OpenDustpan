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
// proof that the refresh key of the browser signs, with a server nonce. One
// session generation for each principal ends every session of that
// principal.
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
	// allows the narrow path RefreshCookiePath, so that the browser sends the
	// cookie to the session routes only.
	RefreshCookie = "__Secure-dustpan_refresh"
	// RefreshCookiePath is the path of the refresh cookie. Only RefreshRoute
	// and SignOutRoute open the cookie.
	RefreshCookiePath = "/api/server/session/"
	// RefreshRoute is the path of the refresh route.
	RefreshRoute = "/api/server/session/refresh"
	// SignOutRoute is the path of the sign-out route.
	SignOutRoute = "/api/server/session/sign-out"
	// NonceHeader is the header that carries a server nonce to the browser.
	NonceHeader = "DPoP-Nonce"
	// ForgeCookiePrefix starts the name of each cookie that holds the token of
	// a Git host user. The connection ID follows it.
	ForgeCookiePrefix = "__Host-dustpan_forge_"
	// SignInRequestCookie is the name of the sealed cookie that holds a
	// sign-in through an identity provider between its start and its
	// callback. It is SameSite=Lax, because the callback is a navigation
	// from another site.
	SignInRequestCookie = "__Host-dustpan_signin"
	// PendingCookie is the name of the sealed cookie that holds a pending
	// sign-in of MFA until the second factor. It is SameSite=Strict.
	PendingCookie = "__Host-dustpan_pending"
)

// Limits of the cookies and of the proof.
const (
	// AccessLifetime is the longest life of an access cookie.
	AccessLifetime = 5 * time.Minute
	// NonceWindow is how old the server time in the nonce of a proof can
	// be. It is the window of a proof, measured with the clock of the
	// server only.
	NonceWindow = 30 * time.Second
	// NonceFutureTolerance is how far after the time of the server the time
	// in a nonce can be. It allows for replicas whose clocks differ by up to
	// 5 seconds.
	NonceFutureTolerance = 5 * time.Second
	// NonceReuse is how long each replica sends the same nonce again. A
	// nonce does not need to be unique, and the reuse limits the seals of
	// the key.
	NonceReuse = 5 * time.Second
	// ProofIATTolerance is how far the iat of a proof can be from the time
	// of the server. It refuses a proof from a device with a clock far off.
	ProofIATTolerance = 24 * time.Hour
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
	// MaxRefreshCookies is the largest number of refresh cookies that a
	// request can carry. A sibling host can set a second one.
	MaxRefreshCookies = 2
	// DefaultKeyRotation is the default period between two session key
	// versions.
	DefaultKeyRotation = 30 * 24 * time.Hour
	// KeyReloadInterval is how often each replica reads the key ring again.
	// It is also the bound on the time that a retired version still opens.
	KeyReloadInterval = time.Minute
	// KeyOnDemandReloadEvery is the shortest time between two reads of the
	// ring for a cookie that names a key ID that the replica does not hold.
	KeyOnDemandReloadEvery = 10 * time.Second
	// KeySealDelay is the time between the creation of a version and its
	// first seal, more than four reload intervals.
	KeySealDelay = 5 * time.Minute
	// KeySealCountRotate is the count of seals of one version on one replica
	// that makes the replica add a new version. With random 96-bit nonces, a
	// key allows 2^32 seals. 16 replicas at this count make 2^31, and the
	// other half covers the seals with the old version until the new one
	// seals.
	KeySealCountRotate = 1 << 27
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
	// ErrNonceStale reports a server nonce older than NonceWindow, or one
	// whose time is more than NonceFutureTolerance after the time of the
	// server.
	ErrNonceStale = errors.New("server nonce is outside its window")
	// ErrEnrollmentRequired reports a request of an enroll-only session to
	// a route that does not enroll a factor, read the session, refresh or
	// sign out.
	ErrEnrollmentRequired = errors.New("the session can only enroll a factor")
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
	// ErrProofStale reports a proof whose iat is further than
	// ProofIATTolerance from the time of the server.
	ErrProofStale = errors.New("refresh proof time is far from the server time")
	// ErrProofNonce reports a proof with no nonce claim, or a nonce that does
	// not open.
	ErrProofNonce = errors.New("refresh proof has no valid server nonce")
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

// FreshKind names how the last fresh authentication happened.
type FreshKind string

// Kinds of a fresh authentication.
const (
	// FreshNone is no fresh authentication.
	FreshNone FreshKind = ""
	// FreshPasskey is a passkey with user verification.
	FreshPasskey FreshKind = "passkey"
	// FreshMFA is a password and a second factor.
	FreshMFA FreshKind = "mfa"
	// FreshPassword is a password alone, for a person with no factor where
	// no SSO provider exists and MFA is not required.
	FreshPassword FreshKind = "password"
	// FreshSSO is a new authentication at the SSO identity provider.
	FreshSSO FreshKind = "sso"
)

// Restriction limits what a session can do.
type Restriction string

// Restrictions of a session.
const (
	// RestrictionNone is a session with no restriction.
	RestrictionNone Restriction = ""
	// RestrictionEnrollOnly is a session that can only enroll a factor,
	// read the session, refresh and sign out.
	RestrictionEnrollOnly Restriction = "enroll-only"
)

// Claims are the fields that the access cookie and the refresh cookie share.
// A zero MFAAt or FreshAt is no MFA step or no fresh authentication. The
// issuer computes each deadline from its own clock.
type Claims struct {
	// SignInAt is the time of the sign-in. It never changes for the life of
	// the sign-in.
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
	// FreshKind is the kind of the last fresh authentication.
	FreshKind FreshKind
	// Restriction limits the session.
	Restriction Restriction
	// Generation is the session generation of the principal that the issue
	// read. The cookie works only while it equals the stored generation.
	Generation uint64
	// RefreshKey is the thumbprint of the refresh key of the browser. Each
	// issue after the first copies it, and never reads a key from the
	// request.
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
	// Generation is the session generation of the session that got the
	// token. Each bump ends the cookie.
	Generation uint64
	// Token is the user token. It is never refreshed.
	Token ForgeToken
}

// RequestClaims are the fields of a sign-in request cookie: the request of
// the start, the SHA-256 digest of the OAuth state, and the thumbprint of the
// refresh key that the start proved. The cookie holds the digest, never the
// plain state. The callback compares the digest of the state query parameter
// with StateDigest in constant time, and refuses a difference.
type RequestClaims struct {
	Request     Request
	StateDigest Digest
	RefreshKey  Thumbprint
}

// PendingClaims are the fields of a pending sign-in cookie. The factor lock
// of the person counts the wrong factors, so the cookie holds no count.
type PendingClaims struct {
	// CreatedAt is the time of the first factor.
	CreatedAt time.Time
	// ExpiresAt is at most 5 minutes after CreatedAt.
	ExpiresAt time.Time
	// AuthTime is the time of the first factor.
	AuthTime time.Time
	// PrincipalID names the person.
	PrincipalID string
	// Provider names the sign-in method of the first factor.
	Provider string
	// Restriction is RestrictionEnrollOnly for a person with no factor
	// after the grace.
	Restriction Restriction
	// Generation is the session generation that the first factor read.
	Generation uint64
	// RefreshKey is the thumbprint of the refresh key that the first factor
	// proved.
	RefreshKey Thumbprint
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

// StateSealer seals and opens the short state of a sign-in and the server
// nonces, with the session key ring and the same seal as Sealer. The
// associated data holds the cookie name, or "dpop-nonce" for a nonce, so a
// value of one kind never opens as another. Each Open method returns the
// errors of the Open methods of Sealer.
type StateSealer interface {
	// SealRequest returns the value of a sign-in request cookie with c.
	SealRequest(ctx context.Context, c RequestClaims) (string, error)
	// OpenRequest returns the claims of the sign-in request cookie raw. It
	// returns ErrCookieExpired at or after the expiry of the request.
	OpenRequest(ctx context.Context, raw string, now time.Time) (RequestClaims, error)
	// SealPending returns the value of a pending sign-in cookie with c.
	SealPending(ctx context.Context, c PendingClaims) (string, error)
	// OpenPending returns the claims of the pending sign-in cookie raw. It
	// returns ErrCookieExpired at or after c.ExpiresAt.
	OpenPending(ctx context.Context, raw string, now time.Time) (PendingClaims, error)
	// SealNonce returns a server nonce with the time at.
	SealNonce(ctx context.Context, at time.Time) (string, error)
	// OpenNonce returns the time of the server nonce raw. It returns
	// ErrNonceStale when that time is more than NonceWindow before now, or
	// more than NonceFutureTolerance after now.
	OpenNonce(ctx context.Context, raw string, now time.Time) (time.Time, error)
}

// WrappedKey is one session key version as SQL keeps it: wrapped by the
// key-encryption key, never in plain form. A version opens from its
// creation, and seals from SealsFrom, so that each replica loads it before
// the first cookie that names it. SealsUntil is zero while no newer version
// exists. RetiredAt is zero while the version can open.
type WrappedKey struct {
	CreatedAt  time.Time
	SealsFrom  time.Time
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
	// that was the newest to k.SealsFrom, and appends e. It returns
	// ErrInvalid when k fails validation or k.SealsFrom is before
	// k.CreatedAt, and ErrAlreadyExists when the ID is taken.
	AddKey(ctx context.Context, k WrappedKey, e audit.Entry) error
	// ListKeys returns each version that is not retired, newest first.
	ListKeys(ctx context.Context) ([]WrappedKey, error)
	// RetireKeys sets RetiredAt of each version in ids to at, and appends e.
	// It returns ErrKeyNotFound when a version does not exist, ErrKeyRetired
	// when one is retired, and ErrInvalid when ids holds the newest version.
	// It changes nothing on an error.
	RetireKeys(ctx context.Context, ids []string, at time.Time, e audit.Entry) error
}

// BumpCause names why the session generation of a principal moved.
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
// refresh, reads about the person of the session: the owner, its session
// generation, and the policies of the installation, from one read. The
// generation is at least 1.
type PrincipalState struct {
	Owner      Owner
	Policy     Policy
	Accounts   AccountPolicy
	Generation uint64
}

// PrincipalStateStore reads and bumps the session generation of principals.
type PrincipalStateStore interface {
	// PrincipalState returns the state of the principal id. It returns
	// ErrPrincipalNotFound when the principal does not exist, was deleted,
	// or is not a person. A disabled principal comes back with its disable
	// time: the caller decides.
	PrincipalState(ctx context.Context, id string) (PrincipalState, error)
	// BumpGeneration adds 1 to the session generation of the principal id
	// and appends e, in one transaction, and returns the new generation. It
	// needs no clock. An expected generation other than 0 makes the bump a
	// compare-and-swap: when the stored generation is another one, it
	// returns ErrGenerationChanged and writes nothing. Two bumps with the
	// same expected generation never both pass. It returns ErrInvalid for a
	// cause that ValidBumpCause refuses, ErrPrincipalNotFound as
	// PrincipalState does, and audit.ErrInvalidEntry for an entry that fails
	// validation.
	BumpGeneration(ctx context.Context, id string, cause BumpCause, expected uint64, e audit.Entry) (uint64, error)
}

// ProofTarget is the request that a refresh proof must name: the method and
// the public URL of the refresh route, with no query and no fragment.
type ProofTarget struct {
	Method string
	URL    string
}

// VerifiedProof is what a good proof gives: the RFC 7638 thumbprint of its
// key (over crv, kty, x and y only), its nonce claim, still sealed, and the
// server time in that nonce.
type VerifiedProof struct {
	NonceAt    time.Time
	Nonce      string
	Thumbprint Thumbprint
}

// ProofVerifier checks a refresh proof. It keeps no list of used proofs: the
// nonce window limits a replay. It never opens a URL from the proof, and it
// compares htu with the configured public URL, never with the Host header.
// An implementation opens the nonce with a StateSealer, so that it refuses
// a proof outside the window by itself.
type ProofVerifier interface {
	// Verify checks the proof raw for the request want at now, its nonce
	// included. It returns ErrProofMalformed, ErrProofSignature,
	// ErrProofStale, ErrProofTarget, ErrProofNonce or ErrNonceStale. The
	// caller opens the nonce again as a defense-in-depth layer, compares the
	// thumbprint with the cookie in constant time, and returns ErrProofKey
	// on a difference.
	Verify(ctx context.Context, raw string, want ProofTarget, now time.Time) (VerifiedProof, error)
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
	RefusalGeneration Refusal = "generation"
	RefusalIdle       Refusal = "idle"
	RefusalAbsolute   Refusal = "absolute"
	RefusalSubject    Refusal = "subject"
)

// RefreshRequest is what the refresh route reads from the request. The
// HTTP adapter copies each value and checks none of them: the service
// checks each one.
type RefreshRequest struct {
	Now time.Time
	// Cookies holds each refresh cookie of the request, in the order of the
	// Cookie header. More than MaxRefreshCookies is refused.
	Cookies      []string
	Proof        string
	Origin       string
	SecFetchSite string
	SourceIP     string
	UserAgent    string
}

// Issued is the answer of a good refresh or sign-in: the values of the two
// cookies, a new server nonce, and the claims of the access cookie.
type Issued struct {
	Access       string
	Refresh      string
	Nonce        string
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
	// EnrollRoute reports a route that an enroll-only session can call.
	EnrollRoute bool
}

// SignOutRequest is what the sign-out route reads from the request. It
// needs an access cookie or a refresh cookie that opens, also after its
// expiry, and no valid generation, because a sign-out only ends things.
type SignOutRequest struct {
	Now            time.Time
	AccessCookie   string
	RefreshCookies []string
	CSRFHeader     string
	Origin         string
	SecFetchSite   string
	SourceIP       string
	UserAgent      string
}

// CookieSessions is the port that the HTTP adapters call to check and to
// refresh sessions. internal/sessionauth uses it.
type CookieSessions interface {
	// Authenticate checks the access cookie of r: the origin, the seal, the
	// expiry, the CSRF value for a method that can change state, the
	// principal state, the generation, the deadlines under the current
	// policy, and the restriction. It returns ErrUnauthenticated, wrapped
	// with the cause, on any refusal, and ErrEnrollmentRequired for an
	// enroll-only session on another route.
	Authenticate(ctx context.Context, r AccessRequest) (AccessClaims, PrincipalState, error)
	// Refresh runs the steps of a refresh in the order of the Refusal
	// constants. A refusal returns ErrUnauthenticated, wrapped with the
	// cause, sets no cookie, and appends a refusal entry. A proof with no
	// nonce or an old one returns ErrNonceStale with a new nonce in Issued,
	// and appends no entry.
	Refresh(ctx context.Context, r RefreshRequest) (Issued, error)
	// SignOut ends each run of the sign-in of the cookie that opens, and
	// appends the entry. It returns ErrUnauthenticated when no cookie opens
	// or the origin or the CSRF value is wrong. The adapter expires each
	// session cookie either way.
	SignOut(ctx context.Context, r SignOutRequest) error
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
