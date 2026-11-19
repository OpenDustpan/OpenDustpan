package backup_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
)

// restoreSetupWithRow builds a restore service whose backup registry holds a
// row of bkp-restore in the state. The target holds the data of bkp-restore.
func restoreSetupWithRow(t *testing.T, state backup.State) *restoreServiceSetup {
	t.Helper()

	return restoreSetupOver(t, &state)
}

// restoreSetupWithoutRow builds a restore service whose backup registry holds
// no row. The target holds the data of bkp-restore.
func restoreSetupWithoutRow(t *testing.T) *restoreServiceSetup {
	t.Helper()

	return restoreSetupOver(t, nil)
}

// restoreSetupOver builds a restore service over a fresh backup registry. A
// non-nil state adds the row of bkp-restore in that state.
func restoreSetupOver(t *testing.T, state *backup.State) *restoreServiceSetup {
	t.Helper()

	return newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
		reg := newFakeRegistry()

		exporter := &stubBackupExporter{
			sum: testSummary(), data: nil, order: &orderLog{mu: sync.Mutex{}, events: nil},
			hook: atomic.Pointer[func()]{},
		}

		service, err := backup.NewService(backup.Config{
			Exporter: exporter, Kinds: fixedKind(installation.KindProduction), Registry: reg,
			Operations: reg.ops, Blobs: newMemStore(), StateOpener: nil, Target: cfg.Target,
			TargetName: "mem:target", Now: nil, Version: "test-1.0", MaxSQLBytes: 0, Owner: "", Lease: 0, Logger: nil,
		})
		if err != nil {
			t.Fatalf("new backup service: %v", err)
		}

		cfg.Backups = service

		if state != nil {
			reg.mu.Lock()
			defer reg.mu.Unlock()

			reg.rows["bkp-restore"] = backup.Record{
				StartedAt: time.UnixMilli(1_700_000_000_000).UTC(), EndedAt: time.Time{}, ID: "bkp-restore",
				Target: "mem:target", Error: "", State: *state, Attempt: "",
			}
		}
	})
}

func startRequestOf(id backup.ID) backup.RestoreRequest {
	return backup.RestoreRequest{
		BackupID: id, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester", KeepAccessTables: false,
	}
}

// TestRestoreStartRefusesABackupThatDidNotSucceed proves that a start for a
// backup that runs or failed is refused with a text that names the state, and
// that no restore row is made.
func TestRestoreStartRefusesABackupThatDidNotSucceed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state backup.State
		want  error
	}{
		{"running", backup.StateRunning, backup.ErrStillRunning},
		{"failed", backup.StateFailed, backup.ErrEndedFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := restoreSetupWithRow(t, tc.state)
			id := backup.MintRestoreID()

			if err := setup.service.Start(t.Context(), id, startRequestOf(setup.id)); !errors.Is(err, tc.want) {
				t.Fatalf("Start = %v, want %v", err, tc.want)
			}

			if _, err := setup.regs.GetRestore(t.Context(), id); !errors.Is(err, backup.ErrRestoreNotFound) {
				t.Errorf("restore row of a refused start: %v, want ErrRestoreNotFound", err)
			}
		})
	}
}

// TestRestoreStartRefusesAnUnknownBackup proves that an ID with no row and no
// data in the target answers ErrNotFound and makes no restore row, and that a
// backup with data in the target and no row (a new installation) still starts.
func TestRestoreStartRefusesAnUnknownBackup(t *testing.T) {
	t.Parallel()

	setup := restoreSetupWithoutRow(t)
	id := backup.MintRestoreID()

	err := setup.service.Start(t.Context(), id, startRequestOf("bkp-nonexistent"))
	if !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("Start of an unknown backup = %v, want ErrNotFound", err)
	}

	if _, getErr := setup.regs.GetRestore(t.Context(), id); !errors.Is(getErr, backup.ErrRestoreNotFound) {
		t.Errorf("restore row of a refused start: %v, want ErrRestoreNotFound", getErr)
	}

	fresh := backup.MintRestoreID()
	if err = setup.service.Start(t.Context(), fresh, startRequestOf(setup.id)); err != nil {
		t.Fatalf("Start of a backup with data and no row = %v, want nil", err)
	}

	if row := pollRestoreSucceeded(t, setup.regs, fresh); row.State != backup.RestoreSucceeded {
		t.Errorf("restore state = %q, want succeeded", row.State)
	}
}
