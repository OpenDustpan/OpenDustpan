package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var _ workspace.StateAtReader = (*Store)(nil)

// WorkspaceStateAt implements workspace.StateAtReader.
func (s *Store) WorkspaceStateAt(ctx context.Context, id workspace.ID, at time.Time) (workspace.StateAt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.queries.WorkspaceExistsByID(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.StateAt{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.StateAt{}, fmt.Errorf("select workspace: %w", err)
	}

	row, err := s.queries.WorkspaceStateAt(ctx, db.WorkspaceStateAtParams{
		WorkspaceID: id.String(), FinalizedAtMs: codec.Millis(at),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.StateAt{}, s.noWorkspaceStateAt(ctx, id)
	}

	if err != nil {
		return workspace.StateAt{}, fmt.Errorf("select state version: %w", err)
	}

	return workspace.StateAt{
		FinalizedAt:  codec.FromMillis(row.FinalizedAtMs),
		By:           row.Holder,
		StateVersion: workspace.StateVersionID(row.ID),
	}, nil
}

// noWorkspaceStateAt builds the error for a time before the first finalized
// state version.
func (s *Store) noWorkspaceStateAt(ctx context.Context, id workspace.ID) error {
	first, err := s.queries.FirstWorkspaceStateFinalize(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return &workspace.NoStateAtError{First: time.Time{}}
	}

	if err != nil {
		return fmt.Errorf("select first state version: %w", err)
	}

	return &workspace.NoStateAtError{First: codec.FromMillis(first)}
}
