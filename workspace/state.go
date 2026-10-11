package workspace

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/internal/token"
)

// Sentinel errors of state versions.
var (
	// ErrNoState reports a workspace that has no state yet.
	ErrNoState = errors.New("workspace has no state")
	// ErrStateVersionNotFound reports that a state version does not exist.
	ErrStateVersionNotFound = errors.New("state version not found")
	// ErrInvalidState reports malformed state metadata: lineage, serial or MD5.
	ErrInvalidState = errors.New("invalid state version")
	// ErrStaleSerial reports a serial not higher than the current state's.
	ErrStaleSerial = errors.New("state serial is not newer than the current state")
	// ErrLineageMismatch reports state from another lineage than the current state.
	ErrLineageMismatch = errors.New("state lineage differs from the current state")
	// ErrChecksumMismatch reports uploaded bytes whose MD5 differs from the declared one.
	ErrChecksumMismatch = errors.New("uploaded state does not match its declared MD5")
	// ErrUploadDenied reports an upload capability that is unknown, used or expired.
	ErrUploadDenied = errors.New("state upload not allowed")
	// ErrTooLarge reports state over the size limit.
	ErrTooLarge = errors.New("state too large")
)

const (
	maxLineageLength = 100
	maxOutputsBytes  = 16 << 20
)

// StateVersionID identifies a state version ("sv-...").
type StateVersionID string

// String returns the ID as a plain string.
func (id StateVersionID) String() string {
	return string(id)
}

// StateStatus is where a state version is in its upload.
type StateStatus string

// State version statuses.
const (
	// StatusPending is a version whose content has not been uploaded yet.
	StatusPending StateStatus = "pending"
	// StatusFinalized is a version whose content was uploaded and verified.
	StatusFinalized StateStatus = "finalized"
)

const (
	// DefaultStatePageSize is the page size when the caller passes a
	// non-positive limit. It matches the default page size of the TFE list,
	// so an unpaged call reads what the OpenTofu client expects.
	DefaultStatePageSize = 20
	// MaxStatePageSize caps the page size of a state version list.
	MaxStatePageSize = 100
)

// Page selects a window of a newest-first list.
type Page struct {
	Limit  int
	Offset int
}

// StateVersion is one immutable state of a workspace. The content lives in
// the blob port; this is its metadata.
type StateVersion struct {
	CreatedAt       time.Time
	FinalizedAt     time.Time
	UploadExpiresAt time.Time
	// Upload is the digest of the single-use capability that may upload the content.
	Upload    token.Digest
	ID        StateVersionID
	Workspace ID
	// Holder is who held the workspace lock when the version was declared.
	Holder string
	// LockID is the lock acquisition the version was declared under.
	// Finalizing requires that the workspace is still held by it.
	LockID  string
	Lineage string
	Status  StateStatus
	// Content is the blob holding the state, set when the upload finalizes.
	// Every upload attempt writes its own key, so a failed or losing attempt
	// can never remove content another attempt made current.
	Content blob.Key
	// Outputs is the client's JSON rendering of the root outputs (the TFE
	// json-state-outputs attribute), kept opaque. It is the only source of
	// outputs the server has, because state itself may be encrypted.
	Outputs []byte
	MD5     [md5.Size]byte
	Serial  int64
	Size    int64
	// Force skips the lineage and serial checks, as `tofu state push -force`
	// does. It never skips the lock.
	Force bool
	// RestoredFrom is the state version this version was restored from. It is
	// empty when the version is not a restore.
	RestoredFrom StateVersionID
}

// NewState is the metadata a client declares before uploading state.
type NewState struct {
	Lineage string
	// MD5 is the hex MD5 of the content to be uploaded.
	MD5 string
	// Outputs is the client's JSON rendering of the root outputs, if it sent one.
	Outputs []byte
	Serial  int64
	Force   bool
}

// parse validates the declared metadata.
func (n NewState) parse() ([md5.Size]byte, error) {
	var sum [md5.Size]byte

	switch {
	case n.Lineage == "", len(n.Lineage) > maxLineageLength:
		return sum, fmt.Errorf("%w: lineage %q", ErrInvalidState, n.Lineage)
	case n.Serial < 0:
		return sum, fmt.Errorf("%w: serial %d", ErrInvalidState, n.Serial)
	case len(n.Outputs) > maxOutputsBytes:
		return sum, fmt.Errorf("%w: outputs of %d bytes, limit %d", ErrTooLarge, len(n.Outputs), maxOutputsBytes)
	}

	raw, err := hex.DecodeString(n.MD5)
	if err != nil || len(raw) != md5.Size {
		return sum, fmt.Errorf("%w: md5 %q", ErrInvalidState, n.MD5)
	}

	copy(sum[:], raw)

	return sum, nil
}

// follows checks that a new state may follow current: same lineage and a
// higher serial, unless forced.
func (n NewState) follows(current StateVersion) error {
	switch {
	case n.Force:
		return nil
	case n.Lineage != current.Lineage:
		return fmt.Errorf("%w: %q, current %q", ErrLineageMismatch, n.Lineage, current.Lineage)
	case n.Serial <= current.Serial:
		return fmt.Errorf("%w: %d, current %d", ErrStaleSerial, n.Serial, current.Serial)
	default:
		return nil
	}
}

// retriedBy reports whether tok is the capability that already finalized sv:
// a client retrying an upload whose response it never received.
func (sv StateVersion) retriedBy(tok token.Token) bool {
	return sv.Status == StatusFinalized && sv.Upload.Matches(tok)
}

// accepts reports whether tok may upload content for sv at now.
func (sv StateVersion) accepts(tok token.Token, now time.Time) error {
	switch {
	case sv.Status != StatusPending:
		return fmt.Errorf("%w: state version %s is %s", ErrUploadDenied, sv.ID, sv.Status)
	case !sv.Upload.Matches(tok):
		return fmt.Errorf("%w: wrong capability for state version %s", ErrUploadDenied, sv.ID)
	case !now.Before(sv.UploadExpiresAt):
		return fmt.Errorf("%w: capability for state version %s expired", ErrUploadDenied, sv.ID)
	default:
		return nil
	}
}
