package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

// LiveBlobReader reads the live rows that name one blob. The restore reads it
// just before it deletes a damaged live blob, so the proof of the damage does
// not rest on the backup target alone.
type LiveBlobReader interface {
	// LiveBlobRef returns the reference that the live rows give for key: the
	// size and the MD5 of the plain bytes, and the seal flag. It returns an
	// error that wraps ErrNotFound when no live row names key.
	LiveBlobRef(ctx context.Context, key string) (BlobRef, error)
}

// errNoDamageEvidence reports a live blob that differs from the backup and
// that the restore cannot prove damaged. It fails closed: the restore treats
// the blob as live data and does not overwrite it.
var errNoDamageEvidence = errors.New("the restore cannot prove that the live blob is damaged")

// damageEvidence returns the damaged entry of the live key of ref, only when
// all of these hold:
//
//   - this restore started the backup before the restore, and the step
//     record of that step holds the digest of its manifest;
//   - the manifest in the target has that digest now;
//   - the manifest records the key as damaged, with an entry that is valid;
//   - the live key holds the bytes of that entry: the same size and MD5.
//
// Any other result, also a manifest that cannot be read, returns an error that
// wraps errNoDamageEvidence.
func (s *RestoreService) damageEvidence(ctx context.Context, in restoreInput, ref ManifestBlob) (DamagedBlob, error) {
	none := DamagedBlob{Key: "", State: "", Reason: "", MD5: "", Size: 0}

	if in.CheckOnly || in.PreBackupID == "" || !in.evidence.Created || in.evidence.Digest == "" {
		return none, errNoDamageEvidence
	}

	pre, err := readManifest(ctx, s.target, in.PreBackupID)
	if err != nil {
		return none, errors.Join(errNoDamageEvidence, err)
	}

	// The target is not authenticated. The digest in the step record binds
	// the evidence to the manifest that this run read and audited.
	digest, err := manifestDigest(pre)
	if err != nil || digest != in.evidence.Digest {
		return none, errors.Join(errNoDamageEvidence, fmt.Errorf("the manifest of backup %s changed after the "+
			"backup before the restore: %w", in.PreBackupID, ErrCorrupt), err)
	}

	entry, ok := damagedEvidence(pre, ref.Key)
	if !ok || entry.State != DamageMismatch {
		return none, errNoDamageEvidence
	}

	// Defense in depth: the read of the manifest checks each damaged entry.
	// This check stays so that an entry that a new reader does not check
	// cannot let the restore replace a live blob.
	if err := validDamagedEntry(entry); err != nil {
		return none, errors.Join(errNoDamageEvidence, err)
	}

	recorded := ManifestBlob{Key: entry.Key, Size: entry.Size, MD5: entry.MD5, Sealed: false}
	if err := s.verifyLiveBlob(ctx, recorded); err != nil {
		return none, errors.Join(errNoDamageEvidence, err)
	}

	return entry, nil
}

// confirmLiveDamage reads the live row of key from the database and confirms
// that the live bytes still do not match it. It returns nil only for a size
// or an MD5 that differ from the row. A row that cannot be read, live bytes
// that match the row, and every other error return an error that wraps
// errNoDamageEvidence.
//
// Defense in depth: damageEvidence proves the damage from the backup before
// the restore. This check stays so that a forged or stale entry in the backup
// target alone cannot make the restore delete a live blob: it does not rely
// on the target, the manifest digest or the step record.
func (s *RestoreService) confirmLiveDamage(ctx context.Context, key blob.Key) error {
	reader, ok := s.live.(LiveBlobReader)
	if !ok {
		return fmt.Errorf("%w: the live store cannot read the row of a blob", errNoDamageEvidence)
	}

	row, err := reader.LiveBlobRef(ctx, key.String())
	if err != nil {
		return errors.Join(
			errNoDamageEvidence,
			fmt.Errorf("read the live row of blob %q: %w", truncateKey(key.String()), err),
		)
	}

	if row.Key != key.String() {
		return fmt.Errorf("%w: the live row names another key", errNoDamageEvidence)
	}

	if row.Sealed {
		return s.confirmSealedDamage(ctx, key, row)
	}

	verifyErr := s.verifyLiveBlob(ctx, ManifestBlob{
		Key: row.Key, Size: row.Size, MD5: hex.EncodeToString(row.MD5[:]), Sealed: false,
	})

	switch {
	case errors.Is(verifyErr, ErrCorrupt):
		return nil
	case verifyErr == nil:
		return fmt.Errorf("%w: the live bytes match their live row", errNoDamageEvidence)
	default:
		return errors.Join(errNoDamageEvidence, verifyErr)
	}
}

// confirmSealedDamage confirms that the plain bytes of a sealed live blob do
// not match their live row. Sealed bytes that do not open are not damage: the
// cause can be a key or a key provider. Only a size or an MD5 that differ from
// the row confirm the damage.
func (s *RestoreService) confirmSealedDamage(ctx context.Context, key blob.Key, row BlobRef) error {
	if s.backups == nil || s.backups.opener == nil {
		return fmt.Errorf("%w: no state opener", errNoDamageEvidence)
	}

	size, sum, err := s.backups.hashPlainCopy(ctx, key, row)

	switch {
	case err != nil && errors.Is(err, ErrLiveBlobDamaged):
		return nil
	case err != nil:
		return errors.Join(errNoDamageEvidence, err)
	case size != row.Size || !bytes.Equal(sum, row.MD5[:]):
		return nil
	default:
		return fmt.Errorf("%w: the plain live bytes match their live row", errNoDamageEvidence)
	}
}

