package backup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/blob"
)

// ErrLiveBlobDamaged reports a live blob whose plain bytes do not match the
// record of its own live row: another size or another MD5. A StateOpener
// wraps it, so the backup before a restore can tell damage from a read error.
// Sealed content that does not open is not damage: a key or a key provider
// can cause it.
var ErrLiveBlobDamaged = errors.New("live blob does not match its record")

// ErrLiveBlobUnopened reports a sealed live blob whose bytes do not open. A
// StateOpener wraps it. Damaged bytes and a wrong key both cause it, so the
// backup before a restore records the blob and the restore stops: it never
// writes over the blob. Every other backup stops at it.
var ErrLiveBlobUnopened = errors.New("sealed live blob does not open")

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
	// ReasonUnopened says that the bytes of a sealed live blob do not open.
	// Damaged bytes and a wrong key both cause it.
	ReasonUnopened DamageReason = "unopened"
)

// DamagedBlob names one live blob that the backup before a restore found
// missing or damaged, and did not copy. Size, MD5 and Sealed are the
// reference that the live row gives for the key, the same values that the
// SQL part of the backup holds. A restore of this backup matches its staged
// data against them, and it writes nothing to the key: the backup holds no
// copy of it. MD5 is lowercase hex.
type DamagedBlob struct {
	Key    string       `json:"key"`
	State  DamageState  `json:"state"`
	Reason DamageReason `json:"reason"`
	MD5    string       `json:"md5"`
	Size   int64        `json:"size"`
	Sealed bool         `json:"sealed,omitempty"`
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

// damagedOnce refuses a manifest that names a damaged key also among its
// blobs or its unverified keys. A key is in one list only: a restore copies
// the blobs and leaves the damaged keys as they are.
func damagedOnce(manifest Manifest) error {
	listed := make(map[string]bool, len(manifest.Blobs)+len(manifest.Unverified))

	for _, ref := range manifest.Blobs {
		listed[ref.Key] = true
	}

	for _, key := range manifest.Unverified {
		listed[key] = true
	}

	for i, entry := range manifest.Damaged {
		if listed[entry.Key] {
			return fmt.Errorf("damaged entry %d: the key is also in another list: %w", i, errDamagedEntry)
		}
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
		if entry.Reason != ReasonAbsent {
			return fmt.Errorf("%w: a missing entry has the reason %q", errDamagedEntry, ReasonAbsent)
		}
	case DamageMismatch:
		if !slices.Contains([]DamageReason{ReasonSize, ReasonChecksum, ReasonSealed, ReasonUnopened}, entry.Reason) {
			return fmt.Errorf("%w: unknown reason of a damaged entry", errDamagedEntry)
		}
	default:
		return fmt.Errorf("%w: unknown state", errDamagedEntry)
	}

	raw, err := hex.DecodeString(entry.MD5)
	if err != nil || len(raw) != blob.NewMD5().Size() || hex.EncodeToString(raw) != entry.MD5 || entry.Size < 0 {
		return fmt.Errorf("%w: an entry needs the size and the lowercase MD5 of the live row", errDamagedEntry)
	}

	return nil
}

// damagedRef returns the blob reference of entry, which validDamagedEntry
// checked.
func damagedRef(entry DamagedBlob) (BlobRef, error) {
	raw, err := hex.DecodeString(entry.MD5)
	if err != nil || len(raw) != blob.NewMD5().Size() {
		return BlobRef{MD5: [16]byte{}, Key: "", Size: 0, Sealed: false},
			fmt.Errorf("%w: MD5 of %q", errDamagedEntry, truncateKey(entry.Key))
	}

	var sum [16]byte

	copy(sum[:], raw)

	return BlobRef{MD5: sum, Key: entry.Key, Size: entry.Size, Sealed: entry.Sealed}, nil
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

// missingDamage returns the damage of the live key of ref with no object.
func missingDamage(ref BlobRef, cause error) *liveDamageError {
	return &liveDamageError{cause: cause, entry: damagedEntry(ref, DamageMissing, ReasonAbsent)}
}

// mismatchDamage returns the damage of the live bytes of ref that do not
// match their row, for reason.
func mismatchDamage(ref BlobRef, reason DamageReason, cause error) *liveDamageError {
	return &liveDamageError{cause: cause, entry: damagedEntry(ref, DamageMismatch, reason)}
}

// damagedEntry returns the entry of ref with state and reason, and the
// reference of the live row.
func damagedEntry(ref BlobRef, state DamageState, reason DamageReason) DamagedBlob {
	return DamagedBlob{
		Key: ref.Key, State: state, Reason: reason, MD5: hex.EncodeToString(ref.MD5[:]), Size: ref.Size,
		Sealed: ref.Sealed,
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

// MaxReportedBlobKeys bounds the keys that a restore names in its report and
// in its error text. The audit event and the counts give the total.
const MaxReportedBlobKeys = 10

// BlobFindings is what one backup found at the live blobs that its data
// names: the count of the keys that the data names, the count of those that
// the live store did not hold, the count of those whose bytes did not match
// their records, and the first keys of both, in key order.
type BlobFindings struct {
	Named   int      `json:"named"`
	Missing int      `json:"missing"`
	Damaged int      `json:"damaged"`
	Keys    []string `json:"keys"`
}

// Empty reports whether f names no missing and no damaged blob.
func (f BlobFindings) Empty() bool { return f.Missing == 0 && f.Damaged == 0 }

// findingsOf returns the findings of manifest. Every blob that its data names
// is in Blobs, in Damaged or in Unverified. A key that more than one list
// names counts once.
func findingsOf(manifest Manifest) BlobFindings {
	named := make(map[string]bool, len(manifest.Blobs)+len(manifest.Damaged)+len(manifest.Unverified))

	for _, ref := range manifest.Blobs {
		named[ref.Key] = true
	}

	for _, key := range manifest.Unverified {
		named[key] = true
	}

	for _, entry := range manifest.Damaged {
		named[entry.Key] = true
	}

	out := BlobFindings{
		Named:   len(named),
		Missing: 0, Damaged: 0, Keys: []string{},
	}

	for _, entry := range manifest.Damaged {
		if entry.State == DamageMissing {
			out.Missing++
		} else {
			out.Damaged++
		}

		if len(out.Keys) < MaxReportedBlobKeys {
			out.Keys = append(out.Keys, truncateKey(entry.Key))
		}
	}

	return out
}

// damagedKeys returns the first keys of the entries of manifest in state, in
// key order, up to MaxReportedBlobKeys.
func damagedKeys(manifest Manifest, state DamageState) []string {
	var out []string

	for _, entry := range manifest.Damaged {
		if entry.State == state && len(out) < MaxReportedBlobKeys {
			out = append(out, truncateKey(entry.Key))
		}
	}

	return out
}

// MissingBlobLimit is the bound on the live blobs that the backup before a
// restore may find missing. A restore stops before it changes live data when
// the count is above Count, or when the count is above Percent percent of the
// blobs that the live data names and also above PercentAbove. Many missing
// blobs point to a blob store setting that names another store, and not to
// lost blobs. PercentAbove lets a small installation restore over one
// missing blob.
type MissingBlobLimit struct {
	Count        int `json:"count"`
	Percent      int `json:"percent"`
	PercentAbove int `json:"percentAbove"`
}

// The values of the bound on missing live blobs. They are a choice of the
// product manager, close to the bound of other mass changes of the project:
// more than 10, or more than 20 percent when more than 1. No setting changes
// them.
const (
	missingBlobLimitCount        = 10
	missingBlobLimitPercent      = 20
	missingBlobLimitPercentAbove = 1
)

// EffectiveMissingBlobLimit returns the bound on missing live blobs that a
// restore applies.
func EffectiveMissingBlobLimit() MissingBlobLimit {
	return MissingBlobLimit{
		Count: missingBlobLimitCount, Percent: missingBlobLimitPercent, PercentAbove: missingBlobLimitPercentAbove,
	}
}

// Exceeded reports whether missing of named blobs is above the bound. A count
// that is negative, or above named, is above the bound: fail closed.
func (l MissingBlobLimit) Exceeded(missing, named int) bool {
	if missing < 0 || named < 0 || missing > named {
		return true
	}

	return missing > l.Count || (missing > l.PercentAbove && missing*100 > l.Percent*named)
}

// ErrLiveBlobsDamaged reports that the backup before a restore found live
// blobs whose bytes do not match their records. Blobs are write-once, so the
// restore never writes over a live blob: it stops before any change of the
// live data. It matches ErrCorrupt.
var ErrLiveBlobsDamaged = errors.New("live blobs do not match their records")

// ErrLiveBlobsMissing reports that the backup before a restore found more
// live blobs missing than MissingBlobLimit allows. The restore stops before
// any change of the live data. It matches ErrCorrupt.
var ErrLiveBlobsMissing = errors.New("too many live blobs are missing")

// LiveBlobsError is the refusal of a restore at the live blobs that the
// backup before it found. Its text names the counts, the first keys and the
// next step, and it is safe to store: each key is quoted.
type LiveBlobsError struct {
	// Sentinel is ErrLiveBlobsDamaged or ErrLiveBlobsMissing.
	Sentinel error
	Findings BlobFindings
	Limit    MissingBlobLimit
	// Keys are the first keys of the refused state, in key order.
	Keys []string
}

// Error returns the text for the operator.
func (e *LiveBlobsError) Error() string {
	quoted := make([]string, 0, len(e.Keys))
	for _, key := range e.Keys {
		quoted = append(quoted, strconv.Quote(key))
	}

	keys := strings.Join(quoted, ", ")

	if errors.Is(e.Sentinel, ErrLiveBlobsMissing) {
		return fmt.Sprintf("the backup before the restore found %d of %d live blobs missing, more than the limit "+
			"of %d blobs, or %d percent when more than %d, first %s. The restore changed no live data. Check the "+
			"blob store setting of the server with opendustpan store check --target blobs: %s: %s",
			e.Findings.Missing, e.Findings.Named, e.Limit.Count, e.Limit.Percent, e.Limit.PercentAbove, keys,
			ErrLiveBlobsMissing, ErrCorrupt)
	}

	return fmt.Sprintf("the backup before the restore found %d live blobs whose bytes do not match their "+
		"records or do not open: %s. A restore never writes over a live blob, so it changed no live data. "+
		"Put the good bytes back at these keys from a copy of the blob store, then run opendustpan restore "+
		"again. Sealed bytes that do not open can also come from a wrong key-encryption key: %s: %s",
		e.Findings.Damaged, keys, ErrLiveBlobsDamaged, ErrCorrupt)
}

// Is reports whether target is the sentinel of e or ErrCorrupt.
func (e *LiveBlobsError) Is(target error) bool {
	return target == ErrCorrupt || (e.Sentinel != nil && target == e.Sentinel)
}

// checkLiveFindings refuses the restore when the manifest of the backup
// before it names damaged live blobs, or more missing live blobs than limit
// allows. It returns nil, or a *LiveBlobsError.
func checkLiveFindings(manifest Manifest, limit MissingBlobLimit) error {
	return checkFindings(
		findingsOf(manifest), limit, damagedKeys(manifest, DamageMismatch), damagedKeys(manifest, DamageMissing),
	)
}

// checkFindings refuses the restore when found names a damaged live blob, or
// more missing live blobs than limit allows. The refusal names damaged or
// missing as its keys. It returns nil, or a *LiveBlobsError.
func checkFindings(found BlobFindings, limit MissingBlobLimit, damaged, missing []string) error {
	if found.Damaged > 0 {
		return &LiveBlobsError{Sentinel: ErrLiveBlobsDamaged, Findings: found, Limit: limit, Keys: damaged}
	}

	if limit.Exceeded(found.Missing, found.Named) {
		return &LiveBlobsError{Sentinel: ErrLiveBlobsMissing, Findings: found, Limit: limit, Keys: missing}
	}

	return nil
}
