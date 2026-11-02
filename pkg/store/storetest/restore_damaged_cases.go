package storetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
)

// restoreDamagedCases are the cases of a restore over a live blob that is
// missing or damaged. Blobs are write-once: a restore never writes over a
// live blob, and it stops before any change of the live data at a damaged
// live blob or at more missing live blobs than its bound allows. The backup
// before a restore stays restorable.
func restoreDamagedCases() []restoreCase {
	return []restoreCase{
		{
			name: "a restore stops before any live change at a live blob that does not match its record",
			test: testWorkerDamagedLiveBlob,
		},
		{
			name: "a restore writes a live blob that the backup before the restore found missing within the bound",
			test: testWorkerMissingWithinBound,
		},
		{
			name: "a restore with force never replaces a live blob that matched its record and differs from the backup",
			test: testWorkerChangedLiveBlob,
		},
		{
			name: "a backup before a restore that found a missing live blob undoes the restore and leaves that key",
			test: testWorkerRestoreOfPreBackup,
		},
		{
			name: "a restore refuses a pre-backup ID that names a backup that exists",
			test: testWorkerExistingPreBackupID,
		},
		{
			name: "a restore stops before any live change at a backup before it that another restore started",
			test: testWorkerForeignPreBackup,
		},
		{
			name: "a restore refuses a damaged list in a backup that no restore started",
			test: testWorkerForgedDamagedList,
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

func testWorkerMissingWithinBound(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// The live data names one blob, and it is missing. One missing blob is
	// within the bound at every size: the restore adds the key.
	id := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, id, r.backupID)
	row := r.driveToEnd(t, a, id)

	r.mustDamaged(t, pre, backup.DamageMissing)
	r.mustWroteMissing(t, row)
}

// mustWroteMissing checks that restore row succeeded with the warning of
// missing live blobs, that the live key holds the bytes of the backup, and
// that its report and the audit log name what the backup before it found.
func (r *workerRig) mustWroteMissing(t *testing.T, row backup.RestoreRecord) {
	t.Helper()

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore %s = %q (%s), want succeeded", row.ID, row.State, row.Error)
	}

	if !bytes.Equal(r.liveBlobBytes(t), r.backupBlobBytes(t)) {
		t.Fatal("the live key does not hold the bytes of the backup after the restore")
	}

	if !slices.Contains(warningCodes(row), backup.WarnMissingLiveBlobs) {
		t.Fatalf("warnings of restore %s = %v, want %q", row.ID, warningCodes(row), backup.WarnMissingLiveBlobs)
	}

	if row.DevReport == nil || row.DevReport.PreBackupFound == nil || row.DevReport.PreBackupFound.Missing != 1 ||
		!slices.Contains(row.DevReport.PreBackupFound.Keys, r.seed.liveBlob.String()) {
		t.Fatalf("report of restore %s = %+v, want 1 missing", row.ID, row.DevReport)
	}

	if n := r.restoreEntries(t, "backup "+row.PreBackupID.String()+" before restore "+row.ID.String()); n != 1 {
		t.Fatalf("audit entries of the findings = %d, want 1", n)
	}
}

// warningCodes returns the codes of the warnings of row.
func warningCodes(row backup.RestoreRecord) []backup.RestoreWarningCode {
	codes := make([]backup.RestoreWarningCode, 0, len(row.Warnings()))
	for _, w := range row.Warnings() {
		codes = append(codes, w.Code)
	}

	return codes
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

	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// The first restore writes the missing blob. Its backup before the
	// restore records the key as missing and holds no copy of it.
	first := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, first, r.backupID)
	r.mustWroteMissing(t, r.driveToEnd(t, a, first))
	r.mustDamaged(t, pre, backup.DamageMissing)

	if err := backup.Check(t.Context(), r.target, pre); err != nil {
		t.Fatalf("backup check of the backup before the restore = %v, want nil", err)
	}

	if r.hasLater(t) {
		t.Fatal("the first restore did not replace the live data")
	}

	// The backup before the first restore must restore, to undo it: its data
	// names the key, and the restore leaves the key as it is.
	written := r.liveBlobBytes(t)

	second := backup.MintRestoreID()
	r.startWithPreBackupID(t, a, second, pre)
	row := r.driveToEnd(t, a, second)

	if row.State != backup.RestoreSucceeded {
		t.Fatalf("restore of the backup before a restore = %q (%s), want succeeded", row.State, row.Error)
	}

	if got := r.liveBlobBytes(t); !bytes.Equal(got, written) {
		t.Fatalf("live key = %q, want the bytes that it held", got)
	}

	// The first restore removed the namespace that came after the backup.
	// The restore of the backup before it brings the live data back.
	if !r.hasLater(t) {
		t.Fatal("the restore of the backup before the first restore did not bring the live data back")
	}

	if !slices.Contains(warningCodes(row), backup.WarnBlobsNotHeld) {
		t.Fatalf("warnings of restore %s = %v, want %q", row.ID, warningCodes(row), backup.WarnBlobsNotHeld)
	}

	if row.DevReport == nil || row.DevReport.NotHeld == nil || row.DevReport.NotHeld.Missing != 1 ||
		!slices.Contains(row.DevReport.NotHeld.Keys, r.seed.liveBlob.String()) {
		t.Fatalf("report of restore %s = %+v, want the key that the backup does not hold", row.ID, row.DevReport)
	}
}

