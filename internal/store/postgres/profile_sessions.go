package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

var _ profile.Sessions = (*Sessions)(nil)

// Sessions is the store of open sessions on the database of a Store. It
// implements profile.Sessions. A separate type keeps its methods apart from
// the methods of the other ports of the Store.
type Sessions struct {
	store *Store
}

// Sessions returns the store of open sessions on s.
func (s *Store) Sessions() *Sessions {
	return &Sessions{store: s}
}

// Put implements profile.Sessions.
func (p *Sessions) Put(
	ctx context.Context, sess profile.Session, fence namespace.Fence, e []audit.Entry,
) error {
	if err := checkSessionPut(sess, fence, e); err != nil {
		return err
	}

	written := profile.SessionWritten{Session: sess}

	return p.inAuditedTx(ctx, fence, e, &written, func(q *db.Queries) error {
		return insertOpenSession(ctx, q, sess)
	})
}

// checkSessionPut checks the entries, the session and the fence of a Put
// before it writes.
func checkSessionPut(sess profile.Session, fence namespace.Fence, e []audit.Entry) error {
	if len(e) == 0 {
		return fmt.Errorf("%w: a write needs at least one audit entry", profile.ErrInvalid)
	}

	for n := range e {
		if err := e[n].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[n].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, e[n].Namespace, fence.Namespace)
		}
	}

	if err := sess.Valid(); err != nil {
		return err
	}

	if sess.Namespace != fence.Namespace {
		return fmt.Errorf("%w: session names %q, fence names %q", profile.ErrInvalid, sess.Namespace, fence.Namespace)
	}

	return nil
}

// insertOpenSession stores sess in the transaction of q: it reads the live root
// and counts the live sessions of the owner before the insert.
func insertOpenSession(ctx context.Context, q *db.Queries, sess profile.Session) error {
	nsID, err := profileNamespaceID(ctx, q, sess.Namespace)
	if err != nil {
		return err
	}

	if _, err = liveProfile(ctx, q, nsID, sess.Root); err != nil {
		return fmt.Errorf("root profile %s: %w", sess.Root, err)
	}

	live, err := q.CountLiveProfileOpenSessions(ctx, db.CountLiveProfileOpenSessionsParams{
		NamespaceID: nsID, OwnerKind: string(sess.Owner.Kind), OwnerID: sess.Owner.ID,
		NowMs: codec.Millis(sess.CreatedAt),
	})
	if err != nil {
		return fmt.Errorf("count open sessions: %w", err)
	}

	if live >= profile.MaxLiveSessions {
		return fmt.Errorf("%w: owner %s holds %d live sessions",
			profile.ErrSessionLimit, sess.Owner.ID, profile.MaxLiveSessions)
	}

	paths, err := encodeSessionPaths(sess.Paths)
	if err != nil {
		return err
	}

	err = q.InsertProfileOpenSession(ctx, db.InsertProfileOpenSessionParams{
		NamespaceID: nsID, ID: sess.ID, RootProfileID: string(sess.Root),
		OwnerKind: string(sess.Owner.Kind), OwnerID: sess.Owner.ID, Consumer: sess.Consumer, Paths: paths,
		Sealed: sess.Sealed, CreatedAtMs: codec.Millis(sess.CreatedAt), ExpiresAtMs: codec.Millis(sess.ExpiresAt),
	})
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: session %s is in use", profile.ErrConflict, sess.ID)
	}

	if err != nil {
		return fmt.Errorf("insert open session: %w", err)
	}

	return nil
}

// Get implements profile.Sessions.
func (p *Sessions) Get(
	ctx context.Context, ns namespace.Name, id string, owner profile.SessionOwner, now time.Time,
) (profile.Session, error) {
	var out profile.Session

	err := p.read(ctx, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, ns)
		if err != nil {
			return err
		}

		row, err := q.GetProfileOpenSession(ctx, db.GetProfileOpenSessionParams{
			NamespaceID: nsID, ID: id, OwnerKind: string(owner.Kind), OwnerID: owner.ID,
			NowMs: codec.Millis(now),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return profile.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select open session: %w", err)
		}

		out, err = decodeSessionRow(ns, row)

		return err
	})
	if err != nil {
		return profile.Session{}, err
	}

	return out, nil
}

