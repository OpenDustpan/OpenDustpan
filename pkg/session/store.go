package session

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Store is the persistence port for sessions and sign-in requests. Sessions
// belong to the installation, so no write carries a namespace fence.
//
// Each change of a session takes the audit entry of the change, in the scope
// audit.ServerScope, and appends it in the same transaction as the change:
// both commit, or neither. A change returns audit.ErrInvalidEntry, and
// changes nothing, when the entry fails validation or names another scope.
//
// A store keeps the digest of each secret, never the secret.
type Store interface {
	// CreateSession persists s with the digest d of its secret, sets the
	// last sign-in time of its principal to s.CreatedAt when that is later
	// than the one it holds, and appends e, in one transaction. It returns
	// ErrInvalid when s fails validation, ErrAlreadyExists when the ID or
	// the digest is taken, ErrPrincipalNotFound when the principal
	// s.PrincipalID does not exist, was deleted or is not a person, and
	// ErrPrincipalDisabled when it is disabled.
	CreateSession(ctx context.Context, s Session, d Digest, e audit.Entry) error
	// GetSession returns the session with the ID id, or ErrNotFound.
	GetSession(ctx context.Context, id string) (Session, error)
	// GetSessionByDigest returns the session whose secret has the digest
	// d, with its owner (its activity time and its break-glass flag too),
	// the stored digest, the session policy and the account part of the
	// access token policy, all from one read. It returns ErrNotFound when
	// no session has the digest, or when the owner was deleted. An ended
	// session comes back as it is, and a disabled owner comes back with
	// its disable time: the caller decides.
	GetSessionByDigest(ctx context.Context, d Digest) (Match, error)
	// TouchSession sets the last use of the session id to at, when the
	// session has not ended, is not idle or expired under l at that time
	// (see Limits.ActiveAfter), and its last use is at or before at minus
	// every.
	// A touch writes no audit entry. It returns nil when the session does
	// not exist, ended, is idle or expired, or was used recently.
	TouchSession(ctx context.Context, id string, at time.Time, every time.Duration, l Limits) error
	// EndSession sets the end time of the session id to e.At and its end
	// reason to reason, and appends e, in one transaction. It returns
	// ErrInvalid for a reason that ValidEndReason refuses, ErrNotFound when
	// no such session exists, and ErrEnded when it ended.
	EndSession(ctx context.Context, id string, reason EndReason, e audit.Entry) error
	// CreateRequest persists the sign-in request r, keyed by the digest d of
	// its state. It returns ErrInvalid when r fails validation, and
	// ErrAlreadyExists when the digest is taken.
	CreateRequest(ctx context.Context, r Request, d Digest) error
	// TakeRequest deletes the sign-in request with the state digest d and
	// returns it, so that a request works once. It returns ErrNotFound when
	// no request has the digest, or when it expired at or before at; it
	// deletes an expired request too.
	TakeRequest(ctx context.Context, d Digest, at time.Time) (Request, error)
	// PruneRequests deletes the sign-in requests that expired at or before
	// at, and returns how many it deleted.
	PruneRequests(ctx context.Context, at time.Time) (int64, error)
}
