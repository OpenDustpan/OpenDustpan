package codec

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/json"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// WorkspaceRow is the column representation of a workspace. An unlocked
// workspace keeps empty lock columns and a zero locked-at time. A workspace
// without state keeps an empty current state version ID.
type WorkspaceRow struct {
	Namespace           string
	NamespaceID         string
	Name                string
	ID                  string
	LockID              string
	LockHolder          string
	LockReason          string
	CurrentStateVersion string
	Revision            int64
	LockedAtMs          int64
	CreatedAtMs         int64
	UpdatedAtMs         int64
	AllowForcedWrites   bool
	AllowUnlockedWrites bool
	// The record of the last released lock. A workspace without a record
	// keeps empty text columns and a zero time.
	LastReleaseLockID string
	LastReleaseHolder string
	LastReleaseBy     string
	LastReleaseKind   string
	LastReleaseAtMs   int64
}

// StateVersionRow is the column representation of a state version. The upload
// capability is a digest only. Unset times are 0 and a pending version keeps
// an empty content key and size 0. RestoredFrom is empty unless the version
// restores another one. SensitiveMarks holds the kept secret paths as JSON,
// or nil when the version keeps none.
type StateVersionRow struct {
	ID                string
	WorkspaceID       string
	Holder            string
	LockID            string
	Lineage           string
	Status            string
	ContentKey        string
	UploadDigest      []byte
	Outputs           []byte
	MD5               []byte
	Serial            int64
	Size              int64
	Force             bool
	Sealed            bool
	RestoredFrom      string
	FencedBy          string
	UnlockedWrite     bool
	SensitiveMarks    []byte
	CreatedAtMs       int64
	FinalizedAtMs     int64
	UploadExpiresAtMs int64
}

// ToWorkspaceRow converts a domain workspace into a row. The current state
// version ID is empty here; FinalizeStateVersion sets it with its own query.
func ToWorkspaceRow(w workspace.Workspace) WorkspaceRow {
	return WorkspaceRow{
		Namespace:           w.Ref.Namespace.String(),
		NamespaceID:         w.NamespaceID.String(),
		Name:                w.Ref.Name.String(),
		ID:                  w.ID.String(),
		LockID:              w.Lock.ID,
		LockHolder:          w.Lock.Holder,
		LockReason:          w.Lock.Reason,
		CurrentStateVersion: "",
		Revision:            w.Revision,
		LockedAtMs:          OptionalMillis(w.Lock.LockedAt),
		CreatedAtMs:         Millis(w.CreatedAt),
		UpdatedAtMs:         Millis(w.UpdatedAt),
		AllowForcedWrites:   w.Overrides.Forced,
		AllowUnlockedWrites: w.Overrides.Unlocked,
		LastReleaseLockID:   w.LastRelease.LockID,
		LastReleaseHolder:   w.LastRelease.Holder,
		LastReleaseBy:       w.LastRelease.By,
		LastReleaseKind:     string(w.LastRelease.Kind),
		LastReleaseAtMs:     OptionalMillis(w.LastRelease.At),
	}
}

