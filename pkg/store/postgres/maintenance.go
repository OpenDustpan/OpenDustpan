package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
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
		return "", fmt.Errorf(
			"select installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	state, err := maintenance.ParseState(raw)
	if err != nil {
		return "", fmt.Errorf(
			"select installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
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
		if err := lockWritesExclusive(ctx, q, s.stateLockWait, lockTimeoutError(state)); err != nil {
			return err
		}

		if err := clearJournalOnEnter(ctx, q, state); err != nil {
			return err
		}

		n, err := q.SetInstallationState(ctx, string(state))
		if err != nil {
			return fmt.Errorf(
				"store installation state postgres database: %w",
				storeerr.Sentinel(maintenance.ErrUnavailable, err),
			)
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
// stateLockTimeout, and the call then reports onTimeout, so a stuck write
// cannot block the other writes without limit. Every other failure reports
// maintenance.ErrUnavailable.
func lockWritesExclusive(ctx context.Context, q *db.Queries, wait time.Duration, onTimeout error) error {
	if wait <= 0 {
		wait = stateLockTimeout
	}

	if err := q.SetLockTimeout(ctx, strconv.FormatInt(wait.Milliseconds(), 10)+"ms"); err != nil {
		return fmt.Errorf(
			"store installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	if err := q.LockWritesExclusive(ctx, writeLockClass); err != nil {
		if ctx.Err() == nil && isLockTimeout(err) {
			return fmt.Errorf("store installation state postgres database: waited %s: %w", wait, onTimeout)
		}

		return fmt.Errorf(
			"store installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	return nil
}

// lockTimeoutError returns the error that a wait for the write lock reports
// when it ends at the lock timeout, for a write that stores next. An enter
// reports maintenance.ErrBusy, so the caller may try again. A leave reports
// maintenance.ErrUnavailable.
func lockTimeoutError(next maintenance.State) error {
	if next == maintenance.StateMaintenance {
		return maintenance.ErrBusy
	}

	return maintenance.ErrUnavailable
}

// unavailable keeps err when it already wraps maintenance.ErrBusy or
// maintenance.ErrUnavailable, and otherwise reports maintenance.ErrUnavailable
// without driver text.
func unavailable(err error) error {
	if errors.Is(err, maintenance.ErrBusy) || errors.Is(err, maintenance.ErrUnavailable) {
		return err
	}

	return fmt.Errorf(
		"store installation state postgres database: %w",
		storeerr.Sentinel(maintenance.ErrUnavailable, err),
	)
}

// SetStateIf implements maintenance.Store. It stores next only when the
// stored state is expected, in one conditional UPDATE in a transaction, and
// reports ErrStateChanged when no row changed. It reports ErrInvalidState
// for any other expected or next value, and maps any driver failure to
// maintenance.ErrUnavailable without driver text.
func (s *Store) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	return s.setStateIfWithin(ctx, expected, next, nil)
}

// withinError carries an error of the within function of setStateIfWithin
// past the mapping to maintenance.ErrUnavailable.
type withinError struct {
	err error
}

func (e withinError) Error() string { return e.err.Error() }

func (e withinError) Unwrap() error { return e.err }

// checkStatePair returns ErrInvalidState unless expected and next are known
// states.
func checkStatePair(expected, next maintenance.State) error {
	if expected != maintenance.StateNormal && expected != maintenance.StateMaintenance {
		return fmt.Errorf("%w: expected %q", maintenance.ErrInvalidState, expected)
	}

	if next != maintenance.StateNormal && next != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, next)
	}

	return nil
}

// setStateIfWithin runs SetStateIf, and runs within in its transaction after
// the write lock and before any change. An error of within rolls back and
// returns as it is. With within, a state that is not expected rolls back
// too, so the writes of within never commit without the state change.
func (s *Store) setStateIfWithin(
	ctx context.Context, expected, next maintenance.State, within func(q *db.Queries) error,
) error {
	if err := checkStatePair(expected, next); err != nil {
		return err
	}

	var changed int64

	errChanged := fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		n, txErr := s.setStateIn(ctx, q, expected, next, within)
		if txErr != nil {
			return txErr
		}

		if n == 0 && within != nil {
			return errChanged
		}

		changed = n

		return nil
	})
	if inner, ok := errors.AsType[withinError](err); ok {
		return inner.err
	}

	if errors.Is(err, maintenance.ErrStateChanged) {
		return errChanged
	}

	if err != nil {
		return unavailable(err)
	}

	if changed == 0 {
		return errChanged
	}

	return nil
}

// setStateIn is the transaction of setStateIfWithin on q. It returns the
// count of changed rows.
func (s *Store) setStateIn(
	ctx context.Context, q *db.Queries, expected, next maintenance.State, within func(q *db.Queries) error,
) (int64, error) {
	if err := lockWritesExclusive(ctx, q, s.stateLockWait, lockTimeoutError(next)); err != nil {
		return 0, err
	}

	if within != nil {
		if err := within(q); err != nil {
			return 0, withinError{err: err}
		}
	}

	if err := clearJournalOnEnter(ctx, q, next); err != nil {
		return 0, err
	}

	n, err := q.SetInstallationStateIf(ctx, db.SetInstallationStateIfParams{
		State:   string(next),
		State_2: string(expected),
	})
	if err != nil {
		return 0, fmt.Errorf(
			"store installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	return n, nil
}

// clearJournalOnEnter starts a new stay in maintenance mode when next is
// maintenance: it clears the journal of revocations, in the transaction of q
// and before the state changes. It clears nothing when the state is
// maintenance already, so a second enter keeps the journal of the stay.
func clearJournalOnEnter(ctx context.Context, q *db.Queries, next maintenance.State) error {
	if next != maintenance.StateMaintenance {
		return nil
	}

	if err := q.ClearMaintenanceRevocationsOnEnter(ctx); err != nil {
		return fmt.Errorf(
			"store installation state postgres database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	return nil
}
