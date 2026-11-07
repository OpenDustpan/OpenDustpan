package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/namelockstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ authgate.NameLockCounter = (*Store)(nil)

// nameLockTx is the namelockstore.Tx of one transaction.
type nameLockTx struct {
	q *db.Queries
}

func (t nameLockTx) NowMs(ctx context.Context) (int64, error) {
	now, err := t.q.DatabaseNowMs(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the clock: %w", err)
	}

	return now, nil
}

func (t nameLockTx) Ensure(ctx context.Context, scope, name, sub string) error {
	if err := t.q.EnsureNameLock(ctx, db.EnsureNameLockParams{Scope: scope, Name: name, Sub: sub}); err != nil {
		return fmt.Errorf("insert name count: %w", err)
	}

	return nil
}

func (t nameLockTx) Get(ctx context.Context, scope, name, sub string) (namelockstore.Row, error) {
	r, err := t.q.GetNameLock(ctx, db.GetNameLockParams{Scope: scope, Name: name, Sub: sub})
	if err != nil {
		return namelockstore.Row{}, fmt.Errorf("select name count: %w", err)
	}

	return namelockstore.Row(r), nil
}

func (t nameLockTx) AppendAudit(ctx context.Context, e audit.Entry) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	if _, err := appendAuditEntry(ctx, t.q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

func (t nameLockTx) HasNotification(ctx context.Context, dedupeKey string) (bool, error) {
	_, err := t.q.GetNotificationIDByDedupe(ctx, db.GetNotificationIDByDedupeParams{
		Scope: audit.ServerScope.String(), DedupeKey: dedupeKey,
	})

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("select notification by dedupe key: %w", err)
	}
}

func (t nameLockTx) AppendNotification(ctx context.Context, n notify.Notification) error {
	return appendNotifications(ctx, t.q, audit.ServerScope, []notify.Notification{n})
}

func (t nameLockTx) Set(ctx context.Context, scope, name string, r namelockstore.Row) error {
	err := t.q.SetNameLock(ctx, db.SetNameLockParams{
		Failures: r.Failures, LockedUntilMs: r.LockedUntilMs, Level: r.Level, LastFailureMs: r.LastFailureMs,
		LastLockEndMs: r.LastLockEndMs, ResetAfterMs: r.ResetAfterMs, Scope: scope, Name: name, Sub: r.Sub,
	})
	if err != nil {
		return fmt.Errorf("update name count: %w", err)
	}

	return nil
}

// AddNameFailure implements authgate.NameLockCounter. The whole add runs in
// one write transaction, so concurrent adds count one after the other.
func (s *Store) AddNameFailure(ctx context.Context, f authgate.NameFailure) (authgate.NameLock, error) {
	if err := authgate.CheckLockRequest(f.Scope, f.Name, f.Sub, f.Rule); err != nil {
		return authgate.NameLock{}, err
	}

	if err := f.Events.Validate(); err != nil {
		return authgate.NameLock{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		out    authgate.NameLock
		locked bool
	)

	err := s.inTx(ctx, func(q *db.Queries) error {
		lock, addErr := namelockstore.Add(ctx, nameLockTx{q: q}, f)
		if errors.Is(addErr, authgate.ErrNameLocked) {
			// A locked name writes nothing: the transaction commits no change.
			out, locked = lock, true

			return nil
		}

		out = lock

		return addErr
	})
	if err != nil {
		return authgate.NameLock{}, err
	}

	if locked {
		return out, authgate.ErrNameLocked
	}

	return out, nil
}

// ClearNameFailures implements authgate.NameLockCounter.
func (s *Store) ClearNameFailures(ctx context.Context, scope, name, sub string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.ClearNameLockSub(ctx, db.ClearNameLockSubParams{Scope: scope, Name: name, Sub: sub}); err != nil {
			return fmt.Errorf("clear name count: %w", err)
		}

		return nil
	})
}

