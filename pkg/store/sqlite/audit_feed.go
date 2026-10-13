package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.Feed = (*Store)(nil)

// After implements audit.Feed: at most limit entries of ns above after,
// oldest first.
func (s *Store) After(ctx context.Context, ns namespace.Name, after int64, limit int) ([]audit.Entry, error) {
	rows, err := s.queries.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
		Namespace: ns.String(),
		Sequence:  after,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select audit entries after %d: %w", after, err)
	}

	entries := make([]audit.Entry, 0, len(rows))
	for i := range rows {
		entries = append(entries, fromAuditRow(rows[i]))
	}

	return entries, nil
}

// SinkCursor implements audit.Feed. A sink with no row reads 0.
func (s *Store) SinkCursor(ctx context.Context, sink string, ns namespace.Name) (int64, error) {
	seq, err := s.queries.GetAuditSinkCursor(ctx, db.GetAuditSinkCursorParams{Sink: sink, Namespace: ns.String()})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read audit sink cursor: %w", err)
	}

	return seq, nil
}

// AdvanceSinkCursor implements audit.Feed: it moves the cursor up in one
// transaction with the fence check, and never moves it down.
func (s *Store) AdvanceSinkCursor(ctx context.Context, fence namespace.Fence, sink string, seq int64) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		err := q.AdvanceAuditSinkCursor(ctx, db.AdvanceAuditSinkCursorParams{
			Sink:        sink,
			Namespace:   fence.Namespace.String(),
			Sequence:    seq,
			UpdatedAtMs: codec.Millis(time.Now()),
		})
		if err != nil {
			return fmt.Errorf("advance audit sink cursor: %w", err)
		}

		return nil
	})
}
