package sqlite_test

import (
	"time"

	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestItemKeyBackupRestore proves that a backup copies the wrapped item key
// blobs and that content opens after a restore brought back a lost blob.
func TestItemKeyBackupRestore(t *testing.T) {
	t.Parallel()

	storetest.RunItemKeyBackup(t, func(t *testing.T, in storetest.ItemKeyBackupInput) *storetest.ItemKeyBackupRig {
		t.Helper()

		st, ok := openBackup(t).(*backupStore)
		if !ok {
			t.Fatal("the backup store is not a *backupStore")
		}

		if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
			t.Fatalf("claim the installation kind: %v", err)
		}

		live, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open live blob store: %v", err)
		}

		t.Cleanup(func() { _ = live.Close() }) //nolint:errcheck // Test cleanup.

		backups, err := backup.NewService(backup.Config{
			Exporter: st, Kinds: st, Registry: st, Blobs: live, Target: in.Target, TargetName: "dir:test",
			Operations: st.DurableOperationsPort(), Version: "test", StateOpener: nil, Owner: "", Lease: 0, Now: nil,
			MaxSQLBytes: 0, Logger: nil,
		})
		if err != nil {
			t.Fatalf("new backup service: %v", err)
		}

		restores, err := backup.NewRestoreService(backup.RestoreConfig{
			Live: st, Operations: st.DurableOperationsPort(), Backups: backups,
			Maintenance: storetest.StateMaintenance{State: st, Hold: false},
			Blobs:       live, Target: in.Target, Key: in.Key, Installation: "test", AfterApply: nil, Admit: nil,
			AllowDevBackups: false, AllowDevHint: "", Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
		})
		if err != nil {
			t.Fatalf("new restore service: %v", err)
		}

		return &storetest.ItemKeyBackupRig{Store: st, Backups: backups, Restores: restores, Live: live}
	})
}
