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
// it into a store. Restore calls it on the SQL part to prove that the
// key-encryption key opens the backup before it writes anything (ADR 30
// decision 7). Each store adapter implements it for its own stream: SQLite
// reads one row of secret_keys from the file image, PostgreSQL from the COPY
// bytes of secret_keys.
type KeyPeeker interface {
	// PeekKey returns one data key wrapped by wrapperID from the export in
	// r. It returns ErrCorrupt when r holds no key of wrapperID.
	PeekKey(ctx context.Context, r io.Reader, wrapperID string) (secret.WrappedKey, error)
}

// RestoreStore is the SQL destination of a restore: it reports its own
// dialect, version and emptiness through Export, loads through Import, and
// proves the key through PeekKey. Every store adapter implements it.
type RestoreStore interface {
	Exporter
	Importer
	KeyPeeker
}

// RestoreDest holds the destination of a restore: the live blob store that
// receives the blobs, the empty SQL store that receives the tables, and the
// region of this installation for the region warning. A blank Region disables
// the warning.
type RestoreDest struct {
	Blobs  blob.Store
	Store  RestoreStore
	Region string
}

// Restore loads backup id from target into dest in the order of ADR 30
// decision 8: manifest checks, key check, empty destination, blobs, SQL data
// and row counts. It streams the blobs and the SQL part through verification,
// copy and load; the key check reads one wrapped key without loading the
// destination. Every check runs before the first blob write. It returns the
// region warning, which is empty when Region is blank or among the home
// regions of the manifest. A failed restore leaves an empty database; it
// never leaves a database that names missing blobs, because the blobs land
// before the SQL data.
func Restore(
	ctx context.Context, target blob.Store, id ID, dest RestoreDest, key secret.Wrapper,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("restore backup %s: %w", id, err)
	}

	if target == nil || dest.Blobs == nil || dest.Store == nil {
		return "", fmt.Errorf("restore backup %s: %w", id, ErrMissingDependency)
	}

	job := restoreJob{target: target, store: dest.Store, id: id}

	plan, err := job.loadPlan(ctx)
	if err != nil {
		return "", err
	}

	if verifyErr := job.verifyPre(ctx, plan, key); verifyErr != nil {
		return "", verifyErr
	}

	if copyErr := job.copyBlobs(ctx, dest.Blobs, plan.manifest.Blobs); copyErr != nil {
		return "", copyErr
	}

	got, importErr := job.importSQL(ctx, plan)
	if importErr != nil {
		return "", importErr
	}

	if countErr := checkRestoreCounts(got, plan.manifest, job.id); countErr != nil {
		return "", countErr
	}

	return restoreWarning(plan.manifest, dest.Region), nil
}

// restoreJob carries the fixed endpoints of one restore: the backup target to
// read from, the SQL store to load into, and the backup ID. Methods take only
// what changes per step.
type restoreJob struct {
	target blob.Store
	store  RestoreStore
	id     ID
}

// restorePlan carries the checked manifest with the decoded SQL checksum, the
// blob references and the destination summary.
type restorePlan struct {
	manifest  Manifest
	wantSHA   []byte
	wantBlobs []BlobRef
	dst       Summary
}

// loadPlan reads the manifest, decodes its checksums and reads the
// destination. It checks the dialect and the schema version.
func (j restoreJob) loadPlan(ctx context.Context) (restorePlan, error) {
	manifest, err := readManifest(ctx, j.target, j.id)
	if err != nil {
		return restorePlan{}, err
	}

	wantSHA, err := parseSQLSHA(manifest, j.id)
	if err != nil {
		return restorePlan{}, err
	}

	wantBlobs, err := parseManifestBlobs(manifest, j.id)
	if err != nil {
		return restorePlan{}, err
	}

	dst, err := j.store.Export(ctx, io.Discard)
	if err != nil {
		return restorePlan{}, fmt.Errorf("restore backup %s: read destination: %w", j.id, err)
	}

	if err := checkRestoreDialect(manifest, dst, j.id); err != nil {
		return restorePlan{}, err
	}

	if err := checkRestoreSchema(manifest, dst, j.id); err != nil {
		return restorePlan{}, err
	}

	return restorePlan{manifest: manifest, wantSHA: wantSHA, wantBlobs: wantBlobs, dst: dst}, nil
}

