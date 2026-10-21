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

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/scopeaudit"
)

// ErrJournalConflict is returned when a batch holds a sequence ID that is stored
// with different content, or when a batch is only partly stored (ADR 43, decision
// 6.3). The server records the refusal and stores nothing from the batch.
var ErrJournalConflict = errors.New("journal batch differs from the batch stored with the same sequence")

// ErrJournalIncomplete is returned by Complete with succeeded when the update
// holds a recorded refusal or a gap in its stored sequence IDs. The update
// stays in progress and the stack stays locked. The message names the one
// command that ends the update.
var ErrJournalIncomplete = errors.New("a journal batch was not stored: run pulumi cancel")

// ErrJournalKey is returned when a row names a blob key that does not belong to
// its stack and update. The caller builds the key, so this is a bug in the caller.
var ErrJournalKey = errors.New("journal blob key does not belong to the update")

// ErrNilVerifier is returned by NewJournalEntries for a nil verifier. Without a
// verifier a batch would be stored unchecked, so the writer is never built with one.
var ErrNilVerifier = errors.New("journal writer needs a batch verifier")

// ErrJournalBatch is returned for a batch body that has no entries, an entry with a
// sequence ID of 0 or less, or a sequence ID that the batch holds twice.
var ErrJournalBatch = errors.New("journal batch is malformed")

// BatchVerifier checks one journal batch before the server stores it. The types
// are standard library types, so an implementation needs no import of this
// package. version is the journal version that the server negotiated for the
// update. known holds the completed operation IDs that the update already
// holds: the operations of success entries with a state, which are the only
// ones that later references can name. A reference to an entry must name one
// of them, or a completed entry earlier in the same batch. recordsChanged
// reports whether a stored batch of the update holds an entry that changes the
// replay records. A write entry must come before all of them, so a batch that
// holds one is refused when recordsChanged is set. It returns whether the batch
// holds an entry that changes the records, so the store sets the flag of the
// update in the same transaction as the write. It returns an error when the
// batch cannot replay.
type BatchVerifier interface {
	VerifyBatch(body []byte, version int, known []int64, recordsChanged bool) (changed bool, err error)
}

// JournalBatch is one batch that a journaled update sends (ADR 43, decision 6.1).
// Content is the body as received. The store keeps it in a blob under a key that
// holds the lowest sequence ID of the batch. Version is the journal version that
// the server negotiated for the update.
type JournalBatch struct {
	Ref Ref
	// Namespace is the ID of the namespace of Ref. The service takes it from
	// the namespace row that it resolved and authorized, never from a request
	// field. The writer builds the scoped blob view from it.
	Namespace namespace.ID
	StackID   ID
	Update    UpdateID
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
	// later references, so only those are stored for the known set. When the
	// batch holds a rebuilt base state entry, only the operations after the
	// last such entry are kept, as the replay drops earlier records.
	OperationIDs []int64
	// HasRebuild reports whether the batch holds a rebuilt base state entry
	// (kind 7). The store clears the operation IDs of earlier batches in the
	// same transaction as the write, so the known set starts again from this
	// batch.
	HasRebuild bool
	// Verify checks the batch against the completed operation IDs that the update
	// holds and the flag that reports whether a stored batch holds an entry that
	// changes the replay records. The store calls it in the fenced transaction,
	// after the retry check and before the write, with the operation IDs of the
	// stored rows and the flag. It returns whether the batch holds such an entry,
	// so the store sets the flag in the same transaction as the write. A nil Verify
	// skips the check. A non-nil error refuses the batch with ErrJournalBatch.
	Verify func(known []int64, recordsChanged bool) (changed bool, err error)
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
	// Verify function that returns an error is refused with ErrJournalBatch. When
	// Verify reports that the batch holds an entry that changes the replay records,
	// the flag of the update is set in the same transaction as the write. When
	// the row reports a rebuilt base state entry, the operation IDs of earlier
	// batches are cleared in the same transaction, so the known set starts
	// again from this batch.
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
	// RecordJournalRefusal stores one refused or unstored batch of the update: the
	// sequence IDs the server could read, the status it answered and the reason.
	// A batch the server could not parse records no sequence IDs. It returns nil
	// for a missing update, so a refusal that races the end of its update is
	// dropped instead of failing the error path. It checks the fence.
	RecordJournalRefusal(ctx context.Context, refusal JournalRefusal, fence namespace.Fence) error
	// JournalRefusals returns the recorded refusals of the update, in arrival
	// order. A missing update yields an empty list.
	JournalRefusals(ctx context.Context, stackID ID, update UpdateID) ([]JournalRefusal, error)
	// JournalSequences returns the stored sequence IDs of the update, in order.
	// A missing update yields an empty list. The complete check finds a gap in
	// them: a missing sequence ID between 1 and the highest stored one.
	JournalSequences(ctx context.Context, stackID ID, update UpdateID) ([]int64, error)
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

// JournalRefusal records one journal batch of an update that the server
// refused or failed to store. SequenceIDs holds the sequence IDs the server
// could read from the batch, in body order, and is empty when the batch did
// not parse. Status is the HTTP status the journal route answered for the
// batch, and Reason is the message it answered.
type JournalRefusal struct {
	Ref         Ref
	Update      UpdateID
	SequenceIDs []int64
	Status      int
	Reason      string
}

// refusal statuses: the HTTP statuses the journal route answers for a refused
// or unstored batch. The domain keeps its own copy so it never imports net/http.
const (
	refusalBadRequest   = 400
	refusalUnauthorized = 401
	refusalConflict     = 409
	refusalStoreError   = 500
)

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
	blobs              blobAccess
	rows               JournalStore
	verifier           BatchVerifier
	maxCheckpointBytes int
}

