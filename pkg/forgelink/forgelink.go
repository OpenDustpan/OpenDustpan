// Package forgelink links an account of a Git host to a principal of the
// installation. Only an account that a person linked can run a comment command
// of a review request, and the command then runs as that principal with its
// actions.
//
// A link needs a proof from both sides. The person signs in to the console and
// starts a link request, and the server shows the one-time token to that person
// only. The Git host account proves itself with a signed event of the Git host
// that carries the token, and the server takes the ID of the account from that
// event, never from the body of a request of a person. The person then looks at
// the account that proved itself and accepts it. Neither side alone makes a
// link.
//
// An account is named by the ID that the Git host gives it. The login of an
// account can change, so the login is only text for a person to read and is
// never compared. A link never replaces another link silently.
package forgelink

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

// Sentinel errors of this package and of its Store.
var (
	// ErrInvalid reports a value that fails validation.
	ErrInvalid = errors.New("invalid Git host account link")
	// ErrNotFound reports an unknown link or link request.
	ErrNotFound = errors.New("link of the Git host account not found")
	// ErrConflict reports a write that collides with a stored row: a link that
	// another principal holds, a link request with a hash that exists, or a link
	// request that another account proved already.
	ErrConflict = errors.New("conflict with a link of a Git host account")
	// ErrUsed reports a link request that a person accepted already.
	ErrUsed = errors.New("link request already used")
	// ErrExpired reports a link request after its expiry.
	ErrExpired = errors.New("link request expired")
	// ErrNotProved reports an accept of a link request that no Git host account
	// proved yet.
	ErrNotProved = errors.New("no Git host account proved itself for this link request")
	// ErrDenied reports an accept by a principal other than the one that started
	// the link request.
	ErrDenied = errors.New("link request belongs to another principal")
	// ErrNotPerson reports a caller that is not a person with a record, or a
	// principal that cannot make a link: an agent, a service principal and a
	// break-glass account.
	ErrNotPerson = errors.New("only a person can link a Git host account")
	// ErrLimit reports a person who has MaxOpenRequests link requests that can
	// still be accepted.
	ErrLimit = errors.New("too many open link requests")
	// ErrNotLinked reports an account that has no link that works: no link, or a
	// link of a principal that is disabled or deleted.
	ErrNotLinked = errors.New("the Git host account is not linked")
)

// Bounds of a link and of a link request.
const (
	// RequestLifetime is how long a person has from the start of a link request
	// to its accept. The proof of the account must come inside this time too.
	RequestLifetime = 5 * time.Minute
	// MinTokenLen is the minimum length of the one-time token of a link request.
	// The server makes it from at least 32 random bytes.
	MinTokenLen = 43
	// MaxTokenLen caps a token, so that a hash input stays small.
	MaxTokenLen = 512
	// MaxOpenRequests is the most link requests of one principal that can still
	// be accepted at one time.
	MaxOpenRequests = 10
	// MaxIDLen caps an account ID, a host, a login and a delivery ID.
	MaxIDLen = 200
)

// Account names one account of a Git host. Kind is the kind of the Git host,
// Host is the host name of its API in lowercase letters, with a port when the
// port is not the default, and ID is the stable ID that the Git host gives the
// account. Each field is exact: two IDs that differ in one character, or only in
// case, are two accounts, and a host in other letters is another host.
type Account struct {
	Kind forge.Kind
	Host string
	ID   string
}

// Validate returns an error that wraps ErrInvalid when a is not an account. It
// does not change a value to make it valid.
func (a Account) Validate() error {
	switch {
	case a.Kind != forge.KindGitHub && a.Kind != forge.KindGitLab:
		return fmt.Errorf("%w: the kind of the Git host is not known", ErrInvalid)
	case !validHost(a.Host):
		return fmt.Errorf("%w: the host is a name of lowercase letters, digits, \".\" and \"-\", with an optional port",
			ErrInvalid)
	case !validText(a.ID):
		return fmt.Errorf("%w: the ID of an account has 1 to %d visible characters and no space", ErrInvalid, MaxIDLen)
	default:
		return nil
	}
}

// validHost reports whether h is a host name that is already in its one form.
func validHost(h string) bool {
	name, port, hasPort := strings.Cut(h, ":")
	if len(h) > MaxIDLen || !validHostName(name) {
		return false
	}

	return !hasPort || validPort(port)
}

// validHostName reports whether name is a DNS name of lowercase letters, digits,
// "." and "-" that does not start with "." or "-".
func validHostName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
		return false
	}

	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}

	return true
}

// validPort reports whether port is a port number with no leading zero.
func validPort(port string) bool {
	const maxPortDigits = 5

	if port == "" || len(port) > maxPortDigits || port[0] == '0' {
		return false
	}

	for i := range len(port) {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}

	return true
}

// validText reports whether s is valid UTF-8 of 1 to MaxIDLen bytes with no
// space, no control character and no hidden rune.
func validText(s string) bool {
	if s == "" || len(s) > MaxIDLen || !utf8.ValidString(s) {
		return false
	}

	return !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || audit.IsHiddenRune(r)
	})
}

// validLogin reports whether login is text for a person to read. An empty login
// is valid: some Git hosts give none.
func validLogin(login string) bool {
	return login == "" || validText(login)
}

// Link maps an account of a Git host to a principal. Login is the login that
// the account had when the link was made, for a person to read. It is never
// compared.
type Link struct {
	Account   Account
	Login     string
	Principal string
	LinkedAt  time.Time
}

