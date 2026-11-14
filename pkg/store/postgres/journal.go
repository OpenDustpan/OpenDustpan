package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// PutJournalRow implements stack.JournalStore. It checks the fence, the stack and
// the lease in the same transaction as the write, then either writes nothing
// (a retry with the same content), or writes the row with the next arrival number
// and one sequence row for each sequence ID.
func (s *Store) PutJournalRow(ctx context.Context, row stack.JournalRow, fence namespace.Fence) (bool, error) {
	// Defense in depth: the journal writer and the batch reader refuse these
	// sequence IDs too. This check stays so that a new caller cannot store a
	// row with no sequence ID, and it does not rely on the writer or the reader.
	if err := stack.CheckJournalSequenceIDs(row.SequenceIDs); err != nil {
		return false, err
	}

	retried := false
	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, row.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return stack.ErrWrongUpdateState
		}

		if err != nil {
			return err
		}

		// The update row is locked before any check, so two batches of one
		// update run one after the other: the second sees the first's row.
		_, lockErr := q.LockUpdateForJournal(ctx, db.LockUpdateForJournalParams{
			StackID: stackID,
			ID:      row.Update.String(),
		})
		if lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return stack.ErrWrongUpdateState
			}

			return fmt.Errorf("lock update for journal: %w", lockErr)
		}

		if guardErr := checkJournalRow(ctx, q, stackID, row); guardErr != nil {
			return guardErr
		}

		done, writeErr := writeJournalRow(ctx, q, stackID, row)
		retried = done

		return writeErr
	})

	return retried, err
}

// writeJournalRow answers a retry without a write (true). Otherwise it runs the
// row's check against the stored operations and the stored records flag and inserts
// the row (false). When the batch holds an entry that changes the replay records,
// the flag of the update is set in the same transaction as the write. When the
// batch holds a rebuilt base state entry, the operation IDs of earlier batches
// are cleared in the same transaction, so the known set starts again from this
// batch.
func writeJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) (bool, error) {
	done, err := journalRetry(ctx, q, stackID, row)
	if err != nil {
		return false, err
	}

	if done {
		return true, nil
	}

	changed, verifyErr := verifyJournalRow(ctx, q, stackID, row)
	if verifyErr != nil {
		return false, verifyErr
	}

	if row.HasRebuild {
		params := db.ClearJournalOperationsParams{StackID: stackID, UpdateID: row.Update.String()}
		if clearErr := q.ClearJournalOperations(ctx, params); clearErr != nil {
			return false, fmt.Errorf("clear journal operations: %w", clearErr)
		}
	}

	if insertErr := insertJournalRow(ctx, q, stackID, row); insertErr != nil {
		return false, insertErr
	}

	if changed {
		params := db.SetJournalRecordsChangedParams{StackID: stackID, ID: row.Update.String()}
		if flagErr := q.SetJournalRecordsChanged(ctx, params); flagErr != nil {
			return false, fmt.Errorf("set journal records changed: %w", flagErr)
		}
	}

	return false, nil
}

// checkJournalRow runs the checks of a batch that the write needs before its retry
// check: the attempt window, the blob key, the lease, the update kind, a
// journaled update, and an open
// journal. A version 0 update saves checkpoints, so it refuses every batch with
// ErrNotJournaled. A closed journal refuses every batch, so no batch can land
// after the build.
func checkJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) error {
	// The caller locked the update row first, so a wait for the lock counts
	// in the attempt window.
	if err := checkAttempt(ctx, q, row.AttemptStartedAt); err != nil {
		return err
	}

	if err := checkJournalKey(ctx, q, stackID, row); err != nil {
		return err
	}

	if err := checkLease(ctx, q, stackID, row.Update, codec.EncodeDigest(row.Lease)); err != nil {
		return err
	}

	if err := checkMutates(ctx, q, stackID, row.Update); err != nil {
		return err
	}

	state, err := q.JournalState(ctx, db.JournalStateParams{StackID: stackID, ID: row.Update.String()})
	if err != nil {
		return fmt.Errorf("read journal state: %w", err)
	}

	if state.JournalVersion == 0 {
		return stack.ErrNotJournaled
	}

	if state.JournalClosed {
		return stack.ErrWrongUpdateState
	}

	return nil
}

