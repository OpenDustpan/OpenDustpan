package sqlite

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
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

	if err := blob.CheckAttempt(started, codec.FromMillis(nowMs)); err != nil {
		return fmt.Errorf("commit write attempt: %w", err)
	}

	return nil
}
