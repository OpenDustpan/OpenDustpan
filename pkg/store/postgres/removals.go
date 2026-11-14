package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ principal.RemovalLog = (*Store)(nil)

// removalIDPrefix prefixes every member removal ID that the store mints.
const removalIDPrefix = "rmv-"

// checkAddRemoval returns the error of a removal or a window that AddRemoval
// cannot store.
func checkAddRemoval(r principal.Removal, window time.Duration) error {
	// Defense in depth: the service that reads a group claim validates the
	// removal before it calls this port, and the store does not rely on that.
	// This check stays so that a new caller cannot store a row that no total
	// covers.
	if err := r.Validate(); err != nil {
		return err
	}

	return principal.ValidateRemovalWindow(window)
}

// AddRemoval implements principal.RemovalLog. It stores the row, deletes the
// rows of the source that are older than MaxRemovalWindow and returns the
// totals of the window in one transaction, so a concurrent write cannot
// change the totals between the write and the read.
func (s *Store) AddRemoval(
	ctx context.Context, r principal.Removal, window time.Duration,
) (principal.RemovalTotals, error) {
	if err := checkAddRemoval(r, window); err != nil {
		return principal.RemovalTotals{}, err
	}

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

		alertedAtMs, err := q.MemberRemovalAlertedAt(ctx, db.MemberRemovalAlertedAtParams{
			SourceKind: string(r.Source.Kind),
			SourceID:   r.Source.ID,
		})
		if err != nil {
			return fmt.Errorf("select the member removal alert time: %w", err)
		}

		out = principal.RemovalTotals{
			SourceRemoved: int(row.SourceRemoved),
			GroupRemoved:  int(row.GroupRemoved),
			GroupMembers:  int(row.GroupMembers),
			AlertedAt:     codec.FromOptionalMillis(alertedAtMs),
		}

		return nil
	})
	if err != nil {
		return principal.RemovalTotals{}, err
	}

	return out, nil
}

// MarkRemovalAlert implements principal.RemovalLog. It stores at as the time
// of the last alert of source.
func (s *Store) MarkRemovalAlert(ctx context.Context, source principal.RemovalSource, at time.Time) error {
	// Defense in depth: the service validates the marker before it calls
	// this port, and the store does not rely on that. This check stays so
	// that a new caller cannot store a marker that no check covers.
	if err := principal.ValidateRemovalAlert(source, at); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		err := q.MarkMemberRemovalAlert(ctx, db.MarkMemberRemovalAlertParams{
			SourceKind:  string(source.Kind),
			SourceID:    source.ID,
			AlertedAtMs: codec.Millis(at),
		})
		if err != nil {
			return fmt.Errorf("mark the member removal alert: %w", err)
		}

		return nil
	})
}
