package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// StuckTestLimit is the outer limit of each wait in a store test for a state
// change. The wait ends when the state changes. The limit exists only to end a
// stuck test, and it is no deadline for the code under test. It is far above
// the stall of a loaded machine running with the race detector and below the
// test timeout of a store package.
const StuckTestLimit = 20 * time.Minute

// ItemKeyBackupRig is everything RunItemKeyBackup needs: a store that keeps
// item keys, the backup and restore services over it and the live blob store,
// as the server wires them.
type ItemKeyBackupRig struct {
	Store interface {
		namespace.Store
		secret.KeyStore
		secret.ItemKeyStore
	}
	Backups  *backup.Service
	Restores *backup.RestoreService
	// Live is the live blob store of the services.
	Live blob.Store
}

// ItemKeyBackupInput is what a rig takes from RunItemKeyBackup: the
// key-encryption key, which the restore proves against the backup, and the
// backup target that both rigs of the run share.
type ItemKeyBackupInput struct {
	Key    secret.Wrapper
	Target blob.Store
}

// ItemKeyBackupFactory returns a fresh rig, with a store and a live blob store
// of its own, for one side of the run.
type ItemKeyBackupFactory func(t *testing.T, in ItemKeyBackupInput) *ItemKeyBackupRig

// itemKeyNoRefusals drops the refusals of the item key blob view; the cases
// read the refusal as the error that the view returns.
type itemKeyNoRefusals struct{}

