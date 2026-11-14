package stack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DeltaInput is a checkpoint delta as a client submits it: the edits that
// turn the last saved deployment into the new one, with the SHA-256 hash of
// the result and the client's sequence number.
type DeltaInput struct {
	// SchemaVersion is the deployment format version, 1 or higher.
	SchemaVersion int
	// Sequence is the client's idempotency counter. The first save of an
	// update is always a full save with sequence 1; a delta carries the
	// next number.
	Sequence int64
	// Hash is the lowercase hex SHA-256 of the deployment that applying
	// Delta to the last saved deployment must produce.
	Hash string
	// Delta is the JSON list of text edits the CLI sends, one per changed
	// span, with byte offsets into the last saved deployment.
	Delta []byte
}

// SaveCheckpointDelta applies a checkpoint delta to the last saved
// checkpoint of the update and stores the result. It follows the same
// blob-first write path as SaveCheckpoint: the result goes to the blob
// store first, then the fenced SQL row names it.
//
// A delta is valid only when the update holds a checkpoint, the sequence
// number is the next one, every edit is inside the text, the edits do not
// overlap, the result fits the size limit, and the hash matches. A repeated
// sequence number with the same hash is a retry and succeeds without
// writing. Any other delta fails with ErrInvalidDelta and saves nothing,
// so the CLI falls back to a full save.
func (s *Updates) SaveCheckpointDelta(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, in DeltaInput,
) error {
	result, save, err := s.deltaSave(ctx, ref, id, tok, in)
	if err != nil {
		return err
	}

	if save == nil {
		return nil
	}

	// The CLI hashes the result it wants, so the hash proves nothing about its
	// shape. A result that the CLI cannot read fails like any invalid delta,
	// and the CLI then sends the whole deployment, which validate checks.
	if shapeErr := checkDeploymentShape(result, in.Sequence); shapeErr != nil {
		return fmt.Errorf("save checkpoint delta of update %s: %w: the result cannot be read", id, ErrInvalidDelta)
	}

	err = s.storeNewCheckpoint(ctx, checkpointSave{
		ref: ref, id: id, stackID: save.stackID, fence: save.fence, lease: save.lease,
		in: CheckpointInput{
			Features:      nil,
			Deployment:    result,
			SchemaVersion: in.SchemaVersion,
			Sequence:      in.Sequence,
			Invalid:       false,
		},
		digest: save.digest,
		prev:   save.prev,
		key:    save.key,
		origin: save.origin,
		blobs:  save.blobs,
	})
	if err != nil {
		return deltaCommitErr(id, err)
	}

	return nil
}

// deltaCommitErr maps a failed delta commit to the error its caller
// reports. A concurrent save of the same update wins the sequence the delta
// wanted, so the delta is no longer next: it fails with ErrInvalidDelta and
// saves nothing, like any other invalid delta. Any other failure passes
// through unchanged.
func deltaCommitErr(id UpdateID, err error) error {
	if errors.Is(err, ErrCheckpointConflict) || errors.Is(err, ErrStaleCheckpoint) {
		return fmt.Errorf("save checkpoint delta of update %s: %w", id, ErrInvalidDelta)
	}

	return err
}

// deltaCommit groups one delta save after the lease, sequence and content
// checks: the row parts with the blob key to write. A nil commit means the
// delta repeats the last save.
type deltaCommit struct {
	stackID ID
	fence   namespace.Fence
	lease   token.Digest
	digest  [sha256.Size]byte
	prev    *Checkpoint
	key     blob.Key
	// origin is the writer of the state version that the save publishes: the
	// one of the update.
	origin identity.StateOrigin
	blobs  *scopedBlobs
}

