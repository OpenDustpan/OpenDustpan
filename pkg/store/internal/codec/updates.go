package codec

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/stack"
)

// ErrCorruptRow reports a stored value the codec cannot decode.
var ErrCorruptRow = errors.New("corrupt row")

// UpdateRow is the column representation of an update. Unset times are 0 and
// an absent lease digest is empty. The stack is its stable ID; the caller
// supplies the current reference. CheckpointBytes is the byte length of the
// saved deployment, read with the row, not stored with it.
type UpdateRow struct {
	StackID          string
	ID               string
	Kind             string
	Status           string
	LeaseDigest      []byte
	Version          int64
	Revision         int64
	LeaseExpiresAtMs int64
	CreatedAtMs      int64
	StartedAtMs      int64
	EndedAtMs        int64
	DryRun           bool
	CreatedBy        string
	RestoredFrom     int64
	CheckpointBytes  int64
}

// CheckpointRow is the column representation of a checkpoint. Features is a
// JSON array of strings. The stack is its stable ID; the caller supplies the
// current reference. ResourceCount holds the number of managed resources
// beyond the automatic stack resource, so the final delete checks it without
// loading the deployment. Rows written before the count hold NULL, which
// means not counted; new checkpoints always store the count.
type CheckpointRow struct {
	StackID       string
	UpdateID      string
	LeaseDigest   []byte
	Digest        []byte
	Features      []byte
	Deployment    []byte
	SchemaVersion int64
	Sequence      int64
	SavedAtMs     int64
	Invalid       bool
	ResourceCount int64
}

// OptionalMillis encodes t as Unix milliseconds, and the zero time as 0.
func OptionalMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return Millis(t)
}

// FromOptionalMillis decodes OptionalMillis.
func FromOptionalMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return FromMillis(ms)
}

// ToUpdateRow converts a domain update into a row for the stack with stackID.
func ToUpdateRow(u stack.Update, stackID stack.ID) UpdateRow {
	return UpdateRow{
		StackID:          stackID.String(),
		ID:               u.ID.String(),
		Kind:             string(u.Kind),
		Status:           string(u.Status),
		LeaseDigest:      EncodeDigest(u.Lease.Digest),
		Version:          u.Version,
		Revision:         u.Revision,
		LeaseExpiresAtMs: OptionalMillis(u.Lease.ExpiresAt),
		CreatedAtMs:      Millis(u.CreatedAt),
		StartedAtMs:      OptionalMillis(u.StartedAt),
		EndedAtMs:        OptionalMillis(u.EndedAt),
		DryRun:           u.DryRun,
		CreatedBy:        u.CreatedBy,
		RestoredFrom:     u.RestoredFrom,
		CheckpointBytes:  u.CheckpointBytes,
	}
}

// FromUpdateRow converts a row into a domain update of ref.
func FromUpdateRow(row UpdateRow, ref stack.Ref) (stack.Update, error) {
	kind, err := stack.ParseUpdateKind(row.Kind)
	if err != nil {
		return stack.Update{}, fmt.Errorf("decode update %s: %w", row.ID, err)
	}

	status, err := parseStatus(row.Status)
	if err != nil {
		return stack.Update{}, fmt.Errorf("decode update %s: %w", row.ID, err)
	}

	lease, err := DecodeDigest(row.LeaseDigest)
	if err != nil {
		return stack.Update{}, fmt.Errorf("decode lease of update %s: %w", row.ID, err)
	}

	return stack.Update{
		CreatedAt: FromMillis(row.CreatedAtMs),
		StartedAt: FromOptionalMillis(row.StartedAtMs),
		EndedAt:   FromOptionalMillis(row.EndedAtMs),
		Lease:     stack.Lease{ExpiresAt: FromOptionalMillis(row.LeaseExpiresAtMs), Digest: lease},
		ID:        stack.UpdateID(row.ID),
		Kind:      kind,
		Status:    status,
		Ref:       ref,
		DryRun:    row.DryRun,
		Version:   row.Version,
		Revision:  row.Revision,
		// The creator and the restore source travel on the row; the size
		// travels with it from the checkpoint join.
		CreatedBy:       row.CreatedBy,
		RestoredFrom:    row.RestoredFrom,
		CheckpointBytes: row.CheckpointBytes,
	}, nil
}

