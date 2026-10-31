package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ stack.StateAtReader = (*Store)(nil)

// StateAt implements stack.StateAtReader.
func (s *Store) StateAt(ctx context.Context, id stack.ID, at time.Time) (stack.StateAt, error) {
	// One snapshot, so that the update, the running update and the first
	// end time come from the same state of the database.
	tx, err := s.txs.BeginTx(ctx, s.snapshotOptions())
	if err != nil {
		return stack.StateAt{}, fmt.Errorf("begin read transaction: %w", readConflict(err))
	}

	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			_ = rollbackErr
		}
	}()

	q := s.queries.WithTx(tx)

	_, err = q.StackExistsByID(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.StateAt{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.StateAt{}, fmt.Errorf("select stack: %w", readConflict(err))
	}

	atMs := codec.Millis(at)

	row, err := q.StateAtUpdate(ctx, db.StateAtUpdateParams{StackID: id.String(), EndedAtMs: atMs})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.StateAt{}, noStateAt(ctx, q, id)
	}

	if err != nil {
		return stack.StateAt{}, fmt.Errorf("select state update: %w", readConflict(err))
	}

	running, err := q.RunningUpdateAt(ctx, db.RunningUpdateAtParams{
		StackID: id.String(), StartedAtMs: atMs, Version: row.Version,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return stack.StateAt{}, fmt.Errorf("select running update: %w", readConflict(err))
	}

	return stack.StateAt{
		EndedAt:   codec.FromMillis(row.EndedAtMs),
		CreatedBy: row.CreatedBy,
		Version:   int(row.Version),
		Running:   int(running),
		Names:     inventory.Names{AtTime: "", Current: ""},
	}, nil
}

// noStateAt builds the error for a time before the first checkpoint.
func noStateAt(ctx context.Context, q *db.Queries, id stack.ID) error {
	first, err := q.FirstStateUpdateEnd(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return &stack.NoStateAtError{First: time.Time{}}
	}

	if err != nil {
		return fmt.Errorf("select first state update: %w", readConflict(err))
	}

	return &stack.NoStateAtError{First: codec.FromMillis(first)}
}
