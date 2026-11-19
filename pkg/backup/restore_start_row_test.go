package backup_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
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

	var registered *backup.Service

	setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
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
		registered = service

		if state != nil {
			reg.mu.Lock()
			defer reg.mu.Unlock()

			reg.rows["bkp-restore"] = backup.Record{
				StartedAt: time.UnixMilli(1_700_000_000_000).UTC(), EndedAt: time.Time{}, ID: "bkp-restore",
				Target: "mem:target", Error: "", State: *state, Attempt: "",
			}
		}
	})

	// The service of the setup reads the registry that holds the row.
	setup.backups = registered

	return setup
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
		{"unknown state", backup.State("bogus"), backup.ErrEndedFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := restoreSetupWithRow(t, tc.state)
			id := backup.MintRestoreID()

			if err := setup.backups.RequireSucceeded(t.Context(), setup.id); !errors.Is(err, tc.want) {
				t.Fatalf("RequireSucceeded = %v, want %v", err, tc.want)
			}

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

// getFailStore fails the Get of every live pointer with err and passes all
// other calls to the embedded store.
type getFailStore struct {
	blob.Store

	err error
}

func (s getFailStore) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if strings.HasSuffix(key.String(), "/live.json") {
		return nil, s.err
	}

	return s.Store.Get(ctx, key)
}

// TestRestoreStartRefusesAPointerThatCannotBeRead proves that a start for a
// backup with no row refuses, and makes no restore row, when the live pointer
// of the target exists but cannot be read. The start must not pass such a
// pointer to the restore.
func TestRestoreStartRefusesAPointerThatCannotBeRead(t *testing.T) {
	t.Parallel()

	const (
		pointerID = "bkp-pointer0001"
		pointer   = "backups/" + pointerID + "/live.json"
	)

	tests := []struct {
		name    string
		pointer []byte
		getErr  error
		want    error
	}{
		{"not JSON", []byte("not json"), nil, backup.ErrCorrupt},
		{"oversize", []byte(`{"attempt":"` + strings.Repeat("a", 2048) + `"}`), nil, backup.ErrCorrupt},
		{"bad attempt", []byte(`{"attempt":"5/../6"}`), nil, backup.ErrCorrupt},
		{"target error", nil, errTargetUnavailable, errTargetUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newRestoreServiceSetupWith(t, func(cfg *backup.RestoreConfig) {
				if tc.getErr != nil {
					cfg.Target = getFailStore{Store: cfg.Target, err: tc.getErr}
				}
			})

			if tc.pointer != nil {
				setup.target.storeObject(t, pointer, tc.pointer)
			}

			id := backup.MintRestoreID()

			err := setup.service.Start(t.Context(), id, startRequestOf(pointerID))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start = %v, want %v", err, tc.want)
			}

			if _, getErr := setup.regs.GetRestore(t.Context(), id); !errors.Is(getErr, backup.ErrRestoreNotFound) {
				t.Errorf("restore row of a refused start: %v, want ErrRestoreNotFound", getErr)
			}
		})
	}
}

// TestRestoreStartRefusesAHostileBackupID proves that a start with a backup ID
// that is not valid answers ErrInvalidID and makes no restore row, and that
// the reader of the live pointer refuses the same IDs.
func TestRestoreStartRefusesAHostileBackupID(t *testing.T) {
	t.Parallel()

	ids := []string{
		"bkp-../x", "bkp-a/b", "bkp-a\x00", "bkp-" + strings.Repeat("a", 10_000), "../x", `bkp-a\b`,
	}

	setup := restoreSetupWithoutRow(t)

	for _, hostile := range ids {
		id := backup.MintRestoreID()

		err := setup.service.Start(t.Context(), id, startRequestOf(backup.ID(hostile)))
		if !errors.Is(err, backup.ErrInvalidID) {
			t.Errorf("Start of %.20q = %v, want ErrInvalidID", hostile, err)
		}

		if _, getErr := setup.regs.GetRestore(t.Context(), id); !errors.Is(getErr, backup.ErrRestoreNotFound) {
			t.Errorf("restore row of %.20q: %v, want ErrRestoreNotFound", hostile, getErr)
		}

		if _, readErr := backup.ReadLiveForTest(t.Context(), setup.target, backup.ID(hostile), 1<<10); !errors.Is(
			readErr, backup.ErrInvalidID,
		) {
			t.Errorf("ReadLiveForTest of %.20q = %v, want ErrInvalidID", hostile, readErr)
		}
	}
}
