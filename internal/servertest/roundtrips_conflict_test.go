package servertest_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// swapRetryTrips is the number of round trips that one failed attempt of the
// swap step adds to a restore: the 41 statements of the attempt, from the
// read of the staged schema to the rollback, and the 6 statements of the
// transaction that ends the claim of the step so that the runner runs it
// again (begin, minimum version, lock, clock, end, commit). The attempt
// counts the rows of each class of the dev strip once, so a new class adds
// one statement. The 47 statements of the first form count the eleven classes
// that were there before the Git host links; the two Git host classes add
// forgeStripClassTrips more. The check of the working break-glass accounts
// reads the clock and counts the live data before the swap fails, which adds
// breakGlassFloorRetryTrips.
const swapRetryTrips = 47 + forgeStripClassTrips + breakGlassFloorRetryTrips

// breakGlassFloorRetryTrips is the number of round trips of the check of the
// working break-glass accounts in a failed attempt of the swap step: the read
// of the clock and the count before the swap. The count after the swap does
// not run.
const breakGlassFloorRetryTrips = 2

// sqlstateDeadlock is the SQLSTATE of a transaction that PostgreSQL ended to
// break a deadlock.
const sqlstateDeadlock = "40P01"

// TestRoundTripsOfRestoreWithSwapConflict forces the lock conflict that
// sometimes ends the swap transaction of a restore on a machine under load,
// and counts the restore. A second connection holds a write lock on the
// durable operations table and asks for the installation row that the swap
// holds, so the two transactions wait for each other. The swap runs the
// deadlock check that finds the cycle, so PostgreSQL ends the swap. The
// runner runs the step again and the restore succeeds. The test checks the
// exact count of the restore with the failed attempt, and the exact count of
// the restore without it, which is the count that the other restore flows
// check.
//
// A waiting backend runs its deadlock check once, deadlock_timeout after its
// wait starts, and PostgreSQL ends the backend that finds the cycle. The
// second connection sets a long deadlock_timeout, so it never checks. The
// hook of the tracer waits until the ask of the second connection waits for
// the installation row before the swap sends its lock, so the cycle is closed
// before the swap starts to wait, and the swap finds it at its check.
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
// closes a lock cycle with the first swap that locks the durable control
// tables. It returns the function that waits for the end of the conflict and
// fails the test when the swap was not the transaction that PostgreSQL ended.
func (h *rtHarness) forceSwapConflict() func() {
	h.t.Helper()

	ctx := h.t.Context()
	conn, tx, live := h.startConflictTx()

	watch, err := pgx.Connect(ctx, h.cluster.SchemaURL())
	if err != nil {
		h.t.Fatalf("connect the watcher: %v", err)
	}

	h.t.Cleanup(func() { closeTestConn(h.t, watch, "watcher") })

	done := make(chan error, 1)
	waited := make(chan error, 1)

	// The hook runs on the goroutine of the swap, before the swap sends its
	// lock on the durable control tables. The swap holds the installation row
	// at that point. The hook starts the ask of the installation row on the
	// conflicting connection and waits until that ask waits for the row. The
	// swap then sends its lock and waits for the conflicting transaction, so
	// the cycle is closed before the swap starts to wait, and the swap finds
	// it at its deadlock check deadlock_timeout later.
	h.tracer.setSwapHook(func() {
		go func() { done <- askForInstallationRow(ctx, tx, live) }()

		waited <- waitForConflictWait(ctx, watch, conn.PgConn().PID())
	})

	return func() {
		h.t.Helper()

		waitConflictResult(ctx, h.t, waited)
		waitConflictResult(ctx, h.t, done)
	}
}

// startConflictTx starts the conflicting transaction of forceSwapConflict:
// it connects, begins, sets the long deadlock_timeout, reads the live schema
// name, and takes the write lock on the durable operations table. It returns
// the connection, the transaction and the live schema name. The test ends
// the connection and the transaction.
func (h *rtHarness) startConflictTx() (*pgx.Conn, pgx.Tx, string) {
	h.t.Helper()

	ctx := h.t.Context()

	conn, err := pgx.Connect(ctx, h.cluster.SchemaURL())
	if err != nil {
		h.t.Fatalf("connect the conflicting transaction: %v", err)
	}

	h.t.Cleanup(func() { closeTestConn(h.t, conn, "conflicting") })

	tx, err := conn.Begin(ctx)
	if err != nil {
		h.t.Fatalf("begin the conflicting transaction: %v", err)
	}

	h.t.Cleanup(func() { rollbackConflictTx(h.t, tx) })

	// A waiting backend runs its deadlock check once, deadlock_timeout after
	// its wait starts, and PostgreSQL ends the backend that finds the cycle.
	// The long timeout keeps this transaction from checking, so the swap is
	// the only backend that checks and the swap is the one that PostgreSQL
	// ends. The statement needs a role that can set deadlock_timeout.
	if _, err = tx.Exec(ctx, "SET LOCAL deadlock_timeout = '1h'"); err != nil {
		h.t.Fatalf("set deadlock_timeout of the conflicting transaction: %v;"+
			" the test needs a role that can set deadlock_timeout", err)
	}

	// Read the live schema name before the conflict: the swap renames the
	// live schema aside, and the ask of the installation row must name the
	// control schema of the live installation.
	var live string
	if err = tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
		h.t.Fatalf("read the live schema: %v", err)
	}

	// The swap renames the live schema aside and then locks the durable
	// operations table in it. A write lock of this transaction makes the swap
	// wait.
	if _, err = tx.Exec(ctx, "LOCK TABLE durable_operations IN ROW EXCLUSIVE MODE"); err != nil {
		h.t.Fatalf("lock the durable operations table: %v", err)
	}

	return conn, tx, live
}

// waitConflictResult waits for the result of one part of the forced conflict,
// and fails the test when it is an error.
func waitConflictResult(ctx context.Context, t *testing.T, result <-chan error) {
	t.Helper()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("force the swap conflict: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for the forced swap conflict: %v", ctx.Err())
	}
}

// conflictWaiting finds the conflicting transaction when it waits for a
// lock.
const conflictWaiting = "SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock'"

// waitForConflictWait waits until the conflicting transaction waits for a
// lock. The conflicting transaction asks for the installation row, which the
// swap holds, so the wait closes the cycle when the swap starts to wait for
// the lock of the conflicting transaction.
func waitForConflictWait(ctx context.Context, watch *pgx.Conn, pid uint32) error {
	for {
		var waiting int
		if err := watch.QueryRow(ctx, conflictWaiting, pid).Scan(&waiting); err != nil {
			return fmt.Errorf("look for the waiting conflicting transaction: %w", err)
		}

		if waiting > 0 {
			return nil
		}

		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for the conflicting transaction: %w", err)
		}
	}
}

// closeTestConn closes conn at the end of the test. The name names the
// connection in the log.
func closeTestConn(t *testing.T, conn *pgx.Conn, name string) {
	t.Helper()

	if err := conn.Close(context.WithoutCancel(t.Context())); err != nil {
		t.Logf("close the %s connection: %v", name, err)
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

// askForInstallationRow locks the installation row of live in tx and ends
// tx. The lock waits for the swap, and the swap waits for tx.
func askForInstallationRow(ctx context.Context, tx pgx.Tx, live string) error {
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
