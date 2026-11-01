package storetest

import (
	"bytes"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// restoreDamagedCases are the cases of a restore over a live blob that is
// missing, damaged, or that differs from the backup and matches its own row.
func restoreDamagedCases() []restoreCase {
	return []restoreCase{
		{
			name: "a restore writes a live blob that the backup before the restore found missing",
			test: testWorkerMissingLiveBlob,
		},
		{
			name: "a restore replaces a live blob that the backup before the restore found damaged",
			test: testWorkerDamagedLiveBlob,
		},
		{
			name: "a restore refuses a damaged live blob whose bytes changed after the backup before the restore",
			test: testWorkerDamagedThenChanged,
		},
		{
			name: "a restore with force never replaces a live blob that matched its record and differs from the backup",
			test: testWorkerChangedLiveBlob,
		},
	}
}

// preBackupOf returns the manifest of the backup before restore row.
func (r *workerRig) preBackupOf(t *testing.T, row backup.RestoreRecord) backup.Manifest {
	t.Helper()

	if row.PreBackupID == "" {
		t.Fatalf("restore %s names no backup before the restore", row.ID)
	}

	manifest, err := r.backups.ReadManifest(t.Context(), row.PreBackupID)
	if err != nil {
		t.Fatalf("read the manifest of backup %s: %v", row.PreBackupID, err)
	}

	return manifest
}

// mustDamaged checks that manifest names only the live blob of the rig as
// damaged, with state.
func (r *workerRig) mustDamaged(t *testing.T, manifest backup.Manifest, state backup.DamageState) backup.DamagedBlob {
	t.Helper()

	if len(manifest.Damaged) != 1 || manifest.Damaged[0].Key != r.seed.liveBlob.String() ||
		manifest.Damaged[0].State != state {
		t.Fatalf("damaged entries of backup %s = %+v, want the key %s as %q",
			manifest.ID, manifest.Damaged, r.seed.liveBlob, state)
	}

	for _, ref := range manifest.Blobs {
		if ref.Key == r.seed.liveBlob.String() {
			t.Fatalf("backup %s lists the damaged key %s among its blobs", manifest.ID, ref.Key)
		}
	}

	return manifest.Damaged[0]
}

// mustRepaired checks that restore id succeeded with the warning of damaged
// live blobs, that the live key holds the bytes of the backup, and that the
// audit log holds the finding of the backup before the restore.
func (r *workerRig) mustRepaired(t *testing.T, row backup.RestoreRecord, found string) {
	t.Helper()

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore %s = %q (%s), want succeeded", row.ID, row.State, row.Error)
	}

	if !bytes.Equal(r.liveBlobBytes(t), r.backupBlobBytes(t)) {
		t.Fatal("the live key does not hold the bytes of the backup after the restore")
	}

	codes := make([]backup.RestoreWarningCode, 0, len(row.Warnings()))
	for _, w := range row.Warnings() {
		codes = append(codes, w.Code)
	}

	if !slices.Contains(codes, backup.WarnDamagedLiveBlobs) {
		t.Fatalf("warnings of restore %s = %v, want %q", row.ID, codes, backup.WarnDamagedLiveBlobs)
	}

	entry := "backup " + row.PreBackupID.String() + " before restore " + row.ID.String() + " found live blob " +
		strconv.Quote(r.seed.liveBlob.String()) + " " + found
	if n := r.restoreEntries(t, entry); n != 1 {
		t.Fatalf("audit entries %q = %d, want 1", entry, n)
	}
}

func testWorkerMissingLiveBlob(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	id := r.start(t, a, false, false)
	row := r.driveToEnd(t, a, id)

	r.mustDamaged(t, r.preBackupOf(t, row), backup.DamageMissing)
	r.mustRepaired(t, row, "missing")
}

func testWorkerDamagedLiveBlob(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	damaged := []byte("tampered")
	putBlob(t, r.live.Store, r.seed.liveBlob, damaged)

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	id := r.start(t, a, false, false)
	row := r.driveToEnd(t, a, id)

	entry := r.mustDamaged(t, r.preBackupOf(t, row), backup.DamageMismatch)

	sum := blob.NewMD5()
	sum.Write(damaged)

	if entry.Size != int64(len(damaged)) || entry.MD5 != hex.EncodeToString(sum.Sum(nil)) {
		t.Fatalf("damaged entry = %+v, want the size and the MD5 of the damaged live bytes", entry)
	}

	r.mustRepaired(t, row, "damaged")

	replaced := "restore " + id.String() + " replaced damaged live blob " + strconv.Quote(r.seed.liveBlob.String())
	if n := r.restoreEntries(t, replaced); n != 1 {
		t.Fatalf("audit entries of the replacement = %d, want 1", n)
	}
}

func testWorkerDamagedThenChanged(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	putBlob(t, r.live.Store, r.seed.liveBlob, []byte("tampered"))

	gate := newStepGate(t, r, backup.RestoreStepCopyBlobs, gatePauseLive)
	a := r.service(t, workerService{
		owner: "instance-a", ops: gate, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	id := r.start(t, a, false, false)
	aDone := runAsync(t, a, gate)
	waitReached(t, gate, aDone)

	// The backup before the restore recorded the damaged bytes. Other bytes
	// reach the live key before the copy: they are not the recorded damage,
	// so the restore must not replace them.
	other := []byte("bytes that are not the recorded damage")
	putBlob(t, r.live.Store, r.seed.liveBlob, other)

	gate.open()
	<-aDone

	row := r.driveToEnd(t, a, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "corrupt") {
		t.Fatalf("restore over changed damaged bytes = %q %q, want failed as corrupt", row.State, row.Error)
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, other) {
		t.Fatalf("live key = %q, want the bytes that it held before the copy", got)
	}

	refusal := "restore " + id.String() + " refused to overwrite live blob " + strconv.Quote(r.seed.liveBlob.String())
	if n := r.restoreEntries(t, refusal); n != 1 {
		t.Fatalf("audit entries of the refusal = %d, want 1", n)
	}

	if n := r.restoreEntries(t, "restore "+id.String()+" replaced damaged live blob"); n != 0 {
		t.Fatalf("audit entries of a replacement = %d, want 0", n)
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

	// The backup before the restore found the live key whole: it holds no
	// evidence of damage. The key then holds other bytes, as live data that
	// is newer than the backup does. Force does not change the refusal.
	differing := []byte("bytes that differ from the backup")
	putBlob(t, r.live.Store, r.seed.liveBlob, differing)

	gate.open()
	<-aDone

	row := r.driveToEnd(t, a, id)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "corrupt") {
		t.Fatalf("restore over a live key that differs = %q %q, want failed as corrupt", row.State, row.Error)
	}

	if len(r.preBackupOf(t, row).Damaged) != 0 {
		t.Fatal("the backup before the restore recorded damage of a whole live key")
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, differing) {
		t.Fatalf("live key = %q, want the bytes that it held before the restore", got)
	}

	refusal := "restore " + id.String() + " refused to overwrite live blob " + strconv.Quote(r.seed.liveBlob.String())
	if n := r.restoreEntries(t, refusal); n != 1 {
		t.Fatalf("audit entries of the refusal = %d, want 1", n)
	}
}