// NewJournalEntries returns a writer over the blob store, the row store and the
// batch verifier. maxCheckpointBytes bounds a batch that holds one entry;
// zero or less means DefaultMaxCheckpointBytes. The writer builds a scoped view
// of the blob store for each call, and writes each refusal of a blob key
// outside that view to refusals.
func NewJournalEntries(
	blobs blob.Store, rows JournalStore, verifier BatchVerifier, maxCheckpointBytes int,
	refusals scopeaudit.Recorder,
) (*JournalEntries, error) {
	if verifier == nil {
		return nil, ErrNilVerifier
	}

	access, err := newBlobAccess(blobs, refusals)
	if err != nil {
		return nil, err
	}

	if maxCheckpointBytes <= 0 {
		maxCheckpointBytes = DefaultMaxCheckpointBytes
	}

	return &JournalEntries{blobs: access, rows: rows, verifier: verifier, maxCheckpointBytes: maxCheckpointBytes}, nil
}

// RemoveBlobs deletes the blobs of journal batches of the update in the stack
// stackID of the namespace ns after the build has removed their rows. A failed
// delete leaves the blob for the sweep. The keys come from rows, so each key
// must name a batch in the journal directory of this update: a key outside it,
// also a key of another stack or another update of the same namespace, is a
// refusal with blob.ErrOutOfScope, and the call deletes nothing for it. The
// returned error joins the failures, and the caller logs it without failing the
// build.
func (j *JournalEntries) RemoveBlobs(
	ctx context.Context, ns namespace.ID, stackID ID, update UpdateID, keys []blob.Key,
) error {
	bl, err := j.blobs.scope(ns, stackID)
	if err != nil {
		return fmt.Errorf("remove journal blobs: %w", err)
	}

	return removeJournalBlobs(ctx, bl.narrow(bl.journalPrefix(stackID, update)), keys)
}

// removeJournalBlobs deletes the blobs under keys through the scoped access bl.
func removeJournalBlobs(ctx context.Context, bl *scopedBlobs, keys []blob.Key) error {
	var errs []error

	for _, key := range keys {
		if err := bl.Delete(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("delete journal blob: %w", err))
		}
	}

	return errors.Join(errs...)
}

// Batches returns the bodies of the stored batches of the update, in arrival
// order, for the replay. It holds every body in memory; a later streaming
// build reads them without holding them all.
func (j *JournalEntries) Batches(
	ctx context.Context, ns namespace.ID, stackID ID, update UpdateID,
) ([][]byte, error) {
	bl, err := j.blobs.scope(ns, stackID)
	if err != nil {
		return nil, fmt.Errorf("read journal batches: %w", err)
	}

	// The keys come from rows. Each must name a batch in the journal
	// directory of this update, so the reads are narrowed to it.
	bl = bl.narrow(bl.journalPrefix(stackID, update))

	keys, err := j.rows.JournalKeys(ctx, stackID, update)
	if err != nil {
		return nil, fmt.Errorf("read journal keys: %w", err)
	}

	out := make([][]byte, 0, len(keys))

	for _, key := range keys {
		body, err := batchBody(ctx, bl, key)
		if err != nil {
			return nil, err
		}

		out = append(out, body)
	}

	return out, nil
}

