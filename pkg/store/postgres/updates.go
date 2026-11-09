package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// Locking, so that concurrent writers serialize where they must and never
// deadlock. Every write first takes the namespace row FOR SHARE (the fence).
//   - Starting and saving an update lock the stack row FOR NO KEY UPDATE, so
//     the conflict check, version allocation and stack version change are
//     atomic per stack.
//   - Checkpoint and event writes lock the update row FOR SHARE, so a cancel,
//     which updates that row, waits for them, and they see its result after.
//
// The foreign-key checks of those writes take FOR KEY SHARE on the stack,
// which NO KEY UPDATE does not block.

var (
	_ stack.UpdateStore     = (*Store)(nil)
	_ stack.CheckpointStore = (*Store)(nil)
	_ stack.EventStore      = (*Store)(nil)
)

// CreateUpdate implements stack.UpdateStore.
func (s *Store) CreateUpdate(
	ctx context.Context, u stack.Update, key stack.UpdateKey, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, u.Ref)
		if err != nil {
			return err
		}

		// The reference names the stack that this transaction resolved, so
		// a rename of the stack cannot give the key of the update to
		// another stack.
		item, keyErr := key.RefFor(u.Ref.Namespace, stack.ID(stackID), u.ID)
		if keyErr != nil {
			return keyErr
		}

		row := codec.ToUpdateRow(u, stack.ID(stackID))

		err = q.CreateUpdate(ctx, db.CreateUpdateParams{
			StackID:          row.StackID,
			ID:               row.ID,
			Kind:             row.Kind,
			DryRun:           row.DryRun,
			Status:           row.Status,
			Version:          row.Version,
			Revision:         row.Revision,
			LeaseDigest:      row.LeaseDigest,
			LeaseExpiresAtMs: row.LeaseExpiresAtMs,
			CreatedAtMs:      row.CreatedAtMs,
			StartedAtMs:      row.StartedAtMs,
			EndedAtMs:        row.EndedAtMs,
			CreatedBy:        row.CreatedBy,
			RestoredFrom:     row.RestoredFrom,
			TokenID:          row.TokenID,
			JobID:            row.JobID,
			GitHead:          row.GitHead,
		})
		if err != nil {
			if isForeignKeyViolation(err) {
				// A concurrent DeleteStack removed the stack after the
				// resolve above. CreateUpdate takes no stack lock, so the
				// insert meets the missing stack here.
				return stack.ErrNotFound
			}

			return fmt.Errorf("insert update: %w", err)
		}

		// The record of the item key commits with the update row: a failed
		// key write rolls the update back.
		if _, err := key.NewKey(ctx, itemKeyTx{q: q, fence: fence}, item, fence); err != nil {
			return fmt.Errorf("item key of update %s: %w", u.ID, err)
		}

		return nil
	})
}

// foreignKeyViolation is the SQLSTATE of a PostgreSQL foreign-key violation.
const foreignKeyViolation = "23503"

// isForeignKeyViolation reports whether err wraps a PostgreSQL foreign-key
// violation.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation
}

// GetUpdate implements stack.UpdateStore. One statement finds the stack and
// reads the update, so the read needs no transaction.
func (s *Store) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	row, err := s.queries.GetUpdate(ctx, db.GetUpdateParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
		ID:        id.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select update: %w", readConflict(err))
	}

	return codec.FromUpdateRow(codec.UpdateRow{
		StackID:          row.StackID,
		ID:               row.ID,
		Kind:             row.Kind,
		Status:           row.Status,
		LeaseDigest:      row.LeaseDigest,
		Version:          row.Version,
		Revision:         row.Revision,
		LeaseExpiresAtMs: row.LeaseExpiresAtMs,
		CreatedAtMs:      row.CreatedAtMs,
		StartedAtMs:      row.StartedAtMs,
		EndedAtMs:        row.EndedAtMs,
		DryRun:           row.DryRun,
		CreatedBy:        row.CreatedBy,
		RestoredFrom:     row.RestoredFrom,
		TokenID:          row.TokenID,
		JobID:            row.JobID,
		GitHead:          row.GitHead,
		JournalVersion:   row.JournalVersion,
		CheckpointBytes:  row.CheckpointBytes,
	}, ref)
}