// checkJournalKey refuses a row whose blob key does not belong to the namespace, the stack and the
// update that the row names. The key is built by the caller, so a mismatch is a bug.
func checkJournalKey(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) error {
	nsID, err := q.GetNamespaceID(ctx, row.Ref.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the namespace of the row does not exist", stack.ErrJournalKey)
	}

	if err != nil {
		return fmt.Errorf("read namespace ID: %w", err)
	}

	return stack.CheckJournalBlobKey(namespace.ID(nsID), stack.ID(stackID), row.Update, row.BlobKey)
}

// journalRetry reports whether every sequence ID of the row is stored with the same
// SHA-256 (a retry). It returns ErrJournalConflict when a sequence ID is stored with
// other content, or when only some of them are stored.
func journalRetry(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) (bool, error) {
	stored := 0
	for _, seq := range row.SequenceIDs {
		sum, err := q.JournalSequenceSHA(ctx, db.JournalSequenceSHAParams{
			StackID:  stackID,
			UpdateID: row.Update.String(),
			Sequence: seq,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}

		if err != nil {
			return false, fmt.Errorf("read journal sequence %d: %w", seq, err)
		}

		if !bytes.Equal(sum, row.SHA256[:]) {
			return false, fmt.Errorf("%w: sequence %d", stack.ErrJournalConflict, seq)
		}

		stored++
	}

	switch {
	case stored == 0:
		return false, nil
	case stored == len(row.SequenceIDs):
		return true, nil
	default:
		return false, fmt.Errorf(
			"%w: %d of %d sequences stored",
			stack.ErrJournalConflict,
			stored,
			len(row.SequenceIDs),
		)
	}
}

// insertJournalRow writes the row with the next arrival number of the update and
// one sequence row for each sequence ID of the batch.
func insertJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) error {
	arrival, err := q.NextJournalArrival(ctx, db.NextJournalArrivalParams{
		StackID:  stackID,
		UpdateID: row.Update.String(),
	})
	if err != nil {
		return fmt.Errorf("next journal arrival: %w", err)
	}

	seqs, err := json.Marshal(row.SequenceIDs)
	if err != nil {
		return fmt.Errorf("encode sequence IDs: %w", err)
	}

	ops, err := json.Marshal(row.OperationIDs)
	if err != nil {
		return fmt.Errorf("encode operation IDs: %w", err)
	}

	err = q.InsertJournal(ctx, db.InsertJournalParams{
		StackID:        stackID,
		UpdateID:       row.Update.String(),
		Arrival:        arrival,
		BlobKey:        string(row.BlobKey),
		Sha256:         row.SHA256[:],
		Md5:            row.MD5[:],
		Size:           row.Size,
		LowestSequence: lowestOf(row.SequenceIDs),
		SequenceIds:    string(seqs),
		OperationIds:   string(ops),
	})
	if err != nil {
		return fmt.Errorf("insert journal row: %w", err)
	}

	for _, seq := range row.SequenceIDs {
		err := q.InsertJournalSequence(ctx, db.InsertJournalSequenceParams{
			StackID:  stackID,
			UpdateID: row.Update.String(),
			Sequence: seq,
			Arrival:  arrival,
			Sha256:   row.SHA256[:],
		})
		if err != nil {
			return fmt.Errorf("insert journal sequence %d: %w", seq, err)
		}
	}

	return nil
}

// lowestOf returns the smallest of a non-empty list of sequence IDs.
func lowestOf(seqs []int64) int64 {
	low := seqs[0]
	for _, s := range seqs[1:] {
		low = min(low, s)
	}

	return low
}

