package servertest_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// swapRetryTrips is the number of round trips that one failed attempt of the
// swap step adds to a restore: the 40 statements of the attempt, from the
// read of the staged schema to the rollback, and the 6 statements of the
// transaction that ends the claim of the step so that the runner runs it
// again (begin, minimum version, lock, clock, end, commit).
const swapRetryTrips = 46

// sqlstateDeadlock is the SQLSTATE of a transaction that PostgreSQL ended to
// break a deadlock.
const sqlstateDeadlock = "40P01"

// TestRoundTripsOfRestoreWithSwapConflict forces the lock conflict that
// sometimes ends the swap transaction of a restore on a machine under load,
// and counts the restore. A second connection holds a write lock on the
// durable operations table, and asks for the installation row, which the
// swap holds, when the swap waits for the first lock. The swap is the
// transaction that waited first, so PostgreSQL ends it with a deadlock. The
// runner runs the step again and the restore succeeds. The test checks
// the exact count of the restore with the failed attempt, and the exact count
// of the restore without it, which is the count that the other restore flows
// check.
func TestRoundTripsOfRestoreWithSwapConflict(t *testing.T) {
	t.Parallel()

	size := backupSizes()[0]

	target, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	h := newRoundTripHarness(t, traceBackup, dustpan.WithBackupTarget(target, "test"),
		dustpan.WithOperationLease(roundTripLease))
	h.seedUnits(size.units)

	run := h.runBackupAndRestoreWith(h.forceSwapConflict)

	schema := currentSchemaCounts(t, size.units)
	want := restoreModel().count(schema)

	checkCount(t, "restore of "+size.name+" without the failed swap", counted(run.restore), want)
	checkCount(t, "restore of "+size.name+" with one failed swap",
		withoutTimerReads(withoutPrepares(run.restore)), want+swapRetryTrips)
}

// forceSwapConflict holds a write lock on the durable operations table, and
// closes a lock cycle with the first swap that waits for it. It returns the
// function that waits for the end of the conflict and fails the test when the
// swap was not the transaction that PostgreSQL ended.
func (h *rtHarness) forceSwapConflict() func() {
	h.t.Helper()

	ctx := h.t.Context()
	url := h.cluster.SchemaURL()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		h.t.Fatalf("connect the conflicting transaction: %v", err)
	}

	h.t.Cleanup(func() { closeConflictConn(h.t, conn) })

	tx, err := conn.Begin(ctx)
	if err != nil {
		h.t.Fatalf("begin the conflicting transaction: %v", err)
	}

	h.t.Cleanup(func() { rollbackConflictTx(h.t, tx) })

	// The swap renames the live schema aside and then locks the durable
	// operations table in it. A write lock of this transaction makes the swap
	// wait.
	if _, err = tx.Exec(ctx, "LOCK TABLE durable_operations IN ROW EXCLUSIVE MODE"); err != nil {
		h.t.Fatalf("lock the durable operations table: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- closeLockCycle(ctx, url, tx) }()

	return func() {
		h.t.Helper()

		select {
		case err := <-done:
			if err != nil {
				h.t.Fatalf("force the swap conflict: %v", err)
			}
		case <-time.After(settleLimit):
			h.t.Fatal("the forced swap conflict did not end in time")
		}
	}
}

// errSwapNotWaiting reports that no swap waited for the lock of the second
// transaction in time.
var errSwapNotWaiting = errors.New("the swap did not wait for the lock in time")

// closeConflictConn closes conn at the end of the test.
func closeConflictConn(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	if err := conn.Close(context.WithoutCancel(t.Context())); err != nil {
		t.Logf("close the conflicting connection: %v", err)
	}
}

// rollbackConflictTx ends tx at the end of the test. A transaction that the
// test ended already is closed, which is not an error.
func rollbackConflictTx(t *testing.T, tx pgx.Tx) {
	t.Helper()

	if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Logf("roll back the conflicting transaction: %v", err)
	}
}

// swapWaiting finds a swap that waits for a lock on the durable operations
// table.
const swapWaiting = "SELECT count(*) FROM pg_stat_activity " +
	"WHERE wait_event_type = 'Lock' AND query LIKE 'LOCK TABLE %.durable_operations,%'"

// closeLockCycle waits until a swap waits for the lock of tx, then asks tx
// for the installation row that the swap holds, which closes a cycle. It
// ends tx after the answer, so the runner can run the step again. It returns
// an error when PostgreSQL ended tx and not the swap.
func closeLockCycle(ctx context.Context, url string, tx pgx.Tx) (err error) {
	watch, connErr := pgx.Connect(ctx, url)
	if connErr != nil {
		return fmt.Errorf("connect the watcher: %w", connErr)
	}

	defer func() {
		if closeErr := watch.Close(context.WithoutCancel(ctx)); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	deadline := time.Now().Add(settleLimit)

	for time.Now().Before(deadline) {
		var waiting int
		if scanErr := watch.QueryRow(ctx, swapWaiting).Scan(&waiting); scanErr != nil {
			return fmt.Errorf("look for the waiting swap: %w", scanErr)
		}

		if waiting > 0 {
			return askForInstallationRow(ctx, tx)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the swap: %w", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}

	return errSwapNotWaiting
}

// askForInstallationRow locks the installation row in tx and ends tx. The
// lock waits for the swap, and the swap waits for tx.
func askForInstallationRow(ctx context.Context, tx pgx.Tx) error {
	var live string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
		return fmt.Errorf("read the live schema: %w", err)
	}

	_, err := tx.Exec(ctx, "SELECT state FROM "+pgx.Identifier{live + "_control"}.Sanitize()+
		".installation WHERE id = 1 FOR UPDATE")

	if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && err == nil {
		err = rollbackErr
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlstateDeadlock {
		return fmt.Errorf("PostgreSQL ended the conflicting transaction and not the swap: %w", err)
	}

	return err
}
