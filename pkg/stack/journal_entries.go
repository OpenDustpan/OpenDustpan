package stack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrJournalConflict is returned when a batch holds a sequence ID that is stored
// with different content, or when a batch is only partly stored (ADR 43, decision
// 6.3). The server records the refusal and stores nothing from the batch.
var ErrJournalConflict = errors.New("journal batch differs from the batch stored with the same sequence")

// ErrJournalKey is returned when a row names a blob key that does not belong to
// its stack and update. The caller builds the key, so this is a bug in the caller.
var ErrJournalKey = errors.New("journal blob key does not belong to the update")

// ErrJournalBatch is returned for a batch body that has no entries, an entry with a
// sequence ID of 0 or less, or a sequence ID that the batch holds twice.
var ErrJournalBatch = errors.New("journal batch is malformed")

// JournalBatch is one batch that a journaled update sends (ADR 43, decision 6.1).
// Content is the body as received. The store keeps it in a blob under a key that
// holds the lowest sequence ID of the batch.
type JournalBatch struct {
	Ref     Ref
	StackID ID
	Update  UpdateID
	// Lease is the digest of the lease that authorized the write.
	Lease   token.Digest
	Content []byte
}

// JournalRow is the row of one stored batch: the blob that holds it, and the
// parts that the retry check and the replay read (ADR 43, decision 6.1).
type JournalRow struct {
	Ref          Ref
	Update       UpdateID
	Lease        token.Digest
	BlobKey      blob.Key
	SHA256       [sha256.Size]byte
	Size         int64
	SequenceIDs  []int64
	OperationIDs []int64
}

// JournalStore persists the rows of journal batches.
type JournalStore interface {
	// PutJournalRow stores the row of one batch in the same transaction as
	// checking that the update is still running under row.Lease; otherwise it
	// returns ErrWrongUpdateState. It also checks the fence. The arrival number is
	// the next number of the update, given in that transaction. When every
	// sequence ID of the row is stored with the same SHA-256, the batch is a retry:
	// it returns retried true and writes nothing. A sequence ID stored with a
	// different SHA-256, or a batch that is only partly stored, returns
	// ErrJournalConflict. The blob must already be in the blob store.
	PutJournalRow(ctx context.Context, row JournalRow, fence namespace.Fence) (retried bool, err error)
}

// journalBody is the part of a journal batch body that the blob key and the
// retry check read. The wire format is the batch of the Pulumi CLI (ADR 43,
// decision 8.1).
type journalBody struct {
	Entries []journalEntryIDs `json:"entries"`
}

// journalEntryIDs holds the IDs of one entry.
type journalEntryIDs struct {
	SequenceID  int64 `json:"sequenceID"`
	OperationID int64 `json:"operationID"`
}

// JournalEntries writes journal batches: the blob first, then the row in a fenced
// transaction (ADR 43, decision 6.1). It holds no state of its own.
type JournalEntries struct {
	blobs blob.Store
	rows  JournalStore
}

// NewJournalEntries returns a writer over the blob store and the row store.
func NewJournalEntries(blobs blob.Store, rows JournalStore) *JournalEntries {
	return &JournalEntries{blobs: blobs, rows: rows}
}

// Append stores one batch. It returns nil when the batch is stored, or when it is
// a retry of batch content that is already stored (ADR 43, decisions 6.2 and 6.3).
// It returns ErrJournalConflict when the batch differs from what is stored, and
// ErrWrongUpdateState or namespace.ErrFenced when the write is refused. A refusal
// deletes the blob that this call wrote, as a previous save does. Any other
// failure keeps the blob for the sweep.
func (j *JournalEntries) Append(ctx context.Context, batch JournalBatch, fence namespace.Fence) error {
	ids, err := batchIDs(batch.Content)
	if err != nil {
		return err
	}

	// An empty batch is valid: the CLI sends {} when it has no entries (the
	// upstream field is omitempty). It writes no blob and no row.
	if len(ids.sequences) == 0 {
		return nil
	}

	sum := sha256.Sum256(batch.Content)
	key := journalKey(batch.StackID, batch.Update, lowest(ids.sequences))

	created, err := j.putBlob(ctx, key, batch.Content, sum)
	if err != nil {
		return err
	}

	row := JournalRow{
		Ref:          batch.Ref,
		Update:       batch.Update,
		Lease:        batch.Lease,
		BlobKey:      key,
		SHA256:       sum,
		Size:         int64(len(batch.Content)),
		SequenceIDs:  ids.sequences,
		OperationIDs: ids.operations,
	}

	if _, err := j.rows.PutJournalRow(ctx, row, fence); err != nil {
		err = fmt.Errorf("store journal batch: %w", err)
		if created && refused(err) {
			if derr := j.blobs.Delete(ctx, key); derr != nil {
				// The sweep removes the blob that this delete leaves behind.
				err = errors.Join(err, fmt.Errorf("delete refused journal blob: %w", derr))
			}
		}

		return err
	}

	return nil
}