// StartUpdate implements stack.UpdateStore.
func (s *Store) StartUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) (stack.Update, error) {
	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, u.Ref)
		if err != nil {
			return err
		}

		u.Version = locked.Version
		if u.Mutates() {
			if u.Version, err = allocateVersion(ctx, q, locked.ID); err != nil {
				return err
			}
		}

		if err := saveUpdate(ctx, q, u, locked.ID); err != nil {
			return err
		}

		// A started mutating update holds the stack lock: publish it in the
		// same transaction. Previews and dry runs never lock.
		return publishStartLock(ctx, q, u)
	})
	if err != nil {
		return stack.Update{}, err
	}

	return u, nil
}

// SaveUpdate implements stack.UpdateStore. When a mutating update ends, the
// stack version moves and the inventory entry moves with it, in the same
// transaction.
func (s *Store) SaveUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		locked, err := lockStack(ctx, q, u.Ref)
		if err != nil {
			return err
		}

		if saveErr := saveUpdate(ctx, q, u, locked.ID); saveErr != nil {
			return saveErr
		}

		if journalErr := finishJournalEnd(ctx, q, locked.ID, u); journalErr != nil {
			return journalErr
		}

		if !u.Mutates() || !u.Status.Terminal() || u.Version == 0 {
			return nil
		}

		return endMutatingUpdate(ctx, q, u, locked.ID)
	})
}

// endMutatingUpdate moves the stack version and the inventory entry of the
// stack stackID to the end of the mutating update u, releases the stack lock,
// and appends the change log row. The change log row comes last, after every
// other lock.
func endMutatingUpdate(ctx context.Context, q *db.Queries, u stack.Update, stackID string) error {
	err := q.SetStackVersion(ctx, db.SetStackVersionParams{
		Version:     u.Version,
		UpdatedAtMs: codec.Millis(u.EndedAt),
		Namespace:   u.Ref.Namespace.String(),
		Project:     u.Ref.Project.String(),
		Name:        u.Ref.Stack.String(),
	})
	if err != nil {
		return fmt.Errorf("set stack version: %w", err)
	}

	endedMs := codec.Millis(u.EndedAt)

	if upsertErr := upsertStackInventory(ctx, q, u.Ref, stackID, endedMs); upsertErr != nil {
		return upsertErr
	}

	if lockErr := writeInventoryLock(ctx, q, stackInventoryKey(u), nil); lockErr != nil {
		return lockErr
	}

	// The end of a mutating update is final, whatever its result: the current
	// state version of the unit closes the write-back jobs that wait for it.
	// The origin is the one of the update: the job whose token started it and
	// the commit it reported.
	pubErr := publishUnitState(ctx, q, unitStateWrite{
		kind: unitStateWriteEnd, nsName: u.Ref.Namespace.String(), unitID: stackID,
		version: "", final: false, origin: u.Origin(), atMs: 0,
	})
	if pubErr != nil {
		return pubErr
	}

	return appendStackChange(ctx, q, u.Ref, unitWrite{unitID: stackID, atMs: endedMs, kind: inventory.ChangeChanged})
}

// ActiveUpdate implements stack.UpdateStore. One statement finds the stack
// and reads the update, so the read needs no transaction.
func (s *Store) ActiveUpdate(ctx context.Context, ref stack.Ref) (stack.Update, error) {
	row, err := s.queries.ActiveUpdate(ctx, db.ActiveUpdateParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select active update: %w", readConflict(err))
	}

	return codec.FromUpdateRow(codec.UpdateRow{
		StackID:          row.StackID,
		ID:               row.ID,
		Kind:             row.Kind,
		Status:           row.Status,
		LeaseDigest:      row.LeaseDigest,
		Version:          row.Version,
		Revision:         row.Revision,
		LeaseExpiresAtMs: row.LeaseExpiresAtMs,
		CreatedAtMs:      row.CreatedAtMs,
		StartedAtMs:      row.StartedAtMs,
		EndedAtMs:        row.EndedAtMs,
		DryRun:           row.DryRun,
		CreatedBy:        row.CreatedBy,
		RestoredFrom:     row.RestoredFrom,
		TokenID:          row.TokenID,
		JobID:            row.JobID,
		GitHead:          row.GitHead,
		JournalVersion:   row.JournalVersion,
		CheckpointBytes:  row.CheckpointBytes,
	}, ref)
}

