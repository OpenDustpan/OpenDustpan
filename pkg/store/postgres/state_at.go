package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ stack.StateAtReader = (*Store)(nil)

// StateAt implements stack.StateAtReader.
func (s *Store) StateAt(ctx context.Context, id stack.ID, at time.Time) (stack.StateAt, error) {
	_, err := s.queries.StackExistsByID(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.StateAt{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.StateAt{}, fmt.Errorf("select stack: %w", err)
	}

	atMs := codec.Millis(at)

	row, err := s.queries.StateAtUpdate(ctx, db.StateAtUpdateParams{StackID: id.String(), EndedAtMs: atMs})
	if errors.Is(err, pgx.ErrNoRows) {
		first, firstErr := s.queries.FirstStateUpdateEnd(ctx, id.String())
		if errors.Is(firstErr, pgx.ErrNoRows) {
			return stack.StateAt{}, stack.ErrNoStateAt
		}

		if firstErr != nil {
			return stack.StateAt{}, fmt.Errorf("select first state update: %w", firstErr)
		}

		return stack.StateAt{EndedAt: codec.FromMillis(first), Version: 0, Running: 0}, stack.ErrNoStateAt
	}

	if err != nil {
		return stack.StateAt{}, fmt.Errorf("select state update: %w", err)
	}

	running, err := s.queries.RunningUpdateAt(ctx, db.RunningUpdateAtParams{
		StackID: id.String(), StartedAtMs: atMs,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return stack.StateAt{}, fmt.Errorf("select running update: %w", err)
	}

	return stack.StateAt{
		EndedAt: codec.FromMillis(row.EndedAtMs),
		Version: int(row.Version),
		Running: int(running),
	}, nil
}
