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

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// ErrNilVerifier is returned by NewJournalEntries for a nil batch reader. Without
// one a batch would be stored unchecked, so the writer is never built without it.
var ErrNilVerifier = errors.New("journal writer needs a batch reader")

// ErrJournalBatch is returned for a batch body that the batch reader refuses, for
// example an entry with a sequence ID of 0 or less or a sequence ID that the
// batch holds twice, and for a summary that breaks the rules of BatchSummary.
// A batch with no entries is valid: Append stores nothing for it.
var ErrJournalBatch = errors.New("journal batch is malformed")

// BatchReader decodes one journal batch once, before the server stores it.
// version is the journal version that the server negotiated for the update.
// ReadBatch returns the summary of the batch, or an error when the batch does
// not decode, has a sequence ID that is not positive or that appears twice, or
// breaks a rule of the entries. A body with no entries, such as {} or an empty
// entries list, is valid: its summary has no entries and a Verify that is not
// nil. On an error, the summary holds the sequence IDs that a plain read of
// the body finds, so the refusal record names them.
//
// The types are standard types: BatchSummary is an alias of an unnamed struct
// type, so an implementation declares the same struct type and needs no
// import of this package.
type BatchReader interface {
	ReadBatch(body []byte, version int) (BatchSummary, error)
}

// BatchSummary is one journal batch that a BatchReader decoded. Entries is the
// number of its entries and SequenceIDs their sequence IDs, in the order of
// the body. Operations are the completed operations of the batch that later
// references can name: the operations of success entries with a state, after
// its last rebuilt base state entry. HasRebuild reports such an entry.
//
// Verify checks the entries of that one decode against the stored state of
// the update, and the store calls it in the transaction of the write. known
// holds the completed operations that the update already holds; a reference
// must name one of them, or a completed entry earlier in the batch.
// recordsChanged reports whether a stored batch of the update holds an entry
// that changes the replay records: a write entry must come before all of
// them. Verify returns whether the batch holds an entry that changes the
// records, and an error when the batch cannot replay. Verify is not nil when
// ReadBatch returns no error: the journal writer refuses a summary without it.
type BatchSummary = struct {
	Entries     int
	SequenceIDs []int64
	Operations  []int64
	HasRebuild  bool
	Verify      func(known []int64, recordsChanged bool) (changed bool, err error)
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
	// StartedAt is the start of the write attempt of the batch, by the clock
	// of the service. The row carries it as JournalRow.AttemptStartedAt, so
	// the store refuses a commit after the attempt deadline. A zero time is
	// always refused.
	StartedAt time.Time
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
	// refuses the batch with ErrJournalBatch, so a row is never stored
	// unchecked. A non-nil error refuses the batch with ErrJournalBatch.
	Verify func(known []int64, recordsChanged bool) (changed bool, err error)
	// AttemptStartedAt is the time at which the write attempt of the blob
	// started, by the clock of the writer. The store checks it with
	// blob.CheckAttempt against the clock of the database, in the transaction
	// of the write. The store does not keep it.
	AttemptStartedAt time.Time
}

// JournalBatchRef names the blob of one stored batch with the SHA-256 and the
// size of its bytes, as the row recorded them.
type JournalBatchRef struct {
	Key    blob.Key
	SHA256 [sha256.Size]byte
	Size   int64
}

// NewJournalBatchRef builds the reference of a stored batch from the columns of
// its row. A SHA-256 of another length, or a negative size, is a corrupt row,
// which fails with ErrCheckpointMismatch and never names the key. The row
// stores call it, so the check does not depend on the dialect.
func NewJournalBatchRef(key string, sum []byte, size int64) (JournalBatchRef, error) {
	var ref JournalBatchRef

	if len(sum) != sha256.Size || size < 0 {
		return ref, ErrCheckpointMismatch
	}

	ref.Key = blob.Key(key)
	ref.Size = size
	copy(ref.SHA256[:], sum)

	return ref, nil
}

