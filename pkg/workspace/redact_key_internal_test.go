package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// keyedStore is a blob store whose every call fails with an error that names
// the key, as the fs store does. It embeds a nil blob.Store, so only the three
// calls below are ever made.
type keyedStore struct {
	blob.Store
}

// Put implements blob.Store.
func (keyedStore) Put(_ context.Context, key blob.Key, _ io.Reader) error {
	return keyedFailure("put", key)
}

// Get implements blob.Store.
func (keyedStore) Get(_ context.Context, key blob.Key) (io.ReadCloser, error) {
	return nil, keyedFailure("get", key)
}

// Delete implements blob.Store.
func (keyedStore) Delete(_ context.Context, key blob.Key) error {
	return keyedFailure("delete", key)
}

// keyedFailure is the error of one call. It wraps blob.ErrInvalidKey, which
// blob.DeleteRetried does not retry.
func keyedFailure(op string, key blob.Key) error {
	return fmt.Errorf("%s %s: %w", op, key, blob.ErrInvalidKey)
}

// TestBlobStorePathsRedactTheKey proves that each store path of stateBlobs
// (put, get, delete and the retried delete of the sweep) returns an error whose
// text does not hold the blob key, and that blob.ErrInvalidKey still matches.
// Each case fails when its own redactKey call is removed.
func TestBlobStorePathsRedactTheKey(t *testing.T) {
	t.Parallel()

	const (
		ns = namespace.ID("ns-acmeacmeacmeacmeacme")
		ws = ID("ws-1")
		sv = StateVersionID("sv-1")
	)

	blobs := &stateBlobs{store: keyedStore{Store: nil}, refusals: nil}

	key, err := blobs.attemptKey(ns, ws, sv)
	if err != nil {
		t.Fatalf("attempt key: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"put", func() error {
			return blobs.put(t.Context(), ns, ws, key, strings.NewReader("body"))
		}},
		{"get", func() error {
			_, err := blobs.get(t.Context(), ns, ws, key)

			return err
		}},
		{"delete", func() error {
			return blobs.delete(t.Context(), ns, ws, key)
		}},
		{"deleteRetried", func() error {
			return blobs.deleteRetried(t.Context(), ns, ws, key)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call()
			if err == nil {
				t.Fatal("err = nil, want the store failure")
			}

			if strings.Contains(err.Error(), key.String()) {
				t.Errorf("error %q names the blob key", err)
			}

			if !errors.Is(err, blob.ErrInvalidKey) {
				t.Errorf("error %q does not match blob.ErrInvalidKey", err)
			}
		})
	}
}

// errStoreDown is a store failure that names no key.
var errStoreDown = errors.New("store down")

// TestRedactKeyWithTheFilesystemStore proves that the text of a missing-blob
// error of the real fs store does not carry the blob key once redactKey runs,
// and that blob.ErrNotFound still matches. The test first checks that the fs
// store does name the key in its text, so the redaction is tested on a real
// case.
func TestRedactKeyWithTheFilesystemStore(t *testing.T) {
	t.Parallel()

	store, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open the fs store: %v", err)
	}

	key := blob.Key("namespaces/ns-acmeacmeacmeacmeacme/tofu/ws-1/sv-1/a1")

	_, getErr := store.Get(t.Context(), key)
	if !errors.Is(getErr, blob.ErrNotFound) {
		t.Fatalf("get of a missing blob: err = %v, want blob.ErrNotFound", getErr)
	}

	if !strings.Contains(getErr.Error(), key.String()) {
		t.Fatalf("the fs store error %q does not name the key, so the test proves nothing", getErr)
	}

	got := redactKey(getErr, key)

	if strings.Contains(got.Error(), key.String()) {
		t.Errorf("the redacted error %q still names the key", got)
	}

	if !errors.Is(got, blob.ErrNotFound) {
		t.Errorf("the redacted error %q does not match blob.ErrNotFound", got)
	}
}

// TestRedactKeyKeepsErrorsThatNameNoKey proves that redactKey returns an error
// unchanged when it has no key, when the key is empty, and when the error is nil.
func TestRedactKeyKeepsErrorsThatNameNoKey(t *testing.T) {
	t.Parallel()

	plain := errStoreDown

	tests := []struct {
		name string
		err  error
		key  blob.Key
	}{
		{"nil error", nil, "namespaces/ns-x/tofu/ws-1/sv-1/a1"},
		{"empty key", plain, ""},
		{"error without the key", plain, "namespaces/ns-x/tofu/ws-1/sv-1/a1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := redactKey(tt.err, tt.key); !errors.Is(got, tt.err) {
				t.Fatalf("redactKey() = %v, want the error unchanged", got)
			}
		})
	}
}
