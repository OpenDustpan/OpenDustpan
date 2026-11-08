package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/secret"
)

// StoredBlobBackupRig is everything the stored blob backup cases need: the
// recorder harness over a store (its blob store is the live blob store of the
// services), the registry of the same store, and the backup and restore
// services over it, as the server wires them.
type StoredBlobBackupRig struct {
	Recorder jobtest.RecorderHarness
	Registry registry.Store
	// Keys is the store of the namespaces and the data keys. The restore
	// proves the key-encryption key with a data key, so a case makes one.
	Keys interface {
		namespace.Store
		secret.KeyStore
	}
	Backups  *backup.Service
	Restores *backup.RestoreService
	// Eraser removes the stored size and MD5 of every log chunk and every
	// registry release, as the rows of an earlier version of the server hold
	// them.
	Eraser StoredDigestEraser
}

// StoredDigestEraser clears the check values of the rows that name a blob.
type StoredDigestEraser interface {
	// EraseStoredDigests clears the stored size and the stored MD5 of every
	// log chunk, and the stored files of every registry release.
	EraseStoredDigests(ctx context.Context) error
}

// StoredBlobBackupInput is what a rig takes from the cases: the
// key-encryption key, which the item keys and the restore share, and the
// backup target that the rigs of one case share.
type StoredBlobBackupInput struct {
	Key    secret.Wrapper
	Target blob.Store
}

// StoredBlobBackupFactory returns a fresh rig, with a store and a live blob
// store of its own.
type StoredBlobBackupFactory func(t *testing.T, in StoredBlobBackupInput) *StoredBlobBackupRig

// registryFileBody is the content of the file of the seeded registry release.
const registryFileBody = "the archive of a module"

// storedBlobSeed names what seedStoredBlobs stored.
type storedBlobSeed struct {
	namespace namespace.Namespace
	// jobKeys are the keys of the plan parts and the log chunks, and releaseKey
	// is the key of the file of the release.
	jobKeys    []string
	releaseKey string
}

// RunStoredBlobBackup proves that a backup copies the blobs of job plans, of
// job log chunks and of registry releases, and that a restore into a server
// with an empty blob store brings every one of them back, byte for byte.
func RunStoredBlobBackup(t *testing.T, newRig StoredBlobBackupFactory) {
	t.Helper()

	in := newStoredBlobInput(t)
	source := newRig(t, in)
	seed := seedStoredBlobs(t, source, in.Key)
	backupID := backup.ID(ident.NewID("bkp-"))

	if _, err := source.Backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	manifest, err := source.Backups.ReadManifest(t.Context(), backupID)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if len(manifest.Unverified) != 0 {
		t.Errorf("manifest lists %v as not copied, want none", manifest.Unverified)
	}

	// Each blob that the rows name is in the backup target, with the bytes of
	// the live blob store.
	want := append(slices.Clone(seed.jobKeys), seed.releaseKey)
	for _, key := range want {
		if !slices.ContainsFunc(manifest.Blobs, func(b backup.ManifestBlob) bool { return b.Key == key }) {
			t.Errorf("manifest has no entry for blob %q", key)
		}

		if !sameBlob(t, in.Target, "blobs/"+key, source.Recorder.Blobs, key) {
			t.Errorf("backup copy of blob %q differs from the live blob", key)
		}
	}

	// A second server with an empty store and an empty blob store restores the
	// backup. Only the copy in the backup can bring the blobs back.
	dest := newRig(t, in)
	restoreID := backup.RestoreID(ident.NewID("rst-"))

	req := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}
	if err = dest.Restores.Start(t.Context(), restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	waitItemKeyRestore(t, dest.Restores, restoreID)

	for _, key := range want {
		if !sameBlob(t, dest.Recorder.Blobs, key, source.Recorder.Blobs, key) {
			t.Errorf("restored blob %q differs from the blob of the source", key)
		}
	}
}