// verifyJournalRow runs the row's Verify check against the operation IDs of the rows
// that the update already holds and the flag that reports whether a stored batch
// holds an entry that changes the replay records. It runs in the same transaction
// as the write, so the list and the flag it reads are the ones that the write
// follows. It returns whether the batch holds such an entry.
// A row with no Verify is refused. A refusal wraps ErrJournalBatch.
func verifyJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) (bool, error) {
	// Defense in depth: the journal writer refuses a batch with no entry check
	// before it calls the store. This check stays so that a new caller of
	// PutJournalRow cannot store a batch without the reference and write-order
	// checks.
	if row.Verify == nil {
		return false, fmt.Errorf("%w: the journal row has no entry check", stack.ErrJournalBatch)
	}

	state, err := q.JournalState(ctx, db.JournalStateParams{StackID: stackID, ID: row.Update.String()})
	if err != nil {
		return false, fmt.Errorf("read journal state: %w", err)
	}

	stored, err := q.ListJournalOperations(ctx, db.ListJournalOperationsParams{
		StackID:  stackID,
		UpdateID: row.Update.String(),
	})
	if err != nil {
		return false, fmt.Errorf("list journal operations: %w", err)
	}

	known, err := stack.KnownOperationIDs(stored)
	if err != nil {
		return false, err
	}

	changed, err := row.Verify(known, state.JournalRecordsChanged)
	if err != nil {
		return false, fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
	}

	return changed, nil
}

// JournalKeys implements stack.JournalStore. It is keyed by the stack ID, as the blob
// keys are. A missing update yields no keys.
func (s *Store) JournalKeys(ctx context.Context, stackID stack.ID, update stack.UpdateID) ([]blob.Key, error) {
	rows, err := s.queries.JournalBlobKeys(
		ctx,
		db.JournalBlobKeysParams{StackID: stackID.String(), UpdateID: update.String()},
	)
	if err != nil {
		return nil, fmt.Errorf("select journal blob keys: %w", err)
	}

	return journalBlobKeys(rows), nil
}

// JournalBatches implements stack.JournalStore. It is keyed by the stack ID, as the
// blob keys are. A missing update yields no batches.
func (s *Store) JournalBatches(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]stack.JournalBatchRef, error) {
	rows, err := s.queries.JournalBatchRefs(
		ctx,
		db.JournalBatchRefsParams{StackID: stackID.String(), UpdateID: update.String()},
	)
	if err != nil {
		return nil, fmt.Errorf("select journal batches: %w", err)
	}

	refs := make([]stack.JournalBatchRef, 0, len(rows))

	for i := range rows {
		ref, refErr := stack.NewJournalBatchRef(rows[i].BlobKey, rows[i].Sha256, rows[i].Size)
		if refErr != nil {
			return nil, fmt.Errorf("select journal batches: %w", refErr)
		}

		refs = append(refs, ref)
	}

	return refs, nil
}

// RecordJournalRefusal implements stack.JournalStore. It stores one refused or
// unstored batch of a running update with the next arrival number of its
// refusals, in the same fenced transaction. A missing stack or update, or an
// update that no longer runs, records nothing and returns nil, so a refusal
// that races the end of its update is dropped instead of failing the error path.
func (s *Store) RecordJournalRefusal(
	ctx context.Context, refusal stack.JournalRefusal, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, refusal.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return nil
		}

		if err != nil {
			return err
		}

		running, runningErr := refusalRunning(ctx, q, stackID, refusal.Update)
		if runningErr != nil || !running {
			return runningErr
		}

		return insertJournalRefusal(ctx, q, stackID, refusal)
	})
}