// JournalStore persists the rows of journal batches.
type JournalStore interface {
	// PutJournalRow stores the row of one batch in the same transaction as
	// checking that the update is still running under row.Lease; otherwise it
	// returns ErrWrongUpdateState. It also checks the fence. The arrival number is
	// the next number of the update, given in that transaction. When every
	// sequence ID of the row is stored with the same SHA-256, the batch is a retry:
	// it returns retried true and writes nothing, whatever blob key the row names,
	// so the rows keep the key of the first write and the caller may delete the blob
	// of its own attempt. A sequence ID stored with a
	// different SHA-256, or a batch that is only partly stored, returns
	// ErrJournalConflict. The blob must already be in the blob store. A row with
	// no sequence ID, with a sequence ID that is 0 or less, or with a sequence ID
	// that appears twice is refused with ErrJournalBatch before any read or
	// write. A row with a nil Verify, or with a Verify function that returns an
	// error, is refused with ErrJournalBatch. When Verify reports that the
	// batch holds an entry that changes the replay records, the flag of the
	// update is set in the same transaction as the write. When the row reports
	// a rebuilt base state entry, the operation IDs of earlier batches are
	// cleared in the same transaction, so the known set starts again from this
	// batch. A row whose AttemptStartedAt fails blob.CheckAttempt against the
	// clock of the database, in the same transaction, returns
	// blob.ErrAttemptExpired and writes nothing, also for a retry.
	PutJournalRow(ctx context.Context, row JournalRow, fence namespace.Fence) (retried bool, err error)
	// JournalKeys returns the blob keys of the batches of the update, in arrival
	// order. It is keyed by the stack ID, as the blob keys are. A missing update
	// yields an empty list. The sweep compares this set with the stored blobs.
	JournalKeys(ctx context.Context, stackID ID, update UpdateID) ([]blob.Key, error)
	// JournalBatches returns the blob key, the SHA-256 and the size of each batch
	// of the update, in arrival order, as the rows recorded them. It is keyed by
	// the stack ID, as the blob keys are. A missing update yields an empty list.
	// A reader of a batch compares the bytes with the SHA-256 and the size.
	JournalBatches(ctx context.Context, stackID ID, update UpdateID) ([]JournalBatchRef, error)
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
	refusalUnavailable  = 503
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

// JournalEntries writes journal batches: the blob first, then the row in a fenced
// transaction (ADR 43, decision 6.1). It holds no state of its own.
type JournalEntries struct {
	blobs              blobAccess
	rows               JournalStore
	reader             BatchReader
	maxCheckpointBytes int
}

// NewJournalEntries returns a writer over the blob store, the row store and the
// batch verifier. maxCheckpointBytes bounds a batch that holds one entry;
// zero or less means DefaultMaxCheckpointBytes. The writer builds a scoped view
// of the blob store for each call, and writes each refusal of a blob key
// outside that view to refusals.
func NewJournalEntries(
	blobs blob.Store, rows JournalStore, reader BatchReader, maxCheckpointBytes int,
	refusals scopeaudit.Recorder,
) (*JournalEntries, error) {
	if reader == nil {
		return nil, ErrNilVerifier
	}

	access, err := newBlobAccess(blobs, refusals)
	if err != nil {
		return nil, err
	}

	if maxCheckpointBytes <= 0 {
		maxCheckpointBytes = DefaultMaxCheckpointBytes
	}

	return &JournalEntries{blobs: access, rows: rows, reader: reader, maxCheckpointBytes: maxCheckpointBytes}, nil
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
// A context that ended deletes nothing: the check comes before each delete, so
// no batch blob goes after the context of the delete lease ended.
func removeJournalBlobs(ctx context.Context, bl *scopedBlobs, keys []blob.Key) error {
	var errs []error

	for _, key := range keys {
		if ctxErr := ctx.Err(); ctxErr != nil {
			errs = append(errs, fmt.Errorf("delete journal blob: %w", ctxErr))

			break
		}

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

	refs, err := j.rows.JournalBatches(ctx, stackID, update)
	if err != nil {
		return nil, fmt.Errorf("read journal batches: %w", err)
	}

	out := make([][]byte, 0, len(refs))

	for i := range refs {
		body, err := batchBody(ctx, bl, refs[i])
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

// tooLargeRemedy is the one command that helps after a size refusal of a
// journaled update. The error text and the answer of a journal batch over its
// bound and of a replay past a bound end with it, so the log and the answer
// give the same remedy.
const tooLargeRemedy = "run `pulumi up` with fewer resources"

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
		"%v: journal batch of %d bytes exceeds the limit of %d bytes: %s",
		ErrTooLarge, e.size, e.limit, tooLargeRemedy,
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
		"This journal batch has %d bytes, more than the limit of %d bytes: %s.",
		e.size, e.limit, tooLargeRemedy,
	)
}

// Append stores one batch. It returns nil when the batch is stored, or when it is
// a retry of batch content that is already stored (ADR 43, decisions 6.2 and 6.3).
// It returns ErrJournalConflict when the batch differs from what is stored, and
// ErrWrongUpdateState or namespace.ErrFenced when the write is refused. A refusal,
// and a retry that the rows already hold, delete the blob of this call, which has
// the key of its own attempt. Any other
// failure keeps the blob for the sweep. A batch over MaxJournalBatchBytes is
// refused with ErrTooLarge before anything is stored, unless it holds one
// entry and fits the configured checkpoint bound, because the CLI cannot split
// one entry. Each refusal and each failed store is recorded against the
// update, so a later complete with succeeded finds it.
func (j *JournalEntries) Append(ctx context.Context, batch JournalBatch, fence namespace.Fence) error {
	summary, err := j.readBatch(batch.Content, batch.Version)
	if err != nil {
		// A batch over the bounds is refused with no record, as before it
		// was read.
		if limit, ok := errors.AsType[*batchLimitError](err); ok && limit != nil {
			return err
		}

		return j.recorded(ctx, batch, fence, summary.SequenceIDs, err)
	}

	// An empty batch is valid: the CLI sends {} when it has no entries (the
	// upstream field is omitempty). It writes no blob and no row.
	if len(summary.SequenceIDs) == 0 {
		return nil
	}

	bl, err := j.blobs.scope(batch.Namespace, batch.StackID)
	if err != nil {
		return fmt.Errorf("append journal batch: %w", err)
	}

	sum := sha256.Sum256(batch.Content)
	sumMD5 := blob.DigestBytes(batch.Content).MD5

	key, err := journalKey(bl, batch.StackID, batch.Update, lowest(summary.SequenceIDs))
	if err != nil {
		return j.recorded(ctx, batch, fence, summary.SequenceIDs, err)
	}

	if err = bl.Put(ctx, key, bytes.NewReader(batch.Content)); err != nil {
		return j.recorded(ctx, batch, fence, summary.SequenceIDs, fmt.Errorf("put journal blob: %w", err))
	}

	row := JournalRow{
		Ref:          batch.Ref,
		Update:       batch.Update,
		Lease:        batch.Lease,
		BlobKey:      key,
		SHA256:       sum,
		MD5:          sumMD5,
		Size:         int64(len(batch.Content)),
		SequenceIDs:  summary.SequenceIDs,
		OperationIDs: summary.Operations,
		HasRebuild:   summary.HasRebuild,
		Verify:       summary.Verify,
		// The attempt started before the blob write above.
		AttemptStartedAt: batch.StartedAt,
	}

	if err = j.storeRow(ctx, bl, row, fence); err != nil {
		return j.recorded(ctx, batch, fence, summary.SequenceIDs, err)
	}

	return nil
}

// storeRow stores the row of a batch whose blob is already written under
// row.BlobKey, the key of this attempt. When the rows already hold the batch (a
// retry), they keep the key of the first write, so the blob of this attempt has
// no row and the call deletes it. A clear refusal deletes it too. Any other
// failure keeps the blob for the sweep.
func (j *JournalEntries) storeRow(ctx context.Context, bl *scopedBlobs, row JournalRow, fence namespace.Fence) error {
	retried, err := j.rows.PutJournalRow(ctx, row, fence)
	if err == nil {
		if retried {
			discardOwnBlob(ctx, bl, row.BlobKey)
		}

		return nil
	}

	err = fmt.Errorf("store journal batch: %w", err)
	if refused(err) {
		if derr := bl.Delete(ctx, row.BlobKey); derr != nil {
			// The sweep removes the blob that this delete leaves behind.
			err = errors.Join(err, fmt.Errorf("delete refused journal blob: %w", derr))
		}
	}

	return err
}

// discardOwnBlob deletes the blob under key, the key of this attempt, and
// reports whether the delete worked. It never reads or guesses another key.
func discardOwnBlob(ctx context.Context, bl *scopedBlobs, key blob.Key) bool {
	return bl.Delete(ctx, key) == nil
}

// readBatch decodes content once with the batch reader. It refuses a body
// over both bounds before the decode, and a body over the batch bound that
// does not hold exactly one entry, with a *batchLimitError: the CLI cannot
// split one entry. A body that does not decode has more than one entry for
// this check. Any other refusal of the reader is wrapped in ErrJournalBatch,
// and its summary keeps the sequence IDs that the reader found. A summary
// that breaks the rules of BatchSummary is refused by checkSummary.
func (j *JournalEntries) readBatch(content []byte, version int) (BatchSummary, error) {
	var none BatchSummary

	tooLarge := &batchLimitError{size: len(content), limit: MaxJournalBatchBytes}
	overBatch := len(content) > MaxJournalBatchBytes

	if overBatch && len(content) > j.maxCheckpointBytes {
		return none, tooLarge
	}

	summary, err := j.reader.ReadBatch(content, version)
	if overBatch && (err != nil || summary.Entries != 1) {
		return none, tooLarge
	}

	if err != nil {
		return summary, fmt.Errorf("%w: %w", ErrJournalBatch, err)
	}

	return summary, checkSummary(summary)
}

// checkSummary refuses a summary that the batch reader returned without an
// error but that breaks the rules of BatchSummary: a summary with no Verify,
// an entry count that is not the number of sequence IDs, more operations than
// entries, or a sequence ID that is not positive or that appears twice. The
// refusal wraps ErrJournalBatch.
func checkSummary(summary BatchSummary) error {
	// Defense in depth: the store refuses a row with a nil Verify too. This
	// check stays so that a batch reader that returns no entry check never
	// writes a blob, and does not rely on the store adapter.
	if summary.Verify == nil {
		return fmt.Errorf("%w: the batch reader returned no entry check", ErrJournalBatch)
	}

	// Defense in depth: the batch reader refuses these batches too. These
	// checks stay because the sequence IDs come from the request body and name
	// the blob key, the row and the gap check of the update.
	if summary.Entries != len(summary.SequenceIDs) {
		return fmt.Errorf(
			"%w: the batch has %d entries and %d sequence IDs",
			ErrJournalBatch, summary.Entries, len(summary.SequenceIDs),
		)
	}

	if len(summary.Operations) > summary.Entries {
		return fmt.Errorf(
			"%w: the batch has %d entries and %d operations",
			ErrJournalBatch, summary.Entries, len(summary.Operations),
		)
	}

	return checkSequenceIDs(summary.SequenceIDs)
}

// CheckJournalSequenceIDs refuses the sequence IDs of a journal row: an empty
// list, a sequence ID that is 0 or less, or a sequence ID that appears twice.
// The refusal wraps ErrJournalBatch. The store adapters call it before they
// read or write a row.
//
// Defense in depth: the journal writer stores no empty batch and the batch
// reader refuses these sequence IDs too. This check stays so that a new caller
// of PutJournalRow cannot store such a row, and it does not rely on the writer
// or the reader.
func CheckJournalSequenceIDs(seqs []int64) error {
	if len(seqs) == 0 {
		return fmt.Errorf("%w: the journal row has no sequence ID", ErrJournalBatch)
	}

	return checkSequenceIDs(seqs)
}

// checkSequenceIDs refuses a sequence ID that is 0 or less, or that appears
// twice in seqs. The refusal wraps ErrJournalBatch.
func checkSequenceIDs(seqs []int64) error {
	seen := make(map[int64]bool, len(seqs))

	for _, seq := range seqs {
		if seq <= 0 {
			return fmt.Errorf("%w: sequence ID %d is not positive", ErrJournalBatch, seq)
		}

		if seen[seq] {
			return fmt.Errorf("%w: sequence ID %d appears twice", ErrJournalBatch, seq)
		}

		seen[seq] = true
	}

	return nil
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
	case errors.Is(err, blob.ErrAttemptExpired):
		// The client sends the batch again, and the new attempt has a new
		// start time.
		return refusalUnavailable
	default:
		return refusalStoreError
	}
}

// refused reports whether the store refused the batch for a reason that keeps it
// out of the journal, so its blob has no row to name it.
func refused(err error) bool {
	return errors.Is(err, ErrWrongUpdateState) || errors.Is(err, ErrJournalConflict) ||
		errors.Is(err, ErrJournalBatch) || errors.Is(err, namespace.ErrFenced) ||
		errors.Is(err, ErrNotJournaled) || errors.Is(err, ErrJournalKey) ||
		errors.Is(err, blob.ErrAttemptExpired)
}

// lowest returns the smallest sequence ID of a non-empty batch.
func lowest(seqs []int64) int64 {
	low := seqs[0]
	for _, s := range seqs[1:] {
		low = min(low, s)
	}

	return low
}

// journalKey returns the blob key of one write attempt of a batch in the scope of
// bl: namespaces/<namespace ID>/pulumi/<stack ID>/<update ID>/journal/<lowest
// sequence ID>-<attempt ID>. The attempt ID is new for each call, so no two
// writes share a key.
func journalKey(bl *scopedBlobs, stackID ID, update UpdateID, low int64) (blob.Key, error) {
	return bl.Key(fmt.Sprintf("%s/%s/journal/%d-%s", stackID, update, low, newAttemptID()))
}

// batchBody reads the batch that ref names. It reads at most the recorded size
// plus one byte, and it compares the size and the SHA-256 of the bytes with the
// row. Bytes that differ fail with ErrCheckpointMismatch, so the replay never
// builds a checkpoint from a blob that is not the batch of its row. The error
// never names the key.
func batchBody(ctx context.Context, bl *scopedBlobs, ref JournalBatchRef) ([]byte, error) {
	r, err := bl.Get(ctx, ref.Key)
	if err != nil {
		return nil, fmt.Errorf("read journal batch: %w", err)
	}

	body, readErr := io.ReadAll(io.LimitReader(r, ref.Size+1))
	closeErr := r.Close()

	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close journal batch: %w", closeErr)
	}

	if int64(len(body)) != ref.Size || sha256.Sum256(body) != ref.SHA256 {
		return nil, fmt.Errorf("read journal batch: %w", ErrCheckpointMismatch)
	}

	return body, nil
}
