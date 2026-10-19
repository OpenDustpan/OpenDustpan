package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var _ workspace.BlameStore = (*Store)(nil)

// RecordResourceChanges implements workspace.BlameStore.
func (s *Store) RecordResourceChanges(
	ctx context.Context,
	ns namespace.Name,
	b workspace.ItemBlame,
	fence namespace.Fence,
) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("record resource changes: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.WorkspaceOwnedBy(ctx, db.WorkspaceOwnedByParams{ID: b.Workspace.String(), Namespace: ns.String()})
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select workspace: %w", err)
		}

		moved, err := q.AdvanceWorkspaceBlameCursor(ctx, db.AdvanceWorkspaceBlameCursorParams{
			WorkspaceID: b.Workspace.String(), Ordinal: b.Ordinal,
		})
		if err != nil {
			return fmt.Errorf("advance blame cursor: %w", err)
		}

		if moved == 0 {
			return nil
		}

		return insertBlame(ctx, q, b)
	})
}

// insertBlame writes the item row of b, and its resource change rows.
func insertBlame(ctx context.Context, q *db.Queries, b workspace.ItemBlame) error {
	err := q.InsertWorkspaceBlameItem(ctx, db.InsertWorkspaceBlameItemParams{
		WorkspaceID:    b.Workspace.String(),
		StateVersionID: b.StateVersion.String(),
		Ordinal:        b.Ordinal,
		Opaque:         b.Opaque,
		LineageChanged: b.LineageChanged,
	})
	if err != nil {
		return fmt.Errorf("insert blame item: %w", err)
	}

	for i := range b.Changes {
		r := &b.Changes[i]

		err = q.InsertWorkspaceResourceChange(ctx, db.InsertWorkspaceResourceChangeParams{
			WorkspaceID:    r.Workspace.String(),
			StateVersionID: r.StateVersion.String(),
			Address:        r.Address,
			Ordinal:        r.Ordinal,
			Kind:           string(r.Kind),
			AtMs:           codec.Millis(r.At),
			BySubject:      r.By.Subject,
			ByTokenID:      r.By.TokenID,
			ByJobID:        r.By.JobID,
			ChangedPaths:   int64(r.ChangedPaths),
			Restored:       r.Restored,
			Forced:         r.Forced,
			LineageChanged: r.LineageChanged,
		})
		if err != nil {
			return fmt.Errorf("insert resource change: %w", err)
		}
	}

	return nil
}

// BlameCursor implements workspace.BlameStore.
func (s *Store) BlameCursor(ctx context.Context, id workspace.ID) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ordinal, err := s.queries.GetWorkspaceBlameCursor(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("select blame cursor: %w", err)
	}

	return ordinal, nil
}

// ResourceHistory implements workspace.BlameStore.
func (s *Store) ResourceHistory(
	ctx context.Context, id workspace.ID, address string, before int64, limit int,
) ([]workspace.ResourceChange, error) {
	if limit <= 0 {
		return []workspace.ResourceChange{}, nil
	}

	if before <= 0 {
		before = math.MaxInt64
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListWorkspaceResourceHistory(ctx, db.ListWorkspaceResourceHistoryParams{
		WorkspaceID: id.String(), Address: address, Ordinal: before, Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select resource history: %w", err)
	}

	out := make([]workspace.ResourceChange, 0, len(rows))

	for i := range rows {
		r := &rows[i]
		out = append(out, workspace.ResourceChange{
			At:             codec.FromMillis(r.AtMs),
			Workspace:      id,
			StateVersion:   workspace.StateVersionID(r.StateVersionID),
			Address:        address,
			Kind:           workspace.ResourceChangeKind(r.Kind),
			By:             history.Actor{Subject: r.BySubject, TokenID: r.ByTokenID, JobID: r.ByJobID},
			Ordinal:        r.Ordinal,
			ChangedPaths:   int(r.ChangedPaths),
			Restored:       r.Restored,
			Forced:         r.Forced,
			LineageChanged: r.LineageChanged,
		})
	}

	return out, nil
}

// ItemChanges implements workspace.BlameStore.
func (s *Store) ItemChanges(
	ctx context.Context, id workspace.ID, sv workspace.StateVersionID,
) (workspace.ItemBlame, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := workspace.ItemBlame{
		Workspace:      id,
		StateVersion:   sv,
		Changes:        []workspace.ResourceChange{},
		Ordinal:        0,
		Opaque:         false,
		LineageChanged: false,
	}

	item, err := s.queries.GetWorkspaceBlameItem(ctx, db.GetWorkspaceBlameItemParams{
		WorkspaceID: id.String(), StateVersionID: sv.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}

	if err != nil {
		return workspace.ItemBlame{}, fmt.Errorf("select blame item: %w", err)
	}

	rows, err := s.queries.ListWorkspaceResourceChangesOfItem(ctx, db.ListWorkspaceResourceChangesOfItemParams{
		WorkspaceID: id.String(), StateVersionID: sv.String(),
	})
	if err != nil {
		return workspace.ItemBlame{}, fmt.Errorf("select resource changes: %w", err)
	}

	out.Ordinal = item.Ordinal
	out.Opaque = item.Opaque
	out.LineageChanged = item.LineageChanged

	for i := range rows {
		r := &rows[i]
		out.Changes = append(out.Changes, workspace.ResourceChange{
			At:             codec.FromMillis(r.AtMs),
			Workspace:      id,
			StateVersion:   sv,
			Address:        r.Address,
			Kind:           workspace.ResourceChangeKind(r.Kind),
			By:             history.Actor{Subject: r.BySubject, TokenID: r.ByTokenID, JobID: r.ByJobID},
			Ordinal:        r.Ordinal,
			ChangedPaths:   int(r.ChangedPaths),
			Restored:       r.Restored,
			Forced:         r.Forced,
			LineageChanged: r.LineageChanged,
		})
	}

	return out, nil
}
