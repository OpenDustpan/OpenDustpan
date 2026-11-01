package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// checkAttempt refuses the commit of a write attempt that started at started
// with blob.ErrAttemptExpired when the attempt is outside its time window by
// the clock of the database. The caller runs it in the transaction of the
// commit, so the database clock, not the clock of an instance, decides.
func checkAttempt(ctx context.Context, q *db.Queries, started time.Time) error {
	nowMs, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return fmt.Errorf("read database clock: %w", err)
	}

	return checkAttemptAt(started, nowMs)
}

// checkAttemptAt is checkAttempt for a database clock that the caller has read
// already, in Unix milliseconds, for example in the RETURNING row of the
// statement that writes.
func checkAttemptAt(started time.Time, nowMs int64) error {
	if err := blob.CheckAttempt(started, codec.FromMillis(nowMs)); err != nil {
		return fmt.Errorf("commit write attempt: %w", err)
	}

	return nil
}

var _ blob.DatabaseClock = (*Store)(nil)

// DatabaseNow implements blob.DatabaseClock: the time on the clock of the
// database, which every sweep of blobs takes as now.
func (s *Store) DatabaseNow(ctx context.Context) (time.Time, error) {
	nowMs, err := s.queries.DatabaseNowMs(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("read database clock: %w", err)
	}

	return codec.FromMillis(nowMs), nil
}
