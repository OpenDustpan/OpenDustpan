package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
)

var _ profile.Sessions = (*ProfileSessions)(nil)

// ProfileSessions is the store of open sessions on the database of a Store.
// It implements profile.Sessions. The table holds only the sealed result.
type ProfileSessions struct {
	store *Store
}

// ProfileSessions returns the store of open sessions on s.
func (s *Store) ProfileSessions() *ProfileSessions {
	return &ProfileSessions{store: s}
}

// Put implements profile.Sessions. The transaction holds the fence of the
// namespace, so the count of live sessions and the insert are one step.
func (p *ProfileSessions) Put(
	ctx context.Context, sess profile.Session, fence namespace.Fence, e []audit.Entry,
) error {
	// Defense in depth: profile.NewSession checks a session, and only the zero
	// value skips it. This check stays so that a store never writes a row of
	// the zero value.
	if sess.ID() == "" || sess.Namespace() != fence.Namespace {
		return fmt.Errorf("%w: the session is not valid for the namespace of the fence",
			profile.ErrInvalid)
	}

	paths, err := codec.EncodeSessionPaths(sess.Paths())
	if err != nil {
		return err
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	return p.store.inAuditedWrite(ctx, fence, e, func() any { return profile.SessionWritten{Session: sess} },
		func(q *db.Queries) error {
			nsID, nsErr := profileNamespaceID(ctx, q, sess.Namespace())
			if nsErr != nil {
				return nsErr
			}

			if _, rootErr := liveProfile(ctx, q, nsID, sess.Root()); rootErr != nil {
				return rootErr
			}

			owner := sess.Owner()

			live, countErr := q.CountLiveProfileOpenSessions(ctx, db.CountLiveProfileOpenSessionsParams{
				NamespaceID: nsID, OwnerKind: string(owner.Kind), OwnerID: owner.ID,
				NowMs: codec.Millis(sess.CreatedAt()),
			})
			if countErr != nil {
				return fmt.Errorf("count open sessions: %w", countErr)
			}

			if live >= profile.MaxLiveSessions {
				return profile.ErrSessionLimit
			}

			insertErr := q.InsertProfileOpenSession(ctx, db.InsertProfileOpenSessionParams{
				NamespaceID: nsID, ID: sess.ID(), RootProfileID: string(sess.Root()), OwnerKind: string(owner.Kind),
				OwnerID: owner.ID, Consumer: string(sess.Consumer()), Paths: paths, Sealed: sess.Sealed(),
				CreatedAtMs: codec.Millis(sess.CreatedAt()), ExpiresAtMs: codec.Millis(sess.ExpiresAt()),
			})
			if isUniqueViolation(insertErr) {
				return fmt.Errorf("%w: the session ID %q is in use", profile.ErrConflict, sess.ID())
			}

			if insertErr != nil {
				return fmt.Errorf("insert open session: %w", insertErr)
			}

			return nil
		})
}

// checkSessionKey returns profile.ErrNotFound for an ID that is not a session
// ID and for an owner that cannot own a session. A caller learns nothing from
// the text of the error.
func checkSessionKey(id string, owner profile.SessionOwner) error {
	// Defense in depth: the query binds each value as a parameter, and the
	// service checks the ID too. This check stays so that a hostile ID never
	// reaches the database.
	if _, err := profile.ParseSessionID(id); err != nil {
		return profile.ErrNotFound
	}

	if err := owner.Valid(); err != nil {
		return profile.ErrNotFound
	}

	return nil
}

// Get implements profile.Sessions.
func (p *ProfileSessions) Get(
	ctx context.Context, ns namespace.Name, id string, owner profile.SessionOwner, now time.Time,
) (profile.Session, error) {
	if err := checkSessionKey(id, owner); err != nil {
		return profile.Session{}, err
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var (
		nsID string
		row  db.ProfileOpenSession
	)

	err := p.store.inReadTx(ctx, func(q *db.Queries) error {
		var err error

		nsID, err = profileNamespaceID(ctx, q, ns)
		if err != nil {
			return err
		}

		row, err = q.GetProfileOpenSession(ctx, db.GetProfileOpenSessionParams{
			NamespaceID: nsID, ID: id, OwnerKind: string(owner.Kind), OwnerID: owner.ID, NowMs: codec.Millis(now),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return profile.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select open session: %w", err)
		}

		return nil
	})
	if err != nil {
		return profile.Session{}, err
	}

	return sessionFromRow(sessionReadKey{ns: ns, nsID: nsID, id: id, owner: owner, now: now}, row)
}

// sessionReadKey is the key of one read of Get: the namespace by name and by
// stable ID, the ID of the session, the owner and the time of the read.
type sessionReadKey struct {
	ns    namespace.Name
	nsID  string
	id    string
	owner profile.SessionOwner
	now   time.Time
}

// sessionFromRow decodes the row that the query of Get found, and checks it
// against the key of the read. A row of another namespace, of another owner or
// of an ended session is profile.ErrNotFound. A row that fails the rules of
// profile.NewSession is an error that wraps profile.ErrInvalid.
func sessionFromRow(k sessionReadKey, row db.ProfileOpenSession) (profile.Session, error) {
	// Defense in depth: the query holds the namespace, the ID, the owner and
	// the clock. These checks stay so that a row of another namespace, of
	// another owner or of an ended session never reaches a caller if the
	// query changes.
	if row.NamespaceID != k.nsID {
		return profile.Session{}, profile.ErrNotFound
	}

	sess, err := codec.ProfileSessionFromRow(k.ns, codec.ProfileOpenSessionRow(row))
	if err != nil {
		return profile.Session{}, err
	}

	if sess.ID() != k.id || sess.Owner() != k.owner || !sess.Live(k.now) {
		return profile.Session{}, profile.ErrNotFound
	}

	return sess, nil
}

// Delete implements profile.Sessions. The namespace is the namespace of the
// fence.
func (p *ProfileSessions) Delete(
	ctx context.Context, id string, owner profile.SessionOwner, fence namespace.Fence, e []audit.Entry,
) error {
	if err := checkSessionKey(id, owner); err != nil {
		return err
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	return p.store.inAuditedWrite(ctx, fence, e, func() any { return nil }, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, fence.Namespace)
		if err != nil {
			return err
		}

		n, err := q.DeleteProfileOpenSession(ctx, db.DeleteProfileOpenSessionParams{
			NamespaceID: nsID, ID: id, OwnerKind: string(owner.Kind), OwnerID: owner.ID,
		})
		if err != nil {
			return fmt.Errorf("delete open session: %w", err)
		}

		if n == 0 {
			return profile.ErrNotFound
		}

		return nil
	})
}

// Sweep implements profile.Sessions.
func (p *ProfileSessions) Sweep(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: a sweep deletes 1 or more sessions", profile.ErrInvalid)
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var n int64

	err := p.store.inTx(ctx, func(q *db.Queries) error {
		var err error

		n, err = q.DeleteEndedProfileOpenSessions(ctx, db.DeleteEndedProfileOpenSessionsParams{
			NowMs: codec.Millis(now), PageLimit: int64(limit),
		})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("sweep open sessions: %w", err)
	}

	return int(n), nil
}
