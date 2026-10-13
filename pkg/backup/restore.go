package backup

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"slices"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/secret"
)

// KeyPeeker reads one wrapped data key from an export stream without loading
// it into a store. The server-side restore check calls it on the SQL part to
// prove that the key-encryption key opens the backup before it writes
// anything (ADR 30 decision 7). Each store adapter implements it for its own
// stream: SQLite reads one row of secret_keys from the file image,
// PostgreSQL from the COPY bytes of secret_keys.
type KeyPeeker interface {
	// PeekKey returns one data key wrapped by wrapperID from the export in
	// r. It returns ErrCorrupt when r holds no key of wrapperID.
	PeekPortableKey(
		ctx context.Context,
		open PortableOpener,
		file PortableFile,
		wrapperID string,
	) (secret.WrappedKey, error)
}

// parseSQLSHA decodes the SHA-256 of the manifest. A bad value is a corrupt
// manifest.
func parseSQLSHA(manifest Manifest, id ID) ([]byte, error) {
	sum, err := hex.DecodeString(manifest.SQLSHA256)
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("backup %s has a corrupt manifest: SQL checksum %q: %w",
			id, manifest.SQLSHA256, ErrCorrupt)
	}

	return sum, nil
}

// parseManifestBlobs converts the blob entries to BlobRef values and rejects
// a bad MD5 before any write.
func parseManifestBlobs(manifest Manifest, id ID) ([]BlobRef, error) {
	out := make([]BlobRef, 0, len(manifest.Blobs))

	for _, ref := range manifest.Blobs {
		raw, err := hex.DecodeString(ref.MD5)
		if err != nil || len(raw) != md5.Size {
			return nil, fmt.Errorf("backup %s has a corrupt manifest: blob %q MD5 %q: %w",
				id, ref.Key, ref.MD5, ErrCorrupt)
		}

		var sum [md5.Size]byte

		copy(sum[:], raw)
		out = append(out, BlobRef{MD5: sum, Key: ref.Key, Size: ref.Size, Sealed: ref.Sealed})
	}

	return out, nil
}

// checkRestoreDialect refuses a backup of another dialect. The error names
// both dialects, so the operator knows which release to use.
func checkRestoreDialect(manifest Manifest, dst Summary, id ID) error {
	if manifest.Dialect == dst.Dialect {
		return nil
	}

	return fmt.Errorf("restore backup %s of dialect %q into %q: %w",
		id, manifest.Dialect, dst.Dialect, ErrDialectMismatch)
}

// checkRestoreSchema refuses a backup of another schema version. The error
// names both versions, so the operator knows which release to use.
func checkRestoreSchema(manifest Manifest, dst Summary, id ID) error {
	if manifest.SchemaVersion == dst.SchemaVersion {
		return nil
	}

	return fmt.Errorf("restore backup %s of schema version %d into version %d: %w",
		id, manifest.SchemaVersion, dst.SchemaVersion, ErrSchemaMismatch)
}

// verifySQLPart reads each table file of the SQL part and checks its size and
// SHA-256 against its descriptor. It writes nothing: the bytes go to the hash
// only.
func verifySQLPart(ctx context.Context, target blob.Store, id ID, files []PortableFile) error {
	open := portableOpener(ctx, target, id)

	for _, file := range files {
		reader, err := open(file.Path)
		if err != nil {
			return err
		}

		size, sum, readErr := hashStream(reader)
		closeErr := reader.Close()

		if readErr != nil {
			return fmt.Errorf("read backup object %q: %w", file.Path, readErr)
		}

		if closeErr != nil {
			return fmt.Errorf("close backup object %q: %w", file.Path, closeErr)
		}

		want, err := hex.DecodeString(file.SHA256)
		if err != nil || size != file.Size || !bytes.Equal(sum, want) {
			return fmt.Errorf("backup object %q does not match its descriptor: %w", file.Path, ErrCorrupt)
		}
	}

	return nil
}

