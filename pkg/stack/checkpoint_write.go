package stack

import (
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// checkpointWrite is one write of a checkpoint blob and of the fenced row
// that names it. An ordinary save and a journal build both store through
// writeCheckpoint, so the protocol lives in one place: put the blob under a
// key that is unique to this attempt, then commit the row. The row decides
// which write wins. A row never names a missing blob, and a crash between the
// blob write and the commit leaves a blob that no row names, which the sweep
// collects. No write replaces the blob of another attempt, so the protocol
// does not rely on the store to refuse a second write of one key.
type checkpointWrite struct {
	// op names the write in error texts, for example "save checkpoint".
	op  string
	ref Ref
	id  UpdateID
	// blobs is the scoped blob access of the namespace of the write.
	blobs      *scopedBlobs
	stackID    ID
	key        blob.Key
	deployment []byte
	// commit writes the row that names the blob under the key, with the size
	// and MD5 of the stored bytes and the start of the attempt, which the
	// store checks against the attempt deadline. known is true when the row of
	// the update already holds the same sequence and digest, so the row keeps
	// the blob of the first write. Its error is already wrapped for the caller.
	commit func(ctx context.Context, size int64, sum [md5.Size]byte, started time.Time) (known bool, err error)
}

// writeCheckpoint puts the blob of w, then commits its row. When the row
// decides against this write, the blob of this attempt is garbage and the
// write deletes it. No error text names the key.
//
// The attempt starts before the blob write. The store refuses a commit after
// the attempt deadline with blob.ErrAttemptExpired, so the sweep, which waits
// longer than the deadline, never deletes a blob that a later commit names.
func (s *Updates) writeCheckpoint(ctx context.Context, w checkpointWrite) error {
	started := s.clock()

	size, sum, err := s.putCheckpointBlob(ctx, w.blobs, w.key, w.deployment)
	if err != nil {
		return w.unavailable(ctx)
	}

	known, err := w.commit(ctx, size, sum, started)

	switch {
	case known:
		s.discardAfterReplay(ctx, w)
	case errors.Is(err, ErrCheckpointConflict), errors.Is(err, ErrStaleCheckpoint),
		errors.Is(err, blob.ErrAttemptExpired):
		s.discardOwnBlob(ctx, w)
	default:
	}

	return err
}

// discardAfterReplay deletes the blob of the attempt of w after the row
// answered known. It first reads the row, and it keeps the blob when the row
// names it.
//
// Defense in depth: the store keeps the key of the first write on a replay,
// so the row does not name this attempt's key. This read stays so that a
// store that answers known for the key of this very attempt cannot make the
// write delete a blob that a committed row names. It does not rely on the
// store.
func (s *Updates) discardAfterReplay(ctx context.Context, w checkpointWrite) {
	current, err := s.checkpoints.CheckpointByUpdate(ctx, w.ref, w.id)
	if err != nil || current.BlobKey == w.key {
		return
	}

	s.discardOwnBlob(ctx, w)
}

// discardOwnBlob deletes the blob of the attempt of w after the row decided
// against it. It deletes only the key of this attempt and never lists or
// guesses another key. The delete is best effort: a failure leaves the blob
// for the sweep.
func (s *Updates) discardOwnBlob(ctx context.Context, w checkpointWrite) {
	own := w.blobs.narrow(w.blobs.updatePrefix(w.stackID, w.id))

	if err := s.deleteBlobRetried(ctx, own, w.key); err != nil {
		s.logger.WarnContext(ctx, "delete checkpoint blob of a refused save", slog.String("update", w.id.String()))
	}
}

// unavailable returns the error of a blob access that failed: the error of
// ctx when it ended, else ErrCheckpointUnavailable. The blob error itself is
// dropped, because its text can name the key.
func (w checkpointWrite) unavailable(ctx context.Context) error {
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("%s of update %s: %w", w.op, w.id, cerr)
	}

	return fmt.Errorf("%s of update %s: %w", w.op, w.id, ErrCheckpointUnavailable)
}
