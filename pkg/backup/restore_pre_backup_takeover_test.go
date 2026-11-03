package backup_test

import (
	"context"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
)

// TestRestoreThatStopsDuringItsPreBackupFinishesWithTheSameBackup proves that
// a restore that stops while it holds the claim of its backup before restore
// leaves that backup to the next holder. The first run of the worker stops
// inside the export of the backup. The next run takes the restore over,
// finds the backup that the restore started, finishes it in a second attempt,
// and applies with it.
func TestRestoreThatStopsDuringItsPreBackupFinishesWithTheSameBackup(t *testing.T) {
	t.Parallel()

	setup := newRestoreServiceSetup(t)
	stopped, stop := context.WithCancel(t.Context())

	var once sync.Once

	hook := func() { once.Do(stop) }
	setup.exporter.hook.Store(&hook)

	id := backup.MintRestoreID()
	if err := setup.service.Start(t.Context(), id, backup.RestoreRequest{
		BackupID: setup.id, PreBackupID: "", CheckOnly: false, Force: true, RequestedBy: "tester",
		KeepAccessTables: false,
	}); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	// The first run stops inside the export of the backup before restore,
	// while the restore holds the claim of that backup.
	setup.regs.runWorker(stopped)

	pre := backupOperations(t, setup.backupOps)
	if len(pre) != 1 || pre[0].State != durable.StatePending || pre[0].Attempt != 1 {
		t.Fatalf("backup operations after the stop = %+v, want one pending operation after one attempt", pre)
	}

	row := waitRestoreEnd(t, setup.regs, id)
	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore state = %q, want succeeded (%q)", row.State, row.Error)
	}

	after := backupOperations(t, setup.backupOps)
	if len(after) != 1 || after[0].ID != pre[0].ID || after[0].State != durable.StateSucceeded ||
		after[0].Attempt != 2 {
		t.Fatalf("backup operations after the restore = %+v, want the same operation, succeeded in attempt 2", after)
	}

	if row.PreBackupID == "" || backup.OperationKey(row.PreBackupID) != after[0].Key {
		t.Fatalf("restore applied with the backup %q, want the backup of operation %s", row.PreBackupID, after[0].Key)
	}

	if got, err := setup.backups.Get(t.Context(), row.PreBackupID); err != nil || got.State != backup.StateSucceeded {
		t.Fatalf("backup before restore %s = %+v, %v; want succeeded", row.PreBackupID, got, err)
	}
}

// backupOperations lists the backup operations of ops.
func backupOperations(t *testing.T, ops durable.Store) []durable.Operation {
	t.Helper()

	out, err := ops.List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list backup operations: %v", err)
	}

	return out
}
