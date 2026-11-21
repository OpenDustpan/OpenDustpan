package profile

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Limits of open sessions.
const (
	// SessionIDPrefix starts the ID of every open session.
	SessionIDPrefix = "pos-"
	// MaxLiveSessions is the largest number of live open sessions of one
	// owner.
	MaxLiveSessions = 100
	// MinSessionDuration is the shortest duration of an open session.
	MinSessionDuration = time.Minute
	// MaxSessionDuration is the longest duration of an open session.
	MaxSessionDuration = 12 * time.Hour
	// DefaultSessionDuration is the duration of an open session when the
	// request names none.
	DefaultSessionDuration = 2 * time.Hour
)

// ErrSessionLimit reports an open above MaxLiveSessions live sessions of
// one owner. It wraps ErrLimit.
var ErrSessionLimit = fmt.Errorf("%w: open session limit reached", ErrLimit)

// OwnerKind says what kind of credential owns an open session.
type OwnerKind string

// The owner kinds of an open session.
const (
	// OwnerPrincipal is a principal that opened with its own credential.
	// Until job opens are specified, it is the only kind.
	OwnerPrincipal OwnerKind = "principal"
)

// SessionOwner names the credential that opened a session. Only the same
// owner reads the session.
type SessionOwner struct {
	Kind OwnerKind
	// ID is the subject of a principal.
	ID string
}

// Session is one open session as the store keeps it. Sealed holds the values
// of the open, sealed with the data key of the root profile. The store never
// sees a plain value.
type Session struct {
	// ID has the prefix SessionIDPrefix.
	ID        string
	Namespace namespace.Name
	// Root is the profile that the caller opened.
	Root ID
	// Owner is the only credential that reads the session.
	Owner SessionOwner
	// Consumer is the request that made the session: api, console or pulumi.
	Consumer string
	// Paths are the paths that the open answered. Empty means all.
	Paths []string
	// Sealed is the sealed answer of the open.
	Sealed    []byte
	CreatedAt time.Time
	// ExpiresAt is the end of the session.
	ExpiresAt time.Time
}

// Live reports whether the session has not ended at now.
func (s Session) Live(now time.Time) bool { return now.Before(s.ExpiresAt) }

// Valid reports whether s passes the rules that a store never takes a
// session that fails. A session that fails reads as ErrInvalid.
func (s Session) Valid() error {
	if len(s.ID) <= len(SessionIDPrefix) || len(s.ID) > maxIDLength {
		return fmt.Errorf("%w: session ID %q must start with %q", ErrInvalid, s.ID, SessionIDPrefix)
	}

	if s.ID[:len(SessionIDPrefix)] != SessionIDPrefix {
		return fmt.Errorf("%w: session ID %q must start with %q", ErrInvalid, s.ID, SessionIDPrefix)
	}

	if s.Owner.Kind != OwnerPrincipal || s.Owner.ID == "" {
		return fmt.Errorf("%w: session owner must be a principal with an ID", ErrInvalid)
	}

	if len(s.Sealed) == 0 {
		return fmt.Errorf("%w: session holds no sealed answer", ErrInvalid)
	}

	if !s.ExpiresAt.After(s.CreatedAt) {
		return fmt.Errorf("%w: session end must be after its start", ErrInvalid)
	}

	return nil
}

// SessionWritten is what Sessions.Put passes to the idempotency.Completion
// of its context: the session that it stored. A Put that finds no Completion
// passes nothing.
type SessionWritten struct {
	Session Session
}

// Sessions persists open sessions.
//
// Put takes the fence of the namespace and the audit entries of the write.
// The store checks the fence, the limit and the ID, stores the session and
// appends each entry, in order, in one transaction: all commit, or none. When
// the context carries an idempotency.Completion, Put stores the answer of the
// request in that transaction and passes a SessionWritten, and returns
// idempotency.ErrLost, changing nothing, when the pending record is not in
// place.
//
// A session belongs to one namespace and one owner. An unknown session, a
// session of another owner or namespace, and a session that ended all read
// as ErrNotFound, so a caller learns nothing about a session of another
// owner.
//
// Sessions are not in a backup (backup.EmptiedTables).
type Sessions interface {
	// Put stores s. It returns namespace.ErrFenced for a stale fence,
	// namespace.ErrNotFound for an unknown namespace, ErrSessionLimit when
	// the owner already holds MaxLiveSessions live sessions at s.CreatedAt,
	// ErrConflict for an ID in use, audit.ErrInvalidEntry for an entry that
	// fails validation or names another namespace, and ErrInvalid for a
	// session that fails these rules: an ID without the prefix, an empty
	// Sealed, an ExpiresAt that is not after CreatedAt, an owner without an
	// ID or a kind other than principal, or an empty slice of entries. It
	// changes nothing on an error.
	Put(ctx context.Context, s Session, fence namespace.Fence, e []audit.Entry) error
	// Get reads the live session id of ns that owner opened, at now.
	// ErrNotFound otherwise.
	Get(ctx context.Context, ns namespace.Name, id string, owner SessionOwner, now time.Time) (Session, error)
	// Delete removes the session id of ns that owner opened. It returns
	// ErrNotFound for a session that does not exist, also for an ended
	// session that a sweep has not removed yet, which it removes.
	Delete(ctx context.Context, ns namespace.Name, id string, owner SessionOwner) error
	// Sweep deletes at most limit sessions that ended at or before now, in
	// any namespace, and returns how many it deleted. ErrInvalid for a
	// limit below 1.
	Sweep(ctx context.Context, now time.Time, limit int) (int, error)
}
