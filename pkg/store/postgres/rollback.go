package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// rollbackRead ends the read-only transaction tx of a read, in a defer.
// The read changes nothing, so a failed rollback changes no result. The
// guarded transaction reports the failed round trip to the store watch,
// and pgx closes a connection whose rollback failed. A transaction that
// already ended gives pgx.ErrTxClosed, which is not a failure.
func rollbackRead(ctx context.Context, tx pgx.Tx) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return
	}
}