// deltaSave checks the lease and the validity rules of a delta and returns
// the result to store with its commit. It returns a nil commit for a retry
// of the last save. It saves nothing itself.
func (s *Updates) deltaSave(
	ctx context.Context, ref Ref, id UpdateID, tok token.Token, in DeltaInput,
) ([]byte, *deltaCommit, error) {
	u, ns, err := s.authorizedIn(ctx, ref, id, tok)
	if err != nil {
		return nil, nil, err
	}

	if inputErr := checkDeltaInput(u, in); inputErr != nil {
		return nil, nil, inputErr
	}

	st, prev, err := s.deltaBase(ctx, ref, id)
	if err != nil {
		return nil, nil, err
	}

	bl, err := s.blobs.scope(ns.ID, st)
	if err != nil {
		return nil, nil, fmt.Errorf("save checkpoint delta of update %s: %w", id, err)
	}

	want, err := decodeDeltaHash(in.Hash)
	if err != nil {
		return nil, nil, err
	}

	if retry, err := checkDeltaSequence(prev, in.Sequence, want); err != nil || retry {
		return nil, nil, err
	}

	pending := deltaPending{
		ref:     ref,
		id:      id,
		stackID: st,
		fence:   ns.Fence(),
		lease:   u.Lease.Digest,
		prev:    prev,
		in:      in,
		want:    want,
		origin:  u.Origin(),
		blobs:   bl,
	}

	return s.applyDelta(ctx, pending)
}

// checkDeltaInput rejects a delta on a preview and a delta without a schema
// version or sequence number.
func checkDeltaInput(u Update, in DeltaInput) error {
	if !u.Mutates() {
		return fmt.Errorf("%w: a preview does not save state", ErrWrongUpdateState)
	}

	if in.SchemaVersion < 1 {
		return fmt.Errorf("%w: deployment schema version %d", ErrInvalidDelta, in.SchemaVersion)
	}

	if in.Sequence <= 0 {
		return fmt.Errorf("%w: checkpoint sequence %d", ErrInvalidDelta, in.Sequence)
	}

	return nil
}

// deltaBase returns the stack and its previous checkpoint. A delta needs a
// checkpoint: the first save of an update is always full.
func (s *Updates) deltaBase(ctx context.Context, ref Ref, id UpdateID) (ID, *Checkpoint, error) {
	st, err := s.stacks.GetStack(ctx, ref)
	if err != nil {
		return "", nil, fmt.Errorf("save checkpoint delta of update %s: %w", id, err)
	}

	prev, err := s.previousCheckpoint(ctx, ref, id)
	if err != nil {
		return "", nil, fmt.Errorf("save checkpoint delta of update %s: %w", id, err)
	}

	if prev == nil {
		return "", nil, fmt.Errorf("%w: update has no checkpoint", ErrInvalidDelta)
	}

	return st.ID, prev, nil
}

// decodeDeltaHash decodes the hex SHA-256 a delta carries.
func decodeDeltaHash(hash string) ([]byte, error) {
	want, err := hex.DecodeString(hash)
	if err != nil || len(want) != sha256.Size {
		return nil, fmt.Errorf("%w: checkpoint hash %q", ErrInvalidDelta, hash)
	}

	return want, nil
}

// checkDeltaSequence checks the sequence number against the previous save.
// A repeated number with the same hash is a retry and reports done without
// an error; any other mismatch fails with ErrInvalidDelta.
func checkDeltaSequence(prev *Checkpoint, seq int64, want []byte) (bool, error) {
	if seq == prev.Sequence {
		if !bytes.Equal(want, prev.Digest[:]) {
			return false, fmt.Errorf("%w: sequence number %d", ErrInvalidDelta, seq)
		}

		return true, nil
	}

	if seq != prev.Sequence+1 {
		return false, fmt.Errorf(
			"%w: sequence number %d, want %d",
			ErrInvalidDelta,
			seq,
			prev.Sequence+1,
		)
	}

	return false, nil
}

// deltaPending groups one delta after its checks, so the apply step takes
// one argument next to the context.
type deltaPending struct {
	ref     Ref
	id      UpdateID
	stackID ID
	fence   namespace.Fence
	lease   token.Digest
	prev    *Checkpoint
	in      DeltaInput
	want    []byte
	origin  identity.StateOrigin
	blobs   *scopedBlobs
}