// Delete implements profile.Sessions. It removes the session of the owner,
// and answers ErrNotFound for a session that does not exist, also for an
// ended session that a sweep has not removed yet, which it removes.
func (p *Sessions) Delete(
	ctx context.Context, ns namespace.Name, id string, owner profile.SessionOwner,
) error {
	var ends int64

	err := p.store.inTx(ctx, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, ns)
		if err != nil {
			return err
		}

		ends, err = q.DeleteProfileOpenSession(ctx, db.DeleteProfileOpenSessionParams{
			NamespaceID: nsID, ID: id, OwnerKind: string(owner.Kind), OwnerID: owner.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return profile.ErrNotFound
		}

		return err
	})
	if err != nil {
		return err
	}

	if ends <= time.Now().UTC().UnixMilli() {
		return profile.ErrNotFound
	}

	return nil
}

// Sweep implements profile.Sessions.
func (p *Sessions) Sweep(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: sweep limit must be 1 or more", profile.ErrInvalid)
	}

	var deleted int64

	err := p.store.inTx(ctx, func(q *db.Queries) error {
		n, err := q.DeleteEndedProfileOpenSessions(ctx, db.DeleteEndedProfileOpenSessionsParams{
			NowMs: codec.Millis(now), PageLimit: int32(min(max(limit, 0), math.MaxInt32)),
		})
		if err != nil {
			return fmt.Errorf("delete ended open sessions: %w", err)
		}

		deleted = n

		return nil
	})
	if err != nil {
		return 0, err
	}

	return int(deleted), nil
}

// encodeSessionPaths encodes paths as the JSON array of the paths column.
// Empty means all paths.
func encodeSessionPaths(paths []string) (string, error) {
	if paths == nil {
		paths = []string{}
	}

	text, err := json.Marshal(paths)
	if err != nil {
		return "", fmt.Errorf("%w: encode session paths: %w", profile.ErrInvalid, err)
	}

	return string(text), nil
}

// decodeSessionRow answers the session of row.
func decodeSessionRow(ns namespace.Name, row db.ProfileOpenSession) (profile.Session, error) {
	var paths []string

	if err := json.Unmarshal([]byte(row.Paths), &paths); err != nil {
		return profile.Session{}, fmt.Errorf("%w: stored session holds no paths: %w", profile.ErrInvalid, err)
	}

	return profile.Session{
		ID: row.ID, Namespace: ns, Root: profile.ID(row.RootProfileID),
		Owner: profile.SessionOwner{Kind: profile.OwnerKind(row.OwnerKind), ID: row.OwnerID}, Consumer: row.Consumer,
		Paths: paths, Sealed: row.Sealed, CreatedAt: codec.FromMillis(row.CreatedAtMs),
		ExpiresAt: codec.FromMillis(row.ExpiresAtMs),
	}, nil
}

// read runs fn in one read-only snapshot.
func (p *Sessions) read(ctx context.Context, fn func(q *db.Queries) error) error {
	return p.store.inSnapshotTx(ctx, fn)
}

// inAuditedTx runs fn and appends each entry of es in the same fenced
// transaction. It checks every entry before the transaction starts. When ctx
// carries an idempotency.Completion, the transaction also stores its answer
// for written, which fn fills before it returns.
func (p *Sessions) inAuditedTx(
	ctx context.Context, fence namespace.Fence, es []audit.Entry, written *profile.SessionWritten,
	fn func(q *db.Queries) error,
) error {
	// The store appends the last entry after fn, so fn appends the others.
	last := es[len(es)-1]

	return p.store.inFencedAuditedTx(ctx, fence, &last, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		for n := range len(es) - 1 {
			if _, err := appendAuditEntry(ctx, q, es[n]); err != nil {
				return fmt.Errorf("append audit entry: %w", err)
			}
		}

		return finishCompletion(ctx, q, *written)
	})
}
