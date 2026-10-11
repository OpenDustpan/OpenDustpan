package codec

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// WorkspaceRow is the column representation of a workspace. An unlocked
// workspace keeps empty lock columns and a zero locked-at time. A workspace
// without state keeps an empty current state version ID.
type WorkspaceRow struct {
	Namespace           string
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
}

// StateVersionRow is the column representation of a state version. The upload
// capability is a digest only. Unset times are 0 and a pending version keeps
// an empty content key and size 0. RestoredFrom is empty unless the version
// restores another one.
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
	RestoredFrom      string
	CreatedAtMs       int64
	FinalizedAtMs     int64
	UploadExpiresAtMs int64
}

// ToWorkspaceRow converts a domain workspace into a row. The current state
// version ID is empty here; FinalizeStateVersion sets it with its own query.
func ToWorkspaceRow(w workspace.Workspace) WorkspaceRow {
	return WorkspaceRow{
		Namespace:           w.Ref.Namespace.String(),
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
		ID:       workspace.ID(row.ID),
		Ref:      workspace.Ref{Namespace: namespace.Name(row.Namespace), Name: workspace.Name(row.Name)},
		Revision: row.Revision,
	}
}

// ToStateVersionRow converts a domain state version into a row.
func ToStateVersionRow(sv workspace.StateVersion) StateVersionRow {
	content := sv.Content.String()
	md5sum := append([]byte{}, sv.MD5[:]...)

	var upload []byte
	if !sv.Upload.IsZero() {
		upload = append([]byte{}, sv.Upload[:]...)
	}

	outputs := sv.Outputs
	if outputs == nil {
		// NOT NULL is not set on outputs, so nil binds as NULL either way.
		// Keep nil as nil so a round trip preserves the absence of outputs.
		outputs = nil
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
		RestoredFrom:      sv.RestoredFrom.String(),
		CreatedAtMs:       Millis(sv.CreatedAt),
		FinalizedAtMs:     OptionalMillis(sv.FinalizedAt),
		UploadExpiresAtMs: Millis(sv.UploadExpiresAt),
	}
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
		RestoredFrom:    workspace.StateVersionID(row.RestoredFrom),
	}, nil
}
