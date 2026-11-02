package storetest

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// restoreDamagedCases are the cases of a restore over a live blob that is
// missing or damaged. Blobs are write-once: a restore never writes over a
// live blob, and it stops before any change of the live data at a damaged
// live blob or at more missing live blobs than its bound allows. The backup
// before such a restore stays restorable.
func restoreDamagedCases() []restoreCase {
	return []restoreCase{
		{
			name: "a restore stops before any live change at a live blob that does not match its record",
			test: testWorkerDamagedLiveBlob,
		},
		{
			name: "a restore stops before any live change when more live blobs are missing than the bound allows",
			test: testWorkerMissingPastBound,
		},
		{
			name: "a restore with force never replaces a live blob that matched its record and differs from the backup",
			test: testWorkerChangedLiveBlob,
		},
		{
			name: "a backup before a restore that found a missing live blob restores and leaves that key as it is",
			test: testWorkerRestoreOfPreBackup,
		},
	}
}

// startWithPreBackupID starts restore id of backupID on restores with a
// pre-backup ID that the test knows, and returns that ID.
func (r *workerRig) startWithPreBackupID(
	t *testing.T, restores *backup.RestoreService, id backup.RestoreID, backupID backup.ID,
) backup.ID {
	t.Helper()

	pre := backup.ID(ident.NewID("bkp-"))
	req := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: pre, CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := restores.Start(t.Context(), id, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	return pre
}

// mustDamaged checks that the backup pre names only the live blob of the rig
// as damaged, with state and with the reference of its live row, and holds
// no copy of it.
func (r *workerRig) mustDamaged(t *testing.T, pre backup.ID, state backup.DamageState) {
	t.Helper()

	manifest, err := r.backups.ReadManifest(t.Context(), pre)
	if err != nil {
		t.Fatalf("read the manifest of backup %s: %v", pre, err)
	}

	if len(manifest.Damaged) != 1 || manifest.Damaged[0].Key != r.seed.liveBlob.String() ||
		manifest.Damaged[0].State != state {
		t.Fatalf("damaged entries of backup %s = %+v, want the key %s as %q",
			manifest.ID, manifest.Damaged, r.seed.liveBlob, state)
	}

	backed, err := r.backups.ReadManifest(t.Context(), r.backupID)
	if err != nil {
		t.Fatalf("read the manifest of backup %s: %v", r.backupID, err)
	}

	idx := slices.IndexFunc(
		backed.Blobs, func(b backup.ManifestBlob) bool { return b.Key == r.seed.liveBlob.String() },
	)
	if idx < 0 || backed.Blobs[idx].MD5 != manifest.Damaged[0].MD5 ||
		backed.Blobs[idx].Size != manifest.Damaged[0].Size {
		t.Fatalf("damaged entry %+v does not hold the reference of the live row", manifest.Damaged[0])
	}

	for _, ref := range manifest.Blobs {
		if ref.Key == r.seed.liveBlob.String() {
			t.Fatalf("backup %s lists the damaged key %s among its blobs", manifest.ID, ref.Key)
		}
	}
}

// mustStopped checks that restore row failed with text before any change of
// the live data, and that its report and the audit log name what the backup
// before it found.
func (r *workerRig) mustStopped(t *testing.T, row backup.RestoreRecord, text string, missing, damaged int) {
	t.Helper()

	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, text) ||
		!strings.Contains(row.Error, r.seed.liveBlob.String()) || !strings.Contains(row.Error, "changed no live data") {
		t.Fatalf("restore %s = %q %q, want failed with %q and the key", row.ID, row.State, row.Error, text)
	}

	if !r.hasLater(t) {
		t.Fatal("the restore changed the live data")
	}

	if row.DevReport == nil || row.DevReport.PreBackupFound == nil ||
		row.DevReport.PreBackupFound.Missing != missing || row.DevReport.PreBackupFound.Damaged != damaged ||
		!slices.Contains(row.DevReport.PreBackupFound.Keys, r.seed.liveBlob.String()) {
		t.Fatalf("report of restore %s = %+v, want %d missing and %d damaged", row.ID, row.DevReport, missing, damaged)
	}

	if n := r.restoreEntries(t, "backup "); n != 1 {
		t.Fatalf("audit entries of the findings = %d, want 1", n)
	}
}