// CountRunningJournaledUpdates returns the count of updates that run now
// with journal version 1 or higher. The upgrade check answers it, so the
// report can warn before a downgrade to a binary without the journal route.
func (s *Store) CountRunningJournaledUpdates(ctx context.Context) (int64, error) {
	count, err := s.queries.CountRunningJournaledUpdates(ctx)
	if err != nil {
		return 0, fmt.Errorf("count running journaled updates: %w", err)
	}

	return count, nil
}

// ListUpdates implements stack.UpdateStore.
func (s *Store) ListUpdates(ctx context.Context, ref stack.Ref, page stack.Page) ([]stack.Update, error) {
	var out []stack.Update

	// One snapshot, so the statements of the read see one state.
	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		var err error

		out, err = listUpdatesWith(ctx, q, ref, page)

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// listUpdatesWith is ListUpdates on the queries of one snapshot.
func listUpdatesWith(ctx context.Context, q *db.Queries, ref stack.Ref, page stack.Page) ([]stack.Update, error) {
	stackID, err := lookupStackID(ctx, q, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := q.ListUpdates(ctx, db.ListUpdatesParams{
		StackID: stackID,
		Limit:   clampInt32(page.Limit),
		Offset:  clampInt32(page.Offset),
	})
	if err != nil {
		return nil, fmt.Errorf("select updates: %w", err)
	}

	converted := make([]codec.UpdateRow, 0, len(rows))
	for i := range rows {
		converted = append(converted, codec.UpdateRow{
			StackID:          rows[i].StackID,
			ID:               rows[i].ID,
			Kind:             rows[i].Kind,
			Status:           rows[i].Status,
			LeaseDigest:      rows[i].LeaseDigest,
			Version:          rows[i].Version,
			Revision:         rows[i].Revision,
			LeaseExpiresAtMs: rows[i].LeaseExpiresAtMs,
			CreatedAtMs:      rows[i].CreatedAtMs,
			StartedAtMs:      rows[i].StartedAtMs,
			EndedAtMs:        rows[i].EndedAtMs,
			DryRun:           rows[i].DryRun,
			CreatedBy:        rows[i].CreatedBy,
			RestoredFrom:     rows[i].RestoredFrom,
			TokenID:          rows[i].TokenID,
			JobID:            rows[i].JobID,
			GitHead:          rows[i].GitHead,
			JournalVersion:   rows[i].JournalVersion,
			CheckpointBytes:  rows[i].CheckpointBytes,
		})
	}

	return codec.FromUpdateRows(converted, ref)
}

// PutCheckpoint implements stack.CheckpointStore. The row keeps the blob key
// with the size, the digest and the sequence, and no content. A checkpoint
// without a blob key returns stack.ErrCheckpointNoKey. Each write of the
// running update overwrites the row of that update. The row stores no
// resource count, so the save path never scans the deployment.
func (s *Store) PutCheckpoint(ctx context.Context, cp stack.Checkpoint, fence namespace.Fence) (bool, error) {
	var known bool

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

		var writeErr error

		known, writeErr = writeCheckpoint(ctx, q, cp, stackID, row)

		return writeErr
	})
	if err != nil {
		return false, err
	}

	return known, nil
}

// checkCheckpointKey refuses a checkpoint whose blob key does not belong to the
// namespace, the stack and the update of the row. The service builds the key,
// so a mismatch is a bug or an attack, and no row names the blob.
func checkCheckpointKey(ctx context.Context, q *db.Queries, stackID string, cp stack.Checkpoint) error {
	nsID, err := q.GetNamespaceID(ctx, cp.Ref.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: the namespace of the row does not exist", stack.ErrCheckpointKey)
	}

	if err != nil {
		return fmt.Errorf("read namespace ID: %w", err)
	}

	return stack.CheckCheckpointBlobKey(namespace.ID(nsID), stack.ID(stackID), cp.Update, cp.BlobKey)
}