// ClearName implements authgate.NameLockCounter. The clear of each scope and
// the append of e run in one transaction.
func (s *Store) ClearName(ctx context.Context, scopes []string, name string, e audit.Entry) (int64, error) {
	if err := authgate.CheckClearRequest(scopes, name); err != nil {
		return 0, err
	}

	if err := validServerEntry(e); err != nil {
		return 0, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		total = 0

		for _, scope := range scopes {
			cleared, err := q.ClearNameLocks(ctx, db.ClearNameLocksParams{Scope: scope, Name: name})
			if err != nil {
				return fmt.Errorf("clear name counts: %w", err)
			}

			total += cleared
		}

		entry := e
		entry.Changes = append(append([]audit.Change{}, e.Changes...), authgate.ClearedChange(total))

		if _, err := appendAuditEntry(ctx, q, entry); err != nil {
			return fmt.Errorf("append the entry of the clear: %w", err)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return total, nil
}

// NameLocks implements authgate.NameLockCounter.
func (s *Store) NameLocks(ctx context.Context, scope, name string) ([]authgate.NameLock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListNameLocks(ctx, db.ListNameLocksParams{Scope: scope, Name: name})
	if err != nil {
		return nil, fmt.Errorf("list name counts: %w", err)
	}

	out := make([]authgate.NameLock, 0, len(rows))
	for _, r := range rows {
		out = append(out, namelockstore.Lock(namelockstore.Row(r)))
	}

	return out, nil
}

// PruneNameLocks implements authgate.NameLockCounter. It measures the age of
// each count, the end of a lock and the reset of a level against now, the
// time that its caller gives. The delete, the entries of the end of the locks
// and the append of e run in one transaction.
func (s *Store) PruneNameLocks(
	ctx context.Context, now time.Time, age time.Duration, limits authgate.PruneLimits, e audit.Entry,
) (int64, error) {
	if err := authgate.CheckPruneRequest(now, age, limits); err != nil {
		return 0, err
	}

	if err := validServerEntry(e); err != nil {
		return 0, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		nowMs := codec.Millis(now)

		before := nowMs - age.Milliseconds()

		// Defense in depth: CheckPruneRequest refuses an ended limit above the
		// limit too. This bound stays so that a change of that check never lets
		// a run delete more counts than its limit.
		ended, err := pruneEnded(ctx, q, e, endedPrune{
			before: before, now: nowMs, limit: int64(min(limits.EndedLimit, limits.Limit)),
		})
		if err != nil {
			return err
		}

		rest := max(int64(limits.Limit)-ended, 0)

		plain, err := q.PruneNameLocks(ctx, db.PruneNameLocksParams{BeforeMs: before, NowMs: nowMs, MaxRows: rest})
		if err != nil {
			return fmt.Errorf("prune name counts: %w", err)
		}

		deleted := plain + ended
		entry := e
		entry.Changes = append(append([]audit.Change{}, e.Changes...),
			authgate.PrunedChanges(deleted, ended)...)

		if _, err := appendAuditEntry(ctx, q, entry); err != nil {
			return fmt.Errorf("append the entry of the prune: %w", err)
		}

		n = deleted

		return nil
	})
	if err != nil {
		return 0, err
	}

	return n, nil
}

// endedPrune is the time and the bound of the prune of the counts with an
// ended lock that no attempt saw.
type endedPrune struct {
	before, now, limit int64
}

// pruneEnded deletes the counts of p and appends the entry of the end of each
// lock, built from the entry e of the prune, in the transaction of q. It
// returns how many counts it deleted.
func pruneEnded(ctx context.Context, q *db.Queries, e audit.Entry, p endedPrune) (int64, error) {
	ended, err := q.PruneEndedNameLocks(ctx, db.PruneEndedNameLocksParams{
		BeforeMs: p.before, NowMs: p.now, MaxRows: p.limit,
	})
	if err != nil {
		return 0, fmt.Errorf("prune name counts with an ended lock: %w", err)
	}

	for _, row := range ended {
		end := authgate.PrunedLockEndedEntry(e, row.Scope, codec.FromMillis(row.LockedUntilMs))
		if _, appendErr := appendAuditEntry(ctx, q, end); appendErr != nil {
			return 0, fmt.Errorf("append the entry of the end of a lock: %w", appendErr)
		}
	}

	return int64(len(ended)), nil
}

// NameLockKey returns the key of the digests of the names of the lockout by
// name. The first call writes a new random key, and every later call and
// every instance on the database reads the same key. A stored key of another
// length fails closed with namelockstore.ErrKey.
func (s *Store) NameLockKey(ctx context.Context) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, err := s.queries.GetNameLockKey(ctx)
	if err == nil {
		return checkedKey(key)
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("select the key of the names: %w", err)
	}

	fresh, err := namelockstore.NewKey()
	if err != nil {
		return nil, err
	}

	err = s.inTx(ctx, func(q *db.Queries) error {
		if insertErr := q.InsertNameLockKey(ctx, fresh); insertErr != nil {
			return fmt.Errorf("insert the key of the names: %w", insertErr)
		}

		stored, getErr := q.GetNameLockKey(ctx)
		if getErr != nil {
			return fmt.Errorf("select the key of the names: %w", getErr)
		}

		key = stored

		return nil
	})
	if err != nil {
		return nil, err
	}

	return checkedKey(key)
}

// checkedKey returns key, or namelockstore.ErrKey for a key of a wrong
// length, so a changed row never weakens the digests.
func checkedKey(key []byte) ([]byte, error) {
	if err := namelockstore.CheckKey(key); err != nil {
		return nil, err
	}

	return key, nil
}