// damagedCopyKey returns the target key of the copy of the damaged live bytes
// of key: backups/<pre-backup ID>/damaged/<key>.
func damagedCopyKey(pre ID, key string) (blob.Key, error) {
	if _, err := ParseID(pre.String()); err != nil {
		return "", err
	}

	dest, err := blob.ParseKey("backups/" + pre.String() + "/damaged/" + key)
	if err != nil {
		return "", fmt.Errorf("target key of the damaged copy of blob %q: %w", truncateKey(key), err)
	}

	return dest, nil
}

// preserveDamaged copies the damaged live bytes of live into the target under
// dest, and checks the copy against entry. A copy that an earlier try left
// stays when it holds the bytes of entry. Any failure returns an error, and
// the caller deletes nothing.
func (s *RestoreService) preserveDamaged(ctx context.Context, live, dest blob.Key, entry DamagedBlob) error {
	want := ManifestBlob{Key: entry.Key, Size: entry.Size, MD5: entry.MD5, Sealed: false}

	check, err := newObjectCheck(entry.Key, blobWant(want))
	if err != nil {
		return err
	}

	reader, err := s.blobs.Get(ctx, live)
	if err != nil {
		return fmt.Errorf("read damaged live blob %q: %w", truncateKey(entry.Key), err)
	}

	putErr := s.target.Put(ctx, dest, io.TeeReader(reader, check))
	closeErr := reader.Close()

	switch {
	case putErr != nil && errors.Is(putErr, blob.ErrExists):
		return s.checkDamagedCopy(ctx, dest, want)
	case putErr != nil:
		return fmt.Errorf("copy damaged live blob %q to the target: %w", truncateKey(entry.Key), putErr)
	case closeErr != nil:
		return fmt.Errorf("close damaged live blob %q: %w", truncateKey(entry.Key), closeErr)
	}

	return check.verify(entry.Key)
}

// checkDamagedCopy checks that the target object at dest holds the bytes of
// want.
func (s *RestoreService) checkDamagedCopy(ctx context.Context, dest blob.Key, want ManifestBlob) error {
	if err := checkObject(ctx, s.target, dest, blobWant(want)); err != nil {
		return fmt.Errorf("the target holds another copy of damaged live blob %q: %w", truncateKey(want.Key), err)
	}

	return nil
}

// removeDamaged deletes the damaged live blob of ref, so that the checked
// bytes of the backup can go there. Before the delete, it proves the damage
// again from the backup before the restore, confirms it against the live row,
// copies the damaged bytes into the target under
// backups/<pre-backup ID>/damaged/<key>, and stores the audit event of the
// replacement, which names that copy. It fails closed: when any of these
// fails, it deletes nothing. A proof that fails refuses the blob as live data
// that differs. A stop after the delete leaves the key absent, and the next try
// writes the checked bytes.
func (s *RestoreService) removeDamaged(
	ctx context.Context, in restoreInput, fence durable.StepFence, ref ManifestBlob, live blob.Key,
) error {
	// Defense in depth: the copy checks the keys of the manifest before it
	// starts. This check stays so that no caller can delete a key that a
	// restore does not write.
	if err := checkRestoreKey(live.String()); err != nil {
		return err
	}

	entry, err := s.damageEvidence(ctx, in, ref)
	if err != nil {
		return s.refuseDifferingBlob(ctx, in, fence, ref, fmt.Errorf("%w: %w", ErrCorrupt, err))
	}

	if confirmErr := s.confirmLiveDamage(ctx, live); confirmErr != nil {
		return s.refuseDifferingBlob(ctx, in, fence, ref, fmt.Errorf("%w: %w", ErrCorrupt, confirmErr))
	}

	dest, err := damagedCopyKey(in.PreBackupID, ref.Key)
	if err != nil {
		return err
	}

	if preserveErr := s.preserveDamaged(ctx, live, dest, entry); preserveErr != nil {
		return preserveErr
	}

	if err := s.live.RecordRestoreEvent(ctx, fence, in.ID, replacedDamagedAuditObject(in, ref.Key, dest)); err != nil {
		return fmt.Errorf("record the replacement of damaged live blob %q: %w", truncateKey(ref.Key), err)
	}

	if err := s.blobs.Delete(ctx, live); err != nil {
		return fmt.Errorf("delete damaged live blob %q: %w", truncateKey(ref.Key), err)
	}

	s.logger.WarnContext(ctx, "restore replaces a damaged live blob",
		slog.String("restore_id", in.ID.String()), slog.String("key", truncateKey(ref.Key)))

	return nil
}

// FindBlobRef returns the reference of key in refs, which are ordered by
// key, as a store reads them for a backup. It wraps ErrNotFound when refs
// hold no reference of key. A store calls it in LiveBlobRef.
func FindBlobRef(refs []BlobRef, key string) (BlobRef, error) {
	i, found := slices.BinarySearchFunc(refs, key, func(r BlobRef, k string) int { return strings.Compare(r.Key, k) })
	if !found {
		return BlobRef{MD5: [16]byte{}, Key: "", Size: 0, Sealed: false},
			fmt.Errorf("no live row names blob %q: %w", truncateKey(key), ErrNotFound)
	}

	return refs[i], nil
}