func testWorkerDamagedLiveBlob(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	damaged := []byte("tampered")
	putBlob(t, r.live.Store, r.seed.liveBlob, damaged)

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	id := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, id, r.backupID)
	row := r.driveToEnd(t, a, id)

	r.mustDamaged(t, pre, backup.DamageMismatch)
	r.mustStopped(t, row, "do not match their records", 0, 1)

	if got := r.liveBlobBytes(t); !bytes.Equal(got, damaged) {
		t.Fatalf("live key = %q, want the damaged bytes that it held", got)
	}

	if n := r.restoreEntries(t, "restore "+id.String()+" refused to overwrite live blob"); n != 0 {
		t.Fatalf("audit entries of a refusal at the copy = %d, want 0: the copy step must not run", n)
	}
}

func testWorkerMissingPastBound(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// The live data names one blob, and it is missing: 100 percent, which a
	// wrong blob store setting gives.
	id := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, id, r.backupID)
	row := r.driveToEnd(t, a, id)

	r.mustDamaged(t, pre, backup.DamageMissing)
	r.mustStopped(t, row, "store check --target blobs", 1, 0)

	if _, err := r.live.Get(t.Context(), r.seed.liveBlob); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get the live key = %v, want ErrNotFound: the restore must not write it", err)
	}
}

func testWorkerChangedLiveBlob(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	gate := newStepGate(t, r, backup.RestoreStepCopyBlobs, gatePauseLive)
	a := r.service(t, workerService{
		owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	id := r.start(t, a, false, true)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	// The backup before the restore found the live key whole. The key then
	// holds other bytes, as live data that is newer than the backup does.
	// Force does not change the refusal.
	differing := []byte("bytes that differ from the backup")
	putBlob(t, r.live.Store, r.seed.liveBlob, differing)

	gate.open()
	<-aDone

	row := r.driveToEnd(t, a, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "corrupt") {
		t.Fatalf("restore over a live key that differs = %q %q, want failed as corrupt", row.State, row.Error)
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, differing) {
		t.Fatalf("live key = %q, want the bytes that it held before the restore", got)
	}

	refusal := "restore " + id.String() + " refused to overwrite live blob " + `"` + r.seed.liveBlob.String() + `"`
	if n := r.restoreEntries(t, refusal); n != 1 {
		t.Fatalf("audit entries of the refusal = %d, want 1", n)
	}
}

func testWorkerRestoreOfPreBackup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	whole := r.liveBlobBytes(t)

	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// The first restore stops at the missing blob. Its backup before the
	// restore records the key as missing and holds no copy of it.
	first := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, first, r.backupID)

	if row := r.driveToEnd(t, a, first); row.State != backup.RestoreFailed {
		t.Fatalf("first restore = %q (%s), want failed", row.State, row.Error)
	}

	r.mustDamaged(t, pre, backup.DamageMissing)

	if err := backup.Check(t.Context(), r.target, pre); err != nil {
		t.Fatalf("backup check of the backup before the restore = %v, want nil", err)
	}

	// The operator puts the blob back. The backup before the first restore
	// must restore: its data names the key, and the restore leaves the key
	// as it is.
	putBlob(t, r.live.Store, r.seed.liveBlob, whole)

	second := backup.MintRestoreID()
	r.startWithPreBackupID(t, a, second, pre)
	row := r.driveToEnd(t, a, second)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore of the backup before a restore = %q (%s), want succeeded", row.State, row.Error)
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, whole) {
		t.Fatalf("live key = %q, want the bytes that it held", got)
	}

	codes := make([]backup.RestoreWarningCode, 0, len(row.Warnings()))
	for _, w := range row.Warnings() {
		codes = append(codes, w.Code)
	}

	if !slices.Contains(codes, backup.WarnBlobsNotHeld) {
		t.Fatalf("warnings of restore %s = %v, want %q", row.ID, codes, backup.WarnBlobsNotHeld)
	}

	if row.DevReport == nil || row.DevReport.NotHeld == nil || row.DevReport.NotHeld.Missing != 1 ||
		!slices.Contains(row.DevReport.NotHeld.Keys, r.seed.liveBlob.String()) {
		t.Fatalf("report of restore %s = %+v, want the key that the backup does not hold", row.ID, row.DevReport)
	}
}