// verifyPre verifies the SQL part, proves the key and refuses a destination
// that is not empty. It writes nothing.
func (j restoreJob) verifyPre(ctx context.Context, plan restorePlan, key secret.Wrapper) error {
	if err := verifySQLPart(ctx, j.target, j.id, plan.manifest.SQLSize, plan.wantSHA); err != nil {
		return err
	}

	check := backupKeyCheck{target: j.target, peeker: j.store, id: j.id, manifest: plan.manifest, key: key}

	if keyErr := check.run(ctx); keyErr != nil {
		return keyErr
	}

	for _, table := range Tables {
		// The migration seeds the installation row: it is bookkeeping, not
		// data, so a fresh destination still counts as empty.
		if table == "installation" {
			continue
		}

		if plan.dst.RowCounts[table] > 0 {
			return fmt.Errorf("restore destination holds %d rows of %s: %w",
				plan.dst.RowCounts[table], table, ErrNotEmpty)
		}
	}

	return nil
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
		out = append(out, BlobRef{MD5: sum, Key: ref.Key, Size: ref.Size})
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

// verifySQLPart Gets the SQL part and counts its bytes through SHA-256. It
// writes nothing: the bytes go to the hash only.
func verifySQLPart(ctx context.Context, target blob.Store, id ID, wantSize int64, wantSHA []byte) error {
	key, err := blob.ParseKey("backups/" + id.String() + "/db")
	if err != nil {
		return fmt.Errorf("restore backup %s: %w", id, err)
	}

	reader, err := target.Get(ctx, key)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("backup is incomplete: object %q is missing: %w", key, ErrCorrupt)
		}

		return fmt.Errorf("read backup object %q: %w", key, err)
	}

	size, sum, readErr := hashStream(reader)
	closeErr := reader.Close()

	if readErr != nil {
		return fmt.Errorf("read backup object %q: %w", key, readErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", key, closeErr)
	}

	if size != wantSize || !bytes.Equal(sum, wantSHA) {
		return fmt.Errorf("backup object %q holds %d bytes, the manifest names %d: %w",
			key, size, wantSize, ErrCorrupt)
	}

	return nil
}

// copyBlobs streams each blob of the manifest from the target into the
// destination store. It hashes while it copies: on a size or MD5 mismatch it
// deletes the copied object and reports ErrCorrupt. ErrExists is an error.
func (j restoreJob) copyBlobs(ctx context.Context, dest blob.Store, refs []ManifestBlob) error {
	for _, ref := range refs {
		if err := copyRestoreBlob(ctx, j.target, dest, ref); err != nil {
			return err
		}
	}

	return nil
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

// importSQL Gets the SQL part a third time and loads it while it verifies
// the size and hash. A mismatch fails the load with ErrCorrupt, which leaves
// an empty store.
func (j restoreJob) importSQL(ctx context.Context, plan restorePlan) (Summary, error) {
	var none Summary

	key, err := blob.ParseKey("backups/" + j.id.String() + "/db")
	if err != nil {
		return none, fmt.Errorf("restore backup %s: %w", j.id, err)
	}

	reader, err := j.target.Get(ctx, key)
	if err != nil {
		if isNotFound(err) {
			return none, fmt.Errorf("backup is incomplete: object %q is missing: %w", key, ErrCorrupt)
		}

		return none, fmt.Errorf("read backup object %q: %w", key, err)
	}

	verifier := &sqlVerifier{
		reader:   reader,
		hash:     sha256.New(),
		wantSHA:  plan.wantSHA,
		wantSize: plan.manifest.SQLSize,
		size:     0,
		err:      nil,
	}
	got, importErr := j.store.Import(ctx, verifier, restoreWant(plan.manifest, plan.wantBlobs))
	closeErr := reader.Close()

	if importErr != nil {
		return none, fmt.Errorf("load backup %s: %w", j.id, importErr)
	}

	if closeErr != nil {
		return none, fmt.Errorf("close backup object %q: %w", key, closeErr)
	}

	// The verifier reports a short or tampered stream at EOF. Import may
	// already have failed on it; when it did not, fail here before the row
	// counts can pass on partial data.
	if verifier.err != nil {
		return none, fmt.Errorf("load backup %s: %w", j.id, verifier.err)
	}

	return got, nil
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

// checkRestoreCounts compares the loaded row counts with the manifest.
func checkRestoreCounts(got Summary, manifest Manifest, id ID) error {
	for _, table := range Tables {
		if got.RowCounts[table] != manifest.RowCounts[table] {
			return fmt.Errorf("restore backup %s: table %s holds %d rows, the manifest names %d: %w",
				id, table, got.RowCounts[table], manifest.RowCounts[table], ErrCorrupt)
		}
	}

	return nil
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

// sqlVerifier wraps the third Get of the SQL part: it hashes and counts the
// bytes while the importer reads them, and fails the read at EOF when the
// size or hash differs from the manifest.
type sqlVerifier struct {
	reader   io.ReadCloser
	hash     hash.Hash
	wantSHA  []byte
	wantSize int64
	size     int64
	err      error
}

func (v *sqlVerifier) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}

	n, err := v.reader.Read(p)
	if n > 0 {
		v.size += int64(n)

		// hash.Hash.Write never returns an error; the check satisfies errcheck.
		if _, hashErr := v.hash.Write(p[:n]); hashErr != nil {
			v.err = errors.Join(ErrCorrupt, hashErr)

			return 0, v.err
		}
	}

	if err == io.EOF {
		switch {
		case v.size != v.wantSize:
			v.err = fmt.Errorf("%w: SQL data holds %d bytes, the manifest names %d",
				ErrCorrupt, v.size, v.wantSize)

			return n, v.err
		case !bytes.Equal(v.hash.Sum(nil), v.wantSHA):
			v.err = fmt.Errorf("%w: SQL data does not match the manifest checksum", ErrCorrupt)

			return n, v.err
		default:
			// A reader may return its last bytes together with io.EOF:
			// pass them on with it.
			return n, io.EOF
		}
	}

	if err != nil {
		return n, fmt.Errorf("read backup data: %w", err)
	}

	return n, nil
}
