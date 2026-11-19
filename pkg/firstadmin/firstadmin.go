// Package firstadmin makes the first administrator of an installation.
//
// The bootstrap administrator asks for a one-time code for one person. The
// person sends the code in a console session with a fresh sign-in. One store
// transaction then makes the site-administrator grant on the installation,
// spends the code and stores the marker "first administrator made". After
// the marker the route is closed for good.
//
// This package holds the port, the code, its digest and the pure checks that
// the service and both store adapters call, so that a rule lives in one
// place and cannot differ between dialects. It never holds the code of an
// offer: only the digest is stored.
package firstadmin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// The rules of CheckPrincipal. Each error wraps ErrPrincipalInvalid, so a
// caller that checks only ErrPrincipalInvalid sees all of them, and a caller
// that needs the rule checks one of these.
var (
	// ErrPrincipalNotPerson reports a principal that is not a person of the
	// installation: its ID has not the form of a person ID, it is a service,
	// or it belongs to a namespace.
	ErrPrincipalNotPerson = fmt.Errorf("%w: the principal is not a person of the installation", ErrPrincipalInvalid)
	// ErrPrincipalDisabled reports a principal that is disabled.
	ErrPrincipalDisabled = fmt.Errorf("%w: the principal is disabled", ErrPrincipalInvalid)
	// ErrPrincipalBreakGlass reports a break-glass person.
	ErrPrincipalBreakGlass = fmt.Errorf("%w: the principal is a break-glass person", ErrPrincipalInvalid)
	// ErrPrincipalTempAdmin reports a temporary administrator.
	ErrPrincipalTempAdmin = fmt.Errorf("%w: the principal is a temporary administrator", ErrPrincipalInvalid)
	// ErrPrincipalDevSubject reports a principal with a dev: subject.
	ErrPrincipalDevSubject = fmt.Errorf("%w: the principal has a dev: subject", ErrPrincipalInvalid)
	// ErrPrincipalBootstrapSubject reports a principal with the subject of the
	// bootstrap administrator.
	ErrPrincipalBootstrapSubject = fmt.Errorf("%w: the principal has a bootstrap subject", ErrPrincipalInvalid)
	// ErrPrincipalNotLive reports a principal that does not exist in the live
	// rows, or that is not the principal of the session.
	ErrPrincipalNotLive = fmt.Errorf("%w: the principal is not live", ErrPrincipalInvalid)
)

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrAlreadyMade reports that the marker is stored. The offer and the
	// redeem are closed.
	ErrAlreadyMade = errors.New("the first administrator is made already")
	// ErrCodeInvalid reports a code that is wrong, spent, expired, absent
	// or made for another person. One error covers the five cases, so that
	// the answer does not say which one happened.
	ErrCodeInvalid = errors.New("invalid code")
	// ErrPrincipalInvalid reports a principal that cannot be the first
	// administrator: not live, not enabled, not a person of the
	// installation, a break-glass person, a temporary administrator, or a
	// dev: subject.
	ErrPrincipalInvalid = errors.New("invalid principal for the first administrator")
	// ErrReasonInvalid reports a reason that is blank, too long, not UTF-8
	// or that holds a control or format character.
	ErrReasonInvalid = errors.New("invalid reason")
	// ErrSessionInvalid reports a console session that the store cannot use for
	// the redeem: it does not exist, it ended, it is idle or expired under the
	// session policy, or it belongs to another person.
	ErrSessionInvalid = errors.New("the console session is not valid for the redeem")
	// ErrGrantExists reports a person that holds the role already. Nothing
	// is spent and no marker is stored.
	ErrGrantExists = errors.New("the person holds the role already")
	// ErrStaleSignIn reports a sign-in that is older than the window, a
	// sign-in after now, or a window of 0.
	ErrStaleSignIn = errors.New("the sign-in is not fresh")
	// ErrInvalid reports an input that fails validation.
	ErrInvalid = errors.New("invalid first administrator input")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

