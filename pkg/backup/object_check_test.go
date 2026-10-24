package backup_test

import (
	"crypto"
	"encoding/hex"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// sumOf returns the lowercase hex sum of data with the hash function h.
func sumOf(h crypto.Hash, data []byte) string {
	digest := h.New()
	digest.Write(data)

	return hex.EncodeToString(digest.Sum(nil))
}

// TestObjectCheckFailsClosed proves the one check of the size and the
// checksum of a backup object, for an MD5 of a blob and a SHA-256 of a table
// file: a manifest entry with no valid checksum, a size that differs, and a
// sum that differs are each refused with ErrCorrupt. Only an exact match
// passes.
func TestObjectCheckFailsClosed(t *testing.T) {
	t.Parallel()

	data := []byte("object bytes")

	for _, h := range []crypto.Hash{crypto.MD5, crypto.SHA256} {
		good := sumOf(h, data)
		other := sumOf(h, []byte("other bytes!"))

		tests := []struct {
			name string
			sum  string
			size int64
			data []byte
			ok   bool
		}{
			{name: "exact match", sum: good, size: int64(len(data)), data: data, ok: true},
			{name: "no checksum", sum: "", size: int64(len(data)), data: data, ok: false},
			{name: "checksum that is not hex", sum: "zz" + good[2:], size: int64(len(data)), data: data, ok: false},
			{
				name: "checksum that is too short", sum: good[:len(good)-2], size: int64(len(data)), data: data,
				ok: false,
			},
			{name: "checksum of another hash", sum: good + "00", size: int64(len(data)), data: data, ok: false},
			{name: "fewer bytes than the manifest names", sum: good, size: int64(len(data)) + 1, data: data, ok: false},
			{name: "same size, other bytes", sum: other, size: int64(len(data)), data: data, ok: false},
			{name: "no bytes", sum: good, size: int64(len(data)), data: nil, ok: false},
		}

		for _, tt := range tests {
			err := backup.CheckObjectBytes(h, tt.sum, tt.size, tt.data)
			if tt.ok && err != nil {
				t.Errorf("%v %s: err = %v, want nil", h, tt.name, err)
			}

			if !tt.ok && !errors.Is(err, backup.ErrCorrupt) {
				t.Errorf("%v %s: err = %v, want ErrCorrupt", h, tt.name, err)
			}
		}
	}
}