// Incomplete reports whether the update holds a recorded refusal for a batch
// that is still unstored, or a gap in its stored sequence IDs, so a complete
// with succeeded must not end it. A refusal whose sequence IDs are all stored
// is a batch that a retry stored after the refusal, so it does not block the
// end. A refusal with no sequence IDs names nothing stored, so it still
// blocks it. A gap is a missing sequence ID between 1 and the highest stored
// one. An update with no stored batches and no refusal is complete, so an
// empty journaled update still ends.
func (j *JournalEntries) Incomplete(ctx context.Context, stackID ID, update UpdateID) (bool, error) {
	refusals, err := j.rows.JournalRefusals(ctx, stackID, update)
	if err != nil {
		return false, fmt.Errorf("read journal refusals: %w", err)
	}

	seqs, err := j.rows.JournalSequences(ctx, stackID, update)
	if err != nil {
		return false, fmt.Errorf("read journal sequences: %w", err)
	}

	if hasUnstoredRefusal(refusals, seqs) {
		return true, nil
	}

	return hasJournalGap(seqs), nil
}

// hasUnstoredRefusal reports whether a refusal names a batch that is still
// unstored: a refusal with no sequence IDs, or with a sequence ID missing
// from the stored ones.
func hasUnstoredRefusal(refusals []JournalRefusal, seqs []int64) bool {
	stored := make(map[int64]bool, len(seqs))
	for _, s := range seqs {
		stored[s] = true
	}

	for _, r := range refusals {
		if len(r.SequenceIDs) == 0 {
			return true
		}

		for _, s := range r.SequenceIDs {
			if !stored[s] {
				return true
			}
		}
	}

	return false
}

// hasJournalGap reports whether seqs miss a sequence ID between 1 and the
// highest stored one. Stored sequence IDs start at 1 and never repeat, so a
// journal without a gap holds exactly as many as its highest.
func hasJournalGap(seqs []int64) bool {
	if len(seqs) == 0 {
		return false
	}

	var highest int64

	for _, s := range seqs {
		highest = max(highest, s)
	}

	return int64(len(seqs)) != highest
}

// batchLimitError reports a journal batch over MaxJournalBatchBytes. Its
// Error text keeps the update wrapper for the log, and Message is the one
// sentence the server answers. It wraps ErrTooLarge.
type batchLimitError struct {
	size  int
	limit int
}

// Error returns the refusal with the batch size and the batch bound.
func (e *batchLimitError) Error() string {
	return fmt.Sprintf(
		"%v: journal batch of %d bytes exceeds the limit of %d bytes: "+
			"split the change into smaller updates and run `pulumi up` again",
		ErrTooLarge, e.size, e.limit,
	)
}

// Unwrap returns ErrTooLarge.
func (e *batchLimitError) Unwrap() error { return ErrTooLarge }

// BatchOverLimit returns the refusal of a journal batch of size bytes over the
// batch bound limit. It wraps ErrTooLarge and answers one sentence at the edge.
func BatchOverLimit(size, limit int) error {
	return &batchLimitError{size: size, limit: limit}
}

// Message returns the one-sentence answer for the batch refusal.
func (e *batchLimitError) Message() string {
	return fmt.Sprintf(
		"This journal batch has %d bytes, more than the limit of %d bytes: run `pulumi up` with fewer resources.",
		e.size, e.limit,
	)
}