// FromUpdateRows converts rows into domain updates of ref.
func FromUpdateRows(rows []UpdateRow, ref stack.Ref) ([]stack.Update, error) {
	out := make([]stack.Update, 0, len(rows))

	for i := range rows {
		u, err := FromUpdateRow(rows[i], ref)
		if err != nil {
			return nil, err
		}

		out = append(out, u)
	}

	return out, nil
}

// ToCheckpointRow converts a domain checkpoint into a row for the stack with stackID.
func ToCheckpointRow(cp stack.Checkpoint, stackID stack.ID) (CheckpointRow, error) {
	features := cp.Features
	if features == nil {
		features = []string{}
	}

	encoded, err := json.Marshal(features)
	if err != nil {
		return CheckpointRow{}, fmt.Errorf("encode checkpoint features: %w", err)
	}

	deployment := cp.Deployment
	if deployment == nil {
		// NOT NULL columns: a nil slice would bind as NULL.
		deployment = []byte{}
	}

	return CheckpointRow{
		StackID:       stackID.String(),
		UpdateID:      cp.Update.String(),
		LeaseDigest:   EncodeDigest(cp.Lease),
		Digest:        cp.Digest[:],
		Features:      encoded,
		Deployment:    deployment,
		SchemaVersion: int64(cp.SchemaVersion),
		Sequence:      cp.Sequence,
		SavedAtMs:     Millis(cp.SavedAt),
		Invalid:       cp.Invalid,
		ResourceCount: stack.CountManagedResources(cp.Deployment),
	}, nil
}

// FromCheckpointRow converts a row into a domain checkpoint of ref.
func FromCheckpointRow(row CheckpointRow, ref stack.Ref) (stack.Checkpoint, error) {
	var features []string
	if err := json.Unmarshal(row.Features, &features); err != nil {
		return stack.Checkpoint{}, fmt.Errorf("decode checkpoint features of update %s: %w", row.UpdateID, err)
	}

	if len(features) == 0 {
		features = nil
	}

	lease, err := DecodeDigest(row.LeaseDigest)
	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("decode checkpoint lease of update %s: %w", row.UpdateID, err)
	}

	if len(row.Digest) != sha256.Size {
		return stack.Checkpoint{}, fmt.Errorf(
			"%w: checkpoint digest of update %s has %d bytes", ErrCorruptRow, row.UpdateID, len(row.Digest),
		)
	}

	return stack.Checkpoint{
		SavedAt:       FromMillis(row.SavedAtMs),
		Features:      features,
		Deployment:    row.Deployment,
		Update:        stack.UpdateID(row.UpdateID),
		Ref:           ref,
		Lease:         lease,
		SchemaVersion: int(row.SchemaVersion),
		Sequence:      row.Sequence,
		Digest:        [sha256.Size]byte(row.Digest),
		Invalid:       row.Invalid,
	}, nil
}

// EncodeDigest encodes a lease digest; the zero digest encodes as empty.
func EncodeDigest(d token.Digest) []byte {
	if d.IsZero() {
		return []byte{}
	}

	return d[:]
}

// DecodeDigest decodes EncodeDigest.
func DecodeDigest(b []byte) (token.Digest, error) {
	switch len(b) {
	case 0:
		return token.Digest{}, nil
	case len(token.Digest{}):
		return token.Digest(b), nil
	default:
		return token.Digest{}, fmt.Errorf("%w: digest of %d bytes, want %d", ErrCorruptRow, len(b), len(token.Digest{}))
	}
}

func parseStatus(s string) (stack.UpdateStatus, error) {
	switch st := stack.UpdateStatus(s); st {
	case stack.StatusNotStarted, stack.StatusRunning, stack.StatusSucceeded, stack.StatusFailed, stack.StatusCanceled:
		return st, nil
	default:
		return "", fmt.Errorf("%w: unknown status %q", ErrCorruptRow, s)
	}
}
