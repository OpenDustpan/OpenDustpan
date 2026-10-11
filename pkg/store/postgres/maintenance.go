package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ maintenance.Store = (*Store)(nil)

// GetState implements maintenance.Store. The migration seeds the one row; a
// missing row reads normal, so a store that predates the seed still serves.
// It maps any driver failure to maintenance.ErrUnavailable without driver
// text, and so it maps a stored value outside the known set: an unknown
// stored value is a server fault, never a bad request, and the value itself
// stays out of the text.
func (s *Store) GetState(ctx context.Context) (maintenance.State, error) {
	raw, err := s.queries.GetInstallationState(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return maintenance.StateNormal, nil
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

// SetState implements maintenance.Store. It creates the row when it is
// missing and reports ErrInvalidState for any other value. It maps any
// driver failure to maintenance.ErrUnavailable without driver text.
func (s *Store) SetState(ctx context.Context, state maintenance.State) error {
	if state != maintenance.StateNormal && state != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, state)
	}

	if err := s.queries.UpsertInstallationState(ctx, string(state)); err != nil {
		return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	return nil
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

	err := s.inTx(ctx, func(q *db.Queries) error {
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
		if errors.Is(err, maintenance.ErrUnavailable) {
			return err
		}

		return fmt.Errorf("store installation state postgres database: %w", maintenance.ErrUnavailable)
	}

	if changed == 0 {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	return nil
}