// refusalRunning locks the update row and reports whether the update still
// runs, so its refusal is kept. Two refusals of one update run one after the
// other: the second sees the first's row. A missing update reports false, so
// a refusal that races the end of its update is dropped instead of failing
// the error path.
func refusalRunning(
	ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID,
) (bool, error) {
	_, lockErr := q.LockUpdateForJournal(ctx, db.LockUpdateForJournalParams{
		StackID: stackID,
		ID:      id.String(),
	})
	if errors.Is(lockErr, pgx.ErrNoRows) {
		return false, nil
	}

	if lockErr != nil {
		return false, fmt.Errorf("lock update for journal: %w", lockErr)
	}

	state, stateErr := q.JournalState(ctx, db.JournalStateParams{
		StackID: stackID,
		ID:      id.String(),
	})
	if errors.Is(stateErr, pgx.ErrNoRows) {
		return false, nil
	}

	if stateErr != nil {
		return false, fmt.Errorf("read journal state: %w", stateErr)
	}

	return state.Status == string(stack.StatusRunning), nil
}

// insertJournalRefusal stores one refusal with the next arrival number of the
// refusals of the update, in the caller's transaction.
func insertJournalRefusal(
	ctx context.Context, q *db.Queries, stackID string, refusal stack.JournalRefusal,
) error {
	arrival, err := q.NextRefusalArrival(ctx, db.NextRefusalArrivalParams{
		StackID:  stackID,
		UpdateID: refusal.Update.String(),
	})
	if err != nil {
		return fmt.Errorf("next refusal arrival: %w", err)
	}

	seqs, err := marshalRefusalSequences(refusal.SequenceIDs)
	if err != nil {
		return err
	}

	return q.InsertJournalRefusal(ctx, db.InsertJournalRefusalParams{
		StackID:     stackID,
		UpdateID:    refusal.Update.String(),
		Arrival:     arrival,
		SequenceIds: seqs,
		Status:      int64(refusal.Status),
		Reason:      refusal.Reason,
	})
}

// marshalRefusalSequences encodes the sequence IDs of a refusal. An empty
// list encodes as an empty array, never null, so reads need no second shape.
func marshalRefusalSequences(seqs []int64) (string, error) {
	if len(seqs) == 0 {
		return "[]", nil
	}

	raw, err := json.Marshal(seqs)
	if err != nil {
		return "", fmt.Errorf("encode refusal sequence IDs: %w", err)
	}

	return string(raw), nil
}

// finishJournalEnd ends the journal bookkeeping of an update in the same
// transaction as its end transition. A journaled update that ends with
// succeeded rechecks its refusals: the complete check ran before the build,
// so a refusal recorded after it still stops the end. Cancel and failure end
// the update despite refusals and drop their rows; the build already dropped
// the rows of an update with stored batches.
func finishJournalEnd(ctx context.Context, q *db.Queries, stackID string, u stack.Update) error {
	if !u.Mutates() || !u.Status.Terminal() {
		return nil
	}

	if u.Status == stack.StatusSucceeded {
		if u.JournalVersion == 0 {
			return nil
		}

		return checkNoUnstoredRefusal(ctx, q, stackID, u.ID)
	}

	if u.Status != stack.StatusCanceled && u.Status != stack.StatusFailed {
		return nil
	}

	refusalsErr := q.DeleteJournalRefusals(ctx, db.DeleteJournalRefusalsParams{
		StackID:  stackID,
		UpdateID: u.ID.String(),
	})
	if refusalsErr != nil {
		return fmt.Errorf("delete journal refusals: %w", refusalsErr)
	}

	return nil
}

