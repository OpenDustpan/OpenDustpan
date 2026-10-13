package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
// row's check against the stored operations and inserts the row (false).
func writeJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) (bool, error) {
	done, err := journalRetry(ctx, q, stackID, row)
	if err != nil {
		return false, err
	}

	if done {
		return true, nil
	}

	if verifyErr := verifyJournalRow(ctx, q, stackID, row); verifyErr != nil {
		return false, verifyErr
	}

	return false, insertJournalRow(ctx, q, stackID, row)
}

// checkJournalRow runs the checks of a batch that the write needs before its retry
// check: the blob key, the lease and the update kind.
func checkJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) error {
	if err := checkJournalKey(stackID, row); err != nil {
		return err
	}

	if err := checkLease(ctx, q, stackID, row.Update, codec.EncodeDigest(row.Lease)); err != nil {
		return err
	}

	return checkMutates(ctx, q, stackID, row.Update)
}

// checkJournalKey refuses a row whose blob key does not belong to the stack and the
// update that the row names. The key is built by the caller, so a mismatch is a bug.
func checkJournalKey(stackID string, row stack.JournalRow) error {
	prefix := "pulumi/" + stackID + "/" + row.Update.String() + "/journal/"
	if !strings.HasPrefix(string(row.BlobKey), prefix) {
		return fmt.Errorf("%w: %q", stack.ErrJournalKey, row.BlobKey)
	}

	return nil
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
// that the update already holds. It runs in the same transaction as the write, so
// the list it reads is the list that the write follows (ADR 43, decision 6.4). A
// refusal wraps ErrJournalBatch.
func verifyJournalRow(ctx context.Context, q *db.Queries, stackID string, row stack.JournalRow) error {
	if row.Verify == nil {
		return nil
	}

	stored, err := q.ListJournalOperations(ctx, db.ListJournalOperationsParams{
		StackID:  stackID,
		UpdateID: row.Update.String(),
	})
	if err != nil {
		return fmt.Errorf("list journal operations: %w", err)
	}

	known, err := stack.KnownOperationIDs(stored)
	if err != nil {
		return err
	}

	if err := row.Verify(known); err != nil {
		return fmt.Errorf("%w: %w", stack.ErrJournalBatch, err)
	}

	return nil
}

// JournalKeys implements stack.JournalStore. A missing stack or update yields no keys.
func (s *Store) JournalKeys(ctx context.Context, ref stack.Ref, update stack.UpdateID) ([]blob.Key, error) {
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := s.queries.JournalBlobKeys(ctx, db.JournalBlobKeysParams{StackID: stackID, UpdateID: update.String()})
	if err != nil {
		return nil, fmt.Errorf("select journal blob keys: %w", err)
	}

	return journalBlobKeys(rows), nil
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

		if writeErr := writeCheckpoint(ctx, q, cp, stackID, row); writeErr != nil {
			return writeErr
		}

		keys, err = removeJournal(ctx, q, stackID, cp.Update)

		return err
	})
	if err != nil {
		return nil, err
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