// writeCheckpoint runs writeCheckpointRows and makes sure that the attempt
// window of cp was checked against the clock of the database after every lock
// wait of the transaction. The upsert returns the time of that clock after its
// own lock wait, so the common save pays no round trip for the check. A write
// that did not reach the upsert (a replay) or that lost it reads the clock
// here, after the last lock wait. A refusal returns the error, and the caller
// rolls the transaction back: the attempt writes nothing, also when it repeats
// a committed checkpoint.
func writeCheckpoint(
	ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string, row codec.CheckpointRow,
) (bool, error) {
	write, err := writeCheckpointRows(ctx, q, cp, stackID, row)
	if err != nil {
		return false, err
	}

	if write.checked {
		return write.known, nil
	}

	if attemptErr := checkAttempt(ctx, q, cp.AttemptStartedAt); attemptErr != nil {
		return false, attemptErr
	}

	return write.known, nil
}

// checkpointWrite is the result of writeCheckpointRows. known is true for a
// replay. checked is true when the upsert wrote the row and the attempt window
// was checked with the clock that the upsert returned.
type checkpointWrite struct {
	known   bool
	checked bool
}

// writeCheckpointRows checks the key, the lease, rejects previews, checks the sequence,
// then upserts the checkpoint of its update. It returns true when the last checkpoint of the
// update has the same sequence and digest: the write is a replay and the row keeps its blob key.
// The result says whether the upsert wrote the row, and so whether the attempt window was checked
// with the clock that the upsert returned.
func writeCheckpointRows(
	ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string, row codec.CheckpointRow,
) (checkpointWrite, error) {
	if keyErr := checkCheckpointKey(ctx, q, stackID, cp); keyErr != nil {
		return checkpointWrite{known: false, checked: false}, keyErr
	}

	if leaseErr := checkLease(ctx, q, stackID, cp.Update, row.LeaseDigest); leaseErr != nil {
		return checkpointWrite{known: false, checked: false}, leaseErr
	}

	if previewErr := checkMutates(ctx, q, stackID, cp.Update); previewErr != nil {
		return checkpointWrite{known: false, checked: false}, previewErr
	}

	if cp.Sequence > 0 {
		retried, seqErr := checkSequence(ctx, q, cp, stackID)
		if seqErr != nil || retried {
			return checkpointWrite{known: retried, checked: false}, seqErr
		}
	}

	nowMs, putErr := q.PutCheckpoint(ctx, db.PutCheckpointParams{
		StackID:       row.StackID,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		Digest:        row.Digest,
		Invalid:       row.Invalid,
		Features:      row.Features,
		SavedAtMs:     row.SavedAtMs,
		BlobKey:       row.BlobKey,
		Size:          pgtype.Int8{Int64: row.Size, Valid: true},
		BlobSize:      pgtype.Int8{Int64: row.BlobSize, Valid: true},
		BlobMd5:       row.BlobMD5,
	})
	if errors.Is(putErr, pgx.ErrNoRows) {
		// The write lost the race and wrote no row.
		raced, raceErr := lostCheckpointRace(ctx, q, cp, stackID)

		return checkpointWrite{known: raced, checked: false}, raceErr
	}

	if putErr != nil {
		return checkpointWrite{known: false, checked: false}, fmt.Errorf("upsert checkpoint: %w", putErr)
	}

	if attemptErr := checkAttemptAt(cp.AttemptStartedAt, nowMs); attemptErr != nil {
		return checkpointWrite{known: false, checked: false}, attemptErr
	}

	// The digest of the stored row is the state version of the unit: a plan of
	// the state that this checkpoint replaced is stale. The write pays no
	// round trip for the time: it is the clock that the upsert returned.
	if pubErr := publishCheckpointState(ctx, q, cp, row, nowMs); pubErr != nil {
		return checkpointWrite{known: false, checked: false}, pubErr
	}

	return checkpointWrite{known: false, checked: true}, nil
}