// Append stores one batch. It returns nil when the batch is stored, or when it is
// a retry of batch content that is already stored (ADR 43, decisions 6.2 and 6.3).
// It returns ErrJournalConflict when the batch differs from what is stored, and
// ErrWrongUpdateState or namespace.ErrFenced when the write is refused. A refusal
// deletes the blob that this call wrote, as a previous save does. Any other
// failure keeps the blob for the sweep. A batch over MaxJournalBatchBytes is
// refused with ErrTooLarge before anything is stored, unless it holds one
// entry and fits the configured checkpoint bound, because the CLI cannot split
// one entry. Each refusal and each failed store is recorded against the
// update, so a later complete with succeeded finds it.
func (j *JournalEntries) Append(ctx context.Context, batch JournalBatch, fence namespace.Fence) error {
	if batchTooLarge(batch.Content, j.maxCheckpointBytes) {
		return &batchLimitError{size: len(batch.Content), limit: MaxJournalBatchBytes}
	}

	ids, err := batchIDs(batch.Content)
	if err != nil {
		return j.recorded(ctx, batch, fence, readableSequences(batch.Content), err)
	}

	// An empty batch is valid: the CLI sends {} when it has no entries (the
	// upstream field is omitempty). It writes no blob and no row.
	if len(ids.sequences) == 0 {
		return nil
	}

	bl, err := j.blobs.scope(batch.Namespace, batch.StackID)
	if err != nil {
		return fmt.Errorf("append journal batch: %w", err)
	}

	sum := sha256.Sum256(batch.Content)
	sumMD5 := md5.Sum(batch.Content) //nolint:gosec // Integrity checksum for the backup manifest, not security.

	key, err := journalKey(bl, batch.StackID, batch.Update, lowest(ids.sequences))
	if err != nil {
		return j.recorded(ctx, batch, fence, ids.sequences, err)
	}

	created, err := putBlob(ctx, bl, key, batch.Content, sum)
	if err != nil {
		return j.recorded(ctx, batch, fence, ids.sequences, err)
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
		HasRebuild:   ids.hasRebuild,
		Verify: func(known []int64, recordsChanged bool) (bool, error) {
			return j.verifier.VerifyBatch(batch.Content, batch.Version, known, recordsChanged)
		},
	}

	if _, err := j.rows.PutJournalRow(ctx, row, fence); err != nil {
		err = fmt.Errorf("store journal batch: %w", err)
		if created && refused(err) {
			if derr := bl.Delete(ctx, key); derr != nil {
				// The sweep removes the blob that this delete leaves behind.
				err = errors.Join(err, fmt.Errorf("delete refused journal blob: %w", derr))
			}
		}

		return j.recorded(ctx, batch, fence, ids.sequences, err)
	}

	return nil
}

// singleEntryBatch reports whether body holds exactly one journal entry. A
// batch over the batch bound still passes when it holds one entry and fits
// the checkpoint bound, because the CLI cannot split one entry.
func singleEntryBatch(body []byte) bool {
	var batch journalBatchBody
	if err := json.Unmarshal(body, &batch); err != nil {
		return false
	}

	return len(batch.Entries) == 1
}

// batchTooLarge reports whether content of length n fails the batch bound:
// over MaxJournalBatchBytes, unless it holds one entry and fits the
// checkpoint bound limit. A body that does not decode has more than one
// entry for this check, so it fails over the batch bound.
func batchTooLarge(content []byte, checkpointLimit int) bool {
	if len(content) <= MaxJournalBatchBytes {
		return false
	}

	return len(content) > checkpointLimit || !singleEntryBatch(content)
}

// recorded stores the refusal of a batch that Append did not store, then
// returns the refusal error. A failed record joins the refusal error, so the
// route still answers the refusal.
func (j *JournalEntries) recorded(
	ctx context.Context, batch JournalBatch, fence namespace.Fence, seqs []int64, err error,
) error {
	refusal := JournalRefusal{
		Ref:         batch.Ref,
		Update:      batch.Update,
		SequenceIDs: seqs,
		Status:      refusalStatus(err),
		Reason:      err.Error(),
	}

	if recErr := j.rows.RecordJournalRefusal(ctx, refusal, fence); recErr != nil {
		return errors.Join(err, fmt.Errorf("record journal refusal: %w", recErr))
	}

	return err
}