// checkNoUnstoredRefusal reports ErrJournalIncomplete when the update holds a
// refusal for a batch that is still unstored: a refusal with no sequence IDs,
// or with a sequence ID missing from the stored sequences. It runs in the end
// transition, so a refusal recorded after the complete check still stops the
// end. A refusal whose sequence IDs are all stored is a batch that a retry
// stored after the refusal, so it does not stop it.
func checkNoUnstoredRefusal(ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID) error {
	rows, err := q.ListJournalRefusals(ctx, db.ListJournalRefusalsParams{
		StackID:  stackID,
		UpdateID: id.String(),
	})
	if err != nil {
		return fmt.Errorf("select journal refusals: %w", err)
	}

	if len(rows) == 0 {
		return nil
	}

	seqs, err := q.ListJournalSequences(ctx, db.ListJournalSequencesParams{
		StackID:  stackID,
		UpdateID: id.String(),
	})
	if err != nil {
		return fmt.Errorf("select journal sequences: %w", err)
	}

	stored := make(map[int64]bool, len(seqs))
	for _, seq := range seqs {
		stored[seq] = true
	}

	for _, row := range rows {
		var named []int64

		if err := json.Unmarshal([]byte(row.SequenceIds), &named); err != nil {
			return fmt.Errorf("decode refusal sequence IDs: %w", err)
		}

		if len(named) == 0 {
			return stack.ErrJournalIncomplete
		}

		for _, seq := range named {
			if !stored[seq] {
				return stack.ErrJournalIncomplete
			}
		}
	}

	return nil
}