// publishCheckpointState records the digest of the stored checkpoint row as
// the current state version of its stack, at the time atMs of the write. A
// checkpoint that the row marks invalid carries a version of its own, so it
// changes the version of a valid write that the same update replaced.
func publishCheckpointState(
	ctx context.Context, q *db.Queries, cp stack.Checkpoint, row codec.CheckpointRow, atMs int64,
) error {
	return publishUnitState(ctx, q, unitStateWrite{
		kind:    unitStateWriteState,
		nsName:  cp.Ref.Namespace.String(),
		unitID:  row.StackID,
		version: checkpointDigest{digest: row.Digest, invalid: row.Invalid}.stateVersion(),
		final:   false,
		origin:  cp.Origin,
		atMs:    atMs,
	})
}

// lostCheckpointRace answers an upsert that wrote no row: a write of the same
// sequence, or of a higher one, landed after the read of checkSequence and
// before this write. The row of the update decided, so this write is a replay
// when the sequence and the digest are the same, a conflict when only the
// sequence is the same, and stale when the stored sequence is higher.
//
// Defense in depth: checkSequence answers the same cases from an earlier read.
// This answer reads the row after the write that won, and it does not rely on
// that earlier read. It fails closed with ErrCheckpointConflict for a row that
// does not explain the lost write.
func lostCheckpointRace(ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string) (bool, error) {
	retried, err := checkSequence(ctx, q, cp, stackID)
	if err != nil || retried {
		return retried, err
	}

	return false, stack.ErrCheckpointConflict
}

// LatestCheckpoint implements stack.CheckpointStore. It returns the
// checkpoint of the stack's highest version that has one. One statement finds
// the stack and reads the checkpoint, so the read needs no transaction.
func (s *Store) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	row, err := s.queries.GetLatestCheckpointByRef(ctx, db.GetLatestCheckpointByRefParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", readConflict(err))
	}

	return codec.FromCheckpointRow(codec.CheckpointRow{
		StackID:       row.StackID,
		NamespaceID:   row.NamespaceID,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		Digest:        row.Digest,
		Features:      row.Features,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		SavedAtMs:     row.SavedAtMs,
		Invalid:       row.Invalid,
		BlobKey:       row.BlobKey,
		Size:          row.Size.Int64,
		BlobSize:      row.BlobSize.Int64,
		BlobMD5:       row.BlobMd5,
	}, ref)
}

// CheckpointByVersion implements stack.CheckpointStore. One statement finds
// the stack and reads the checkpoint, so the read needs no transaction.
func (s *Store) CheckpointByVersion(
	ctx context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	row, err := s.queries.GetCheckpointByVersion(ctx, db.GetCheckpointByVersionParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
		Version:   version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", readConflict(err))
	}

	return codec.FromCheckpointRow(codec.CheckpointRow{
		StackID:       row.StackID,
		NamespaceID:   row.NamespaceID,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		Digest:        row.Digest,
		Features:      row.Features,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		SavedAtMs:     row.SavedAtMs,
		Invalid:       row.Invalid,
		BlobKey:       row.BlobKey,
		Size:          row.Size.Int64,
		BlobSize:      row.BlobSize.Int64,
		BlobMD5:       row.BlobMd5,
	}, ref)
}

// CheckpointByUpdate implements stack.CheckpointStore. One statement finds
// the stack and reads the checkpoint, so the read needs no transaction.
func (s *Store) CheckpointByUpdate(
	ctx context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	row, err := s.queries.GetCheckpointByUpdateRef(ctx, db.GetCheckpointByUpdateRefParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
		UpdateID:  id.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", readConflict(err))
	}

	return codec.FromCheckpointRow(codec.CheckpointRow{
		StackID:       row.StackID,
		NamespaceID:   row.NamespaceID,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		Digest:        row.Digest,
		Features:      row.Features,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		SavedAtMs:     row.SavedAtMs,
		Invalid:       row.Invalid,
		BlobKey:       row.BlobKey,
		Size:          row.Size.Int64,
		BlobSize:      row.BlobSize.Int64,
		BlobMD5:       row.BlobMd5,
	}, ref)
}