// RunStoredBlobBackupUnverified proves the rule for a row that names a blob
// without the size and the MD5 of its stored bytes, as a row of an earlier
// version of the server does: the backup copies no such blob, lists its key in
// the manifest, and still copies every blob that it can check; a restore of
// that backup works; a restore of a manifest with more keys than the bound on
// the unverified keys works and records the count and the bound; and a restore
// of a backup whose manifest does not list such a blob refuses, with
// ErrBlobsNotBacked.
func RunStoredBlobBackupUnverified(t *testing.T, newRig StoredBlobBackupFactory) {
	t.Helper()

	in := newStoredBlobInput(t)
	source := newRig(t, in)
	seed := seedStoredBlobs(t, source, in.Key)

	if err := source.Eraser.EraseStoredDigests(t.Context()); err != nil {
		t.Fatalf("erase the stored digests: %v", err)
	}

	backupID := backup.ID(ident.NewID("bkp-"))
	if _, err := source.Backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	manifest, err := source.Backups.ReadManifest(t.Context(), backupID)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	checkNotCopied(t, in, manifest, seed)

	// A restore of that backup works: the manifest lists what it does not hold.
	listed := newRig(t, in)
	listedID := backup.RestoreID(ident.NewID("rst-"))

	if err = listed.Restores.Start(t.Context(), listedID, restoreRequest(backupID)); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	waitItemKeyRestore(t, listed.Restores, listedID)

	// The bound on unverified keys stops a new backup. A manifest that an
	// earlier version of the server wrote stays restorable, also when it
	// names more keys than the bound allows.
	checkRestoreOverTheBound(t, newRig, in, backupID)

	// A manifest that does not list the blobs is the manifest of a backup that
	// lost them. The restore refuses it before it changes anything.
	dropUnverified(t, in.Target, backupID)

	refused := newRig(t, in)
	refusedID := backup.RestoreID(ident.NewID("rst-"))

	if err = refused.Restores.Start(t.Context(), refusedID, restoreRequest(backupID)); err != nil {
		t.Fatalf("start restore of the backup with an unlisted blob: %v", err)
	}

	row := waitRestoreEnd(t, refused.Restores, refusedID)
	if row.State != backup.RestoreFailed || !strings.Contains(row.Error, "backup is incomplete") ||
		!strings.Contains(row.Error, "does not hold") {
		t.Errorf(
			"restore of a backup that lost blobs = state %q, error %q; want failed, backup is incomplete",
			row.State,
			row.Error,
		)
	}
}

// checkNotCopied checks the backup of rows without check values: the manifest
// lists the log chunks and the registry file, the target holds none of them,
// and the target holds the plan parts, whose values are in the plan document.
// It returns the keys that the manifest lists.
func checkNotCopied(t *testing.T, in StoredBlobBackupInput, manifest backup.Manifest, seed storedBlobSeed) []string {
	t.Helper()

	notCopied := slices.DeleteFunc(append(slices.Clone(seed.jobKeys), seed.releaseKey), func(key string) bool {
		return strings.Contains(key, "/plan/")
	})
	slices.Sort(notCopied)

	if !slices.Equal(manifest.Unverified, notCopied) {
		t.Errorf("manifest lists %v as not copied, want %v", manifest.Unverified, notCopied)
	}

	for _, key := range notCopied {
		if rc, getErr := in.Target.Get(t.Context(), mustBlobKey(t, "blobs/"+key)); getErr == nil {
			if closeErr := rc.Close(); closeErr != nil {
				t.Errorf("close blob %q: %v", key, closeErr)
			}

			t.Errorf("the backup copied blob %q, which has no check value", key)
		}
	}

	for _, key := range seed.jobKeys {
		if strings.Contains(key, "/plan/") {
			_ = readBlob(t, in.Target, "blobs/"+key)
		}
	}

	return notCopied
}

