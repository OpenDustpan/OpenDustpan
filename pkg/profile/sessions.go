package profile

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Limits of open sessions.
const (
	// SessionIDPrefix starts the ID of every open session.
	SessionIDPrefix = "pos-"
	// MaxLiveSessions is the largest number of live open sessions of one
	// principal.
	MaxLiveSessions = 100
	// MinSessionDuration is the shortest duration of an open session.
	MinSessionDuration = time.Minute
	// MaxSessionDuration is the longest duration of an open session.
	MaxSessionDuration = 12 * time.Hour
	// DefaultSessionDuration is the duration of an open session when the
	// request names none.
	DefaultSessionDuration = 2 * time.Hour
	// MaxResultBytes is the largest result of one open, before it is sealed.
	MaxResultBytes = 4 << 20
	// MaxSealedBytes is the largest sealed result that a store accepts. It
	// holds the result and the overhead of the seal.
	MaxSealedBytes = MaxResultBytes + 4096
	// MaxSessionPaths is the largest number of paths that one open asks.
	MaxSessionPaths = 1024
	// MaxSessionPathBytes is the longest path that one open asks.
	MaxSessionPathBytes = 1024
	// MaxSessionOwnerBytes is the longest subject of an owner.
	MaxSessionOwnerBytes = 256

	maxSessionIDLength = 64
)

// ErrSessionLimit reports an open above MaxLiveSessions live sessions of one
// owner. It wraps ErrLimit.
var ErrSessionLimit = fmt.Errorf("%w: a principal holds at most %d live open sessions", ErrLimit, MaxLiveSessions)

// OwnerKind says what kind of credential owns an open session. Only a
// principal owns one.
type OwnerKind string

// OwnerPrincipal is a principal that opened with its own credential.
const OwnerPrincipal OwnerKind = "principal"

// SessionOwner names the principal that opened a session. Only the same
// owner reads the session.
type SessionOwner struct {
	Kind OwnerKind
	// ID is the subject of the principal.
	ID string
}

// PrincipalOwner returns the owner of a principal with the subject subject.
func PrincipalOwner(subject string) SessionOwner {
	return SessionOwner{Kind: OwnerPrincipal, ID: subject}
}

// Valid reports why o cannot own a session, or nil. It checks the kind with
// an allowlist and the ID for length and control characters.
func (o SessionOwner) Valid() error {
	if o.Kind != OwnerPrincipal {
		return fmt.Errorf("%w: a session owner is a principal", ErrInvalid)
	}

	if o.ID == "" || len(o.ID) > MaxSessionOwnerBytes || hasControl(o.ID) {
		return fmt.Errorf("%w: a session owner needs a subject of 1 to %d bytes with no control character",
			ErrInvalid, MaxSessionOwnerBytes)
	}

	return nil
}

// OpenConsumer is the consumer of an open that stores its result as a
// session. The other consumers of an open get the result in the answer, and
// the server stores nothing.
type OpenConsumer string

// The consumers that make a session.
const (
	OpenConsumerAPI     OpenConsumer = "api"
	OpenConsumerConsole OpenConsumer = "console"
	OpenConsumerPulumi  OpenConsumer = "pulumi"
)

// ParseOpenConsumer returns s as a consumer that makes a session, or
// ErrInvalid. It uses an allowlist.
func ParseOpenConsumer(s string) (OpenConsumer, error) {
	switch c := OpenConsumer(s); c {
	case OpenConsumerAPI, OpenConsumerConsole, OpenConsumerPulumi:
		return c, nil
	default:
		return "", fmt.Errorf("%w: the consumer %q is not one of api, console and pulumi", ErrInvalid, s)
	}
}

// NewSessionID returns a new open session ID.
func NewSessionID() string { return ident.NewID(SessionIDPrefix) }

// ParseSessionID returns s as an open session ID, or ErrInvalid. It never
// reads s as a path.
func ParseSessionID(s string) (string, error) {
	rest, ok := strings.CutPrefix(s, SessionIDPrefix)
	if !ok || rest == "" || !ident.ValidSlug("a"+rest, maxSessionIDLength) {
		return "", fmt.Errorf("%w: a session ID starts with %q and holds lowercase letters, digits and \"-\"",
			ErrInvalid, SessionIDPrefix)
	}

	return s, nil
}