// ReferencedBlobKeys implements stack.CheckpointStore. One statement
// finds the stack and reads the keys, so the read needs no transaction. A
// stack that does not exist, or that is soft-deleted, returns ErrNotFound.
func (s *Store) ReferencedBlobKeys(ctx context.Context, stackID stack.ID) ([]blob.Key, error) {
	rows, err := s.queries.ListReferencedBlobKeys(ctx, stackID.String())
	if err != nil {
		return nil, fmt.Errorf("select referenced blob keys: %w", readConflict(err))
	}

	// No row means no live stack with this ID: fail closed, so a sweep
	// deletes nothing.
	if len(rows) == 0 {
		return nil, stack.ErrNotFound
	}

	out := make([]blob.Key, 0, len(rows))

	for _, row := range rows {
		// A live stack without keys gives one row with an empty key.
		if row == "" {
			continue
		}

		key, err := blob.ParseKey(row)
		if err != nil {
			return nil, fmt.Errorf("%w: blob key %q: %w", codec.ErrCorruptRow, row, err)
		}

		out = append(out, key)
	}

	return out, nil
}

// AppendEvents implements stack.EventStore.
func (s *Store) AppendEvents(ctx context.Context, batch stack.EventBatch, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, batch.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return stack.ErrWrongUpdateState
		}

		if err != nil {
			return err
		}

		if err := checkLease(ctx, q, stackID, batch.Update, codec.EncodeDigest(batch.Lease)); err != nil {
			return err
		}

		for _, e := range batch.Events {
			err := q.AppendEvent(ctx, db.AppendEventParams{
				StackID:     stackID,
				UpdateID:    batch.Update.String(),
				Sequence:    e.Sequence,
				TimestampMs: codec.Millis(e.Timestamp),
				Payload:     e.Payload,
			})
			if err != nil {
				return fmt.Errorf("insert event %d: %w", e.Sequence, err)
			}
		}

		return nil
	})
}

// ListEvents implements stack.EventStore. One statement finds the stack,
// checks the update and reads the page of events, so the read needs no
// transaction. No stack and no update both report ErrUpdateNotFound. After a
// stack delete, the name no longer finds the stack.
func (s *Store) ListEvents(
	ctx context.Context, ref stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	rows, err := s.queries.ListEvents(ctx, db.ListEventsParams{
		After:     after,
		PageLimit: clampInt32(limit),
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
		ID:        id.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("select events: %w", readConflict(err))
	}

	// The update row comes back even with no event in the page, so no row
	// means no stack or no update.
	if len(rows) == 0 {
		return nil, stack.ErrUpdateNotFound
	}

	events := make([]stack.Event, 0, len(rows))
	for _, row := range rows {
		if !row.HasEvent {
			continue
		}

		events = append(events, stack.Event{
			Timestamp: codec.FromMillis(row.TimestampMs),
			Payload:   row.Payload,
			Sequence:  row.Sequence,
		})
	}

	return events, nil
}

// stackID resolves ref to its stable ID, or ErrUpdateNotFound if the stack
// does not exist.
func lookupStackID(ctx context.Context, q *db.Queries, ref stack.Ref) (string, error) {
	id, err := q.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", stack.ErrUpdateNotFound
	}

	if err != nil {
		return "", fmt.Errorf("resolve stack: %w", err)
	}

	return id, nil
}

// stackIDFor resolves ref to its stable ID inside a fenced write, or
// ErrNotFound if the stack does not exist.
func stackIDFor(ctx context.Context, q *db.Queries, ref stack.Ref) (string, error) {
	id, err := q.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", stack.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("resolve stack: %w", err)
	}

	return id, nil
}

// lockedStack is a stack row held FOR NO KEY UPDATE with its stable ID.
type lockedStack struct {
	ID      string
	Version int64
}

