package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var _ workspace.StateAtReader = (*Store)(nil)

// WorkspaceStateAt implements workspace.StateAtReader.
func (s *Store) WorkspaceStateAt(ctx context.Context, id workspace.ID, at time.Time) (workspace.StateAt, error) {
	// One snapshot, so that the state version and the first finalize time
	// come from the same state of the database.
	tx, err := s.txs.BeginTx(ctx, s.snapshotOptions())
	if err != nil {
		return workspace.StateAt{}, fmt.Errorf("begin read transaction: %w", readConflict(err))
	}

	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			_ = rollbackErr
		}
	}()

	q := s.queries.WithTx(tx)

	_, err = q.WorkspaceExistsByID(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.StateAt{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.StateAt{}, fmt.Errorf("select workspace: %w", readConflict(err))
	}

	row, err := q.WorkspaceStateAt(ctx, db.WorkspaceStateAtParams{
		WorkspaceID: id.String(), FinalizedAtMs: codec.Millis(at),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.StateAt{}, noWorkspaceStateAt(ctx, q, id)
	}

	if err != nil {
		return workspace.StateAt{}, fmt.Errorf("select state version: %w", readConflict(err))
	}

	return workspace.StateAt{
		FinalizedAt:  codec.FromMillis(row.FinalizedAtMs),
		By:           row.Holder,
		StateVersion: workspace.StateVersionID(row.ID),
	}, nil
}

// noWorkspaceStateAt builds the error for a time before the first finalized
// state version.
func noWorkspaceStateAt(ctx context.Context, q *db.Queries, id workspace.ID) error {
	first, err := q.FirstWorkspaceStateFinalize(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return &workspace.NoStateAtError{First: time.Time{}}
	}

	if err != nil {
		return fmt.Errorf("select first state version: %w", readConflict(err))
	}

	return &workspace.NoStateAtError{First: codec.FromMillis(first)}
}
