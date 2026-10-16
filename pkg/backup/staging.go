package backup

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The manifest records MD5 per ADR 30, for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/secret"
)

// CheckStore is the live store ports that a server-side restore check needs
// (ADR 38 decision 3): it reads the live dialect and version through
// ExportPortable, proves the key-encryption key through PeekPortableKey, and
// stages the SQL part through StagePortable. It never loads into the live data.
type CheckStore interface {
	PortableExporter
	KeyPeeker
	PortableStager
	// DiscardStaging removes the staging area, if any. It leaves the live data
	// unchanged. A check-only restore calls it after the checks.
	DiscardStaging(ctx context.Context) error
}

// CheckInput holds one server-side restore check: the backup target to read
// from, the live store to stage in, the key-encryption key, the backup ID, and
// the region of this installation for the region warning. A blank Region
// disables the warning.
type CheckInput struct {
	Target blob.Store
	Live   CheckStore
	Key    secret.Wrapper
	ID     ID
	Region string
}

// CheckRestore runs every restore check of ADR 38 decision 3 in check-only
// mode: the manifest, the key-encryption key, every blob the manifest names
// with size and checksum, the SQL part with size and checksum, and the staged
// data with row counts, foreign keys, blob references and schema version. It
// loads the SQL part into the staging area only, then removes the staging
// area and applies nothing. The live data stays unchanged, also after a
// failed check. When the final discard fails, the staging area may remain:
// discard it again. It returns the Summary of the staged data with the region
// warning, which is empty when the region is blank or among the home regions
// of the manifest.
func CheckRestore(ctx context.Context, in CheckInput) (Summary, string, error) {
	var none Summary

	if err := ctx.Err(); err != nil {
		return none, "", fmt.Errorf("check restore of backup %s: %w", in.ID, err)
	}

	if in.Target == nil || in.Live == nil {
		return none, "", fmt.Errorf("check restore of backup %s: %w", in.ID, ErrMissingDependency)
	}

	plan, err := loadCheckPlan(ctx, in.Target, in.ID, in.Live)
	if err != nil {
		return none, "", err
	}

	if sqlErr := verifySQLPart(ctx, in.Target, in.ID, plan.manifest.SQLFiles); sqlErr != nil {
		return none, "", sqlErr
	}

	check := backupKeyCheck{
		target:   in.Target,
		peeker:   in.Live,
		id:       in.ID,
		manifest: plan.manifest,
		key:      in.Key,
	}

	if keyErr := check.run(ctx); keyErr != nil {
		return none, "", keyErr
	}

	if blobErr := verifyTargetBlobs(ctx, in.Target, plan.manifest.Blobs); blobErr != nil {
		return none, "", blobErr
	}

	staged, err := stageBackup(ctx, in.Target, in.Live, in.ID, stagePlan{
		want:  restoreWant(plan.manifest, plan.wantBlobs),
		files: plan.manifest.SQLFiles,
	})
	if err != nil {
		return none, "", err
	}

	if discardErr := in.Live.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
		return none, "", fmt.Errorf(
			"check restore of backup %s: discard staging area: %w (staging area may remain, discard it again)",
			in.ID,
			discardErr,
		)
	}

	return staged, restoreWarning(plan.manifest, in.Region), nil
}

// checkPlan carries the checked manifest of one restore check with the decoded
// SQL checksum, the blob references and the live summary.
type checkPlan struct {
	manifest  Manifest
	wantSHA   []byte
	wantBlobs []BlobRef
	live      Summary
}

// loadCheckPlan reads the manifest, decodes its checksums, reads the live
// summary, and checks the dialect and the schema version.
func loadCheckPlan(ctx context.Context, target blob.Store, id ID, live CheckStore) (checkPlan, error) {
	manifest, err := readManifest(ctx, target, id)
	if err != nil {
		return checkPlan{}, err
	}

	wantSHA, err := parseSQLSHA(manifest, id)
	if err != nil {
		return checkPlan{}, err
	}

	wantBlobs, err := parseManifestBlobs(manifest, id)
	if err != nil {
		return checkPlan{}, err
	}

	liveSum, _, err := live.ExportPortable(ctx, discardSink)
	if err != nil {
		return checkPlan{}, fmt.Errorf("check restore of backup %s: read live data: %w", id, err)
	}

	if schemaErr := checkRestoreSchema(manifest, liveSum, id); schemaErr != nil {
		return checkPlan{}, schemaErr
	}

	return checkPlan{manifest: manifest, wantSHA: wantSHA, wantBlobs: wantBlobs, live: liveSum}, nil
}

