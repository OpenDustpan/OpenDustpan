package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.Feed = (*Store)(nil)

// After implements audit.Feed: at most limit entries of ns above after,
// oldest first.
func (s *Store) After(ctx context.Context, ns namespace.Name, after int64, limit int) ([]audit.Entry, error) {
	rows, err := s.queries.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
		Namespace: ns.String(),
		Sequence:  after,
		Limit:     int32(min(max(limit, 0), math.MaxInt32)),
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
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read audit sink cursor: %w", err)
	}

	return seq, nil
}

// AdvanceSinkCursor implements audit.Feed: it moves the cursor up in one
// transaction with the fence check, and never moves it down. The write
// checks claim with the claim rule of the sink lease, and answers
// durable.ErrClaimLost when claim is not the live claim of the sink. The
// cursor of audit.ServerScope has no fence: the scope describes the
// installation.
// The advance runs on the control path with no installation state check:
// the shipper of a sink is no tool or installation API request, and a
// refused advance in maintenance mode would deliver the same entries again
// in a loop.
func (s *Store) AdvanceSinkCursor(
	ctx context.Context,
	fence namespace.Fence,
	claim durable.Claim,
	sink string,
	seq int64,
) error {
	inTx := func(fn func(q *db.Queries) error) error { return s.inMinimumFencedUpdateTx(ctx, fence, fn) }
	if fence.Namespace == audit.ServerScope {
		inTx = func(fn func(q *db.Queries) error) error { return s.inMinimumTx(ctx, fn) }
	}

	return inTx(func(q *db.Queries) error {
		now, err := checkSinkClaim(ctx, q, sink, claim)
		if err != nil {
			return err
		}

		err = q.AdvanceAuditSinkCursor(ctx, db.AdvanceAuditSinkCursorParams{
			Sink:        sink,
			Namespace:   fence.Namespace.String(),
			Sequence:    seq,
			UpdatedAtMs: now,
		})
		if err != nil {
			return fmt.Errorf("advance audit sink cursor: %w", err)
		}

		return nil
	})
}
