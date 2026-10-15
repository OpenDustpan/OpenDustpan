package stack

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The backup manifest records MD5 of stored bytes, for integrity, not security.
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

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

// ErrNilVerifier is returned by NewJournalEntries for a nil verifier. Without a
// verifier a batch would be stored unchecked, so the writer is never built with one.
var ErrNilVerifier = errors.New("journal writer needs a batch verifier")

// journalSweepGrace is how long after its write a journal blob stays before the
// sweep may delete it, so a blob written just before its row is not removed.
// It matches the grace period of checkpoint blobs.
const journalSweepGrace = time.Hour

// ErrJournalBatch is returned for a batch body that has no entries, an entry with a
// sequence ID of 0 or less, or a sequence ID that the batch holds twice.
var ErrJournalBatch = errors.New("journal batch is malformed")

// BatchVerifier checks one journal batch before the server stores it. The types
// are standard library types, so an implementation needs no import of this
// package. version is the journal version that the server negotiated for the
// update. known holds the completed operation IDs that the update already
// holds: the operations of success entries with a state, which are the only
// ones that later references can name. A reference to an entry must name one
// of them, or a completed entry earlier in the same batch. It returns an error
// when the batch cannot replay.
type BatchVerifier interface {
	VerifyBatch(body []byte, version int, known []int64) error
}

// JournalBatch is one batch that a journaled update sends (ADR 43, decision 6.1).
// Content is the body as received. The store keeps it in a blob under a key that
// holds the lowest sequence ID of the batch. Version is the journal version that
// the server negotiated for the update.
type JournalBatch struct {
	Ref     Ref
	StackID ID
	Update  UpdateID
	// Lease is the digest of the lease that authorized the write.
	Lease   token.Digest
	Content []byte
	Version int
}

// JournalRow is the row of one stored batch: the blob that holds it, and the
// parts that the retry check and the replay read.
type JournalRow struct {
	Ref     Ref
	Update  UpdateID
	Lease   token.Digest
	BlobKey blob.Key
	SHA256  [sha256.Size]byte
	// MD5 is the MD5 of the stored bytes. The backup manifest names the blob by it.
	MD5         [md5.Size]byte
	Size        int64
	SequenceIDs []int64
	// OperationIDs holds the completed operation IDs of the batch: the
	// operations of success entries with a state. Only those can be named by
	// later references, so only those are stored for the known set.
	OperationIDs []int64
	// Verify checks the batch against the completed operation IDs that the update
	// holds. The store calls it in the fenced transaction, after the retry check
	// and before the write, with the operation IDs of the stored rows. A nil
	// Verify skips the check. A non-nil error refuses the batch with ErrJournalBatch.
	Verify func(known []int64) error
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
	// ErrJournalConflict. The blob must already be in the blob store. A row with a
	// Verify function that returns an error is refused with ErrJournalBatch.
	PutJournalRow(ctx context.Context, row JournalRow, fence namespace.Fence) (retried bool, err error)
	// JournalKeys returns the blob keys of the batches of the update, in arrival
	// order. It is keyed by the stack ID, as the blob keys are. A missing update
	// yields an empty list. The sweep compares this set with the stored blobs.
	JournalKeys(ctx context.Context, stackID ID, update UpdateID) ([]blob.Key, error)
	// JournalSweepable reports whether the sweep may delete the journal blobs of the
	// update: its journal is closed, or the update no longer runs. Then no batch can
	// be added while the sweep runs. It returns ErrUpdateNotFound for a missing update,
	// so a sweep never deletes for a stack it cannot find.
	JournalSweepable(ctx context.Context, stackID ID, update UpdateID) (bool, error)
}

// BuiltCheckpointStore writes the checkpoint of a built update and removes its
// journal in one transaction (ADR 43, decision 6.7). The build (item 10) is the
// only caller. An ordinary save uses CheckpointStore.PutCheckpoint, which keeps the
// journal.
type BuiltCheckpointStore interface {
	// PutBuiltCheckpoint runs every check of PutCheckpoint and then deletes the
	// batch rows and sequence rows of the update, in the same fenced transaction.
	// When a check fails, it deletes nothing. It returns the blob keys of the
	// deleted rows. The caller removes those blobs after the commit with RemoveBlobs.
	PutBuiltCheckpoint(ctx context.Context, cp Checkpoint, fence namespace.Fence) ([]blob.Key, error)
}

