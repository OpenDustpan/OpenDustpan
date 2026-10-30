package forgelink

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Acceptance is what a person gives to Store.AcceptRequest. TokenSHA256 names
// the request, Principal is the principal that accepts, Previous is the
// principal of the current link of the account that the accept page showed, or
// empty, and At is the time of the accept.
type Acceptance struct {
	TokenSHA256 string
	Principal   string
	Previous    string
	At          time.Time
}

// Accepted is the result of an accept: the link that it made, and the principal
// that lost a link, or empty.
type Accepted struct {
	Link     Link
	Replaced string
}

// Store keeps the links and the link requests. A link and a link request are
// rows of the installation, not of a namespace: a person has one principal for
// every namespace. So a write takes no fence. A write that changes access
// appends its audit entry e in its transaction, in the scope audit.ServerScope.
// It returns audit.ErrInvalidEntry for an entry that fails validation or that
// names another scope. A failed write appends nothing and changes nothing.
type Store interface {
	// CreateRequest stores r. It returns ErrInvalid for a request that fails
	// Validate or that has a proof or an accept, ErrConflict for a request with
	// the hash of a stored one, and ErrLimit when the principal of r has
	// MaxOpenRequests requests that can still be accepted at the time of r. In the
	// same transaction it deletes the requests of that principal that expired
	// before the time of r, so the table never grows without a bound.
	CreateRequest(ctx context.Context, r Request) error
	// GetRequest returns the request with the token hash tokenSHA256, or
	// ErrNotFound.
	GetRequest(ctx context.Context, tokenSHA256 string) (Request, error)
	// ProveRequest records the proof p on the request with the token hash
	// tokenSHA256 at now, once, in one transaction with a check of the request,
	// and returns the request with its proof. It returns ErrInvalid for a proof
	// that fails Validate, ErrNotFound, ErrUsed, ErrExpired after the expiry of
	// the request, and ErrConflict when another account proved the request
	// already. A repeat of the proof of the same account changes nothing and
	// returns the request, so a Git host that sends an event again is safe. The
	// proof comes from a signed event, and the store trusts the caller for it.
	ProveRequest(ctx context.Context, tokenSHA256 string, p Proof, now time.Time) (Request, error)
	// AcceptRequest makes the link of the request with the token hash
	// a.TokenSHA256 for a.Principal, once, in one transaction. In that transaction it reads the
	// request, applies Request.CheckAccept, checks the current link of the
	// account against previous, stores the link with Request.LinkOf, marks the
	// request as used at a.At, and appends the audit entry e. So one token never
	// makes two links, and a token never gets used without a link.
	//
	// a.Previous is the principal of the current link of the account that the
	// accept page showed to the person, or empty when the page showed no link.
	// AcceptRequest never replaces a link silently: it returns ErrConflict when
	// a.Previous is empty and the account has a link, and when a.Previous is not
	// empty and the stored link is missing or names another principal.
	//
	// It returns ErrNotFound, ErrUsed, ErrExpired, ErrDenied, ErrNotProved, and
	// ErrConflict. Accepted.Replaced is the principal that lost a link, or empty.
	AcceptRequest(ctx context.Context, a Acceptance, e audit.Entry) (Accepted, error)
	// DeleteLink deletes the link of the account a and appends the audit entry
	// e. It returns ErrNotFound for an account with no link and ErrInvalid for an
	// account that fails Validate.
	DeleteLink(ctx context.Context, a Account, e audit.Entry) error
	// LinkOf returns the link of the account a, or ErrNotFound. The fields of a
	// match exactly.
	LinkOf(ctx context.Context, a Account) (Link, error)
	// LinksOf returns the links of the principal, ordered by kind, host and
	// account ID, so a person can see and delete them.
	LinksOf(ctx context.Context, principal string) ([]Link, error)
}
