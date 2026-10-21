package stack

import (
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/blob"
)

// checkpointWrite is one write of a checkpoint blob and of the fenced row
// that names it. An ordinary save and a journal build both store through
// writeCheckpoint, so the crash-retry protocol lives in one place: put the
// blob, adopt a blob that a first attempt left under the key, then commit the
// row. A row never names a missing blob, and a crash between the blob write
// and the commit never blocks the retry.
type checkpointWrite struct {
	// op names the write in error texts, for example "save checkpoint".
	op string
	id UpdateID
	// blobs is the scoped blob access of the namespace of the write.
	blobs      *scopedBlobs
	key        blob.Key
	deployment []byte
	// other answers a stored blob under the key whose bytes differ from the
	// bytes of this write. gave is the stored gzip bytes with their size and
	// MD5, or nil when the stored blob is over the size limit.
	other func(ctx context.Context, gave []byte, size int64, sum [md5.Size]byte) error
	// commit writes the row that names the blob under the key, with the size
	// and MD5 of the stored bytes. Its error is already wrapped for the
	// caller.
	commit func(ctx context.Context, size int64, sum [md5.Size]byte) error
}

// writeCheckpoint puts the blob of w, then commits its row. A blob that is
// already under the key belongs to a first attempt whose commit never
// landed: the retry adopts it when it holds the same bytes, and gives any
// other blob to w.other. No error text names the key.
func (s *Updates) writeCheckpoint(ctx context.Context, w checkpointWrite) error {
	size, sum, err := s.putCheckpointBlob(ctx, w.blobs, w.key, w.deployment)
	if err != nil {
		if errors.Is(err, blob.ErrExists) {
			return s.adoptCheckpoint(ctx, w, size, sum)
		}

		return w.unavailable(ctx)
	}

	return w.commit(ctx, size, sum)
}

// adoptCheckpoint answers a blob write that met an existing key: the first
// attempt wrote it but never committed its row. A blob with the same bytes
// commits the row and succeeds.
//
// size with sum are the size and MD5 of the bytes this write tried to
// store, returned with the ErrExists error. When the stored bytes match
// them, the retry adopts without decompressing anything: gzip decodes
// deterministically, so equal stored bytes mean equal content. That fast path
// hashes the compressed bytes once, instead of paying a gunzip with two hash
// passes over a large checkpoint. The same binary compresses the same
// content to the same bytes, so stored bytes that differ hold other content
// for an ordinary save. Across Go releases the compressed bytes may differ.
// During a rolling upgrade, a retried save with a sequence number whose
// first attempt ran on a binary of another release can then get
// ErrConcurrentModification instead of an adopt. A retried save without a
// sequence number mints the next key and succeeds.
func (s *Updates) adoptCheckpoint(ctx context.Context, w checkpointWrite, size int64, sum [md5.Size]byte) error {
	raw, err := w.blobs.Get(ctx, w.key)
	if err != nil {
		return w.unavailable(ctx)
	}

	gave, err := readStoredBlob(raw, s.storedLimit())
	if err != nil {
		if errors.Is(err, ErrCheckpointMismatch) {
			return w.other(ctx, nil, 0, [md5.Size]byte{})
		}

		return w.unavailable(ctx)
	}

	if storedSize, storedSum := storedAttrs(gave); storedSize != size || storedSum != sum {
		return w.other(ctx, gave, storedSize, storedSum)
	}

	return w.commit(ctx, size, sum)
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
