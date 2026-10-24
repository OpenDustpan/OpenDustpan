package backup

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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

// objectCheck counts and hashes the bytes of one object while they stream,
// and compares them with what the manifest records. It is the one check of
// the size and the checksum of an object: backup check and both restore paths
// use it.
type objectCheck struct {
	digest   hash.Hash
	sum      []byte
	size     int64
	wantSize int64
}

// newObjectCheck returns the check of want for the object key. A sum that is
// not hex of the length of the hash returns an error that wraps ErrCorrupt,
// so an object with no checksum in the manifest never passes.
func newObjectCheck(key string, want objectWant) (*objectCheck, error) {
	sum, err := hex.DecodeString(want.sum)
	if err != nil || len(sum) != want.hash.Size() {
		return nil, fmt.Errorf("backup is corrupt: the manifest names no valid checksum for object %q: %w", key,
			ErrCorrupt)
	}

	return &objectCheck{digest: want.hash.New(), sum: sum, size: 0, wantSize: want.size}, nil
}

// Write hashes and counts p.
func (c *objectCheck) Write(p []byte) (int, error) {
	n, err := c.digest.Write(p)
	c.size += int64(n)

	if err != nil {
		return n, fmt.Errorf("hash backup object: %w", err)
	}

	return n, nil
}

// verify returns an error that wraps ErrCorrupt when the bytes that Write got
// are not the size or the checksum that the manifest records for the object
// key.
func (c *objectCheck) verify(key string) error {
	if c.size != c.wantSize {
		return fmt.Errorf(
			"backup is incomplete: object %q holds %d bytes, the manifest names %d: %w",
			key,
			c.size,
			c.wantSize,
			ErrCorrupt,
		)
	}

	if !bytes.Equal(c.digest.Sum(nil), c.sum) {
		return fmt.Errorf("backup is corrupt: object %q does not match the checksum that the manifest names: %w",
			key, ErrCorrupt)
	}

	return nil
}

// checkObject reads the object key from target and returns an error that
// wraps ErrCorrupt when it is missing, when its size is not want.size, or when
// its checksum is not want.sum. A sum that is not hex of the length of the
// hash also gives ErrCorrupt, so an object with no checksum in the manifest
// never passes.
func checkObject(ctx context.Context, target blob.Store, key blob.Key, want objectWant) error {
	check, err := newObjectCheck(string(key), want)
	if err != nil {
		return err
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return &MissingObjectError{Key: key}
		}

		return fmt.Errorf("read backup object %q: %w", key, err)
	}

	_, readErr := io.Copy(check, reader)
	closeErr := reader.Close()

	if readErr != nil {
		return fmt.Errorf("read backup object %q: %w", key, readErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", key, closeErr)
	}

	return check.verify(string(key))
}
