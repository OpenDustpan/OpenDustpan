package storetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
	"opendustpan.org/opendustpan/pkg/secret"
)

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
	ctx := t.Context()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: now(), Name: "acme", HomeRegion: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = source.Store.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	scope, err := secret.NewScope(ns.Name, "unit-backup")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}

	// The restore proves the key-encryption key with a data key that the same
	// wrapper wrapped, so the backup needs one next to the item keys.
	namespaces, err := namespace.NewService(namespace.Config{Store: source.Store, Now: nil, Region: "local"})
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

	sourceSealer, sourceBlobs := itemKeySealerOf(t, source, wrapper)
	liveRef := mustItemRef(t, scope, "pln-live")
	shreddedRef := mustItemRef(t, scope, "pln-shredded")
	binding := []byte("pln-live")
	plain := []byte("content that must open after the restore")

	liveKey := newItemKeyIn(t, source, sourceSealer, ns, liveRef)
	shreddedKey := newItemKeyIn(t, source, sourceSealer, ns, shreddedRef)
	sealed := readAllSealed(t, sourceSealer, liveRef, binding, plain)

	// A redaction shreds the record and then deletes the blob.
	if _, err = source.Store.ShredItemKey(ctx, shreddedRef, now(), ns.Fence()); err != nil {
		t.Fatalf("shred: %v", err)
	}

	liveBlob := itemKeyBlobKey(t, sourceBlobs, ns.ID, liveKey)
	shreddedBlob := itemKeyBlobKey(t, sourceBlobs, ns.ID, shreddedKey)

	if err = source.Live.Delete(ctx, shreddedBlob); err != nil {
		t.Fatalf("delete the blob of the shredded key: %v", err)
	}

	backupID := backup.ID(ident.NewID("bkp-"))
	if _, err = source.Backups.Run(ctx, backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	// The backup holds the key blob of the live key and none of the shredded
	// key.
	if _, err = target.Get(ctx, mustBlobKey(t, "blobs/"+liveBlob.String())); err != nil {
		t.Fatalf("the backup holds no copy of the key blob: %v", err)
	}

	if rc, getErr := target.Get(ctx, mustBlobKey(t, "blobs/"+shreddedBlob.String())); getErr == nil {
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

	if err = dest.Restores.Start(ctx, restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	waitItemKeyRestore(t, dest.Restores, restoreID)

	destSealer, _ := itemKeySealerOf(t, dest, wrapper)

	got, err := openAll(t, destSealer, liveRef, binding, sealed)
	if err != nil {
		t.Fatalf("open restored content: %v", err)
	}

	if !bytes.Equal(got, plain) {
		t.Errorf("restored content = %q, want %q", got, plain)
	}

	if rc, getErr := dest.Live.Get(ctx, shreddedBlob); getErr == nil {
		_ = rc.Close() //nolint:errcheck // Test read.

		t.Error("the restore brought back the blob of a shredded key")
	}

	if _, openErr := destSealer.OpenContent(ctx, shreddedRef, binding, strings.NewReader("x")); !errors.Is(
		openErr, secret.ErrShredded,
	) {
		t.Errorf("open with the shredded key err = %v, want ErrShredded", openErr)
	}
}

// itemKeySealerOf returns the sealer over the store and the live blob store
// of rig, and the key blob view that it uses.
func itemKeySealerOf(
	t *testing.T, rig *ItemKeyBackupRig, wrapper secret.Wrapper,
) (*secret.ItemKeySealer, *secret.ItemKeyBlobs) {
	t.Helper()

	blobs, err := secret.NewItemKeyBlobs(rig.Live, itemKeyNoRefusals{})
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

	key, err := blobs.Key(ns, k.ID, k.Blob.Version)
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
		return nil, err //nolint:wrapcheck // The case reads the error as it is.
	}

	out, err := io.ReadAll(rd)
	if err != nil {
		return nil, err //nolint:wrapcheck // The case reads the error as it is.
	}

	return out, nil
}

// waitItemKeyRestore polls the restore until it succeeds, and fails on any
// other end state or after a minute.
func waitItemKeyRestore(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	deadline := time.Now().Add(time.Minute)

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
