package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ maintenance.Store = (*Store)(nil)

// GetState implements maintenance.Store. The migration seeds the one row, so
// a missing row is a server fault: it reports maintenance.ErrUnavailable.
// It maps any driver failure to maintenance.ErrUnavailable without driver
// text, and so it maps a stored value outside the known set: an unknown
// stored value is a server fault, never a bad request, and the value itself
// stays out of the text.
func (s *Store) GetState(ctx context.Context) (maintenance.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := s.queries.GetInstallationState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf(
			"select installation state sqlite database: row is missing: %w",
			maintenance.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"select installation state sqlite database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	state, err := maintenance.ParseState(raw)
	if err != nil {
		return "", fmt.Errorf(
			"select installation state sqlite database: %w",
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	if state != maintenance.StateNormal && state != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, state)
	}

	var n int64

	unlock, lockErr := s.lockEnter(ctx, state)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		if err := clearJournalOnEnter(ctx, q, state); err != nil {
			return err
		}

		var err error

		n, err = q.SetInstallationState(ctx, string(state))

		return err
	})
	if err != nil {
		return fmt.Errorf(
			"store installation state sqlite database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	if n == 0 {
		return fmt.Errorf("store installation state sqlite database: row is missing: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// SetStateIf implements maintenance.Store. It stores next only when the
// stored state is expected, in one conditional UPDATE in a transaction, and
// reports ErrStateChanged when no row changed. It reports ErrInvalidState
// for any other expected or next value, and maps any driver failure to
// maintenance.ErrUnavailable without driver text.
func (s *Store) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// setStateIfWithin runs SetStateIf, and runs within in its transaction
// before any change. An error of within rolls back and returns as it is.
// With within, a state that is not expected rolls back too, so the writes of
// within never commit without the state change. The caller holds the read
// lock of the store.
func (s *Store) setStateIfWithin(
	ctx context.Context, expected, next maintenance.State, within func(q *db.Queries) error,
) error {
	if err := checkStatePair(expected, next); err != nil {
		return err
	}

	var changed int64

	errChanged := fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)

	unlock, lockErr := s.lockEnter(ctx, next)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		n, txErr := setStateIn(ctx, q, expected, next, within)
		if txErr != nil {
			return txErr
		}

		if n == 0 && within != nil {
			return errChanged
		}

		changed = n

		return nil
	})

	switch inner, ok := errors.AsType[withinError](err); {
	case ok:
		return inner.err
	case errors.Is(err, maintenance.ErrStateChanged):
		return errChanged
	case errors.Is(err, maintenance.ErrUnavailable):
		return err
	case err != nil:
		return fmt.Errorf(
			"store installation state sqlite database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	case changed == 0:
		return errChanged
	default:
		return nil
	}
}

// setStateIn is the transaction of setStateIfWithin on q. It returns the
// count of changed rows.
func setStateIn(
	ctx context.Context, q *db.Queries, expected, next maintenance.State, within func(q *db.Queries) error,
) (int64, error) {
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
			"store installation state sqlite database: %w",
			storeerr.Sentinel(maintenance.ErrUnavailable, err),
		)
	}

	return n, nil
}

// maxEnterWait bounds the wait of an enter of maintenance mode for the
// checked writes in flight. The guide of backup and restore names this value.
const maxEnterWait = 30 * time.Second

// lockEnter holds enterGate exclusive when next is maintenance mode, so that
// the checked writes in flight end and no new one starts until the enter
// ends. It returns the function that releases the gate. It returns
// maintenance.ErrBusy when the writes in flight do not end within
// s.enterWait, and an error for both the context error and
// maintenance.ErrUnavailable when ctx ends during the wait. A move back to
// normal takes no gate: no writer is refused then, and none competes for the
// lock.
func (s *Store) lockEnter(ctx context.Context, next maintenance.State) (func(), error) {
	if next != maintenance.StateMaintenance {
		return func() {}, nil
	}

	wait, cancel := context.WithTimeoutCause(ctx, s.enterWait, maintenance.ErrBusy)
	defer cancel()

	unlock, err := s.enterGate.lock(wait)
	if err == nil {
		return unlock, nil
	}

	if ctx.Err() == nil && errors.Is(context.Cause(wait), maintenance.ErrBusy) {
		return nil, fmt.Errorf("store installation state sqlite database: waited %s: %w", s.enterWait,
			maintenance.ErrBusy)
	}

	return nil, fmt.Errorf("store installation state sqlite database: %w: %w", err, maintenance.ErrUnavailable)
}
