package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

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

		done, err := journalRetry(ctx, q, stackID, row)
		if err != nil {
			return err
		}

		retried = done
		if done {
			return nil
		}

		return insertJournalRow(ctx, q, stackID, row)
	})

	return retried, err
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
