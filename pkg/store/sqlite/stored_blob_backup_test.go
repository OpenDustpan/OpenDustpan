package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// storedDigestEraser clears the check values of the rows that name a blob, with
// direct SQL on a second handle, as the rows of an earlier version hold them.
type storedDigestEraser struct{ path string }

// EraseStoredDigests implements storetest.StoredDigestEraser.
func (e storedDigestEraser) EraseStoredDigests(ctx context.Context) error {
	conn, err := sql.Open("sqlite", "file:"+e.path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return fmt.Errorf("open the database: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for _, stmt := range []string{
		`UPDATE job_log_chunks SET stored_size = 0, stored_md5 = X''`,
		`UPDATE registry_releases SET stored_files_json = ''`,
	} {
		if _, execErr := conn.ExecContext(ctx, stmt); execErr != nil {
			return fmt.Errorf("erase stored digests: %w", execErr)
		}
	}

	return nil
}

// newStoredBlobRig returns a rig over a fresh store and a fresh live blob store.
func newStoredBlobRig(t *testing.T, in storetest.StoredBlobBackupInput) *storetest.StoredBlobBackupRig {
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

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live blob store: %v", closeErr)
		}
	})

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
		Maintenance: storetest.StateMaintenance{State: st, Hold: false}, Blobs: live,
		Target: in.Target, Key: in.Key, Installation: "test", AfterApply: nil, Admit: nil, AllowDevBackups: false,
		AllowDevHint: "", Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	jobs := st.Jobs()
	ops := st.DurableOperationsPort()

	return &storetest.StoredBlobBackupRig{
		Recorder: jobtest.NewRecorderHarness(t, jobtest.RecorderParts{
			Harness: jobtest.Harness{
				Jobs:       jobs,
				Durable:    ops,
				Freezes:    jobs,
				Claimer:    job.NewClaimer(jobs, ops, st.Store, jobtest.NoGates{}),
				Namespaces: st.Store,
				Domain:     &jobDomain{s: st.Store},
				WriteState: nil,
			},
			Records: jobs, ItemKeys: st.Store, Blobs: live, Wrapper: in.Key,
		}),
		Registry: st.Registry(),
		Keys:     st.Store,
		Backups:  backups,
		Restores: restores,
		Eraser:   storedDigestEraser{path: st.path},
	}
}

func TestStoredBlobBackup(t *testing.T) {
	t.Parallel()

	storetest.RunStoredBlobBackup(t, newStoredBlobRig)
}

func TestStoredBlobBackupUnverified(t *testing.T) {
	t.Parallel()

	storetest.RunStoredBlobBackupUnverified(t, newStoredBlobRig)
}