// stagePlan names what a stage checks: the summary the backup must match, and the
// table files to load.
type stagePlan struct {
	want  Summary
	files []PortableFile
}

// stageBackup stages the table files of backup id in the live store. A failed
// stage removes the staging area before the error returns, so the live data
// stays unchanged.
func stageBackup(
	ctx context.Context, target blob.Store, live CheckStore, id ID, plan stagePlan,
) (Summary, error) {
	var none Summary

	staged, stageErr := live.StagePortable(ctx, portableOpener(ctx, target, id), plan.want, plan.files)
	if stageErr != nil {
		// Best-effort cleanup after a failed stage: the staging area must go,
		// and the error below already names the cause.
		_ = live.DiscardStaging(context.WithoutCancel(ctx)) //nolint:errcheck // Best-effort cleanup.

		return none, fmt.Errorf("check restore of backup %s: %w", id, stageErr)
	}

	return staged, nil
}

// discardSink is a sink that keeps no bytes: a summary reads the store through it.
func discardSink(string) (io.WriteCloser, error) {
	return nopSinkCloser{Writer: io.Discard}, nil
}

// nopSinkCloser is a writer whose Close does nothing.
type nopSinkCloser struct {
	io.Writer
}

// Close does nothing.
func (nopSinkCloser) Close() error {
	return nil
}

// verifyTargetBlobs checks that the target holds every blob the manifest names,
// with the recorded size and MD5. It writes nothing.
func verifyTargetBlobs(ctx context.Context, target blob.Store, refs []ManifestBlob) error {
	for _, ref := range refs {
		if err := verifyTargetBlob(ctx, target, ref); err != nil {
			return err
		}
	}

	return nil
}

