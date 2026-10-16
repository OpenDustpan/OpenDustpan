package accesstoken

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Store is the persistence port for access tokens. Tokens belong to the
// installation, so no write carries a namespace fence.
//
// Each change takes the audit entry of the change, in the scope
// audit.ServerScope, and appends it in the same transaction as the change
// (ADR 60, fail closed): both commit, or neither. A change returns
// audit.ErrInvalidEntry, and changes nothing, when the entry fails
// validation or names another scope. The time of the change is e.At.
//
// A store keeps the digest of each secret, never the secret.
type Store interface {
	TokenStore
	PolicyStore
}

// TokenStore keeps the token records.
type TokenStore interface {
	// CreateToken persists t with the digest d of its secret. It returns
	// ErrAlreadyExists when the ID or the digest is taken, and
	// ErrPrincipalNotFound when the principal t.PrincipalID does not exist
	// or was deleted.
	CreateToken(ctx context.Context, t Token, d Digest, e audit.Entry) error
	// GetToken returns the token with the ID id, or ErrNotFound.
	GetToken(ctx context.Context, id string) (Token, error)
	// GetTokenByDigest returns the token whose secret has the digest d, with
	// its owner, the stored digest and the token policy of the
	// installation, read in one lookup, so a policy change applies to the
	// next lookup on every instance. It returns ErrNotFound when no token
	// has the digest, or when the owner was deleted. A disabled owner comes
	// back with its disable time.
	GetTokenByDigest(ctx context.Context, d Digest) (Match, error)
	// ListTokens returns the tokens that match q, newest first: the rows of
	// q and the count of every row that matches the filters of q, with no
	// regard to the cursor, read in one transaction.
	ListTokens(ctx context.Context, q Query) ([]Token, int64, error)
	// RevokeToken sets the revoke time of the token id to e.At. It returns
	// ErrNotFound when no such token exists, and ErrEnded when it was
	// revoked.
	RevokeToken(ctx context.Context, id string, e audit.Entry) error
	// RotateToken ends the token r.OldID and issues r.Next with the digest
	// r.Digest in one transaction. It sets the expiry of the old token to
	// r.OldEndsAt when that is earlier, and its ReplacedBy to r.Next.ID. It
	// returns ErrNotFound when no token r.OldID exists, ErrEnded when it is
	// not active at e.At or a rotation replaced it, and the errors of
	// CreateToken for r.Next.
	RotateToken(ctx context.Context, r Replacement, e audit.Entry) error
	// TouchToken sets the last-used time of the token id to at, when no
	// time is set or the set time is at or before at minus every. A touch
	// writes no audit entry. It returns nil when the token does not exist
	// or the time is recent.
	TouchToken(ctx context.Context, id string, at time.Time, every time.Duration) error
}

// PolicyStore keeps the token life cycle policy and runs the inactivity
// disable.
type PolicyStore interface {
	// GetPolicy returns the policy of the installation: the seeded
	// DefaultPolicy until a change.
	GetPolicy(ctx context.Context) (Policy, error)
	// UpdatePolicy reads the policy, calls update with it, and writes the
	// policy and the audit entry that update returns, in one transaction
	// that holds the lock of the audit scope @server: two changes run one
	// after the other, and each one sees the result of the one before. An
	// error of update ends the transaction with no change, and UpdatePolicy
	// returns it as it is. It returns the stored policy.
	UpdatePolicy(ctx context.Context, update PolicyUpdate) (Policy, error)
	// SweepInactive disables the inactive tokens and user accounts of s in
	// one transaction, at the time of the database clock. It skips a token
	// that is revoked, disabled or expired at that time, and a principal
	// that is deleted or disabled. It appends one audit entry for each
	// record it disables, made by entry from the time of the run and the
	// audit object of the record (TokenRef for a token, principal.Ref for a
	// principal), in the same transaction: all of them commit, or none. It
	// returns the time of the run and what it disabled.
	SweepInactive(ctx context.Context, s Sweep, entry SweepEntryFunc) (SweepResult, error)
}

// PolicyUpdate returns the new policy and the audit entry of a change, from
// the policy that the store holds. An error refuses the change.
type PolicyUpdate func(old Policy) (Policy, audit.Entry, error)

// SweepEntryFunc returns the audit entry of one record that a sweep
// disables, from the time of the run and the audit object of the record.
type SweepEntryFunc func(at time.Time, object string) audit.Entry

// Match is the answer of a lookup by digest: the token, its owner, the
// digest that the store keeps, and the token policy of the installation,
// read in the same lookup.
type Match struct {
	Token  Token
	Owner  Owner
	Digest Digest
	Policy Policy
}

// Replacement is one rotation as a store writes it: the old token, its new
// end time, and the new token with the digest of its secret.
type Replacement struct {
	OldEndsAt time.Time
	OldID     string
	Next      Token
	Digest    Digest
}

// Query is a page request as a store reads it. PrincipalID keeps the tokens
// of one principal, or of every principal when it is empty. Filter keeps
// the tokens by their status at the time At: active (not revoked, not
// disabled, and with an expiry after At), expiring (active, and with an
// expiry at or before At plus ExpiringWithin), or ended (revoked, disabled,
// or with an expiry at or before At). FilterAll keeps every token. When HasAfter is set, the rows come after the
// row with the creation time AfterCreatedAt and the ID AfterID in the order
// newest first: an earlier creation time, or the same time and a smaller
// ID. Limit bounds the rows.
type Query struct {
	At             time.Time
	AfterCreatedAt time.Time
	PrincipalID    string
	AfterID        string
	Filter         Filter
	Limit          int
	HasAfter       bool
}
