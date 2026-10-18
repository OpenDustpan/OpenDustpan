package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ stack.StateAtReader = (*Store)(nil)

// StateAt implements stack.StateAtReader.
func (s *Store) StateAt(ctx context.Context, id stack.ID, at time.Time) (stack.StateAt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.queries.StackExistsByID(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return stack.StateAt{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.StateAt{}, fmt.Errorf("select stack: %w", err)
	}

	atMs := codec.Millis(at)

	row, err := s.queries.StateAtUpdate(ctx, db.StateAtUpdateParams{StackID: id.String(), EndedAtMs: atMs})
	if errors.Is(err, sql.ErrNoRows) {
		first, firstErr := s.queries.FirstStateUpdateEnd(ctx, id.String())
		if errors.Is(firstErr, sql.ErrNoRows) {
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
		StackID: id.String(), StartedAtMs: atMs, EndedAtMs: atMs,
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return stack.StateAt{}, fmt.Errorf("select running update: %w", err)
	}

	return stack.StateAt{
		EndedAt: codec.FromMillis(row.EndedAtMs),
		Version: int(row.Version),
		Running: int(running),
	}, nil
}