// verifyTargetBlob Gets one blob from the target and compares its bytes with
// the size and MD5 of the manifest. It writes nothing.
func verifyTargetBlob(ctx context.Context, target blob.Store, ref ManifestBlob) error {
	wantMD5, err := hex.DecodeString(ref.MD5)
	if err != nil || len(wantMD5) != md5.Size {
		return fmt.Errorf("%w: blob %q MD5 %q", ErrCorrupt, ref.Key, ref.MD5)
	}

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

	counter := &md5SizeWriter{hash: md5.New(), size: 0} //nolint:gosec // The manifest records MD5, for integrity.

	_, copyErr := io.Copy(io.Discard, io.TeeReader(reader, counter))
	closeErr := reader.Close()

	if copyErr != nil {
		return fmt.Errorf("read backup object %q: %w", source, copyErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close backup object %q: %w", source, closeErr)
	}

	if counter.size != ref.Size || !bytes.Equal(counter.hash.Sum(nil), wantMD5) {
		return fmt.Errorf("%w: blob %q holds %d bytes, the manifest names %d",
			ErrCorrupt, ref.Key, counter.size, ref.Size)
	}

	return nil
}

// backupKeyCheck carries one key-encryption key check: the backup target to
// read the SQL part from, the peeker that knows the stream format, the backup
// ID, the manifest, and the key-encryption key.
type backupKeyCheck struct {
	target   blob.Store
	peeker   KeyPeeker
	id       ID
	manifest Manifest
	key      secret.Wrapper
}

// run proves that the key-encryption key opens the backup before any write.
// With no wrapper IDs in the manifest no key is needed. Otherwise the wrapper
// must name one of them and open one data key of the SQL part. Every error
// names DUSTPAN_KEY_ENCRYPTION_KEY.
func (c backupKeyCheck) run(ctx context.Context) error {
	if len(c.manifest.WrapperIDs) == 0 {
		return nil
	}

	if c.key == nil {
		return fmt.Errorf("restore backup %s needs a key for wrappers %q: "+
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key: %w",
			c.id, c.manifest.WrapperIDs, secret.ErrKeyNotConfigured)
	}

	if !slices.Contains(c.manifest.WrapperIDs, c.key.ID()) {
		return fmt.Errorf("restore backup %s: key %q does not match wrappers %q: "+
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key: %w",
			c.id, c.key.ID(), c.manifest.WrapperIDs, secret.ErrWrapperMismatch)
	}

	err := OpenPortableKey(ctx, c.peeker, portableOpener(ctx, c.target, c.id), c.manifest.SQLFiles, c.key)
	if err != nil {
		return fmt.Errorf("restore backup %s: %w", c.id, err)
	}

	return nil
}

// OpenPortableKey proves that key opens one data key of the SQL part in
// files, read with open, before a restore writes anything. It reads a key of
// key.ID() from the secret_keys file. When that file holds none, it reads one
// from the installation_secret_keys file through the InstallationKeyPeeker of
// peeker, and returns ErrKeyUnchecked when peeker does not implement it.
// Every error about the key names DUSTPAN_KEY_ENCRYPTION_KEY.
func OpenPortableKey(
	ctx context.Context, peeker KeyPeeker, open PortableOpener, files []PortableFile, key secret.Wrapper,
) error {
	if key == nil {
		return fmt.Errorf("open a data key of the backup: set DUSTPAN_KEY_ENCRYPTION_KEY: %w",
			secret.ErrKeyNotConfigured)
	}

	wrapped, installation, err := peekAnyBackupKey(ctx, peeker, open, files, key.ID())
	if err != nil {
		return err
	}

	openKey := secret.OpenKey
	if installation {
		openKey = secret.OpenInstallationKey
	}

	opened, err := openKey(ctx, key, wrapped)
	if err != nil {
		return fmt.Errorf("key %q does not open the backup: "+
			"set DUSTPAN_KEY_ENCRYPTION_KEY to the matching key: %w", key.ID(), err)
	}

	clear(opened)

	return nil
}

// peekAnyBackupKey reads one data key of wrapperID from the secret_keys file,
// or else from the installation_secret_keys file. It reports whether the key
// is an installation key.
func peekAnyBackupKey(
	ctx context.Context, peeker KeyPeeker, open PortableOpener, files []PortableFile, wrapperID string,
) (secret.WrappedKey, bool, error) {
	var none secret.WrappedKey

	wrapped, err := peekBackupKey(ctx, peeker, open, files, wrapperID)
	if err == nil || !errors.Is(err, ErrNoDataKey) {
		return wrapped, false, err
	}

	installation, ok := peeker.(InstallationKeyPeeker)
	if !ok {
		return none, false, fmt.Errorf("the store cannot read the installation keys of the backup, "+
			"so the restore cannot check the key-encryption key: %w", ErrKeyUnchecked)
	}

	file, ok := portableFileOf(files, "installation_secret_keys")
	if !ok {
		return none, false, fmt.Errorf("the SQL part holds no installation_secret_keys file: %w", ErrCorrupt)
	}

	wrapped, err = installation.PeekPortableInstallationKey(ctx, open, file, wrapperID)
	if err != nil {
		return none, false, fmt.Errorf("read backup key of %q: %w", file.Path, err)
	}

	return wrapped, true, nil
}

// peekBackupKey reads one data key of wrapperID from the secret_keys file of the
// SQL part, without loading any store.
func peekBackupKey(
	ctx context.Context, peeker KeyPeeker, open PortableOpener, files []PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	file, ok := portableFileOf(files, "secret_keys")
	if !ok {
		return none, fmt.Errorf("the SQL part holds no secret_keys file: %w", ErrCorrupt)
	}

	wrapped, peekErr := peeker.PeekPortableKey(ctx, open, file, wrapperID)
	if peekErr != nil {
		return none, fmt.Errorf("read backup key of %q: %w", file.Path, peekErr)
	}

	return wrapped, nil
}