const (
	// CodeBytes is the size of a code, in bytes of the random source.
	CodeBytes = 32
	// CodeLength is the length of the text form of a code: base64url
	// without padding.
	CodeLength = 43
	// Lifetime is the time an offer works. The store fixes it with a CHECK.
	Lifetime = 10 * time.Minute
	// LifetimeMs is Lifetime in whole milliseconds, as stored.
	LifetimeMs = int64(Lifetime / time.Millisecond)
	// DigestLength is the length of the hex form of a digest.
	DigestLength = 64
	// Role is the one role the code gives.
	Role = "site-administrator"
)

const maxIDLength = 64

// Code is the one-time secret. Its text form is never logged, never stored
// and never put in an audit entry or an error. It lives in the answer of
// the offer and in the body of the redeem only.
type Code string

// NewCode reads CodeBytes bytes from r and returns the code. A reader that
// fails or that returns fewer bytes gives an error and no code.
func NewCode(r io.Reader) (Code, error) {
	if r == nil {
		return "", fmt.Errorf("%w: no random source", ErrInvalid)
	}

	b := make([]byte, CodeBytes)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", fmt.Errorf("read the random source: %w", err)
	}

	return Code(base64.RawURLEncoding.EncodeToString(b)), nil
}

// ValidForm reports whether c has the form of a code: CodeLength characters
// of base64url that decode to CodeBytes bytes. It is a check of the form
// only and says nothing about the value.
func (c Code) ValidForm() bool {
	if len(c) != CodeLength {
		return false
	}

	b, err := base64.RawURLEncoding.Strict().DecodeString(string(c))

	return err == nil && len(b) == CodeBytes
}

// Digest returns the SHA-256 digest of c in lowercase hex. The store keeps
// the digest only.
func (c Code) Digest() string {
	sum := sha256.Sum256([]byte(c))

	return hex.EncodeToString(sum[:])
}

// String hides the code from a format verb, so a log line or an error that
// formats it by mistake prints no secret.
func (Code) String() string { return "[code]" }

// GoString hides the code from the %#v verb.
func (Code) GoString() string { return "[code]" }

