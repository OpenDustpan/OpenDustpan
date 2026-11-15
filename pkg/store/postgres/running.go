package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ blobguard.Watch  = (*Store)(nil)
	_ blobguard.Leases = (*Store)(nil)
)

// Running implements blobguard.Watch: a backup in the running state, or else a
// restore in the checking or applying state, read in one statement.
func (s *Store) Running(ctx context.Context) (blobguard.Work, bool, error) {
	row, err := s.queries.RunningWork(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return blobguard.Work{Kind: "", ID: ""}, false, nil
	}

	if err != nil {
		return blobguard.Work{Kind: "", ID: ""}, false, fmt.Errorf("select running backup or restore: %w", err)
	}

	return blobguard.Work{Kind: blobguard.Kind(row.Kind), ID: row.ID}, true, nil
}

// TakeLease implements blobguard.Leases: it writes the lease of holder in one
// statement, and it writes nothing and answers the running work while a
// backup runs or a restore checks or applies. The transaction first takes the
// shared form of the blob delete lease lock, and the take runs as a later
// statement, so its READ COMMITTED snapshot is newer than the lock: a start
// that committed before the lock is visible, and a start that commits after
// it waits for this take. The write runs in a control transaction that
// refuses a binary below the minimum of the schema, so a binary of a rolling
// deploy that an upgrade left behind takes no new lease.
func (s *Store) TakeLease(
	ctx context.Context,
	holder string,
	ttl time.Duration,
) (blobguard.Work, bool, error) {
	var (
		work blobguard.Work
		busy bool
	)

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
		// The shared form of the lease lock, before the take reads the
		// running work: the take must see every start that the lock ordered
		// before it.
		if err := q.LockBlobDeleteLeaseShared(ctx, blobDeleteLockClass); err != nil {
			return fmt.Errorf("lock the blob delete leases: %w", err)
		}

		row, err := q.TakeBlobDeleteLease(ctx, db.TakeBlobDeleteLeaseParams{
			Holder: holder,
			TtlMs:  ttl.Milliseconds(),
		})
		if err != nil {
			return fmt.Errorf("take the blob delete lease: %w", err)
		}

		// An empty kind is the answer of a written lease.
		if row.Kind == "" {
			return nil
		}

		work = blobguard.Work{Kind: blobguard.Kind(row.Kind), ID: row.ID}
		busy = true

		return nil
	})
	if err != nil {
		return blobguard.Work{Kind: "", ID: ""}, false, err
	}

	return work, busy, nil
}

// ReleaseLease implements blobguard.Leases: it deletes the lease row of
// holder. A holder with no row is not an error.
func (s *Store) ReleaseLease(ctx context.Context, holder string) error {
	return s.inMinimumTx(ctx, func(q *db.Queries) error {
		if err := q.ReleaseBlobDeleteLease(ctx, holder); err != nil {
			return fmt.Errorf("release the blob delete lease: %w", err)
		}

		return nil
	})
}

// LiveLeases implements blobguard.Leases: the number of lease rows that end
// after the database clock.
func (s *Store) LiveLeases(ctx context.Context) (int, error) {
	count, err := s.queries.CountLiveBlobDeleteLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("count live blob delete leases: %w", err)
	}

	return int(count), nil
}
