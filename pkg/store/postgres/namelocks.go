package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/namelockstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
	r, err := t.q.GetNameLockForUpdate(ctx, db.GetNameLockForUpdateParams{Scope: scope, Name: name, Sub: sub})
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

// AddNameFailure implements authgate.NameLockCounter. The add locks the row
// of the count in its transaction, so concurrent adds count one after the
// other.
func (s *Store) AddNameFailure(ctx context.Context, f authgate.NameFailure) (authgate.NameLock, error) {
	if err := authgate.CheckLockRequest(f.Scope, f.Name, f.Sub, f.Rule); err != nil {
		return authgate.NameLock{}, err
	}

	if err := f.Events.Validate(); err != nil {
		return authgate.NameLock{}, err
	}

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

// PruneNameLocks implements authgate.NameLockCounter. It reads the time from
// the clock of the store. The delete and the append of e run in one
// transaction.
func (s *Store) PruneNameLocks(ctx context.Context, age time.Duration, limit int, e audit.Entry) (int64, error) {
	if err := authgate.CheckPruneRequest(age, limit); err != nil {
		return 0, err
	}

	if err := validServerEntry(e); err != nil {
		return 0, err
	}

	var n int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the clock of the store: %w", err)
		}

		ends, err := q.PruneNameLocks(ctx, db.PruneNameLocksParams{
			BeforeMs: now - age.Milliseconds(), NowMs: now, MaxRows: int64(limit),
		})
		if err != nil {
			return fmt.Errorf("prune name counts: %w", err)
		}

		entry := e
		entry.Changes = append(append([]audit.Change{}, e.Changes...), authgate.PrunedChanges(ends)...)

		if _, err := appendAuditEntry(ctx, q, entry); err != nil {
			return fmt.Errorf("append the entry of the prune: %w", err)
		}

		n = int64(len(ends))

		return nil
	})
	if err != nil {
		return 0, err
	}

	return n, nil
}

// NameLockKey returns the key of the digests of the names of the lockout by
// name. The first call writes a new random key, and every later call and
// every instance on the database reads the same key. A stored key of another
// length fails closed with namelockstore.ErrKey.
func (s *Store) NameLockKey(ctx context.Context) ([]byte, error) {
	key, err := s.queries.GetNameLockKey(ctx)
	if err == nil {
		return checkedKey(key)
	}

	if !errors.Is(err, pgx.ErrNoRows) {
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
