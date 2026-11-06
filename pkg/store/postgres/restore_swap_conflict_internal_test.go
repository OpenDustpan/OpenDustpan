package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// sqlstateDeadlock is the SQLSTATE of a transaction that PostgreSQL ended to
// break a deadlock.
const sqlstateDeadlock = "40P01"

// conflictWait bounds the wait of the forced conflict for the swap.
const conflictWait = 30 * time.Second

// errSwapNotWaiting reports that no swap waited for the lock of the second
// transaction in time.
var errSwapNotWaiting = errors.New("the swap did not wait for the lock in time")

// TestRestoreSwapLosesALockConflictOnTheDurableTables forces the conflict
// that can end a swap while another transaction writes the durable tables:
// the other transaction holds a write lock on the durable operations table
// and then asks for the installation row that the swap holds. The swap waits
// for the first lock in keepControlTables, so the two wait for each other.
// The swap is the one that waited first, so PostgreSQL ends the swap. The
// swap must roll back as a whole, with the error of the deadlock, and a
// second swap, as the retry of the step, must succeed.
func TestRestoreSwapLosesALockConflictOnTheDurableTables(t *testing.T) {
	t.Parallel()

	url := membershipURL(t)
	s := openAt(t, url, "")
	staged := storetest.StageRestoreArea(t, s)

	tx := holdDurableOperations(t, url)

	conflict := make(chan error, 1)

	go func() { conflict <- closeLockCycle(t.Context(), url, tx) }()

	_, applyErr := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, false)

	if err := <-conflict; err != nil {
		t.Fatalf("force the conflict: %v", err)
	}

	var pgErr *pgconn.PgError
	if !errors.As(applyErr, &pgErr) || pgErr.Code != sqlstateDeadlock {
		t.Fatalf("swap with a forced conflict = %v, want SQLSTATE %s", applyErr, sqlstateDeadlock)
	}

	ops := s.DurableOperationsPort()

	if _, ok := mustGetOperation(t, ops, staged.Swap.Operation).Step(backup.RestoreStepSwap); ok {
		t.Fatal("the failed swap recorded its step")
	}

	if _, err := s.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, false); err != nil {
		t.Fatalf("swap after the conflict = %v, want nil", err)
	}

	if _, ok := mustGetOperation(t, ops, staged.Swap.Operation).Step(backup.RestoreStepSwap); !ok {
		t.Fatal("the swap after the conflict did not record its step")
	}
}

// holdDurableOperations starts a transaction on a second connection that
// holds a write lock on the durable operations table. The swap renames the
// live schema aside and then locks that table in it, so the lock makes the
// swap wait. The test ends the connection and the transaction.
func holdDurableOperations(t *testing.T, url string) pgx.Tx {
	t.Helper()

	other, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatalf("connect the second connection: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := other.Close(context.WithoutCancel(t.Context())); closeErr != nil {
			t.Logf("close the second connection: %v", closeErr)
		}
	})

	tx, err := other.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin the second transaction: %v", err)
	}

	t.Cleanup(func() {
		// The conflict ends the transaction, so a closed transaction is not
		// an error.
		if rollbackErr := tx.Rollback(context.WithoutCancel(t.Context())); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Logf("roll back the second transaction: %v", rollbackErr)
		}
	})

	if _, err = tx.Exec(t.Context(), "LOCK TABLE durable_operations IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatalf("lock the durable operations table: %v", err)
	}

	return tx
}

// closeLockCycle waits until a swap waits for a lock of tx, then asks tx for
// the installation row that the swap holds, which closes a cycle. It ends tx
// after the answer. It returns an error when PostgreSQL ended tx and not the
// swap.
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

	const waiting = "SELECT count(*) FROM pg_stat_activity " +
		"WHERE wait_event_type = 'Lock' AND query LIKE 'LOCK TABLE %.durable_operations,%'"

	deadline := time.Now().Add(conflictWait)

	for time.Now().Before(deadline) {
		var n int
		if scanErr := watch.QueryRow(ctx, waiting).Scan(&n); scanErr != nil {
			return fmt.Errorf("look for the waiting swap: %w", scanErr)
		}

		if n > 0 {
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

	_, err := tx.Exec(ctx, "SELECT state FROM "+pgIdent(live+"_control")+".installation WHERE id = 1 FOR UPDATE")

	if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && err == nil {
		err = rollbackErr
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == sqlstateDeadlock {
		return fmt.Errorf("PostgreSQL ended the second transaction and not the swap: %w", err)
	}

	return err
}