// FromWorkspaceRow converts a row into a domain workspace.
func FromWorkspaceRow(row WorkspaceRow) workspace.Workspace {
	return workspace.Workspace{
		CreatedAt: FromMillis(row.CreatedAtMs),
		UpdatedAt: FromMillis(row.UpdatedAtMs),
		Lock: workspace.Lock{
			LockedAt: FromOptionalMillis(row.LockedAtMs),
			ID:       row.LockID,
			Holder:   row.LockHolder,
			Reason:   row.LockReason,
		},
		ID:          workspace.ID(row.ID),
		NamespaceID: namespace.ID(row.NamespaceID),
		Ref:         workspace.Ref{Namespace: namespace.Name(row.Namespace), Name: workspace.Name(row.Name)},
		Overrides:   namespace.Overrides{Forced: row.AllowForcedWrites, Unlocked: row.AllowUnlockedWrites},
		Revision:    row.Revision,
		LastRelease: releaseFromRow(row), Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// releaseFromRow returns the record of the last release of a workspace row.
// A kind that is not a release kind gives no record, so a row from another
// version or a changed row never matches a repeated request.
//
// Defense in depth: the CHECK of the column refuses such a kind at each
// write, and a restore refuses it before the copy. This check stays so that a
// row that skips both still reads as no record.
func releaseFromRow(row WorkspaceRow) workspace.Release {
	kind := workspace.ReleaseKind(row.LastReleaseKind)
	if !kind.Valid() {
		return workspace.Release{At: time.Time{}, LockID: "", Holder: "", By: "", Kind: ""}
	}

	return workspace.Release{
		At:     FromOptionalMillis(row.LastReleaseAtMs),
		LockID: row.LastReleaseLockID,
		Holder: row.LastReleaseHolder,
		By:     row.LastReleaseBy,
		Kind:   kind,
	}
}

// ToStateVersionRow converts a domain state version into a row.
func ToStateVersionRow(sv workspace.StateVersion) StateVersionRow {
	content := sv.Content.String()
	md5sum := append([]byte{}, sv.MD5[:]...)

	// The column is NOT NULL. A version with no upload capability, such as a
	// fence, stores an empty digest, which reads back as the zero digest.
	upload := []byte{}
	if !sv.Upload.IsZero() {
		upload = append([]byte{}, sv.Upload[:]...)
	}

	outputs := sv.Outputs
	if outputs == nil {
		// NOT NULL is not set on outputs, so nil binds as NULL either way.
		// Keep nil as nil so a round trip preserves the absence of outputs.
		outputs = nil
	}

	marks, err := MarshalSensitiveMarks(sv.SensitiveMarks)
	if err != nil {
		// The marks come from the server itself, so they always encode.
		// Still, never fail a write for them: no marks masks everything.
		marks = nil
	}

	return StateVersionRow{
		ID:                sv.ID.String(),
		WorkspaceID:       sv.Workspace.String(),
		Holder:            sv.Holder,
		LockID:            sv.LockID,
		Lineage:           sv.Lineage,
		Status:            string(sv.Status),
		ContentKey:        content,
		UploadDigest:      upload,
		Outputs:           outputs,
		MD5:               md5sum,
		Serial:            sv.Serial,
		Size:              sv.Size,
		Force:             sv.Force,
		Sealed:            sv.Sealed,
		RestoredFrom:      sv.RestoredFrom.String(),
		FencedBy:          sv.FenceBy,
		UnlockedWrite:     sv.UnlockedWrite,
		SensitiveMarks:    marks,
		CreatedAtMs:       Millis(sv.CreatedAt),
		FinalizedAtMs:     OptionalMillis(sv.FinalizedAt),
		UploadExpiresAtMs: Millis(sv.UploadExpiresAt),
	}
}

// MarshalSensitiveMarks renders marks as JSON for the column, or nil when
// the version keeps none, so an empty and a missing set read the same.
func MarshalSensitiveMarks(marks workspace.SensitiveMarks) ([]byte, error) {
	if len(marks) == 0 {
		return nil, nil
	}

	raw, err := json.Marshal(marks)
	if err != nil {
		return nil, fmt.Errorf("encode sensitive marks: %w", err)
	}

	return raw, nil
}

// decodeSensitiveMarks reads the kept secret paths of a state version. A
// NULL column reads as no marks. Corrupt JSON fails with ErrCorruptRow, as
// the stored data is corrupt.
func decodeSensitiveMarks(id string, raw []byte) (workspace.SensitiveMarks, error) {
	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // Nil without an error means the version keeps no marks.
	}

	var marks workspace.SensitiveMarks

	if err := json.Unmarshal(raw, &marks); err != nil {
		return nil, fmt.Errorf("%w: state version %s sensitive marks: %w", ErrCorruptRow, id, err)
	}

	return marks, nil
}

// FromStateVersionRow converts a row into a domain state version.
func FromStateVersionRow(row StateVersionRow) (workspace.StateVersion, error) {
	if len(row.MD5) != md5.Size {
		return workspace.StateVersion{}, fmt.Errorf(
			"%w: state version %s MD5 has %d bytes", ErrCorruptRow, row.ID, len(row.MD5),
		)
	}

	var sum [md5.Size]byte

	copy(sum[:], row.MD5)

	var upload token.Digest
	if len(row.UploadDigest) != 0 {
		var err error

		upload, err = DecodeDigest(row.UploadDigest)
		if err != nil {
			return workspace.StateVersion{}, fmt.Errorf("decode upload of state version %s: %w", row.ID, err)
		}
	}

	var content blob.Key
	if row.ContentKey != "" {
		var err error

		content, err = blob.ParseKey(row.ContentKey)
		if err != nil {
			return workspace.StateVersion{}, fmt.Errorf("decode content of state version %s: %w", row.ID, err)
		}
	}

	status := workspace.StateStatus(row.Status)
	switch status {
	case workspace.StatusPending, workspace.StatusFinalized:
	default:
		return workspace.StateVersion{}, fmt.Errorf("%w: unknown status %q of state version %s",
			ErrCorruptRow, row.Status, row.ID)
	}

	var finalizedAt time.Time
	if row.FinalizedAtMs != 0 {
		finalizedAt = FromMillis(row.FinalizedAtMs)
	}

	marks, err := decodeSensitiveMarks(row.ID, row.SensitiveMarks)
	if err != nil {
		return workspace.StateVersion{}, err
	}

	return workspace.StateVersion{
		CreatedAt:       FromMillis(row.CreatedAtMs),
		FinalizedAt:     finalizedAt,
		UploadExpiresAt: FromMillis(row.UploadExpiresAtMs),
		Upload:          upload,
		ID:              workspace.StateVersionID(row.ID),
		Workspace:       workspace.ID(row.WorkspaceID),
		Holder:          row.Holder,
		LockID:          row.LockID,
		Lineage:         row.Lineage,
		Status:          status,
		Content:         content,
		Outputs:         row.Outputs,
		MD5:             sum,
		Serial:          row.Serial,
		Size:            row.Size,
		Force:           row.Force,
		Sealed:          row.Sealed,
		RestoredFrom:    workspace.StateVersionID(row.RestoredFrom),
		FenceBy:         row.FencedBy, UnlockedWrite: row.UnlockedWrite,
		SensitiveMarks: marks, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}