// newStoredBlobInput returns a random key-encryption key and a backup target.
func newStoredBlobInput(t *testing.T) StoredBlobBackupInput {
	t.Helper()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("key-encryption key: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper(kek)
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	target, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	return StoredBlobBackupInput{Key: wrapper, Target: target}
}

// restoreRequest returns the request of a plain restore of the backup id.
func restoreRequest(id backup.ID) backup.RestoreRequest {
	return backup.RestoreRequest{
		BackupID: id, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester", KeepAccessTables: false,
	}
}

// seedStoredBlobs stores one plan and two log chunks through the recorder and
// one registry release, with the blobs of each. It returns the key of every
// blob that the rows name.
func seedStoredBlobs(t *testing.T, rig *StoredBlobBackupRig, wrapper secret.Wrapper) storedBlobSeed {
	t.Helper()

	ctx := t.Context()
	seed := jobtest.SeedArtifacts(t, rig.Recorder)

	ns, err := rig.Recorder.Namespaces.GetNamespace(ctx, seed.Namespace)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	objects, err := blobtest.ListAll(ctx, rig.Recorder.Blobs, "namespaces/"+ns.ID.String()+"/job/")
	if err != nil {
		t.Fatalf("list the job blobs: %v", err)
	}

	out := storedBlobSeed{namespace: ns, jobKeys: nil, releaseKey: ""}

	for _, o := range objects {
		out.jobKeys = append(out.jobKeys, o.Key.String())
	}

	// The key blobs of the item keys are not job blobs: they live under
	// item-keys/. Two plan parts and two log chunks are what the case checks.
	if len(out.jobKeys) != 4 {
		t.Fatalf("job blobs = %v, want 2 plan parts and 2 log chunks", out.jobKeys)
	}

	out.releaseKey = publishModule(t, rig, ns)

	makeDataKey(t, rig, ns, wrapper)

	return out
}

// makeDataKey stores a data key that the wrapper wrapped. The restore proves
// the key-encryption key against the data keys of the backup.
func makeDataKey(t *testing.T, rig *StoredBlobBackupRig, ns namespace.Namespace, wrapper secret.Wrapper) {
	t.Helper()

	scope, err := secret.NewScope(ns.Name, "unit-backup")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: rig.Keys, Now: nil, Installation: ns.PrimaryInstallation},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: rig.Keys, Wrapper: wrapper, Namespaces: namespaces, Now: now, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	if _, err = secrets.Encrypt(t.Context(), scope, [][]byte{[]byte("proof")}); err != nil {
		t.Fatalf("make a data key: %v", err)
	}
}

// publishModule publishes a module release with one archive and stores the
// archive under its content key. It returns that key.
func publishModule(t *testing.T, rig *StoredBlobBackupRig, ns namespace.Namespace) string {
	t.Helper()

	ctx := t.Context()

	digest, err := blob.DigestOf(strings.NewReader(registryFileBody))
	if err != nil {
		t.Fatalf("digest of the archive: %v", err)
	}

	sum := sha256.Sum256([]byte(registryFileBody))
	plain := hex.EncodeToString(sum[:])

	key, err := registry.BlobKey(ns.ID, plain)
	if err != nil {
		t.Fatalf("registry blob key: %v", err)
	}

	if err = rig.Recorder.Blobs.Put(ctx, key, strings.NewReader(registryFileBody)); err != nil {
		t.Fatalf("put the archive: %v", err)
	}

	rel := registry.Release{
		PublishedAt: now(),
		Address:     registry.Address{Namespace: ns.Name, Kind: registry.KindModule, Name: "net", System: "aws"},
		Version:     "1.0.0",
		PublishedBy: "al",

		SumsFile: "", SumsSHA256: "", SignatureFile: "", SignatureSHA256: "", KeyFingerprint: "", Protocols: nil,
		Archives: []registry.Archive{{Platform: "", FileName: "net.zip", SHA256: plain, Size: digest.Size}},
		Files:    []registry.StoredFile{{SHA256: plain, Size: digest.Size, MD5: hex.EncodeToString(digest.MD5[:])}},
	}

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     ns.Name,
		Subject:       "al",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "registry:publish",
		Object:        ns.Name.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	if err = rig.Registry.Publish(ctx, rel, ns.Fence(), entry); err != nil {
		t.Fatalf("publish the release: %v", err)
	}

	return key.String()
}

// sameBlob reports whether the blob under leftKey in left holds the bytes of the
// blob under rightKey in right.
func sameBlob(t *testing.T, left blob.Store, leftKey string, right blob.Store, rightKey string) bool {
	t.Helper()

	return bytes.Equal(readBlob(t, left, leftKey), readBlob(t, right, rightKey))
}

// readBlob returns the bytes under key in store, and fails t when the blob is
// not there.
func readBlob(t *testing.T, store blob.Store, key string) []byte {
	t.Helper()

	rc, err := store.Get(t.Context(), mustBlobKey(t, key))
	if err != nil {
		t.Errorf("get blob %q: %v", key, err)

		return nil
	}

	data, readErr := io.ReadAll(rc)
	if closeErr := rc.Close(); readErr != nil || closeErr != nil {
		t.Errorf("read blob %q: %v", key, errors.Join(readErr, closeErr))
	}

	return data
}

