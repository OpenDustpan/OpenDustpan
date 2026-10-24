package backup

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/pkg/blob"
)

// objectWant is what the manifest records for one object: its size, and the
// hash function and the lowercase hex sum of its checksum.
type objectWant struct {
	size int64
	hash crypto.Hash
	sum  string
}

// tableFileWant returns what the manifest records for a table file: its size
// and its SHA-256.
func tableFileWant(file PortableFile) objectWant {
	return objectWant{size: file.Size, hash: crypto.SHA256, sum: file.SHA256}
}

// blobWant returns what the manifest records for a blob: its size and its
// MD5.
func blobWant(ref ManifestBlob) objectWant {
	return objectWant{size: ref.Size, hash: crypto.MD5, sum: ref.MD5}
}

// checkObject reads the object key from target and returns an error that
// wraps ErrCorrupt when it is missing, when its size is not want.size, or when
// its checksum is not want.sum. A sum that is not hex of the length of the
// hash also gives ErrCorrupt, so an object with no checksum in the manifest
// never passes.
func checkObject(ctx context.Context, target blob.Store, key blob.Key, want objectWant) error {
	wantSum, err := hex.DecodeString(want.sum)
	if err != nil || len(wantSum) != want.hash.Size() {
		return fmt.Errorf("backup is corrupt: the manifest names no valid checksum for object %q: %w", key,
			ErrCorrupt)
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("backup is incomplete: object %q is missing: %w", key, ErrCorrupt)
		}

		return fmt.Errorf("read backup object %q: %w", key, err)
	}

	digest := want.hash.New()

	size, readErr := io.Copy(digest, reader)
	closeErr := reader.Close()

	if readErr != nil {
		return fmt.Errorf("read backup object %q: %w", key, readErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", key, closeErr)
	}

	if size != want.size {
		return fmt.Errorf(
			"backup is incomplete: object %q holds %d bytes, the manifest names %d: %w",
			key,
			size,
			want.size,
			ErrCorrupt,
		)
	}

	if !bytes.Equal(digest.Sum(nil), wantSum) {
		return fmt.Errorf("backup is corrupt: object %q does not match the checksum that the manifest names: %w",
			key, ErrCorrupt)
	}

	return nil
}