// ValidDigest reports whether d is a digest in the stored form: DigestLength
// characters of lowercase hex.
func ValidDigest(d string) bool {
	if len(d) != DigestLength {
		return false
	}

	for i := range len(d) {
		c := d[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// DigestsEqual compares two digests in constant time.
func DigestsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ValidateReason reports whether reason is usable. It uses the rule of the
// reason of a temporary administrator: not blank, UTF-8, at most
// audit.MaxReasonLen characters, and with no control or format character. It
// returns ErrReasonInvalid otherwise.
func ValidateReason(reason string) error {
	if err := tempadmin.ValidateReason(reason); err != nil {
		return fmt.Errorf("%w: %w", ErrReasonInvalid, err)
	}

	return nil
}

// Marker is the stored fact "first administrator made". The IDs are stable
// IDs, never names.
type Marker struct {
	MadeAt      time.Time
	PrincipalID string
	GrantID     string
	SessionID   string
	CreatedBy   string
}

// Offer is the open code, without the code and without its digest.
type Offer struct {
	CreatedAt   time.Time
	ExpiresAt   time.Time
	PrincipalID string
	CreatedBy   string
	Reason      string
}

// State is what the read of the route answers: the marker, when it is set,
// and the open offer, when one exists and has not expired.
type State struct {
	Marker *Marker
	Offer  *Offer
}

// OfferInput is the input of Store.Offer. Digest is the digest of the code.
// CreatedBy is the subject of the bootstrap administrator.
type OfferInput struct {
	Now         time.Time
	PrincipalID string
	Digest      string
	CreatedBy   string
	Reason      string
}

// OfferResult tells whether the offer replaced an open code.
type OfferResult struct {
	ExpiresAt time.Time
	Replaced  bool
}

// RedeemInput is the input of Store.Redeem. PrincipalID and SessionID come
// from the console session of the caller, never from the body. Digest is the
// digest of the code in the body. Window is the freshness window of the
// session policy. AuthTime is the sign-in time that the caller read. The
// store does not trust it: it reads the session row by SessionID in its
// transaction, and it uses the sign-in time of that row. GrantID
// is the ID the service made for the grant.
type RedeemInput struct {
	Now         time.Time
	AuthTime    time.Time
	PrincipalID string
	SessionID   string
	Digest      string
	GrantID     string
	Window      time.Duration
}

// Result is the answer of a redeem.
type Result struct {
	MadeAt      time.Time
	PrincipalID string
	GrantID     string
}

// Store is the port of the first administrator. Each method runs in one
// transaction in the scope of the installation, and each write appends its
// audit entry in that transaction: a failed append rolls the write back.
//
// Offer returns ErrAlreadyMade when the marker is stored, and
// ErrPrincipalInvalid when CheckPrincipal refuses the live row. It replaces
// the one code row.
//
// Redeem checks, in this order: the marker is not set, the code row exists
// and has not expired, the digest matches in constant time, the principal of
// the code is the principal of the session, the live row passes
// CheckPrincipal, the session row exists, is active and is of that principal,
// the stored sign-in is fresh. It then inserts the grant (the role
// Role on the installation, with the creator of the code row as granter),
// deletes the code row, stores the marker and appends the entry. Before the
// append, the transaction sets the reason of the entry to the reason of the
// code row it read, and the change ChangeCreatedBy to the creator of that
// row (StampRedeemEntry). The entry of the caller never decides these two
// facts: an offer that replaced the code after the caller read State cannot
// leave the reason or the creator of the old offer in the entry. Redeem
// returns ErrAlreadyMade, ErrCodeInvalid, ErrPrincipalInvalid, ErrStaleSignIn
// ErrSessionInvalid or ErrGrantExists, and then changes nothing: a code that fails
// ErrGrantExists is not spent.
//
// State returns the marker and the open offer. It never returns a code or a
// digest.
type Store interface {
	State(ctx context.Context, now time.Time) (State, error)
	Offer(ctx context.Context, in OfferInput, e audit.Entry) (OfferResult, error)
	Redeem(ctx context.Context, in RedeemInput, e audit.Entry) (Result, error)
}

// ChangeCreatedBy is the key of the change of the redeem entry that holds the
// creator of the code row.
const ChangeCreatedBy = "createdBy"

// StampRedeemEntry returns e with the reason and the creator that the stored
// code row holds. Both store adapters call it inside the redeem transaction,
// on the row they read there, so that the entry never holds the reason or the
// creator of an offer that another offer replaced. It replaces the change
// with the key ChangeCreatedBy, or adds it when e has none, and it leaves the
// slice of e unchanged.
func StampRedeemEntry(e audit.Entry, createdBy, reason string) audit.Entry {
	changes := make([]audit.Change, 0, len(e.Changes)+1)
	found := false

	for _, c := range e.Changes {
		if c.Key == ChangeCreatedBy {
			c.New = createdBy
			c.Old = nil
			found = true
		}

		changes = append(changes, c)
	}

	if !found {
		changes = append(
			changes,
			audit.Change{Key: ChangeCreatedBy, Old: nil, New: createdBy, Layer: "", TakesEffect: ""},
		)
	}

	e.Changes = changes
	e.Reason = reason

	return e
}

// Facts are the rows a redeem reads in its transaction, for CheckRedeem.
// Principal and TempAdmin come from the live rows. HaveCode is false when
// no code row exists.
type Facts struct {
	Now         time.Time
	AuthTime    time.Time
	CodeExpires time.Time
	CodePrin    string
	CodeDigest  string
	InDigest    string
	InPrincipal string
	Principal   principal.Principal
	Window      time.Duration
	MarkerSet   bool
	HaveCode    bool
	HavePrin    bool
	TempAdmin   bool
	// HaveSession is false when no session row has the ID of the session.
	HaveSession bool
	// SessionOwner is the principal ID of the session row, and SessionStatus is
	// its status at Now under the limits of the session policy. AuthTime is the
	// stored sign-in time of the row, never the time that the caller sent.
	SessionOwner  string
	SessionStatus session.Status
}

// SetSession puts the stored session row s in f: its owner, its status at
// f.Now under the limits l, and its sign-in time. Both store adapters call it
// on the row that they read in the redeem transaction.
func (f *Facts) SetSession(s session.Session, l session.Limits) {
	f.HaveSession = true
	f.SessionOwner = s.PrincipalID
	f.SessionStatus = s.StatusAt(f.Now, l)
	f.AuthTime = s.AuthTime
}

// CheckRedeem applies the rules of the redeem to the facts, in the order of
// Store. It is pure: both adapters call it on the rows they read in the
// transaction. It returns the first error and never says which part of the
// code failed.
func CheckRedeem(f Facts) error {
	if f.MarkerSet {
		return ErrAlreadyMade
	}

	if err := checkCode(f); err != nil {
		return err
	}

	if !f.HavePrin || f.Principal.ID != f.CodePrin {
		return ErrPrincipalNotLive
	}

	if err := CheckPrincipal(f.Principal, f.TempAdmin); err != nil {
		return err
	}

	// The session must exist, be active under the policy, and belong to the
	// person of the code. The sign-in time below is the stored one.
	if !f.HaveSession || f.SessionOwner != f.InPrincipal || f.SessionStatus != session.StatusActive {
		return ErrSessionInvalid
	}

	if !fresh(f.Now, f.AuthTime, f.Window) {
		return ErrStaleSignIn
	}

	return nil
}

// checkCode applies the rules of the code row to the facts: it exists, it has
// not expired, the digest matches and it names the person of the session.
func checkCode(f Facts) error {
	// The compare runs even when no code row exists, so that the time of an
	// absent code and a wrong code is the same.
	digest := f.CodeDigest
	if !f.HaveCode {
		digest = ""
	}

	match := DigestsEqual(digest, f.InDigest)

	if !f.HaveCode || !match || !f.Now.Before(f.CodeExpires) {
		return ErrCodeInvalid
	}

	if f.CodePrin == "" || f.CodePrin != f.InPrincipal {
		return ErrCodeInvalid
	}

	return nil
}

// fresh reports whether a sign-in at authTime is recent enough at now. A
// window of 0 asks at each step, and a sign-in after now is never fresh.
func fresh(now, authTime time.Time, window time.Duration) bool {
	if window <= 0 {
		return false
	}

	age := now.Sub(authTime)

	return age >= 0 && age <= window
}

// CheckPrincipal reports whether p can be the first administrator: a stable
// ID, an enabled person of the installation, not a break-glass person, not
// a temporary administrator, and no dev: subject. tempAdmin is true when
// the principal has a temporary administrator record. It returns
// ErrPrincipalInvalid with the failed rule.
func CheckPrincipal(p principal.Principal, tempAdmin bool) error {
	switch {
	case !ident.Valid(p.ID, maxIDLength) || len(p.ID) <= len(principal.IDPrefix) || p.ID[:len(principal.IDPrefix)] != principal.IDPrefix:
		return ErrPrincipalNotPerson
	case p.Kind != principal.KindPerson:
		return ErrPrincipalNotPerson
	case p.Namespace != "":
		return ErrPrincipalNotPerson
	case !p.DisabledAt.IsZero():
		return ErrPrincipalDisabled
	case p.BreakGlass:
		return ErrPrincipalBreakGlass
	case tempAdmin:
		return ErrPrincipalTempAdmin
	case principal.IsDevSubject(p.Subject):
		return ErrPrincipalDevSubject
	case principal.IsBootstrapSubject(p.Subject):
		return ErrPrincipalBootstrapSubject
	}

	return nil
}