// checkRestoreOverTheBound fills the manifest of the backup id in the target
// of in with one key above the bound on the unverified keys, restores it, and
// proves that the restore succeeds with the count and the bound in the audit
// log. The bound stops a new backup; a manifest that an earlier version of the
// server wrote stays restorable.
func checkRestoreOverTheBound(
	t *testing.T, newRig StoredBlobBackupFactory, in StoredBlobBackupInput, backupID backup.ID,
) {
	t.Helper()

	fillUnverified(t, in.Target, backupID, backup.MaxUnverifiedKeys+1)

	over := newRig(t, in)
	overID := backup.RestoreID(ident.NewID("rst-"))

	if err := over.Restores.Start(t.Context(), overID, restoreRequest(backupID)); err != nil {
		t.Fatalf("start restore of the backup over the bound: %v", err)
	}

	waitItemKeyRestore(t, over.Restores, overID)

	entries, err := over.Recorder.Domain.AuditEntries(t.Context(), audit.ServerScope)
	if err != nil {
		t.Fatalf("read the audit log: %v", err)
	}

	var noted []string

	for i := range entries {
		if strings.Contains(entries[i].Object, "more than the limit of") {
			noted = append(noted, entries[i].Object)
		}
	}

	if len(noted) != 1 {
		t.Fatalf("audit entries that name the unverified keys = %v, want one", noted)
	}

	for _, want := range []string{
		fmt.Sprintf("%d blobs without a size and an MD5", backup.MaxUnverifiedKeys+1),
		fmt.Sprintf("more than the limit of %d", backup.MaxUnverifiedKeys),
		overID.String(),
		backupID.String(),
	} {
		if !strings.Contains(noted[0], want) {
			t.Fatalf("audit entry = %q, want %q", noted[0], want)
		}
	}
}

// dropUnverified rewrites the manifest of the backup id in target without its
// list of blobs that the backup does not hold.
func dropUnverified(t *testing.T, target blob.Store, id backup.ID) {
	t.Helper()

	key := mustBlobKey(t, "backups/"+id.String()+"/manifest.json")

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(readBlob(t, target, key.String()), &doc); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}

	if _, ok := doc["unverified"]; !ok {
		t.Fatal("the manifest has no list of blobs that the backup does not hold")
	}

	delete(doc, "unverified")

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	if err = target.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete the manifest: %v", err)
	}

	if err = target.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

// fillUnverified rewrites the manifest of the backup id in target so that its
// list of blobs that the backup does not hold names count keys. The keys that
// the manifest already names stay, and the extra keys are keys that no row of
// the data names, so a restore of the manifest still works. The list stays
// sorted, as the check of the restore reads it.
func fillUnverified(t *testing.T, target blob.Store, id backup.ID, count int) {
	t.Helper()

	key := mustBlobKey(t, "backups/"+id.String()+"/manifest.json")

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(readBlob(t, target, key.String()), &doc); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}

	var listed []string

	if raw, ok := doc["unverified"]; ok {
		if err := json.Unmarshal(raw, &listed); err != nil {
			t.Fatalf("decode the list of blobs that the backup does not hold: %v", err)
		}
	}

	if len(listed) > count {
		t.Fatalf("the manifest lists %d blobs that the backup does not hold, want at most %d", len(listed), count)
	}

	for i := len(listed); i < count; i++ {
		listed = append(listed, fmt.Sprintf("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/extra/blob-%08d", i))
	}

	slices.Sort(listed)

	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatalf("encode the list of blobs that the backup does not hold: %v", err)
	}

	doc["unverified"] = raw

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}

	if err = target.Delete(t.Context(), key); err != nil {
		t.Fatalf("delete the manifest: %v", err)
	}

	if err = target.Put(t.Context(), key, bytes.NewReader(data)); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

// waitRestoreEnd waits until the restore leaves checking and applying, and
// returns its row. The wait ends on the state change. StuckTestLimit only ends
// a stuck test.
func waitRestoreEnd(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) backup.RestoreRecord {
	t.Helper()

	deadline := time.Now().Add(StuckTestLimit)

	for {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore: %v", err)
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			return row
		}

		// No worker runs in the case: it runs the restore operation itself.
		runRestoreWorker(t, restores)

		if time.Now().After(deadline) {
			t.Fatalf("restore %s did not end", id)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("restore %s: %v", id, t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
