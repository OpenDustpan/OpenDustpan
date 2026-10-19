package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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

	if fence.Namespace != ns {
		return fmt.Errorf("record resource changes: %w", namespace.ErrFenced)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.WorkspaceOwnedBy(ctx, db.WorkspaceOwnedByParams{ID: b.Workspace.String(), Namespace: ns.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select workspace: %w", err)
		}

		skip, err := checkBlameOrder(ctx, q, b)
		if err != nil || skip {
			return err
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

// checkBlameOrder reads the cursor and the item of b. It returns true when b
// is at or below the cursor, so the record changes nothing, and
// workspace.ErrInvalidBlame when b leaves a gap in the order, or when its
// state version already has a blame.
func checkBlameOrder(ctx context.Context, q *db.Queries, b workspace.ItemBlame) (bool, error) {
	cursor, err := q.GetWorkspaceBlameCursor(ctx, b.Workspace.String())
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("select blame cursor: %w", err)
	}

	if b.Ordinal <= cursor {
		return true, nil
	}

	if b.Ordinal != cursor+1 {
		return false, fmt.Errorf(
			"%w: ordinal %d does not follow the cursor %d", workspace.ErrInvalidBlame, b.Ordinal, cursor,
		)
	}

	_, err = q.GetWorkspaceBlameItem(ctx, db.GetWorkspaceBlameItemParams{
		WorkspaceID: b.Workspace.String(), StateVersionID: b.StateVersion.String(),
	})
	if err == nil {
		return false, fmt.Errorf("%w: state version %s already has a blame", workspace.ErrInvalidBlame, b.StateVersion)
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("select blame item: %w", err)
	}

	return false, nil
}

// insertBlame writes the item row of b, and its resource change rows.
func insertBlame(ctx context.Context, q *db.Queries, b workspace.ItemBlame) error {
	err := q.InsertWorkspaceBlameItem(ctx, db.InsertWorkspaceBlameItemParams{
		WorkspaceID:    b.Workspace.String(),
		StateVersionID: b.StateVersion.String(),
		Ordinal:        b.Ordinal,
		Opaque:         b.Opaque,
		LineageChanged: b.LineageChanged,
		ResourceCount:  b.ResourceCount,
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
	ordinal, err := s.queries.GetWorkspaceBlameCursor(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
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

	rows, err := s.queries.ListWorkspaceResourceHistory(ctx, db.ListWorkspaceResourceHistoryParams{
		WorkspaceID: id.String(), Address: address, Before: before, MaxRows: int64(limit),
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
	out := workspace.ItemBlame{
		Workspace:      id,
		StateVersion:   sv,
		Changes:        []workspace.ResourceChange{},
		Ordinal:        0,
		ResourceCount:  0,
		Opaque:         false,
		LineageChanged: false,
	}

	item, err := s.queries.GetWorkspaceBlameItem(ctx, db.GetWorkspaceBlameItemParams{
		WorkspaceID: id.String(), StateVersionID: sv.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
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
	out.ResourceCount = item.ResourceCount
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

// ItemSummaries implements workspace.BlameStore.
func (s *Store) ItemSummaries(
	ctx context.Context, id workspace.ID, svs []workspace.StateVersionID,
) (map[workspace.StateVersionID]workspace.ItemSummary, error) {
	out := make(map[workspace.StateVersionID]workspace.ItemSummary, len(svs))
	if len(svs) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(svs))
	for _, sv := range svs {
		ids = append(ids, sv.String())
	}

	rows, err := s.queries.ListWorkspaceBlameSummaries(ctx, db.ListWorkspaceBlameSummariesParams{
		WorkspaceID: id.String(), StateVersionIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("select blame summaries: %w", err)
	}

	for i := range rows {
		r := &rows[i]
		out[workspace.StateVersionID(r.StateVersionID)] = workspace.ItemSummary{
			Ordinal:        r.Ordinal,
			ResourceCount:  r.ResourceCount,
			Created:        r.Created,
			Updated:        r.Updated,
			Replaced:       r.Replaced,
			Deleted:        r.Deleted,
			Opaque:         r.Opaque,
			LineageChanged: r.LineageChanged,
		}
	}

	return out, nil
}
