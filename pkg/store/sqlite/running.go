package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ blobguard.Watch  = (*Store)(nil)
	_ blobguard.Leases = (*Store)(nil)
)

// errNoRunningWork reports a lease take that wrote no row while no backup and
// no restore runs. The statement refuses the write only while work runs, and
// the read of the work runs in the same transaction, so this state is a
// fault.
var errNoRunningWork = errors.New("no lease written and no running work")

// Running implements blobguard.Watch: a backup in the running state, or else a
// restore in the checking or applying state, read in one statement.
func (s *Store) Running(ctx context.Context) (blobguard.Work, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.RunningWork(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return blobguard.Work{Kind: "", ID: ""}, false, nil
	}

	if err != nil {
		return blobguard.Work{Kind: "", ID: ""}, false, fmt.Errorf("select running backup or restore: %w", err)
	}

	return blobguard.Work{Kind: blobguard.Kind(row.Kind), ID: row.ID}, true, nil
}

// TakeLease implements blobguard.Leases: it writes the lease of holder in one
// statement, and it writes nothing while a backup runs or a restore checks or
// applies. It then reads the running work in the same transaction, which
// begins IMMEDIATE: SQLite runs the write transactions one at a time, so no
// start of a backup or a restore can interleave with this take, and this
// adapter needs no lock as the PostgreSQL adapter does. The write runs in a
// control transaction that refuses a binary below the minimum of the schema,
// so a binary of a rolling deploy that an upgrade left behind takes no new
// lease.
func (s *Store) TakeLease(
	ctx context.Context,
	holder string,
	ttl time.Duration,
) (blobguard.Work, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		work blobguard.Work
		busy bool
	)

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
		written, err := q.TakeBlobDeleteLease(ctx, db.TakeBlobDeleteLeaseParams{
			Holder: holder,
			TtlMs:  ttl.Milliseconds(),
		})
		if err != nil {
			return fmt.Errorf("take the blob delete lease: %w", err)
		}

		if written == 1 {
			return nil
		}

		row, err := q.RunningWork(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return errNoRunningWork
		}

		if err != nil {
			return fmt.Errorf("select running backup or restore: %w", err)
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
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()

	count, err := s.queries.CountLiveBlobDeleteLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("count live blob delete leases: %w", err)
	}

	return int(count), nil
}
