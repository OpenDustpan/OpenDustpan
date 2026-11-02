package backup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
)

// ErrLiveBlobDamaged reports a live blob whose plain bytes do not match the
// record of its own live row: another size or another MD5. A StateOpener
// wraps it, so the backup before a restore can tell damage from a read error.
// Sealed content that does not open is not damage: a key or a key provider
// can cause it.
var ErrLiveBlobDamaged = errors.New("live blob does not match its record")

// DamageState says what the backup before a restore found at a live key.
type DamageState string

// The states of a damaged entry of a manifest.
const (
	// DamageMissing says that the live store holds no object at the key.
	DamageMissing DamageState = "missing"
	// DamageMismatch says that the live bytes do not match their live row.
	DamageMismatch DamageState = "damaged"
)

// DamageReason says why the backup before a restore recorded a live key.
type DamageReason string

// The reasons of a damaged entry of a manifest.
const (
	// ReasonAbsent says that the live key has no object.
	ReasonAbsent DamageReason = "absent"
	// ReasonSize says that the live bytes have another size than the row.
	ReasonSize DamageReason = "size"
	// ReasonChecksum says that the live bytes have another MD5 than the row.
	ReasonChecksum DamageReason = "checksum"
	// ReasonSealed says that the plain bytes of a sealed live blob opened and
	// do not match the size or the MD5 of the row.
	ReasonSealed DamageReason = "sealed"
)

// DamagedBlob names one live blob that the backup before a restore found
// missing or damaged, and did not copy. Size and MD5 describe the stored
// live bytes that the backup read, so a restore can prove that the live key
// still holds those bytes before it replaces them. A missing entry has no
// size and no MD5. MD5 is lowercase hex.
type DamagedBlob struct {
	Key    string       `json:"key"`
	State  DamageState  `json:"state"`
	Reason DamageReason `json:"reason"`
	MD5    string       `json:"md5,omitempty"`
	Size   int64        `json:"size,omitempty"`
}

// errDamagedEntry reports a damaged entry of a manifest that is not valid.
var errDamagedEntry = errors.New("damaged entry is not valid")

// validDamaged checks the damaged entries of a manifest with an allowlist:
// a key that a restore may write, a known state, the reason that fits the
// state, and a size and an MD5 only for damaged bytes. The keys must be
// sorted and unique. The manifest is not authenticated, so a restore must not
// trust an entry that fails.
func validDamaged(entries []DamagedBlob) error {
	prev := ""

	for i, entry := range entries {
		if err := validDamagedEntry(entry); err != nil {
			return fmt.Errorf("damaged entry %d: %w", i, err)
		}

		if i > 0 && prev >= entry.Key {
			return fmt.Errorf("damaged entry %d: keys are not sorted and unique: %w", i, errDamagedEntry)
		}

		prev = entry.Key
	}

	return nil
}

// validDamagedEntry checks one damaged entry. See validDamaged.
func validDamagedEntry(entry DamagedBlob) error {
	if err := checkRestoreKey(entry.Key); err != nil {
		return fmt.Errorf("%w: key: %w", errDamagedEntry, err)
	}

	switch entry.State {
	case DamageMissing:
		if entry.Reason != ReasonAbsent || entry.MD5 != "" || entry.Size != 0 {
			return fmt.Errorf("%w: a missing entry has the reason %q and no size or MD5", errDamagedEntry, ReasonAbsent)
		}

		return nil
	case DamageMismatch:
		return validMismatchEntry(entry)
	default:
		return fmt.Errorf("%w: unknown state", errDamagedEntry)
	}
}

// validMismatchEntry checks one damaged entry of the state DamageMismatch: a
// known reason, a size that is not negative, and a lowercase MD5.
func validMismatchEntry(entry DamagedBlob) error {
	if !slices.Contains([]DamageReason{ReasonSize, ReasonChecksum, ReasonSealed}, entry.Reason) {
		return fmt.Errorf("%w: unknown reason of a damaged entry", errDamagedEntry)
	}

	raw, err := hex.DecodeString(entry.MD5)
	if err != nil || len(raw) != blob.NewMD5().Size() || hex.EncodeToString(raw) != entry.MD5 || entry.Size < 0 {
		return fmt.Errorf("%w: a damaged entry needs the size and the lowercase MD5 of the live bytes",
			errDamagedEntry)
	}

	return nil
}

// liveDamageError is the error of one blob copy that found the live blob missing
// or damaged. It wraps the error of the check, so a backup that does not
// record damage fails with the same text as before.
type liveDamageError struct {
	cause error
	entry DamagedBlob
}

func (d *liveDamageError) Error() string { return d.cause.Error() }

func (d *liveDamageError) Unwrap() error { return d.cause }

// missingDamage returns the damage of a live key with no object.
func missingDamage(key string, cause error) *liveDamageError {
	return &liveDamageError{
		cause: cause,
		entry: DamagedBlob{Key: key, State: DamageMissing, Reason: ReasonAbsent, MD5: "", Size: 0},
	}
}

// mismatchDamage returns the damage of live bytes of size and MD5 sum that do
// not match their row.
func mismatchDamage(key string, reason DamageReason, size int64, sum []byte, cause error) *liveDamageError {
	return &liveDamageError{
		cause: cause,
		entry: DamagedBlob{Key: key, State: DamageMismatch, Reason: reason, MD5: hex.EncodeToString(sum), Size: size},
	}
}

// plainDamageReason returns the reason of plain live bytes of size that do
// not match ref.
func plainDamageReason(size int64, ref BlobRef) DamageReason {
	if size != ref.Size {
		return ReasonSize
	}

	return ReasonChecksum
}

// sourceMissing reports whether err says that the live store has no object.
func sourceMissing(err error) bool {
	return errors.Is(err, blob.ErrNotFound)
}

// damagedEvidence returns the damaged entry of key in manifest, and false
// when the manifest has none.
func damagedEvidence(manifest Manifest, key string) (DamagedBlob, bool) {
	i, found := slices.BinarySearchFunc(manifest.Damaged, key, func(e DamagedBlob, k string) int {
		return strings.Compare(e.Key, k)
	})
	if !found {
		return DamagedBlob{Key: "", State: "", Reason: "", MD5: "", Size: 0}, false
	}

	return manifest.Damaged[i], true
}

// refuseDamagedBackup refuses a backup whose manifest names damaged live
// blobs. Such a backup holds no copy of those blobs, so it is not complete:
// backup check and a restore of it both stop here. It wraps ErrCorrupt.
func refuseDamagedBackup(manifest Manifest) error {
	if len(manifest.Damaged) == 0 {
		return nil
	}

	return fmt.Errorf("%w: backup %s found %d live blobs missing or damaged and holds no copy of them, first %q",
		ErrCorrupt, manifest.ID, len(manifest.Damaged), truncateKey(manifest.Damaged[0].Key))
}
