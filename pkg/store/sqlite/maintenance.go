package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/maintenance"
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
		return "", fmt.Errorf("select installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	state, err := maintenance.ParseState(raw)
	if err != nil {
		return "", fmt.Errorf("select installation state sqlite database: %w", maintenance.ErrUnavailable)
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

	n, err := s.queries.SetInstallationState(ctx, string(state))
	if err != nil {
		return fmt.Errorf("store installation state sqlite database: %w", maintenance.ErrUnavailable)
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

	if expected != maintenance.StateNormal && expected != maintenance.StateMaintenance {
		return fmt.Errorf("%w: expected %q", maintenance.ErrInvalidState, expected)
	}

	if next != maintenance.StateNormal && next != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, next)
	}

	var changed int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		n, err := q.SetInstallationStateIf(ctx, db.SetInstallationStateIfParams{
			State:   string(next),
			State_2: string(expected),
		})
		if err != nil {
			return fmt.Errorf("store installation state sqlite database: %w", maintenance.ErrUnavailable)
		}

		changed = n

		return nil
	})
	if err != nil {
		if errors.Is(err, maintenance.ErrUnavailable) {
			return err
		}

		return fmt.Errorf("store installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	if changed == 0 {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	return nil
}
