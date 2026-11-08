package sqlite

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ principal.RemovalLog = (*Store)(nil)

// removalIDPrefix prefixes every member removal ID that the store mints.
const removalIDPrefix = "rmv-"

// AddRemoval implements principal.RemovalLog. It stores the row, deletes the
// rows of the source that are older than MaxRemovalWindow and returns the
// totals of the window in one transaction, so a concurrent write cannot
// change the totals between the write and the read.
func (s *Store) AddRemoval(
	ctx context.Context, r principal.Removal, window time.Duration,
) (principal.RemovalTotals, error) {
	// Defense in depth: the service that reads a group claim validates the
	// removal before it calls this port, and the store does not rely on that.
	// This check stays so that a new caller cannot store a row that no total
	// covers.
	if err := r.Validate(); err != nil {
		return principal.RemovalTotals{}, err
	}

	if err := principal.ValidateRemovalWindow(window); err != nil {
		return principal.RemovalTotals{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out principal.RemovalTotals

	err := s.inTx(ctx, func(q *db.Queries) error {
		at := codec.Millis(r.At)

		err := q.AddMemberRemoval(ctx, db.AddMemberRemovalParams{
			ID:            ident.NewID(removalIDPrefix),
			SourceKind:    string(r.Source.Kind),
			SourceID:      r.Source.ID,
			GroupID:       r.GroupID,
			Removed:       int64(r.Removed),
			MembersBefore: int64(r.MembersBefore),
			AtMs:          at,
		})
		if err != nil {
			return fmt.Errorf("insert member removal: %w", err)
		}

		_, err = q.PruneMemberRemovals(ctx, db.PruneMemberRemovalsParams{
			SourceKind: string(r.Source.Kind),
			SourceID:   r.Source.ID,
			CutoffMs:   at - principal.MaxRemovalWindow.Milliseconds(),
			MaxRows:    principal.MaxRemovalRowsPruned,
		})
		if err != nil {
			return fmt.Errorf("delete old member removals: %w", err)
		}

		row, err := q.MemberRemovalTotals(ctx, db.MemberRemovalTotalsParams{
			GroupID:    r.GroupID,
			SourceKind: string(r.Source.Kind),
			SourceID:   r.Source.ID,
			LowMs:      at - window.Milliseconds(),
			HighMs:     at,
		})
		if err != nil {
			return fmt.Errorf("select the member removal totals: %w", err)
		}

		out = principal.RemovalTotals{
			SourceRemoved: int(row.SourceRemoved),
			GroupRemoved:  int(row.GroupRemoved),
			GroupMembers:  int(row.GroupMembers),
		}

		return nil
	})
	if err != nil {
		return principal.RemovalTotals{}, err
	}

	return out, nil
}