// portableOpener opens the table files of backup id from the target. A missing
// object is an incomplete backup.
func portableOpener(ctx context.Context, target blob.Store, id ID) PortableOpener {
	return func(path string) (io.ReadCloser, error) {
		key, err := blob.ParseKey("backups/" + id.String() + "/" + path)
		if err != nil {
			return nil, fmt.Errorf("restore backup %s: %w", id, err)
		}

		reader, err := target.Get(ctx, key)
		if err != nil {
			if isNotFound(err) {
				return nil, fmt.Errorf("backup is incomplete: object %q is missing: %w", key, ErrCorrupt)
			}

			return nil, fmt.Errorf("read backup object %q: %w", key, err)
		}

		return reader, nil
	}
}

// copyRestoreBlob streams one blob from the target into the destination.
func copyRestoreBlob(ctx context.Context, target, dest blob.Store, ref ManifestBlob) error {
	source, err := blob.ParseKey("blobs/" + ref.Key)
	if err != nil {
		return fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	reader, err := target.Get(ctx, source)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("backup is incomplete: object %q is missing: %w", source, ErrCorrupt)
		}

		return fmt.Errorf("read backup object %q: %w", source, err)
	}

	putErr := putRestoreBlob(ctx, dest, ref, reader)
	closeErr := reader.Close()

	if putErr != nil {
		return putErr
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", source, closeErr)
	}

	return nil
}

// putRestoreBlob Puts the bytes of reader under the key of ref while it
// hashes them with MD5 and counts them. On a mismatch it deletes the copied
// object.
func putRestoreBlob(ctx context.Context, dest blob.Store, ref ManifestBlob, reader io.Reader) error {
	wantMD5, err := hex.DecodeString(ref.MD5)
	if err != nil || len(wantMD5) != md5.Size {
		return fmt.Errorf("%w: blob %q MD5 %q", ErrCorrupt, ref.Key, ref.MD5)
	}

	destKey, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	counter := &md5SizeWriter{hash: md5.New(), size: 0} //nolint:gosec // The manifest records MD5, for integrity.

	if err := dest.Put(ctx, destKey, io.TeeReader(reader, counter)); err != nil {
		return fmt.Errorf("copy blob %q: %w", ref.Key, err)
	}

	if counter.size != ref.Size || !bytes.Equal(counter.hash.Sum(nil), wantMD5) {
		if delErr := dest.Delete(ctx, destKey); delErr != nil {
			return fmt.Errorf("%w: blob %q does not match the manifest: %w",
				ErrCorrupt, ref.Key, delErr)
		}

		return fmt.Errorf("%w: blob %q holds %d bytes, the manifest names %d",
			ErrCorrupt, ref.Key, counter.size, ref.Size)
	}

	return nil
}

// restoreWant builds the Import want Summary from the manifest.
func restoreWant(manifest Manifest, wantBlobs []BlobRef) Summary {
	return Summary{
		Dialect:       manifest.Dialect,
		HomeRegions:   manifest.HomeRegions,
		WrapperIDs:    manifest.WrapperIDs,
		RowCounts:     manifest.RowCounts,
		Blobs:         wantBlobs,
		SchemaVersion: manifest.SchemaVersion,
	}
}

// restoreWarning warns when Region is not among the home regions of the
// manifest. Restore still keeps the home region of each namespace.
func restoreWarning(manifest Manifest, region string) string {
	if region == "" || slices.Contains(manifest.HomeRegions, region) {
		return ""
	}

	return fmt.Sprintf("region %q is not among the backup regions %q", region, manifest.HomeRegions)
}

// hashStream counts the bytes of reader through SHA-256.
func hashStream(reader io.Reader) (int64, []byte, error) {
	sum := sha256.New()

	size, err := io.Copy(sum, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("hash backup stream: %w", err)
	}

	return size, sum.Sum(nil), nil
}

// isNotFound reports whether err wraps the missing-object error of the blob
// port.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, blob.ErrNotFound)
}

// md5SizeWriter counts and hashes blob bytes while they stream into the
// destination store.
type md5SizeWriter struct {
	hash hash.Hash
	size int64
}

func (w *md5SizeWriter) Write(p []byte) (int, error) {
	if _, err := w.hash.Write(p); err != nil {
		return 0, fmt.Errorf("hash blob: %w", err)
	}

	w.size += int64(len(p))

	return len(p), nil
}
