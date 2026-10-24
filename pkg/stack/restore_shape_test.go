package stack_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestRestoreRefusesAStoredVersionThatCannotBeRead proves that a restore of a
// stored version whose checkpoint the CLI cannot read fails with
// ErrInvalidUpdate, records no update, and keeps the current state. An older
// server could have stored such a checkpoint before it checked the shape.
func TestRestoreRefusesAStoredVersionThatCannotBeRead(t *testing.T) {
	t.Parallel()

	f := newUpdates(t)

	finishVersion(t, f, `{"v":1}`)
	finishVersion(t, f, `{"v":2}`)

	plantCheckpoint(t, f, 1, `"notanobject"`)

	_, err := f.svc.Restore(t.Context(), f.ref, 1, "bob@example.com")
	if !errors.Is(err, stack.ErrInvalidUpdate) {
		t.Fatalf("restore of an unreadable version: err = %v, want ErrInvalidUpdate", err)
	}

	f.store.mu.Lock()
	n := len(f.store.updates)
	f.store.mu.Unlock()

	if n != 2 {
		t.Errorf("%d updates after a refused restore, want 2", n)
	}

	checkRestoreExport(t, f, `{"v":2}`)
	checkLockFree(t, f)
}

// plantCheckpoint replaces the stored checkpoint of version with body: the
// blob holds body gzipped, and the row holds the digest of body. The read of
// the blob passes, so only the shape check can refuse body.
func plantCheckpoint(t *testing.T, f updatesFixture, version int64, body string) {
	t.Helper()

	mb, ok := f.blobs.(*memBlob)
	if !ok {
		t.Fatalf("blob store is %T, want *memBlob", f.blobs)
	}

	var zipped bytes.Buffer

	w := gzip.NewWriter(&zipped)
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("gzip: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	f.store.mu.Lock()
	defer f.store.mu.Unlock()

	for id := range f.store.checkpoints {
		cp := f.store.checkpoints[id]
		if f.store.updates[cp.Update].Version != version {
			continue
		}

		mb.mu.Lock()
		obj, found := mb.objects[cp.BlobKey]
		obj.data = zipped.Bytes()

		if found {
			mb.objects[cp.BlobKey] = obj
		}
		mb.mu.Unlock()

		if !found {
			t.Fatalf("no blob at %q for version %d", cp.BlobKey, version)
		}

		cp.Digest = sha256.Sum256([]byte(body))
		f.store.checkpoints[id] = cp

		return
	}

	t.Fatalf("no checkpoint of version %d", version)
}