// JournalRefusals implements stack.JournalStore. It is keyed by the stack ID,
// as the blob keys are. A missing update yields no refusals.
func (s *Store) JournalRefusals(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]stack.JournalRefusal, error) {
	var (
		rows  []db.ListJournalRefusalsRow
		names db.StackRefByIDRow
	)

	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		var err error

		rows, err = q.ListJournalRefusals(ctx, db.ListJournalRefusalsParams{
			StackID:  stackID.String(),
			UpdateID: update.String(),
		})
		if err != nil {
			return fmt.Errorf("select journal refusals: %w", err)
		}

		if len(rows) == 0 {
			return nil
		}

		names, err = q.StackRefByID(ctx, stackID.String())
		if err != nil {
			return fmt.Errorf("resolve stack: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	ref, err := stack.ParseRef(names.Namespace, names.Project, names.Name)
	if err != nil {
		return nil, fmt.Errorf("parse stack reference: %w", err)
	}

	out := make([]stack.JournalRefusal, 0, len(rows))

	for _, row := range rows {
		var seqs []int64

		if err := json.Unmarshal([]byte(row.SequenceIds), &seqs); err != nil {
			return nil, fmt.Errorf("decode refusal sequence IDs: %w", err)
		}

		out = append(out, stack.JournalRefusal{
			Ref:         ref,
			Update:      update,
			SequenceIDs: seqs,
			Status:      int(row.Status),
			Reason:      row.Reason,
		})
	}

	return out, nil
}

// JournalSequences implements stack.JournalStore. It returns the stored
// sequence IDs of the update, in order. A missing update yields none.
func (s *Store) JournalSequences(
	ctx context.Context, stackID stack.ID, update stack.UpdateID,
) ([]int64, error) {
	seqs, err := s.queries.ListJournalSequences(ctx, db.ListJournalSequencesParams{
		StackID:  stackID.String(),
		UpdateID: update.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("select journal sequences: %w", err)
	}

	return seqs, nil
}

// JournalSweepable implements stack.JournalStore. It reports whether the journal is
// closed or the update no longer runs. A missing update is ErrUpdateNotFound.
func (s *Store) JournalSweepable(ctx context.Context, stackID stack.ID, update stack.UpdateID) (bool, error) {
	state, err := s.queries.JournalState(ctx, db.JournalStateParams{StackID: stackID.String(), ID: update.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, stack.ErrUpdateNotFound
	}

	if err != nil {
		return false, fmt.Errorf("read journal state: %w", err)
	}

	return state.JournalClosed || state.Status != string(stack.StatusRunning), nil
}

// PutBuiltCheckpoint implements stack.BuiltCheckpointStore. The checkpoint write is the
// one of PutCheckpoint, and the journal rows of the update are deleted in the same
// transaction after it. A failed check deletes nothing.
func (s *Store) PutBuiltCheckpoint(
	ctx context.Context,
	cp stack.Checkpoint,
	fence namespace.Fence,
) ([]blob.Key, error) {
	var keys []blob.Key

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, cp.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return stack.ErrWrongUpdateState
		}

		if err != nil {
			return err
		}

		row, rowErr := codec.ToCheckpointRow(cp, stack.ID(stackID))
		if rowErr != nil {
			return rowErr
		}

		if lockErr := lockForBuild(ctx, q, stackID, cp.Update); lockErr != nil {
			return lockErr
		}

		if _, writeErr := writeCheckpoint(ctx, unitStateTx{q: q, logger: s.logger}, cp, stackID, row); writeErr != nil {
			return writeErr
		}

		removed, closeErr := closeAndRemove(ctx, q, stackID, cp.Update)
		keys = removed

		return closeErr
	})
	if err != nil {
		return nil, err
	}

	return keys, nil
}

// closeAndRemove closes the journal of the update and removes its rows, in the caller's
// transaction. A journal that is already closed keeps no rows, so a retry of the build
// deletes nothing and returns no keys.
func closeAndRemove(ctx context.Context, q *db.Queries, stackID string, update stack.UpdateID) ([]blob.Key, error) {
	state, err := q.JournalState(ctx, db.JournalStateParams{StackID: stackID, ID: update.String()})
	if err != nil {
		return nil, fmt.Errorf("read journal state: %w", err)
	}

	if state.JournalClosed {
		return nil, nil
	}

	keys, err := removeJournal(ctx, q, stackID, update)
	if err != nil {
		return nil, err
	}

	if err := q.CloseJournal(ctx, db.CloseJournalParams{StackID: stackID, ID: update.String()}); err != nil {
		return nil, fmt.Errorf("close journal: %w", err)
	}

	return keys, nil
}

// removeJournal deletes the sequence rows and the batch rows of an update, and returns
// the blob keys of the batches it deleted. It runs in the caller's transaction.
func removeJournal(ctx context.Context, q *db.Queries, stackID string, update stack.UpdateID) ([]blob.Key, error) {
	found, err := q.JournalBlobKeys(ctx, db.JournalBlobKeysParams{StackID: stackID, UpdateID: update.String()})
	if err != nil {
		return nil, fmt.Errorf("select journal blob keys: %w", err)
	}

	seqErr := q.DeleteJournalSequences(
		ctx,
		db.DeleteJournalSequencesParams{StackID: stackID, UpdateID: update.String()},
	)
	if seqErr != nil {
		return nil, fmt.Errorf("delete journal sequences: %w", seqErr)
	}

	rowsErr := q.DeleteJournal(ctx, db.DeleteJournalParams{StackID: stackID, UpdateID: update.String()})
	if rowsErr != nil {
		return nil, fmt.Errorf("delete journal rows: %w", rowsErr)
	}

	refusalsErr := q.DeleteJournalRefusals(
		ctx,
		db.DeleteJournalRefusalsParams{StackID: stackID, UpdateID: update.String()},
	)
	if refusalsErr != nil {
		return nil, fmt.Errorf("delete journal refusals: %w", refusalsErr)
	}

	return journalBlobKeys(found), nil
}

// lockForBuild locks the update row FOR UPDATE, as the batch write does (#445), so a
// batch and the build of one update run one after the other.
func lockForBuild(ctx context.Context, q *db.Queries, stackID string, update stack.UpdateID) error {
	_, err := q.LockUpdateForJournal(ctx, db.LockUpdateForJournalParams{
		StackID: stackID,
		ID:      update.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrWrongUpdateState
	}

	if err != nil {
		return fmt.Errorf("lock update for journal: %w", err)
	}

	return nil
}

// journalBlobKeys converts stored blob keys to keys of the blob store.
func journalBlobKeys(rows []string) []blob.Key {
	keys := make([]blob.Key, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, blob.Key(row))
	}

	return keys
}
