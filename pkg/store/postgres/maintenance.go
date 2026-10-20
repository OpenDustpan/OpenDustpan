package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ maintenance.Store = (*Store)(nil)

// GetState implements maintenance.Store. The migration seeds the one row, so
// a missing row is a server fault: it reports maintenance.ErrUnavailable.
// It maps any driver failure to maintenance.ErrUnavailable without driver
// text, and so it maps a stored value outside the known set: an unknown
// stored value is a server fault, never a bad request, and the value itself
// stays out of the text.
func (s *Store) GetState(ctx context.Context) (maintenance.State, error) {
	raw, err := s.queries.GetInstallationState(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf(
			"select installation state postgres database: row is missing: %w",
			maintenance.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf("select installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	state, err := maintenance.ParseState(raw)
	if err != nil {
		return "", fmt.Errorf("select installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	return state, nil
}

// SetState implements maintenance.Store. It reports ErrInvalidState for any
// other value. It writes the seeded row only, and reports
// maintenance.ErrUnavailable when the row is missing. It maps any driver
// failure to maintenance.ErrUnavailable without driver text.
func (s *Store) SetState(ctx context.Context, state maintenance.State) error {
	if state != maintenance.StateNormal && state != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, state)
	}

	var changed int64

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		if err := lockWritesExclusive(ctx, q, s.stateLockWait); err != nil {
			return err
		}

		n, err := q.SetInstallationState(ctx, string(state))
		if err != nil {
			return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
		}

		changed = n

		return nil
	})
	if err != nil {
		return unavailable(err)
	}

	if changed == 0 {
		return fmt.Errorf("store installation state postgres database: row is missing: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// stateLockTimeout bounds the wait of an enter or a leave for the write
// lock. A write transaction that does not end would otherwise hold the
// exclusive request in the queue, and every later write would wait behind it.
const stateLockTimeout = 10 * time.Second

// lockWritesExclusive takes the exclusive form of the write lock for this
// transaction. It waits until every write transaction in flight has ended, and
// it holds back every later write until this transaction commits. A write that
// comes after the commit reads the new state. The wait ends after
// stateLockTimeout, and the call then reports maintenance.ErrUnavailable, so
// a stuck write cannot block the other writes without limit.
func lockWritesExclusive(ctx context.Context, q *db.Queries, wait time.Duration) error {
	if wait <= 0 {
		wait = stateLockTimeout
	}

	if err := q.SetLockTimeout(ctx, strconv.FormatInt(wait.Milliseconds(), 10)+"ms"); err != nil {
		return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	if err := q.LockWritesExclusive(ctx, writeLockClass); err != nil {
		return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// unavailable keeps err when it already wraps maintenance.ErrUnavailable, and
// otherwise reports maintenance.ErrUnavailable without driver text.
func unavailable(err error) error {
	if errors.Is(err, maintenance.ErrUnavailable) {
		return err
	}

	return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
}

// SetStateIf implements maintenance.Store. It stores next only when the
// stored state is expected, in one conditional UPDATE in a transaction, and
// reports ErrStateChanged when no row changed. It reports ErrInvalidState
// for any other expected or next value, and maps any driver failure to
// maintenance.ErrUnavailable without driver text.
func (s *Store) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	if expected != maintenance.StateNormal && expected != maintenance.StateMaintenance {
		return fmt.Errorf("%w: expected %q", maintenance.ErrInvalidState, expected)
	}

	if next != maintenance.StateNormal && next != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, next)
	}

	var changed int64

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		if err := lockWritesExclusive(ctx, q, s.stateLockWait); err != nil {
			return err
		}

		n, err := q.SetInstallationStateIf(ctx, db.SetInstallationStateIfParams{
			State:   string(next),
			State_2: string(expected),
		})
		if err != nil {
			return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
		}

		changed = n

		return nil
	})
	if err != nil {
		return unavailable(err)
	}

	if changed == 0 {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	return nil
}
