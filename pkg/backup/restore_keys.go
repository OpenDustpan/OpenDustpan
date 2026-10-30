package backup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrProtectedKey means that the manifest of a backup names a blob key that a
// restore does not write: a key under a prefix that the server owns, or a key
// outside the prefixes that a restore may write. The restore ends failed
// before it writes anything for that key. It wraps ErrCorrupt.
var ErrProtectedKey = fmt.Errorf("backup names a blob key that a restore does not write: %w", ErrCorrupt)

// storeCheckPrefix is the first segment of the keys that a store check writes.
const storeCheckPrefix = "store-checks"

// namespaceKeyPrefix is the first segment of every blob key that a backup
// copies: namespaces/<namespace ID>/...
const namespaceKeyPrefix = "namespaces"

// protectedFirstSegments returns the first segments of the keys that the server
// owns and that a restore never writes, besides the audit checkpoint copies
// (audit.IsCheckpointKey decides those): the objects of a store check, and the
// temporary keys of a restore.
func protectedFirstSegments() []string {
	return []string{storeCheckPrefix, tempKeyPrefix}
}

// protectedKey reports whether key is under a prefix that the server owns.
// This is the deny layer. It uses audit.IsCheckpointKey for the audit
// checkpoints, and a list for the other prefixes. It does not rely on the
// allowlist of writableKey.
func protectedKey(key blob.Key) bool {
	if audit.IsCheckpointKey(key) {
		return true
	}

	first, _, _ := strings.Cut(key.String(), "/")

	for _, prefix := range protectedFirstSegments() {
		if strings.EqualFold(first, prefix) {
			return true
		}
	}

	return false
}

// writableKey reports whether a restore may write key. This is the allowlist
// layer: a key must be valid, must start with namespaces/<namespace ID>/, and
// must name an object below that directory. It does not rely on the deny list
// of protectedKey, so a prefix that the server adds later stays refused.
func writableKey(key string) bool {
	parsed, err := blob.ParseKey(key)
	if err != nil {
		return false
	}

	segments := strings.Split(parsed.String(), "/")

	if len(segments) < 3 || segments[0] != namespaceKeyPrefix {
		return false
	}

	_, err = namespace.ParseID(segments[1])

	return err == nil
}

// checkRestoreKey returns ErrProtectedKey, naming the key, when a restore may
// not write key. Both layers run, and either one refuses.
func checkRestoreKey(key string) error {
	// Defense in depth: the allowlist and the deny list each refuse a protected
	// key alone. Both stay, so that a change to one of them does not open a
	// prefix, and so that no caller depends on the other layers.
	parsed, err := blob.ParseKey(key)
	if err != nil {
		return fmt.Errorf("%w: %q is not a valid key", ErrProtectedKey, truncateKey(key))
	}

	if protectedKey(parsed) {
		return fmt.Errorf("%w: %q is under a prefix that the server owns", ErrProtectedKey, truncateKey(key))
	}

	if !writableKey(key) {
		return fmt.Errorf("%w: %q is outside the prefixes that a restore writes", ErrProtectedKey, truncateKey(key))
	}

	return nil
}

// maxKeyInEvent bounds the part of a key that an audit event holds.
const maxKeyInEvent = 256

// truncateKey bounds key for an error text or an audit event.
func truncateKey(key string) string {
	if len(key) > maxKeyInEvent {
		return key[:maxKeyInEvent] + "..."
	}

	return key
}

// refusedKeyAuditObject names the refusal of one blob key of the backup of
// the restore in, for its audit entry. The key is quoted, so it holds no
// carriage return and no line feed.
func refusedKeyAuditObject(in restoreInput, key string) string {
	return "restore " + in.ID.String() + " refused blob key " + strconv.Quote(truncateKey(key)) +
		" of backup " + in.BackupID.String() + ": a restore does not write this key"
}

// refuseKeys checks the blob keys of the manifest and, for the first key that
// a restore may not write, stores the audit event of the refusal under the
// fence and returns the error. It fails closed: when the event cannot be
// stored, the restore is refused all the same, and the error of the store is
// joined to the refusal.
func (s *RestoreService) refuseKeys(
	ctx context.Context, fence durable.StepFence, in restoreInput, blobs []ManifestBlob,
) error {
	for _, ref := range blobs {
		keyErr := checkRestoreKey(ref.Key)
		if keyErr == nil {
			continue
		}

		recordErr := s.live.RecordRestoreEvent(ctx, fence, in.ID, refusedKeyAuditObject(in, ref.Key))
		if recordErr != nil {
			// A lost claim or a cancel stops the step. Any other error joins
			// the refusal.
			if errors.Is(recordErr, durable.ErrClaimLost) || errors.Is(recordErr, durable.ErrCanceled) {
				return recordErr
			}

			return errors.Join(keyErr, fmt.Errorf("record the refusal of a blob key: %w", recordErr))
		}

		return keyErr
	}

	return nil
}

// checkExportKeys returns ErrProtectedKey for the first blob key of refs that a
// restore does not write. A backup that names such a key could never be
// restored, so the export refuses it before it copies anything. The text names
// the key, because the operator must find the row that names it.
func checkExportKeys(id ID, refs []BlobRef) error {
	for _, ref := range refs {
		if err := checkRestoreKey(ref.Key); err != nil {
			return fmt.Errorf("backup %s: the data names a blob key that a restore does not write: "+
				"delete the row that names it: %w", id, err)
		}
	}

	return nil
}