// applyDelta applies the delta to the previous checkpoint, checks the hash
// and returns the result with its commit.
func (s *Updates) applyDelta(ctx context.Context, pending deltaPending) ([]byte, *deltaCommit, error) {
	resolved, err := s.resolveCheckpoint(ctx, pending.blobs, *pending.prev)
	if err != nil {
		return nil, nil, fmt.Errorf("save checkpoint delta of update %s: %w", pending.id, err)
	}

	result, err := applyCheckpointDelta(resolved.Deployment, pending.in.Delta, s.maxCheckpoints)
	if err != nil {
		return nil, nil, err
	}

	digest := sha256.Sum256(result)

	if !bytes.Equal(digest[:], pending.want) {
		return nil, nil, fmt.Errorf("%w: checkpoint hash %q", ErrInvalidDelta, pending.in.Hash)
	}

	key, err := checkpointBlobKey(pending.blobs, pending.stackID, pending.id, pending.in.Sequence)
	if err != nil {
		return nil, nil, fmt.Errorf("save checkpoint delta of update %s: %w", pending.id, err)
	}

	return result, &deltaCommit{
		stackID: pending.stackID,
		fence:   pending.fence,
		lease:   pending.lease,
		digest:  digest,
		prev:    pending.prev,
		key:     key,
		origin:  pending.origin,
		blobs:   pending.blobs,
	}, nil
}

// deltaPoint is one end of a text edit span as the CLI sends it. Only the
// byte offset matters; the line and column stay unread.
type deltaPoint struct {
	Offset int `json:"offset"`
}

// deltaSpan is the range of the saved deployment one text edit replaces, in
// byte offsets.
type deltaSpan struct {
	Start deltaPoint `json:"start"`
	End   deltaPoint `json:"end"`
}

// deltaEdit is one text edit as the CLI sends it: the span to replace with
// the new text. The CLI marshals gotextdiff TextEdit values, so the field
// names arrive capitalized; encoding/json matches them either way.
type deltaEdit struct {
	Span    deltaSpan `json:"span"`
	NewText string    `json:"newText"`
}

// applyCheckpointDelta applies the JSON list of text edits in delta to base
// and returns the result. It applies the edits in order by byte offset,
// splicing the bytes without reading them. It fails with ErrInvalidDelta
// when delta is not a JSON list, when an edit starts before the previous
// one ends, when an edit reaches outside the text, or when the result is
// larger than limit.
func applyCheckpointDelta(base, delta []byte, limit int) ([]byte, error) {
	trimmed := bytes.TrimSpace(delta)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty deployment delta", ErrInvalidDelta)
	}

	var edits []deltaEdit

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&edits); err != nil {
		return nil, fmt.Errorf("%w: deployment delta: %w", ErrInvalidDelta, err)
	}

	if edits == nil {
		return nil, fmt.Errorf("%w: deployment delta is null", ErrInvalidDelta)
	}

	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: deployment delta holds extra data", ErrInvalidDelta)
	}

	out := make([]byte, 0, len(base))

	prevEnd := 0

	for i := range edits {
		start, end := edits[i].Span.Start.Offset, edits[i].Span.End.Offset

		if start < prevEnd || end < start || end > len(base) {
			return nil, fmt.Errorf("%w: edit %d outside the text", ErrInvalidDelta, i)
		}

		out = append(out, base[prevEnd:start]...)
		out = append(out, edits[i].NewText...)

		if len(out)+(len(base)-end) > limit {
			return nil, fmt.Errorf("%w: checkpoint over %d bytes", ErrInvalidDelta, limit)
		}

		prevEnd = end
	}

	out = append(out, base[prevEnd:]...)

	if len(out) > limit {
		return nil, fmt.Errorf("%w: checkpoint of %d bytes, limit %d", ErrInvalidDelta, len(out), limit)
	}

	return out, nil
}