// refusalStatus maps the error of a refused or unstored batch to the HTTP
// status the journal route answers for it, so the refusal record names the
// status the caller saw.
func refusalStatus(err error) int {
	switch {
	case errors.Is(err, ErrLeaseInvalid), errors.Is(err, ErrLeaseExpired):
		return refusalUnauthorized
	case errors.Is(err, ErrJournalConflict), errors.Is(err, namespace.ErrFenced),
		errors.Is(err, ErrWrongUpdateState), errors.Is(err, ErrNotJournaled):
		return refusalConflict
	case errors.Is(err, ErrJournalBatch):
		return refusalBadRequest
	default:
		return refusalStoreError
	}
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
func putBlob(
	ctx context.Context,
	bl *scopedBlobs,
	key blob.Key,
	content []byte,
	sum [sha256.Size]byte,
) (bool, error) {
	err := bl.Put(ctx, key, bytes.NewReader(content))
	if err == nil {
		return true, nil
	}

	if !errors.Is(err, blob.ErrExists) {
		return false, fmt.Errorf("put journal blob: %w", err)
	}

	stored, err := digestOf(ctx, bl, key)
	if err != nil {
		return false, err
	}

	if stored != sum {
		return false, fmt.Errorf("%w: blob %s holds other content", ErrJournalConflict, key)
	}

	return false, nil
}

// digestOf reads the blob under key and returns the SHA-256 of its bytes.
func digestOf(ctx context.Context, bl *scopedBlobs, key blob.Key) ([sha256.Size]byte, error) {
	r, err := bl.Get(ctx, key)
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
// references can name. A rebuilt base state entry drops the operations before
// it, as the replay drops its records.
func batchIDs(content []byte) (journalIDs, error) {
	var body journalBody
	if err := json.Unmarshal(content, &body); err != nil {
		return journalIDs{}, fmt.Errorf("%w: %w", ErrJournalBatch, err)
	}

	seen := make(map[int64]bool, len(body.Entries))
	seqs := make([]int64, 0, len(body.Entries))
	ops := make([]int64, 0, len(body.Entries))
	rebuilt := false

	for _, e := range body.Entries {
		if e.SequenceID <= 0 {
			return journalIDs{}, fmt.Errorf("%w: sequence ID %d is not positive", ErrJournalBatch, e.SequenceID)
		}

		if seen[e.SequenceID] {
			return journalIDs{}, fmt.Errorf("%w: sequence ID %d appears twice", ErrJournalBatch, e.SequenceID)
		}

		seen[e.SequenceID] = true
		seqs = append(seqs, e.SequenceID)

		if e.Kind == journalRebuildKind {
			ops = nil
			rebuilt = true

			continue
		}

		if e.Kind == journalSuccessKind && len(e.State) > 0 && string(e.State) != "null" {
			ops = append(ops, e.OperationID)
		}
	}

	return journalIDs{sequences: seqs, operations: ops, hasRebuild: rebuilt}, nil
}

// readableSequences returns the sequence IDs that decode from a batch body
// that batchIDs refused, in body order. Entries that do not decode
// contribute nothing, so a body that does not parse records no sequence IDs.
func readableSequences(content []byte) []int64 {
	var body journalBody
	if err := json.Unmarshal(content, &body); err != nil {
		return nil
	}

	seqs := make([]int64, 0, len(body.Entries))
	for i := range body.Entries {
		seqs = append(seqs, body.Entries[i].SequenceID)
	}

	return seqs
}

// journalSuccessKind is the kind of a success entry. Only such an entry with a
// state creates a resource that later references can name.
const journalSuccessKind = 1

// journalRebuildKind is the kind of a rebuilt base state entry. It drops the
// replay records, so earlier completed operations are no longer referenceable.
const journalRebuildKind = 7

// journalIDs holds the sequence IDs of one batch in the order of the body, its
// completed operation IDs, and whether it holds a rebuilt base state entry.
type journalIDs struct {
	sequences  []int64
	operations []int64
	hasRebuild bool
}

// lowest returns the smallest sequence ID of a non-empty batch.
func lowest(seqs []int64) int64 {
	low := seqs[0]
	for _, s := range seqs[1:] {
		low = min(low, s)
	}

	return low
}

// journalKey returns the blob key of a batch in the scope of bl:
// namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest
// sequence ID> (ADR 43, decision 6.1).
func journalKey(bl *scopedBlobs, stackID ID, update UpdateID, low int64) (blob.Key, error) {
	return bl.Key(fmt.Sprintf("%s/%s/journal/%d", stackID, update, low))
}

// batchBody reads the batch stored under key.
func batchBody(ctx context.Context, bl *scopedBlobs, key blob.Key) ([]byte, error) {
	r, err := bl.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read journal batch: %w", err)
	}

	body, readErr := io.ReadAll(r)
	closeErr := r.Close()

	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close journal batch: %w", closeErr)
	}

	return body, nil
}
