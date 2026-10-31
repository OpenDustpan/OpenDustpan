package backup_test

import (
	"bytes"
	"crypto"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

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
