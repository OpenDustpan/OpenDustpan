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

var _ durable.ScheduleStore = (*DurableOperations)(nil)

// EnsureSchedule implements durable.ScheduleStore.
func (d *DurableOperations) EnsureSchedule(ctx context.Context, spec durable.ScheduleSpec) (durable.Schedule, error) {
	if err := spec.Validate(); err != nil {
		return durable.Schedule{}, err
	}

	var sc durable.Schedule

	err := d.store.inMinimumTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the database clock: %w", err)
		}

		interval := scheduleMs(spec.Interval)

		_, err = q.GetDurableSchedule(ctx, spec.Name)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Two instances can start at the same time and both find no
			// row. The insert then reports no row, because the other
			// instance inserted the schedule first.
			var rows int64

			rows, err = q.InsertDurableSchedule(ctx, db.InsertDurableScheduleParams{
				Name: spec.Name, IntervalMs: interval, NowMs: now,
			})
			if err == nil && rows == 0 {
				err = q.SetDurableScheduleInterval(ctx, db.SetDurableScheduleIntervalParams{
					IntervalMs: interval, NowMs: now, Name: spec.Name,
				})
			}
		case err == nil:
			err = q.SetDurableScheduleInterval(ctx, db.SetDurableScheduleIntervalParams{
				IntervalMs: interval, NowMs: now, Name: spec.Name,
			})
		default:
			return fmt.Errorf("read durable schedule: %w", err)
		}

		if err != nil {
			return fmt.Errorf("write durable schedule: %w", err)
		}

		sc, err = readSchedule(ctx, q, spec.Name, now)

		return err
	})
	if err != nil {
		return durable.Schedule{}, err
	}

	return sc, nil
}

// GetSchedule implements durable.ScheduleStore.
func (d *DurableOperations) GetSchedule(ctx context.Context, name string) (durable.Schedule, error) {
	var sc durable.Schedule

	err := d.store.inSnapshotTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the database clock: %w", err)
		}

		sc, err = readSchedule(ctx, q, name, now)

		return err
	})
	if err != nil {
		return durable.Schedule{}, err
	}

	return sc, nil
}

// ClaimTick implements durable.ScheduleStore. One transaction claims the
// tick and starts its operation: the conditional update requires
// next_run_at_ms to be the value that the claim read, so of several
// concurrent claims at most one succeeds.
func (d *DurableOperations) ClaimTick(ctx context.Context, spec durable.ScheduleSpec) (durable.Tick, error) {
	if err := spec.Validate(); err != nil {
		return durable.Tick{}, err
	}

	var (
		tick    durable.Tick
		claimed bool
	)

	err := d.store.inMinimumTx(ctx, func(q *db.Queries) error {
		var claimErr error

		tick, claimed, claimErr = claimTick(ctx, q, spec)

		return claimErr
	})
	if err != nil {
		return durable.Tick{}, err
	}

	if !claimed {
		return tick, durable.ErrNoWork
	}

	return tick, nil
}

// claimTick claims the tick of spec when it is due, and starts its
// operation, in the transaction of q. It reports false, with the schedule,
// when the tick is not due.
func claimTick(ctx context.Context, q *db.Queries, spec durable.ScheduleSpec) (durable.Tick, bool, error) {
	var tick durable.Tick

	now, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return tick, false, fmt.Errorf("read the database clock: %w", err)
	}

	row, err := q.GetDurableSchedule(ctx, spec.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return tick, false, durable.ErrNotFound
	}

	if err != nil {
		return tick, false, fmt.Errorf("read durable schedule: %w", err)
	}

	if row.NextRunAtMs > now {
		tick.Schedule = scheduleOf(row, now)

		return tick, false, nil
	}

	n, err := q.ClaimDurableScheduleTick(ctx, db.ClaimDurableScheduleTickParams{
		NowMs: now, Name: spec.Name, DueAtMs: row.NextRunAtMs,
	})
	if err != nil {
		return tick, false, fmt.Errorf("claim durable schedule tick: %w", err)
	}

	if n == 0 {
		// Another claim moved the next run first.
		tick.Schedule = scheduleOf(row, now)

		return tick, false, nil
	}

	op, _, err := startOperation(ctx, q, durable.StartRequest{
		Key:     durable.TickKey(spec.Name, codec.FromMillis(row.NextRunAtMs)),
		Kind:    spec.Kind,
		Version: spec.Version,
		Input:   spec.Input,
	})
	if err != nil {
		return tick, false, err
	}

	sc, err := readSchedule(ctx, q, spec.Name, now)
	if err != nil {
		return tick, false, err
	}

	return durable.Tick{Operation: op, Schedule: sc}, true, nil
}

// readSchedule reads the schedule called name at the store time now.
func readSchedule(ctx context.Context, q *db.Queries, name string, now int64) (durable.Schedule, error) {
	row, err := q.GetDurableSchedule(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return durable.Schedule{}, durable.ErrNotFound
	}

	if err != nil {
		return durable.Schedule{}, fmt.Errorf("read durable schedule: %w", err)
	}

	return scheduleOf(row, now), nil
}

// scheduleOf turns a row into a schedule at the store time now.
func scheduleOf(row db.DurableSchedule, now int64) durable.Schedule {
	var last time.Time
	if row.LastClaimedAtMs.Valid {
		last = codec.FromMillis(row.LastClaimedAtMs.Int64)
	}

	return durable.Schedule{
		Name:          row.Name,
		Interval:      time.Duration(row.IntervalMs) * time.Millisecond,
		NextRunAt:     codec.FromMillis(row.NextRunAtMs),
		LastClaimedAt: last,
		Now:           codec.FromMillis(now),
	}
}

// scheduleMs returns d in whole milliseconds, rounded up, so a positive
// duration below one millisecond is not 0.
func scheduleMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}

	return int64((d + time.Millisecond - 1) / time.Millisecond)
}
