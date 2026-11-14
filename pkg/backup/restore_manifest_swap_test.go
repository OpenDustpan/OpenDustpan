package backup_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestRestoreSwappedManifestIsRefusedAtEachStep proves that a restore fails
// with ErrCorrupt and applies nothing when the manifest changes after the
// pin. Each row swaps the manifest from one manifest read on. Read 1 is the
// pin, read 2 is the stage check and read 3 is the check step. The test
// checks the end state only, so it does not show which step refused.
func TestRestoreSwappedManifestIsRefusedAtEachStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from int
	}{
		{name: "stage", from: 2},
		{name: "check", from: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runSwappedManifestRestore(t, tc.from)
		})
	}
}

// runSwappedManifestRestore starts a restore whose manifest reads answer
// tampered bytes from the read at from on. It fails the test unless the
// restore fails with ErrCorrupt and applies nothing.
func runSwappedManifestRestore(t *testing.T, from int) {
	t.Helper()

	setup := newRestoreServiceSetup(t)

	raw := setup.target.readObject(t, "backups/"+setup.id.String()+"/manifest.json")

	var manifest backup.Manifest

	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	manifest.SQLSize++

	tampered, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode tampered manifest: %v", err)
	}

	proxy := &mutatingTarget{
		inner: setup.target, tampered: tampered, mu: sync.Mutex{}, manifests: 0, from: from,
	}

	service, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            setup.live,
		Operations:      setup.live.ops,
		Backups:         setup.backups,
		Maintenance:     setup.maint,
		Blobs:           setup.blobs,
		Target:          proxy,
		Key:             testWrapper(),
		Installation:    "eu-west",
		AfterApply:      nil,
		Admit:           nil,
		Now:             nil,
		Logger:          nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	setup.regs.setWorker(service)

	id := backup.MintRestoreID()
	req := backup.RestoreRequest{
		BackupID:         setup.id,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: false,
	}

	err = service.Start(t.Context(), id, req)
	if err != nil {
		t.Fatalf("start restore: %v", err)
	}

	row := waitRestoreEnd(t, setup.regs, id)

	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want failed", row.State)
	}

	if row.Error != backup.ErrCorrupt.Error() {
		t.Fatalf("restore error = %q, want %q", row.Error, backup.ErrCorrupt)
	}

	if got := setup.live.counts(); got.applied != 0 || got.bumped != 0 {
		t.Fatalf("applied = %d, bumped = %d, want no apply after a manifest swap", got.applied, got.bumped)
	}
}