// refused reports whether the store refused the batch for a reason that keeps it
// out of the journal, so its blob has no row to name it.
func refused(err error) bool {
	return errors.Is(err, ErrWrongUpdateState) || errors.Is(err, ErrJournalConflict) ||
		errors.Is(err, namespace.ErrFenced)
}

// putBlob writes the content under key. It reports whether this call wrote it.
// When the key already holds a blob, the content must match, so that a row
// never names bytes other than the ones the batch carried.
func (j *JournalEntries) putBlob(
	ctx context.Context,
	key blob.Key,
	content []byte,
	sum [sha256.Size]byte,
) (bool, error) {
	err := j.blobs.Put(ctx, key, bytes.NewReader(content))
	if err == nil {
		return true, nil
	}

	if !errors.Is(err, blob.ErrExists) {
		return false, fmt.Errorf("put journal blob: %w", err)
	}

	stored, err := j.digestOf(ctx, key)
	if err != nil {
		return false, err
	}

	if stored != sum {
		return false, fmt.Errorf("%w: blob %s holds other content", ErrJournalConflict, key)
	}

	return false, nil
}

// digestOf reads the blob under key and returns the SHA-256 of its bytes.
func (j *JournalEntries) digestOf(ctx context.Context, key blob.Key) ([sha256.Size]byte, error) {
	r, err := j.blobs.Get(ctx, key)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("read journal blob: %w", err)
	}

	h := sha256.New()
	_, copyErr := io.Copy(h, r)
	closeErr := r.Close()

	if copyErr != nil {
		return [sha256.Size]byte{}, fmt.Errorf("read journal blob: %w", copyErr)
	}

	if closeErr != nil {
		return [sha256.Size]byte{}, fmt.Errorf("close journal blob: %w", closeErr)
	}

	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))

	return sum, nil
}

// batchIDs returns the sequence IDs and the operation IDs of a batch body. Each
// sequence ID must be positive and appear once (ADR 43, decision 5.5). A body with
// no entries returns no IDs and no error.
func batchIDs(content []byte) (journalIDs, error) {
	var body journalBody
	if err := json.Unmarshal(content, &body); err != nil {
		return journalIDs{}, fmt.Errorf("%w: %w", ErrJournalBatch, err)
	}

	seen := make(map[int64]bool, len(body.Entries))
	seqs := make([]int64, 0, len(body.Entries))
	ops := make([]int64, 0, len(body.Entries))

	for _, e := range body.Entries {
		if e.SequenceID <= 0 {
			return journalIDs{}, fmt.Errorf("%w: sequence ID %d is not positive", ErrJournalBatch, e.SequenceID)
		}

		if seen[e.SequenceID] {
			return journalIDs{}, fmt.Errorf("%w: sequence ID %d appears twice", ErrJournalBatch, e.SequenceID)
		}

		seen[e.SequenceID] = true
		seqs = append(seqs, e.SequenceID)
		ops = append(ops, e.OperationID)
	}

	return journalIDs{sequences: seqs, operations: ops}, nil
}

// journalIDs holds the sequence IDs and the operation IDs of one batch, in the
// order of the body.
type journalIDs struct {
	sequences  []int64
	operations []int64
}

// lowest returns the smallest sequence ID of a non-empty batch.
func lowest(seqs []int64) int64 {
	low := seqs[0]
	for _, s := range seqs[1:] {
		low = min(low, s)
	}

	return low
}

// journalKey returns the blob key of a batch (ADR 43, decision 6.1).
func journalKey(stackID ID, update UpdateID, low int64) blob.Key {
	return blob.Key(fmt.Sprintf("pulumi/%s/%s/journal/%d", stackID, update, low))
}
