package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ durable.HeartbeatStore = (*DurableOperations)(nil)

// Beat implements durable.HeartbeatStore. The upsert and the delete of the
// stale beats run in one transaction, at the time of the database.
func (d *DurableOperations) Beat(ctx context.Context, owner string, lease time.Duration) error {
	if owner == "" {
		return fmt.Errorf("heartbeat owner is empty: %w", durable.ErrInvalid)
	}

	if lease <= 0 {
		return fmt.Errorf("heartbeat lease %v is not positive: %w", lease, durable.ErrInvalid)
	}

	return d.store.inControlTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the database clock: %w", err)
		}

		err = q.DeleteStaleDurableHeartbeats(ctx, now-scheduleMs(durable.HeartbeatRetention))
		if err != nil {
			return fmt.Errorf("delete stale durable heartbeats: %w", err)
		}

		err = q.UpsertDurableHeartbeat(
			ctx,
			db.UpsertDurableHeartbeatParams{Owner: owner, NowMs: now, LeaseMs: scheduleMs(lease)},
		)
		if err != nil {
			return fmt.Errorf("write durable heartbeat: %w", err)
		}

		return nil
	})
}

// LastBeat implements durable.HeartbeatStore.
func (d *DurableOperations) LastBeat(ctx context.Context) (durable.Beat, error) {
	var beat durable.Beat

	err := d.store.inSnapshotTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the database clock: %w", err)
		}

		beat.Now = codec.FromMillis(now)

		row, err := q.GetLatestDurableHeartbeat(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read durable heartbeat: %w", err)
		}

		beat.Owner, beat.At = row.Owner, codec.FromMillis(row.AtMs)
		beat.Lease = time.Duration(row.LeaseMs) * time.Millisecond

		return nil
	})
	if err != nil {
		return durable.Beat{}, err
	}

	return beat, nil
}