// KnownOperationIDs returns the completed operation IDs that the stored rows of
// an update hold. Each element is the JSON array that a row keeps (see
// JournalRow.OperationIDs). It returns an error for a value that is not such
// an array.
func KnownOperationIDs(stored []string) ([]int64, error) {
	var known []int64
	for _, raw := range stored {
		var ops []int64
		if err := json.Unmarshal([]byte(raw), &ops); err != nil {
			return nil, fmt.Errorf("decode stored operation IDs: %w", err)
		}

		known = append(known, ops...)
	}

	return known, nil
}

// journalBody is the part of a journal batch body that the blob key, the retry
// check and the completed operation IDs read.
type journalBody struct {
	Entries []journalEntryIDs `json:"entries"`
}

// journalEntryIDs holds the IDs and the completion parts of one entry. Kind 1
// is the success kind, and only a success entry with a state creates a resource
// that later references can name, matching the index that the replay builds.
type journalEntryIDs struct {
	SequenceID  int64           `json:"sequenceID"`
	OperationID int64           `json:"operationID"`
	Kind        int             `json:"kind"`
	State       json.RawMessage `json:"state"`
}

// JournalEntries writes journal batches: the blob first, then the row in a fenced
// transaction (ADR 43, decision 6.1). It holds no state of its own.
type JournalEntries struct {
	blobs    blob.Store
	rows     JournalStore
	verifier BatchVerifier
}

// NewJournalEntries returns a writer over the blob store, the row store and the
// batch verifier.
func NewJournalEntries(blobs blob.Store, rows JournalStore, verifier BatchVerifier) (*JournalEntries, error) {
	if verifier == nil {
		return nil, ErrNilVerifier
	}

	return &JournalEntries{blobs: blobs, rows: rows, verifier: verifier}, nil
}

// RemoveBlobs deletes the blobs of journal batches after the build has removed their
// rows. A failed delete leaves the blob for the sweep. The returned error joins the
// failures, and the caller logs it without failing the build.
func (j *JournalEntries) RemoveBlobs(ctx context.Context, keys []blob.Key) error {
	var errs []error

	for _, key := range keys {
		if err := j.blobs.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete journal blob %s: %w", key, err))
		}
	}

	return errors.Join(errs...)
}

// SweepUpdate deletes the journal blobs of one update that no row names and that are
// older than the grace period at now: batches whose row never committed, and blobs
// that a finished build left behind. It runs only when the store reports the journal
// sweepable, so no batch can be added during the sweep. The listing covers one update
// prefix. It returns the first error that it meets, joined with the others.
func (j *JournalEntries) SweepUpdate(ctx context.Context, stackID ID, update UpdateID, now time.Time) error {
	sweepable, err := j.rows.JournalSweepable(ctx, stackID, update)
	if err != nil {
		return fmt.Errorf("check journal sweepable: %w", err)
	}

	if !sweepable {
		return nil
	}

	prefix := "pulumi/" + stackID.String() + "/" + update.String() + "/journal/"

	listed, err := j.blobs.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list journal blobs: %w", err)
	}

	if len(listed) == 0 {
		return nil
	}

	named, err := j.rows.JournalKeys(ctx, stackID, update)
	if err != nil {
		return fmt.Errorf("read journal keys: %w", err)
	}

	keep := make(map[blob.Key]bool, len(named))
	for _, key := range named {
		keep[key] = true
	}

	var orphans []blob.Key

	for _, obj := range listed {
		if keep[obj.Key] || now.Sub(obj.ModifiedAt) < journalSweepGrace {
			continue
		}

		orphans = append(orphans, obj.Key)
	}

	return j.RemoveBlobs(ctx, orphans)
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
	sumMD5 := md5.Sum(batch.Content) //nolint:gosec // Integrity checksum for the backup manifest, not security.
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
		MD5:          sumMD5,
		Size:         int64(len(batch.Content)),
		SequenceIDs:  ids.sequences,
		OperationIDs: ids.operations,
		Verify: func(known []int64) error {
			return j.verifier.VerifyBatch(batch.Content, batch.Version, known)
		},
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
		errors.Is(err, ErrJournalBatch) || errors.Is(err, namespace.ErrFenced)
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

// batchIDs returns the sequence IDs and the completed operation IDs of a batch
// body. Each sequence ID must be positive and appear once. A body with no
// entries returns no IDs and no error. Only success entries with a state
// contribute operation IDs, as only those create resources that later
// references can name.
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

		if e.Kind == journalSuccessKind && len(e.State) > 0 && string(e.State) != "null" {
			ops = append(ops, e.OperationID)
		}
	}

	return journalIDs{sequences: seqs, operations: ops}, nil
}

// journalSuccessKind is the kind of a success entry. Only such an entry with a
// state creates a resource that later references can name.
const journalSuccessKind = 1

// journalIDs holds the sequence IDs of one batch in the order of the body, and
// its completed operation IDs.
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
