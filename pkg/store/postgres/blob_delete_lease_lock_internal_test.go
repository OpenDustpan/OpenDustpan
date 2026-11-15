package postgres

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// leaseTakeResult holds the three answers of one TakeLease call.
type leaseTakeResult struct {
	work blobguard.Work
	busy bool
	err  error
}

// TestBlobDeleteLeaseTakeWaitsForRestoreStart proves that a take of a blob
// delete lease waits on the exclusive form of the blob delete lease lock that
// a start of a restore holds, and that the take which waited sees the
// checking row that the start commits: the take answers the restore as the
// running work, and it writes no lease row.
func TestBlobDeleteLeaseTakeWaitsForRestoreStart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	s := openKeepLockStore(t, testPostgresURL(t))

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	defer rollbackQuietly(context.WithoutCancel(t.Context()), tx)

	// The start of a restore takes the exclusive form of the lease lock
	// before it writes the checking row. The raw transaction holds that lock
	// until it commits, so the take must wait.
	if err = db.New(tx).LockBlobDeleteLeaseExclusive(ctx, blobDeleteLockClass); err != nil {
		t.Fatalf("take the exclusive lease lock: %v", err)
	}

	taken := make(chan leaseTakeResult, 1)

	go func() {
		work, busy, takeErr := s.TakeLease(ctx, "dlt-test", time.Minute)
		taken <- leaseTakeResult{work: work, busy: busy, err: takeErr}
	}()

	waitForWaitingLeaseLock(ctx, t, s, taken)

	restoreID := ident.NewID("rst-")
	backupID := ident.NewID("bkp-")
	at := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)

	// The checking row, as the start of a restore writes it, in the same
	// transaction that holds the exclusive lock. The commit releases the lock
	// and makes the row visible to the take that waited.
	_, err = tx.Exec(ctx,
		`INSERT INTO restores (id, state, backup_id, started_at_ms) VALUES ($1, 'checking', $2, $3)`,
		restoreID, backupID, at.UnixMilli())
	if err != nil {
		t.Fatalf("insert the checking restore: %v", err)
	}

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got := <-taken

	assertTakeSawRestore(t, got, restoreID)
	assertNoLeaseRows(ctx, t, s)
}

// waitForWaitingLeaseLock waits until pg_locks shows the take as a waiting
// advisory lock of the blob delete class in the database of s, or the context
// ends. The test has a database of its own, so a waiter of another test does
// not count. It fails at once when the take ends without a wait.
func waitForWaitingLeaseLock(ctx context.Context, t *testing.T, s *Store, taken <-chan leaseTakeResult) {
	t.Helper()

	const query = `SELECT EXISTS (
		SELECT 1 FROM pg_locks
		WHERE locktype = 'advisory' AND NOT granted AND classid = $1::oid
			AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	)`

	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("the take did not wait on the lease lock: %v", err)
		}

		var waiting bool

		if err := s.pool.QueryRow(ctx, query, blobDeleteLockClass).Scan(&waiting); err != nil {
			t.Fatalf("read the waiting lease lock: %v", err)
		}

		if waiting {
			return
		}

		select {
		case got := <-taken:
			t.Fatalf("the take ended before it waited on the lease lock: busy = %v, err = %v", got.busy, got.err)
		default:
		}
	}
}

// assertTakeSawRestore checks the answer of a take that waited for the
// commit: no error, busy, and the work of the restore that started.
func assertTakeSawRestore(t *testing.T, got leaseTakeResult, restoreID string) {
	t.Helper()

	if got.err != nil {
		t.Fatalf("take after the restore start committed: %v", got.err)
	}

	if !got.busy {
		t.Fatal("take after the restore start committed: busy = false, want true")
	}

	if got.work.Kind != blobguard.Restore || got.work.ID != restoreID {
		t.Fatalf("take after the restore start committed: work = %v, want restore %s", got.work, restoreID)
	}
}

// assertNoLeaseRows checks that the take wrote no lease row: no live lease,
// and no row of its holder.
func assertNoLeaseRows(ctx context.Context, t *testing.T, s *Store) {
	t.Helper()

	live, err := s.LiveLeases(ctx)
	if err != nil {
		t.Fatalf("count live leases: %v", err)
	}

	if live != 0 {
		t.Fatalf("live leases after the take = %d, want 0", live)
	}

	var rows int64

	err = s.pool.QueryRow(ctx,
		`SELECT count(*) FROM blob_delete_leases WHERE holder = $1`, "dlt-test").Scan(&rows)
	if err != nil {
		t.Fatalf("count the lease rows of the take: %v", err)
	}

	if rows != 0 {
		t.Fatalf("lease rows of holder dlt-test = %d, want 0", rows)
	}
}