// mustRefusedForeign checks that restore row failed with the code of a backup
// before it that it did not start, before any change of the live data, and
// with the audit event of the refusal.
func (r *workerRig) mustRefusedForeign(t *testing.T, row backup.RestoreRecord) {
	t.Helper()

	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, backup.ErrPreBackupForeign.Error()) {
		t.Fatalf("restore %s = %q %q, want failed with %q", row.ID, row.State, row.Error, backup.ErrPreBackupForeign)
	}

	if row.DevReport == nil || row.DevReport.ErrorCode != backup.CodePreBackupForeign {
		t.Fatalf("report of restore %s = %+v, want the code %q", row.ID, row.DevReport, backup.CodePreBackupForeign)
	}

	if !r.hasLater(t) {
		t.Fatal("the restore changed the live data")
	}

	if n := r.restoreEntries(t, "restore "+row.ID.String()+" refused backup "); n != 1 {
		t.Fatalf("audit entries of the refusal = %d, want 1", n)
	}
}

func testWorkerExistingPreBackupID(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	if err := r.live.Store.Delete(t.Context(), r.seed.liveBlob); err != nil {
		t.Fatalf("delete the live blob: %v", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// The pre-backup ID names the backup that the restore reads. A restore
	// must take a new backup of the live data, so the start refuses it.
	id := backup.MintRestoreID()
	req := backup.RestoreRequest{
		BackupID: r.backupID, PreBackupID: r.backupID, CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := a.Start(t.Context(), id, req); !errors.Is(err, backup.ErrPreBackupForeign) {
		t.Fatalf("start restore with an existing pre-backup ID = %v, want ErrPreBackupForeign", err)
	}

	if _, err := a.Get(t.Context(), id); !errors.Is(err, backup.ErrRestoreNotFound) {
		t.Fatalf("get the refused restore = %v, want ErrRestoreNotFound", err)
	}

	if !r.hasLater(t) {
		t.Fatal("the refused restore changed the live data")
	}
}

func testWorkerForeignPreBackup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	// Another restore starts a backup with the pre-backup ID of this restore
	// after its start. The pre-backup step finds that backup and refuses it.
	id := backup.MintRestoreID()
	pre := r.startWithPreBackupID(t, a, id, r.backupID)

	if err := r.backups.StartBeforeRestore(t.Context(), pre, backup.MintRestoreID()); err != nil {
		t.Fatalf("start the backup of another restore: %v", err)
	}

	r.mustRefusedForeign(t, r.driveToEnd(t, a, id))
}

func testWorkerForgedDamagedList(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)

	// A person with access to the target moves the blob of an ordinary backup
	// from its blobs to a damaged list.
	key := mustBlobKey(t, "backups/"+r.backupID.String()+"/manifest.json")

	var manifest backup.Manifest
	if err := json.Unmarshal(readBlob(t, r.target, key.String()), &manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}

	idx := slices.IndexFunc(
		manifest.Blobs,
		func(b backup.ManifestBlob) bool { return b.Key == r.seed.liveBlob.String() },
	)
	if idx < 0 {
		t.Fatalf("the backup does not hold %s", r.seed.liveBlob)
	}

	moved := manifest.Blobs[idx]
	manifest.Blobs = slices.Delete(manifest.Blobs, idx, idx+1)
	manifest.Damaged = []backup.DamagedBlob{{
		Key: moved.Key, State: backup.DamageMissing, Reason: backup.ReasonAbsent, MD5: moved.MD5, Size: moved.Size,
		Sealed: moved.Sealed,
	}}

	forged, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	putBlob(t, r.target, key, forged)

	if err := r.backups.Check(t.Context(), r.backupID); !errors.Is(err, backup.ErrDamagedListForeign) {
		t.Fatalf("backup check of the forged backup = %v, want ErrDamagedListForeign", err)
	}

	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, afterApply: nil, lockWait: 0,
	})

	row := r.driveToEnd(t, a, r.start(t, a, false, false))
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, backup.ErrDamagedListForeign.Error()) ||
		!strings.HasSuffix(row.Error, backup.ErrCorrupt.Error()) {
		t.Fatalf("restore of the forged backup = %q %q, want failed as corrupt", row.State, row.Error)
	}

	if !r.hasLater(t) {
		t.Fatal("the restore of the forged backup changed the live data")
	}
}
