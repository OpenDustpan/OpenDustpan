package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ durable.HeartbeatStore = (*DurableOperations)(nil)

// Beat implements durable.HeartbeatStore. The upsert and the delete of the
// stale beats run in one IMMEDIATE transaction, at the time of the store.
func (o *DurableOperations) Beat(ctx context.Context, owner string, lease time.Duration) error {
	s := o.store

	if owner == "" {
		return fmt.Errorf("heartbeat owner is empty: %w", durable.ErrInvalid)
	}

	if lease <= 0 {
		return fmt.Errorf("heartbeat lease %v is not positive: %w", lease, durable.ErrInvalid)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inMinimumTx(ctx, func(q *db.Queries) error {
		now, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		err = q.DeleteStaleDurableHeartbeats(ctx, now-durationMs(durable.HeartbeatRetention))
		if err != nil {
			return fmt.Errorf("delete stale durable heartbeats: %w", err)
		}

		err = q.UpsertDurableHeartbeat(
			ctx,
			db.UpsertDurableHeartbeatParams{Owner: owner, NowMs: now, LeaseMs: durationMs(lease)},
		)
		if err != nil {
			return fmt.Errorf("write durable heartbeat: %w", err)
		}

		return nil
	})
}

// LastBeat implements durable.HeartbeatStore.
func (o *DurableOperations) LastBeat(ctx context.Context) (durable.Beat, error) {
	s := o.store

	s.mu.RLock()
	defer s.mu.RUnlock()

	var beat durable.Beat

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		now, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		beat.Now = time.UnixMilli(now).UTC()

		row, err := q.GetLatestDurableHeartbeat(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read durable heartbeat: %w", err)
		}

		beat.Owner, beat.At = row.Owner, time.UnixMilli(row.AtMs).UTC()
		beat.Lease = time.Duration(row.LeaseMs) * time.Millisecond

		return nil
	})
	if err != nil {
		return durable.Beat{}, err
	}

	return beat, nil
}
