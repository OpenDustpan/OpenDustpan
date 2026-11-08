package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ session.TicketStore = (*Store)(nil)

// UseTicket implements session.TicketStore. One transaction deletes the
// ended record of the ticket, if any, and inserts the new one. The insert
// does nothing when a live record exists, so exactly one of any number of
// concurrent calls inserts.
func (s *Store) UseTicket(
	ctx context.Context, providerID string, d session.TicketDigest, at, until time.Time,
) error {
	// Defense in depth: the CAS method and the session.StoreTickets adapter
	// check the same limits; this check stays so that a caller of the store
	// alone cannot write a hostile value.
	if err := session.ValidateTicketUse(providerID, d, at, until); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.inTx(ctx, func(q *db.Queries) error {
		_, err := q.DeleteEndedTicket(ctx, db.DeleteEndedTicketParams{
			ProviderID: providerID, TicketDigest: d.String(), UntilMs: codec.Millis(at),
		})
		if err != nil {
			return fmt.Errorf("delete ended record: %w", err)
		}

		n, err := q.InsertTicket(ctx, db.InsertTicketParams{
			ProviderID: providerID, TicketDigest: d.String(), UntilMs: codec.Millis(until),
		})
		if err != nil {
			return fmt.Errorf("insert record: %w", err)
		}

		if n != 1 {
			return session.ErrReplay
		}

		return nil
	})
	if errors.Is(err, session.ErrReplay) {
		return session.ErrReplay
	}

	if err != nil {
		return fmt.Errorf("record used ticket: %w", err)
	}

	return nil
}

// PruneTickets implements session.TicketStore.
func (s *Store) PruneTickets(ctx context.Context, at time.Time) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var n int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error

		n, err = q.PruneUsedTickets(ctx, codec.Millis(at))

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("prune used tickets: %w", err)
	}

	return n, nil
}
