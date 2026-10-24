package backup_test

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
)

// errDeleteFailed is the error of the Delete of failingDelete.
var errDeleteFailed = errors.New("delete failed")

// failingDelete is a memStore whose Delete always fails.
type failingDelete struct {
	*memStore
}

func (failingDelete) Delete(context.Context, blob.Key) error {
	return errDeleteFailed
}

// md5Blob returns the manifest entry of the blob sumBlobKey with the bytes
// data: its size and its MD5.
func md5Blob(data []byte) backup.ManifestBlob {
	digest := crypto.MD5.New()
	digest.Write(data)

	return backup.ManifestBlob{
		Key:    sumBlobKey,
		Size:   int64(len(data)),
		MD5:    hex.EncodeToString(digest.Sum(nil)),
		Sealed: false,
	}
}

// assertPutBlobCopied copies input as the blob sumBlobKey of a restore whose
// manifest holds data. An exact match must be copied.
func assertPutBlobCopied(t *testing.T, data, input []byte) {
	t.Helper()

	dest := newMemStore()

	if err := backup.PutRestoreBlobForTest(t.Context(), dest, md5Blob(data), bytes.NewReader(input)); err != nil {
		t.Fatalf("copy = %v, want nil", err)
	}

	if !dest.has(sumBlobKey) {
		t.Fatal("exact match: the object is not in dest")
	}
}

// assertPutBlobRefused copies input as the blob sumBlobKey of a restore whose
// manifest holds data. The copy must give ErrCorrupt and leave no object in
// dest.
func assertPutBlobRefused(t *testing.T, data, input []byte) {
	t.Helper()

	dest := newMemStore()

	err := backup.PutRestoreBlobForTest(t.Context(), dest, md5Blob(data), bytes.NewReader(input))
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("copy = %v, want ErrCorrupt", err)
	}

	if dest.has(sumBlobKey) {
		t.Fatal("refused copy: the object is still in dest")
	}
}

// TestPutRestoreBlobRefusesOtherBytes proves that the copy of a restore
// refuses a blob with other bytes or another size with ErrCorrupt, and leaves
// no object in dest. An exact match is copied.
func TestPutRestoreBlobRefusesOtherBytes(t *testing.T) {
	t.Parallel()

	data := []byte("blob bytes of the restore")

	tests := []struct {
		name  string
		bytes []byte
		ok    bool
	}{
		{name: "exact match", bytes: data, ok: true},
		{name: "other bytes, same size", bytes: bytes.Repeat([]byte("x"), len(data)), ok: false},
		{name: "shorter", bytes: data[:len(data)-1], ok: false},
		{name: "longer", bytes: append(bytes.Clone(data), '!'), ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.ok {
				assertPutBlobCopied(t, data, tt.bytes)

				return
			}

			assertPutBlobRefused(t, data, tt.bytes)
		})
	}
}

// TestPutRestoreBlobKeepsCorruptAndDeleteErrors proves that when the delete
// of a refused copy fails, the error still matches ErrCorrupt and also holds
// the delete error.
func TestPutRestoreBlobKeepsCorruptAndDeleteErrors(t *testing.T) {
	t.Parallel()

	data := []byte("blob bytes of the restore")
	dest := failingDelete{memStore: newMemStore()}

	err := backup.PutRestoreBlobForTest(t.Context(), dest, md5Blob(data),
		bytes.NewReader([]byte("other bytes, same size!!")))
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("copy = %v, want ErrCorrupt", err)
	}

	if !errors.Is(err, errDeleteFailed) {
		t.Fatalf("copy = %v, want the delete error", err)
	}
}

// TestVerifyLiveBlobRefusesOtherBytes proves that the check of a live blob
// refuses other bytes or another size with ErrCorrupt and the "live blob:"
// prefix, and passes an exact match.
func TestVerifyLiveBlobRefusesOtherBytes(t *testing.T) {
	t.Parallel()

	data := []byte("live blob bytes")

	tests := []struct {
		name  string
		bytes []byte
		ok    bool
	}{
		{name: "exact match", bytes: data, ok: true},
		{name: "other bytes, same size", bytes: bytes.Repeat([]byte("y"), len(data)), ok: false},
		{name: "other size", bytes: data[:len(data)-1], ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			live := newMemStore()
			live.storeObject(t, sumBlobKey, tt.bytes)

			err := backup.VerifyLiveBlobForTest(t.Context(), live, md5Blob(data))
			if tt.ok {
				if err != nil {
					t.Fatalf("verify = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, backup.ErrCorrupt) || !strings.HasPrefix(err.Error(), "live blob: ") {
				t.Fatalf("verify = %v, want ErrCorrupt with the live blob prefix", err)
			}
		})
	}
}
