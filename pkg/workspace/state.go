package workspace

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
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
	// ErrStoredContentMismatch reports stored state whose plain bytes do not
	// match the size and MD5 of its state version row.
	ErrStoredContentMismatch = errors.New("stored state does not match its recorded size and MD5")
	// ErrUploadDenied reports an upload capability that is unknown, used or expired.
	ErrUploadDenied = errors.New("state upload not allowed")
	// ErrAlreadyRestored reports a restored version whose source the current
	// state already restores for the same holder: the restore is a duplicate.
	ErrAlreadyRestored = errors.New("state version is already restored")
	// ErrTooManyPending reports a workspace holding the maximum pending
	// state versions: declare no new version until an old one finalizes or
	// expires past the sweep grace period.
	ErrTooManyPending = errors.New("too many pending state versions")
	// ErrTooLarge reports state over the size limit.
	ErrTooLarge = errors.New("state too large")
	// ErrSealedContent reports sealed content that does not open: cut,
	// reordered, extended or tampered. It deliberately says no more: the
	// error holds no key, scope or content.
	ErrSealedContent = errors.New("cannot open sealed state")
	// ErrUnsealedState reports a state version that is not sealed. Every
	// stored version is sealed, so an import with a plain row fails.
	ErrUnsealedState = errors.New("state version is not sealed")
	// ErrClientEncrypted reports a state version the client encrypted: the
	// server keeps the envelope but cannot read the resources inside it. The
	// resource routes answer 409, while the outputs list still works. It is a
	// fact of the state. The checker of output reads refuses such a read with
	// outputshare.ErrClientEncrypted, a denial of one read; an adapter gives
	// both the same error code.
	ErrClientEncrypted = errors.New("state is encrypted-by-client")
	// ErrResourceNotFound reports that a state holds no resource with the
	// given address.
	ErrResourceNotFound = errors.New("resource not found")
)

// errTooLargeBody reports a body over the state size limit. size is the
// number of bytes read, which is limit+1 when the read stops at the limit,
// so the body is at least that large.
func errTooLargeBody(what string, size, limit int64) error {
	return fmt.Errorf(
		"%w: %s is at least %d bytes, over the limit of %d bytes: set --max-state-bytes to raise the limit",
		ErrTooLarge,
		what,
		size,
		limit,
	)
}

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
	// Content names the latest upload attempt while pending, and the state
	// once the upload finalizes. Every upload attempt writes its own key,
	// so a failed or losing attempt can never remove content another
	// attempt made current.
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
	// UnlockedWrite marks a version declared without the lock, under the
	// override of ADR 44 decision 5. Its finalize does not check the lock.
	UnlockedWrite bool
	// Sealed reports that the content blob and the outputs document are
	// sealed with the data key of the workspace scope. Every stored
	// version is sealed.
	Sealed bool
	// SensitiveMarks holds the secret paths of the version, by resource
	// instance address: each entry names the JSON Pointer paths, relative to
	// the attributes of that instance, that the json-state rendering of the
	// version marked sensitive (ADR 41 decisions 8 and 9). It holds no value,
	// only paths. It is nil for versions stored before the server kept
	// marks, and for versions whose rendering marked nothing.
	SensitiveMarks SensitiveMarks
	// RestoredFrom is the state version this version was restored from. It is
	// empty when the version is not a restore.
	RestoredFrom StateVersionID
	// FenceBy is the subject of the principal that forced the unlock whose
	// fence this version is (ADR 44, decision 1). It is empty when the version
	// is not a fence.
	FenceBy string
	// Origin names the job whose token wrote the version, and the commit it
	// wrote. The store records it with the declare and uses it when the
	// version finalizes; a read gives it as zero.
	Origin identity.StateOrigin
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

// checkUploadCapability reports whether tok may upload content for sv at now.
// A retry with the capability that finalized the version passes. It reads no
// content and writes nothing.
func (sv StateVersion) checkUploadCapability(tok token.Token, now time.Time) error {
	if sv.retriedBy(tok) {
		return nil
	}

	return sv.accepts(tok, now)
}

// checkRenderingCapability reports whether tok may upload the JSON rendering
// of sv at now. A finalized version with the same capability still passes. It
// reads no content and writes nothing.
func (sv StateVersion) checkRenderingCapability(tok token.Token, now time.Time) error {
	if !sv.Upload.Matches(tok) {
		return fmt.Errorf("%w: wrong capability for state version %s", ErrUploadDenied, sv.ID)
	}

	if !now.Before(sv.UploadExpiresAt) {
		return fmt.Errorf("%w: capability for state version %s expired", ErrUploadDenied, sv.ID)
	}

	return nil
}

// accepts reports whether tok may upload content for sv at now.
func (sv StateVersion) accepts(tok token.Token, now time.Time) error {
	switch {
	case sv.Status != StatusPending:
		return fmt.Errorf("%w: state version %s is %s", ErrUploadDenied, sv.ID, sv.Status)
	case sv.Upload == (token.Digest{}):
		// A streamed version's placeholder holds no capability (ADR 49, decision 7).
		return fmt.Errorf("%w: state version %s takes no upload", ErrUploadDenied, sv.ID)
	case !sv.Upload.Matches(tok):
		return fmt.Errorf("%w: wrong capability for state version %s", ErrUploadDenied, sv.ID)
	case !now.Before(sv.UploadExpiresAt):
		return fmt.Errorf("%w: capability for state version %s expired", ErrUploadDenied, sv.ID)
	default:
		return nil
	}
}