// SessionParams is the content of a new Session.
type SessionParams struct {
	ID        string
	Namespace namespace.Name
	// Root is the stable ID of the profile that the caller opened.
	Root     ID
	Owner    SessionOwner
	Consumer OpenConsumer
	// Paths are the paths that the open asked. Empty means all.
	Paths []string
	// Sealed holds the values of the open, sealed with the data key of the
	// root profile.
	Sealed    []byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Session is one open session as the store keeps it. Sealed holds the values
// of the open, sealed with the data key of the root profile: the store never
// sees a plain value. Only NewSession makes a Session, and it checks the
// rules below, so a store never gets a session that fails them.
type Session struct {
	id        string
	namespace namespace.Name
	root      ID
	owner     SessionOwner
	consumer  OpenConsumer
	paths     []string
	sealed    []byte
	createdAt time.Time
	expiresAt time.Time
}

// NewSession checks p and returns the session. It returns ErrInvalid for an
// ID without the prefix or outside the alphabet, an unknown namespace name,
// root ID or consumer, an owner that is not a principal with a subject, a
// path that is empty, has a control character or is too long, more than
// MaxSessionPaths paths, an empty Sealed or one above MaxSealedBytes, an end
// that is not after the start, or an end more than MaxSessionDuration after
// the start.
func NewSession(p SessionParams) (Session, error) {
	if _, err := ParseSessionID(p.ID); err != nil {
		return Session{}, err
	}

	if p.Namespace == "" {
		return Session{}, fmt.Errorf("%w: a session names its namespace", ErrInvalid)
	}

	if _, err := ParseID(string(p.Root)); err != nil {
		return Session{}, err
	}

	if err := p.Owner.Valid(); err != nil {
		return Session{}, err
	}

	if _, err := ParseOpenConsumer(string(p.Consumer)); err != nil {
		return Session{}, err
	}

	if err := checkSessionPaths(p.Paths); err != nil {
		return Session{}, err
	}

	if len(p.Sealed) == 0 || len(p.Sealed) > MaxSealedBytes {
		return Session{}, fmt.Errorf("%w: a session holds 1 to %d sealed bytes", ErrInvalid, MaxSealedBytes)
	}

	if !p.ExpiresAt.After(p.CreatedAt) || p.ExpiresAt.Sub(p.CreatedAt) > MaxSessionDuration {
		return Session{}, fmt.Errorf("%w: a session ends after it starts, at most %s later",
			ErrInvalid, MaxSessionDuration)
	}

	return Session{
		id: p.ID, namespace: p.Namespace, root: p.Root, owner: p.Owner, consumer: p.Consumer,
		paths: slices.Clone(p.Paths), sealed: slices.Clone(p.Sealed),
		createdAt: p.CreatedAt.UTC(), expiresAt: p.ExpiresAt.UTC(),
	}, nil
}

func checkSessionPaths(paths []string) error {
	if len(paths) > MaxSessionPaths {
		return fmt.Errorf("%w: an open asks at most %d paths", ErrInvalid, MaxSessionPaths)
	}

	for _, path := range paths {
		if path == "" || len(path) > MaxSessionPathBytes || hasControl(path) {
			return fmt.Errorf("%w: a path has 1 to %d bytes and no control character",
				ErrInvalid, MaxSessionPathBytes)
		}
	}

	return nil
}

// hasControl reports whether s holds a control character, which includes CR
// and LF, or an invalid byte.
func hasControl(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f || r == utf8.RuneError {
			return true
		}
	}

	return false
}

// ID returns the session ID.
func (s Session) ID() string { return s.id }

// Namespace returns the namespace of the session.
func (s Session) Namespace() namespace.Name { return s.namespace }

// Root returns the stable ID of the profile that the caller opened.
func (s Session) Root() ID { return s.root }

// Owner returns the only owner that reads the session.
func (s Session) Owner() SessionOwner { return s.owner }

// Consumer returns the consumer of the open.
func (s Session) Consumer() OpenConsumer { return s.consumer }

// Paths returns a copy of the paths that the open asked. Empty means all.
func (s Session) Paths() []string { return slices.Clone(s.paths) }

// Sealed returns a copy of the sealed result.
func (s Session) Sealed() []byte { return slices.Clone(s.sealed) }

// CreatedAt returns the start of the session.
func (s Session) CreatedAt() time.Time { return s.createdAt }

// ExpiresAt returns the end of the session.
func (s Session) ExpiresAt() time.Time { return s.expiresAt }

// Live reports whether the session has not ended at now. It is the one rule
// of expiry: a session at its end time is over.
func (s Session) Live(now time.Time) bool { return now.Before(s.expiresAt) }

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
	// namespace.ErrNotFound for an unknown namespace, ErrNotFound for a
	// root profile that is not a live profile of the namespace,
	// ErrSessionLimit when the owner already holds MaxLiveSessions live
	// sessions at the start of s, ErrConflict for an ID in use,
	// audit.ErrInvalidEntry for an entry that fails validation or names
	// another namespace, and ErrInvalid for the zero Session and for an
	// empty slice of entries. It changes nothing on an error.
	Put(ctx context.Context, s Session, fence namespace.Fence, e []audit.Entry) error
	// Get reads the live session id of ns that owner opened, at now.
	// ErrNotFound otherwise, also for an id that is not a session ID.
	Get(ctx context.Context, ns namespace.Name, id string, owner SessionOwner, now time.Time) (Session, error)
	// Delete removes the session id that owner opened in the namespace of
	// fence, ended or not, and appends each entry of e in the same
	// transaction, as Put does. It returns ErrNotFound for an id that is not
	// a session ID, for an owner that cannot own a session and for a session
	// that does not exist, belongs to another owner or is in another
	// namespace. It returns namespace.ErrFenced for a stale fence,
	// namespace.ErrNotFound for an unknown namespace, audit.ErrInvalidEntry
	// for an entry that fails validation or names another namespace, and
	// ErrInvalid for an empty slice of entries. It changes nothing on an
	// error, and writes no entry for a session that it does not find.
	Delete(ctx context.Context, id string, owner SessionOwner, fence namespace.Fence, e []audit.Entry) error
	// Sweep deletes at most limit sessions that ended at or before now, in
	// any namespace, and returns how many it deleted. ErrInvalid for a
	// limit below 1. A sweep takes no fence and writes no audit entry: it
	// removes only rows that no read answers, and the open that made each row
	// wrote its entry. The sweep of the replay records works the same way.
	Sweep(ctx context.Context, now time.Time, limit int) (int, error)
}