func (itemKeyNoRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

func (itemKeyNoRefusals) RecordMismatch(context.Context, namespace.ID, string) error {
	return nil
}

// RunItemKeyBackup proves that a backup copies the wrapped item key blobs and a
// restore brings them back. It backs up one store, then restores the backup
// into a second store with an empty live blob store, as after the loss of a
// server: the content of an item opens there with the key blob that the
// restore copied from the backup. It also proves that the backup leaves out
// the blob of a shredded key, and that the restore does not bring it back.
func RunItemKeyBackup(t *testing.T, newRig ItemKeyBackupFactory) {
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

	t.Cleanup(func() { _ = target.Close() }) //nolint:errcheck // Test cleanup.

	in := ItemKeyBackupInput{Key: wrapper, Target: target}
	source := newRig(t, in)
	seed := seedItemKeyBackup(t, source, wrapper)
	backupID := backup.ID(ident.NewID("bkp-"))

	if _, err = source.Backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	// The backup holds the key blob of the live key and none of the shredded
	// key.
	if _, err = target.Get(t.Context(), mustBlobKey(t, "blobs/"+seed.liveBlob.String())); err != nil {
		t.Fatalf("the backup holds no copy of the key blob: %v", err)
	}

	if rc, getErr := target.Get(t.Context(), mustBlobKey(t, "blobs/"+seed.shreddedBlob.String())); getErr == nil {
		_ = rc.Close() //nolint:errcheck // Test read.

		t.Error("the backup holds the key blob of a shredded key")
	}

	// A second server with an empty store and an empty blob store restores the
	// backup. Only the copy in the backup can bring the key blob back.
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
	checkRestoredItemKeys(t, dest, wrapper, seed)
}

// itemKeyBackupSeed is what seedItemKeyBackup wrote: the references, the sealed
// content of the live item, and the blob keys of both item keys.
type itemKeyBackupSeed struct {
	liveRef, shreddedRef         secret.ItemRef
	liveBlob, shreddedBlob       blob.Key
	binding, plain, sealedOfLive []byte
}

// seedItemKeyBackup writes a namespace, a data key, a live item key with sealed
// content, and a shredded item key whose blob is gone, as a redaction leaves
// them.
func seedItemKeyBackup(t *testing.T, source *ItemKeyBackupRig, wrapper secret.Wrapper) itemKeyBackupSeed {
	t.Helper()

	ctx := t.Context()
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now(), Name: "acme", PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := source.Store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	scope, err := secret.NewScope(ns.Name, "unit-backup")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	// The restore proves the key-encryption key with a data key that the same
	// wrapper wrapped, so the backup needs one next to the item keys.
	namespaces, err := namespace.NewService(namespace.Config{Store: source.Store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: source.Store, Wrapper: wrapper, Namespaces: namespaces, Now: now, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	if _, err = secrets.Encrypt(ctx, scope, [][]byte{[]byte("proof")}); err != nil {
		t.Fatalf("make a data key: %v", err)
	}

	sealer, blobs := itemKeySealerOf(t, source, wrapper)
	seed := itemKeyBackupSeed{
		liveRef: mustItemRef(t, scope, "pln-live"), shreddedRef: mustItemRef(t, scope, "pln-shredded"),
		binding: []byte("pln-live"), plain: []byte("content that must open after the restore"),
		liveBlob: "", shreddedBlob: "", sealedOfLive: nil,
	}

	liveKey := newItemKeyIn(t, source, sealer, ns, seed.liveRef)
	shreddedKey := newItemKeyIn(t, source, sealer, ns, seed.shreddedRef)
	seed.sealedOfLive = readAllSealed(t, sealer, seed.liveRef, seed.binding, seed.plain)

	// A redaction shreds the record and then deletes the blob.
	if _, err = source.Store.ShredItemKey(ctx, seed.shreddedRef, now(), ns.Fence()); err != nil {
		t.Fatalf("shred: %v", err)
	}

	seed.liveBlob = itemKeyBlobKey(t, blobs, ns.ID, liveKey)
	seed.shreddedBlob = itemKeyBlobKey(t, blobs, ns.ID, shreddedKey)

	if err = source.Live.Delete(ctx, seed.shreddedBlob); err != nil {
		t.Fatalf("delete the blob of the shredded key: %v", err)
	}

	return seed
}

// checkRestoredItemKeys proves that the content of the live item opens in the
// restored store, and that the shredded key stays shredded with no blob.
func checkRestoredItemKeys(t *testing.T, dest *ItemKeyBackupRig, wrapper secret.Wrapper, seed itemKeyBackupSeed) {
	t.Helper()

	sealer, _ := itemKeySealerOf(t, dest, wrapper)

	got, err := openAll(t, sealer, seed.liveRef, seed.binding, seed.sealedOfLive)
	if err != nil {
		t.Fatalf("open restored content: %v", err)
	}

	if !bytes.Equal(got, seed.plain) {
		t.Errorf("restored content = %q, want %q", got, seed.plain)
	}

	if rc, getErr := dest.Live.Get(t.Context(), seed.shreddedBlob); getErr == nil {
		_ = rc.Close() //nolint:errcheck // Test read.

		t.Error("the restore brought back the blob of a shredded key")
	}

	rd, openErr := sealer.OpenContent(t.Context(), seed.shreddedRef, seed.binding, strings.NewReader("x"))
	if rd != nil {
		// The open should fail. When it does not, close the reader, so that
		// the failed case does not also keep a key.
		if closeErr := rd.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}

	if !errors.Is(openErr, secret.ErrShredded) {
		t.Errorf("open with the shredded key err = %v, want ErrShredded", openErr)
	}
}

// itemKeySealerOf returns the sealer over the store and the live blob store
// of rig, and the key blob view that it uses.
func itemKeySealerOf(
	t *testing.T, rig *ItemKeyBackupRig, wrapper secret.Wrapper,
) (*secret.ItemKeySealer, *secret.ItemKeyBlobs) {
	t.Helper()

	blobs, err := secret.NewItemKeyBlobs(rig.Live, itemKeyNoRefusals{}, itemKeyNoRefusals{})
	if err != nil {
		t.Fatalf("item key blobs: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: rig.Store, Blobs: blobs, Wrapper: wrapper, Namespaces: rig.Store, Now: now,
	})
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}

	return sealer, blobs
}

// mustBlobKey parses a blob key.
func mustBlobKey(t *testing.T, s string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(s)
	if err != nil {
		t.Fatalf("blob key %q: %v", s, err)
	}

	return key
}

// mustItemRef returns the reference of a plan item of scope.
func mustItemRef(t *testing.T, scope secret.Scope, item string) secret.ItemRef {
	t.Helper()

	ref, err := secret.NewItemRef(scope, secret.ItemKindPlan, item)
	if err != nil {
		t.Fatalf("item ref: %v", err)
	}

	return ref
}

// newItemKeyIn makes the item key of ref in one fenced transaction.
func newItemKeyIn(
	t *testing.T, rig *ItemKeyBackupRig, sealer secret.ItemSealer, ns namespace.Namespace, ref secret.ItemRef,
) secret.ItemKey {
	t.Helper()

	var k secret.ItemKey

	err := rig.Store.InItemKeyTx(t.Context(), ns.Fence(), func(tx secret.ItemKeyTx) error {
		got, newErr := sealer.NewItemKey(t.Context(), tx, ref, ns.Fence())
		k = got

		return newErr
	})
	if err != nil {
		t.Fatalf("new item key %s: %v", ref, err)
	}

	return k
}

// itemKeyBlobKey returns the blob key of the wrapped key of k.
func itemKeyBlobKey(t *testing.T, blobs *secret.ItemKeyBlobs, ns namespace.ID, k secret.ItemKey) blob.Key {
	t.Helper()

	key, err := blobs.Key(ns, k.ID, k.Blob.Version, k.Blob.Attempt)
	if err != nil {
		t.Fatalf("blob key of %s: %v", k.ID, err)
	}

	return key
}

// readAllSealed seals plain for ref and returns the sealed bytes.
func readAllSealed(t *testing.T, s secret.ItemSealer, ref secret.ItemRef, binding, plain []byte) []byte {
	t.Helper()

	rd, err := s.SealContent(t.Context(), ref, binding, bytes.NewReader(plain))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	out, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("read sealed: %v", err)
	}

	return out
}

// openAll opens sealed for ref and reads it to the end.
func openAll(t *testing.T, s secret.ItemSealer, ref secret.ItemRef, binding, sealed []byte) ([]byte, error) {
	t.Helper()

	rd, err := s.OpenContent(t.Context(), ref, binding, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}

	out, err := io.ReadAll(rd)
	if err = errors.Join(err, rd.Close()); err != nil {
		return nil, fmt.Errorf("read opened content: %w", err)
	}

	return out, nil
}

// waitItemKeyRestore polls the restore until it succeeds, and fails on any
// other end state. The wait ends on the state change. StuckTestLimit
// only ends a stuck test.
func waitItemKeyRestore(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	deadline := time.Now().Add(StuckTestLimit)

	for {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore: %v", err)
		}

		if row.State == backup.RestoreSucceeded {
			return
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			t.Fatalf("restore state = %q, want succeeded (error %q)", row.State, row.Error)
		}

		// No worker runs in the case: it runs the restore operation itself.
		runRestoreWorker(t, restores)

		if time.Now().After(deadline) {
			t.Fatalf("restore %s never succeeded", id)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("restore %s: %v", id, t.Context().Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
