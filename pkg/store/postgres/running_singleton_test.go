package postgres_test

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestTwoHandlesStartOneBackupAndOneRestore opens two store handles on one
// schema, as two server instances do, and starts a backup and a restore on
// both at the same moment. The database admits one running backup and one
// running restore: the other start of each fails with the running error.
func TestTwoHandlesStartOneBackupAndOneRestore(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)
	handles := make([]*postgres.Store, 0, 2)

	for range 2 {
		s, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		handles = append(handles, s)
	}

	start := time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC)
	backupErrs := make([]error, len(handles))
	restoreErrs := make([]error, len(handles))

	var wg sync.WaitGroup

	for i, h := range handles {
		wg.Go(func() {
			backupErrs[i] = h.CreateBackup(t.Context(), backup.Record{
				StartedAt: start, EndedAt: time.Time{}, ID: backup.ID(ident.NewID("bkp-")),
				Target: "dir:/var/backups", Error: "", State: backup.StateRunning,
			})
		})
		wg.Go(func() {
			restoreErrs[i] = h.CreateRestore(t.Context(), backup.RestoreRecord{
				StartedAt: start, EndedAt: time.Time{}, ID: backup.RestoreID(ident.NewID("rst-")),
				BackupID: backup.ID(ident.NewID("bkp-")), PreBackupID: "", RequestedBy: "admin", Error: "",
				State: backup.RestoreChecking,
			})
		})
	}

	wg.Wait()

	checkOneStarted(t, "backup", backupErrs, backup.ErrAlreadyRunning)
	checkOneStarted(t, "restore", restoreErrs, backup.ErrRestoreRunning)
}

// checkOneStarted proves that exactly one start of errs succeeded and every
// other one failed with running.
func checkOneStarted(t *testing.T, what string, errs []error, running error) {
	t.Helper()

	started := 0

	for _, err := range errs {
		switch {
		case err == nil:
			started++
		case errors.Is(err, running):
		default:
			t.Fatalf("start %s: %v, want nil or %v", what, err, running)
		}
	}

	if started != 1 {
		t.Fatalf("%s starts that succeeded = %d of %d, want 1: %v", what, started, len(errs), errs)
	}
}