// lockStack locks the stack row for a start or end of one of its updates and
// returns its ID and version.
func lockStack(ctx context.Context, q *db.Queries, ref stack.Ref) (lockedStack, error) {
	row, err := q.LockStackForUpdates(ctx, db.LockStackForUpdatesParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedStack{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return lockedStack{}, fmt.Errorf("lock stack: %w", err)
	}

	return lockedStack{ID: row.ID, Version: row.Version}, nil
}

// allocateVersion returns the next version of a stack with no running
// mutating update, or the error of refuseRunningStack, which names the running
// update. The caller holds the stack lock.
func allocateVersion(ctx context.Context, q *db.Queries, stackID string) (int64, error) {
	if err := refuseRunningStack(ctx, q, stackID); err != nil {
		return 0, err
	}

	highest, err := q.HighestUpdateVersion(ctx, stackID)
	if err != nil {
		return 0, fmt.Errorf("read highest update version: %w", err)
	}

	return highest + 1, nil
}

// saveUpdate writes u if the store holds u.Revision-1.
func saveUpdate(ctx context.Context, q *db.Queries, u stack.Update, stackID string) error {
	row := codec.ToUpdateRow(u, stack.ID(stackID))

	n, err := q.SaveUpdate(ctx, db.SaveUpdateParams{
		Status:           row.Status,
		Version:          row.Version,
		Revision:         row.Revision,
		LeaseDigest:      row.LeaseDigest,
		LeaseExpiresAtMs: row.LeaseExpiresAtMs,
		StartedAtMs:      row.StartedAtMs,
		EndedAtMs:        row.EndedAtMs,
		JournalVersion:   row.JournalVersion,
		TokenID:          row.TokenID,
		JobID:            row.JobID,
		StackID:          row.StackID,
		ID:               row.ID,
		ExpectedRevision: row.Revision - 1,
	})
	if err != nil {
		return fmt.Errorf("update update: %w", err)
	}

	if n > 0 {
		return nil
	}

	exists, err := q.UpdateExists(ctx, db.UpdateExistsParams{StackID: stackID, ID: u.ID.String()})

	switch {
	case err != nil:
		return fmt.Errorf("check update: %w", err)
	case !exists:
		return stack.ErrUpdateNotFound
	default:
		return stack.ErrConcurrentModification
	}
}

// checkLease locks the update FOR SHARE and returns ErrWrongUpdateState
// unless it runs under lease.
func checkLease(ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID, lease []byte) error {
	row, err := q.GetUpdateLeaseForShare(ctx, db.GetUpdateLeaseForShareParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrWrongUpdateState
	}

	if err != nil {
		return fmt.Errorf("read update lease: %w", err)
	}

	running := stack.UpdateStatus(row.Status) == stack.StatusRunning
	if !running || len(lease) == 0 || !bytes.Equal(row.LeaseDigest, lease) {
		return stack.ErrWrongUpdateState
	}

	return nil
}

// checkMutates locks the update FOR SHARE and returns ErrWrongUpdateState
// when it is a preview or a dry run, which never saves state.
func checkMutates(ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID) error {
	row, err := q.GetUpdateKindForShare(ctx, db.GetUpdateKindForShareParams{
		StackID: stackID,
		ID:      id.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrWrongUpdateState
	}

	if err != nil {
		return fmt.Errorf("read update kind: %w", err)
	}

	if row.DryRun || stack.UpdateKind(row.Kind) == stack.KindPreview {
		return stack.ErrWrongUpdateState
	}

	return nil
}

// checkSequence compares cp with the last checkpoint of its update and
// reports whether cp repeats it. The caller holds the update FOR SHARE and
// the update runs, so no other checkpoint of the stack can be written
// concurrently: one mutating update runs per stack.
func checkSequence(ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string) (bool, error) {
	last, err := q.GetCheckpointByUpdate(ctx, db.GetCheckpointByUpdateParams{
		StackID:  stackID,
		UpdateID: cp.Update.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read last checkpoint: %w", err)
	}

	switch {
	case cp.Sequence < last.Sequence:
		return false, stack.ErrStaleCheckpoint
	case cp.Sequence > last.Sequence:
		return false, nil
	case bytes.Equal(last.Digest, cp.Digest[:]):
		return true, nil
	default:
		return false, stack.ErrCheckpointConflict
	}
}

// clampInt32 bounds n to the range of a PostgreSQL integer parameter.
func clampInt32(n int) int32 {
	return int32(max(min(n, math.MaxInt32), 0))
}