// Validate returns an error that wraps ErrInvalid for a link with an account
// that fails Validate, a login that is not valid text, no principal, or no time.
func (l Link) Validate() error {
	if err := l.Account.Validate(); err != nil {
		return err
	}

	if !validLogin(l.Login) || !validText(l.Principal) || l.LinkedAt.IsZero() {
		return fmt.Errorf("%w: a link names an account, a principal and a time", ErrInvalid)
	}

	return nil
}

// Proof is what a signed event of a Git host says about the sender of a comment
// that carries a link token. Only the code that checked the signature of the
// event builds it, with the account from the event. DeliveryID is the ID that
// the Git host gave the delivery, for the record.
type Proof struct {
	Account    Account
	Login      string
	DeliveryID string
}

// Validate returns an error that wraps ErrInvalid for a proof with an account
// that fails Validate, a login that is not valid text, or no delivery ID.
func (p Proof) Validate() error {
	if err := p.Account.Validate(); err != nil {
		return err
	}

	if !validLogin(p.Login) || !validText(p.DeliveryID) {
		return fmt.Errorf("%w: a proof names an account and a delivery", ErrInvalid)
	}

	return nil
}

// Request is a one-time request to link an account of a Git host to the
// principal that started it. The store keeps only TokenSHA256, the SHA-256 of
// the token, so a copy of the database gives no token. Principal is the
// principal of the signed-in person who started the request, set by the server
// and never by a client. The account comes later, from a proof: Proved holds it
// once a signed event of the Git host carried the token. A request is good for
// one accept, until ExpiresAt. UsedAt is the time of the accept.
type Request struct {
	TokenSHA256 string
	Principal   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Proved      Proof
	ProvedAt    time.Time
	UsedAt      time.Time
}

// HashToken returns the SHA-256 of token in lowercase hexadecimal, as
// Request.TokenSHA256 holds it.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// ValidToken reports whether token has a length that the server makes.
func ValidToken(token string) bool {
	return len(token) >= MinTokenLen && len(token) <= MaxTokenLen
}

// NewRequest returns the request of token for principal at now. It returns an
// error that wraps ErrInvalid for a token of a length that the server does not
// make, or a principal that is not valid.
func NewRequest(token, principal string, now time.Time) (Request, error) {
	if !ValidToken(token) {
		return Request{}, fmt.Errorf("%w: a link token has %d to %d characters", ErrInvalid, MinTokenLen, MaxTokenLen)
	}

	r := Request{
		TokenSHA256: HashToken(token), Principal: principal, CreatedAt: now, ExpiresAt: now.Add(RequestLifetime),
		Proved:   Proof{Account: Account{Kind: "", Host: "", ID: ""}, Login: "", DeliveryID: ""},
		ProvedAt: time.Time{}, UsedAt: time.Time{},
	}

	return r, r.Validate()
}

// Validate returns an error that wraps ErrInvalid for a request with a hash
// that is not a SHA-256 in lowercase hexadecimal, a principal that is not
// valid, times that do not match RequestLifetime, or a proof that is only half
// there.
func (r Request) Validate() error {
	b, err := hex.DecodeString(r.TokenSHA256)

	switch {
	case err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != r.TokenSHA256:
		return fmt.Errorf("%w: a link request holds the SHA-256 of its token", ErrInvalid)
	case !validText(r.Principal):
		return fmt.Errorf("%w: a link request names the principal that started it", ErrInvalid)
	case r.CreatedAt.IsZero() || !r.ExpiresAt.Equal(r.CreatedAt.Add(RequestLifetime)):
		return fmt.Errorf("%w: a link request expires %s after its start", ErrInvalid, RequestLifetime)
	case r.ProvedAt.IsZero() != (r.Proved.Account == Account{Kind: "", Host: "", ID: ""}):
		return fmt.Errorf("%w: a proof has an account and a time, or neither", ErrInvalid)
	case !r.ProvedAt.IsZero():
		return r.Proved.Validate()
	default:
		return nil
	}
}

// IsProved reports whether a Git host account proved itself for r.
func (r Request) IsProved() bool {
	return !r.ProvedAt.IsZero()
}

// Check returns nil when r can still take a proof or an accept at now. It
// returns ErrUsed for a request with an accept, and ErrExpired at and after its
// expiry.
func (r Request) Check(now time.Time) error {
	switch {
	case !r.UsedAt.IsZero():
		return ErrUsed
	case !now.Before(r.ExpiresAt):
		return ErrExpired
	default:
		return nil
	}
}

// CheckAccept returns nil when principal can accept r at now. It applies Check,
// then requires the principal that started r (ErrDenied) and a proof
// (ErrNotProved). Every store adapter calls it in the transaction of its
// accept, over the row that it read there, so all adapters apply one rule.
func (r Request) CheckAccept(principal string, now time.Time) error {
	if err := r.Check(now); err != nil {
		return err
	}

	// Defense in depth: the service and the route refuse the other principal
	// too. This check stays so that a new caller of the store cannot make a
	// link of a request that another person started.
	if principal == "" || r.Principal != principal {
		return ErrDenied
	}

	if !r.IsProved() {
		return ErrNotProved
	}

	return nil
}

// LinkOf returns the link that an accept of r makes at now. It is the zero link
// for a request that no account proved.
func (r Request) LinkOf(now time.Time) Link {
	return Link{Account: r.Proved.Account, Login: r.Proved.Login, Principal: r.Principal, LinkedAt: now}
}

// Started is a link request that a person started. Token is the one-time token.
// It is shown once and never stored.
type Started struct {
	Token   string
	Request Request
}

// View is what the accept page shows: the request, and the link that the
// account that proved itself has now. HasCurrent is false when the account has
// no link. CurrentName is the display name of the principal of that link.
type View struct {
	Request     Request
	Current     Link
	HasCurrent  bool
	CurrentName string
}
