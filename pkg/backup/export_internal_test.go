package backup

import (
	"context"
	"crypto"

	"opendustpan.org/opendustpan/pkg/blob"
)

// CheckObjectBytes runs the object check of one manifest entry on data: the
// hash function h, the hex sum and the size that the manifest records. The
// restore paths and backup check run the same check.
func CheckObjectBytes(h crypto.Hash, sum string, size int64, data []byte) error {
	check, err := newObjectCheck("test-object", objectWant{size: size, hash: h, sum: sum})
	if err != nil {
		return err
	}

	if _, err := check.Write(data); err != nil {
		return err
	}

	return check.verify("test-object")
}

// VerifyLiveBlobForTest runs verifyLiveBlob: it checks that the live store
// holds ref with the size and checksum of the manifest.
func VerifyLiveBlobForTest(ctx context.Context, live blob.Store, ref ManifestBlob) error {
	var s RestoreService
	s.blobs = live

	return s.verifyLiveBlob(ctx, ref)
}

// ProtectedKeyForTest runs the deny layer of the restore key check.
func ProtectedKeyForTest(key blob.Key) bool {
	return protectedKey(key)
}

// WritableKeyForTest runs the allowlist layer of the restore key check.
func WritableKeyForTest(key string) bool {
	return writableKey(key)
}

// CheckRestoreKeyForTest runs both layers of the restore key check.
func CheckRestoreKeyForTest(key string) error {
	return checkRestoreKey(key)
}

// SetMaxUnverifiedKeysForTest sets the bound on the unverified keys of a
// backup of s to limit, so a test proves the refusal with few keys.
func SetMaxUnverifiedKeysForTest(s *Service, limit int) {
	s.maxUnverified = limit
}
