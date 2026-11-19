package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// settingLockWait is how long the test waits to see that a write blocks.
const settingLockWait = 500 * time.Millisecond

// TestCheckWidenedSettingLocksTheSettingRow proves that the read of the live
// setting row holds a lock until the swap transaction ends: a write to the row
// on another connection blocks until the commit, so the setting cannot change
// between the check and the swap. The test fails if FOR SHARE leaves the read.
func TestCheckWidenedSettingLocksTheSettingRow(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	ctx := t.Context()

	if _, err := s.pool.Exec(
		ctx, `INSERT INTO settings (scope_id, key, value, updated_at_ms) VALUES ($1, $2, 'true', 0)
			ON CONFLICT (scope_id, key) DO UPDATE SET value = 'true'`,
		serverScopeID, backup.SettingRestoreAccessTables,
	); err != nil {
		t.Fatalf("write the setting row: %v", err)
	}

	swapTx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the swap transaction: %v", err)
	}

	t.Cleanup(func() {
		if rollbackErr := swapTx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil {
			t.Logf("roll back the swap transaction: %v", rollbackErr)
		}
	})

	var d backup.SwapDecision

	d.Widened = true

	if checkErr := checkWidenedSetting(ctx, swapTx, d); checkErr != nil {
		t.Fatalf("checkWidenedSetting with the setting true: %v", checkErr)
	}

	const update = `UPDATE settings SET value = 'false' WHERE scope_id = $1 AND key = $2`

	// The write runs in its own transaction, so that a timeout ends it.
	blocked, cancel := context.WithTimeout(ctx, settingLockWait)
	defer cancel()

	_, err = s.pool.Exec(blocked, update, serverScopeID, backup.SettingRestoreAccessTables)
	if !errors.Is(err, context.DeadlineExceeded) && blocked.Err() == nil {
		t.Fatalf("write to the locked setting row returned %v, want a block until the commit", err)
	}

	if commitErr := swapTx.Commit(ctx); commitErr != nil {
		t.Fatalf("commit the swap transaction: %v", commitErr)
	}

	if _, err = s.pool.Exec(ctx, update, serverScopeID, backup.SettingRestoreAccessTables); err != nil {
		t.Fatalf("write to the setting row after the commit: %v", err)
	}
}
