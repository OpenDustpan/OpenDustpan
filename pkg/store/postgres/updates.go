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
func (s *Store) CreateUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, u.Ref)
		if err != nil {
			return err
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

// GetUpdate implements stack.UpdateStore.
func (s *Store) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return stack.Update{}, err
	}

	row, err := s.queries.GetUpdate(ctx, db.GetUpdateParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select update: %w", err)
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

	return appendStackChange(ctx, q, u.Ref, unitWrite{unitID: stackID, atMs: endedMs, kind: inventory.ChangeChanged})
}

// ActiveUpdate implements stack.UpdateStore.
func (s *Store) ActiveUpdate(ctx context.Context, ref stack.Ref) (stack.Update, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return stack.Update{}, err
	}

	row, err := s.queries.ActiveUpdate(ctx, stackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select active update: %w", err)
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
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListUpdates(ctx, db.ListUpdatesParams{
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
func (s *Store) PutCheckpoint(ctx context.Context, cp stack.Checkpoint, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
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

		return writeCheckpoint(ctx, q, cp, stackID, row)
	})
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

// writeCheckpoint checks the key, the lease, rejects previews, checks the sequence,
// then upserts the checkpoint of its update.
func writeCheckpoint(
	ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string, row codec.CheckpointRow,
) error {
	if keyErr := checkCheckpointKey(ctx, q, stackID, cp); keyErr != nil {
		return keyErr
	}

	if leaseErr := checkLease(ctx, q, stackID, cp.Update, row.LeaseDigest); leaseErr != nil {
		return leaseErr
	}

	if previewErr := checkMutates(ctx, q, stackID, cp.Update); previewErr != nil {
		return previewErr
	}

	if cp.Sequence > 0 {
		retried, seqErr := checkSequence(ctx, q, cp, stackID)
		if seqErr != nil || retried {
			return seqErr
		}
	}

	putErr := q.PutCheckpoint(ctx, db.PutCheckpointParams{
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
	if putErr != nil {
		return fmt.Errorf("upsert checkpoint: %w", putErr)
	}

	return nil
}

// LatestCheckpoint implements stack.CheckpointStore. It returns the
// checkpoint of the stack's highest version that has one.
func (s *Store) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, err
	}

	row, err := s.queries.GetLatestCheckpoint(ctx, stackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
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

// CheckpointByVersion implements stack.CheckpointStore.
func (s *Store) CheckpointByVersion(
	ctx context.Context, ref stack.Ref, version int64,
) (stack.Checkpoint, error) {
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, err
	}

	row, err := s.queries.GetCheckpointByVersion(ctx, db.GetCheckpointByVersionParams{
		StackID: stackID,
		Version: version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
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

// CheckpointByUpdate implements stack.CheckpointStore.
func (s *Store) CheckpointByUpdate(
	ctx context.Context, ref stack.Ref, id stack.UpdateID,
) (stack.Checkpoint, error) {
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, err
	}

	row, err := s.queries.GetCheckpointByUpdate(ctx, db.GetCheckpointByUpdateParams{
		StackID:  stackID,
		UpdateID: id.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
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

// ReferencedCheckpointKeys implements stack.CheckpointStore.
func (s *Store) ReferencedCheckpointKeys(ctx context.Context, ref stack.Ref) ([]blob.Key, error) {
	stackID, err := s.stackID(ctx, ref)
	if errors.Is(err, stack.ErrUpdateNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListCheckpointBlobKeys(ctx, stackID)
	if err != nil {
		return nil, fmt.Errorf("select checkpoint blob keys: %w", err)
	}

	out := make([]blob.Key, 0, len(rows))

	for _, row := range rows {
		key, err := blob.ParseKey(row)
		if err != nil {
			return nil, fmt.Errorf("%w: checkpoint key %q: %w", codec.ErrCorruptRow, row, err)
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

// ListEvents implements stack.EventStore. A stack delete is a soft delete
// that keeps the updates and events of the stack (ADR 47), so a delete
// between the lookup of the stack ID and the read still answers the events.
// After the delete, the name no longer finds the stack.
func (s *Store) ListEvents(
	ctx context.Context, ref stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return nil, err
	}

	exists, err := s.queries.UpdateExists(ctx, db.UpdateExistsParams{StackID: stackID, ID: id.String()})
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}

	if !exists {
		return nil, stack.ErrUpdateNotFound
	}

	rows, err := s.queries.ListEvents(ctx, db.ListEventsParams{
		StackID:  stackID,
		UpdateID: id.String(),
		Sequence: after,
		Limit:    clampInt32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select events: %w", err)
	}

	events := make([]stack.Event, 0, len(rows))
	for _, row := range rows {
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
func (s *Store) stackID(ctx context.Context, ref stack.Ref) (string, error) {
	id, err := s.queries.GetStackID(ctx, db.GetStackIDParams{
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
// mutating update, or ErrUpdateConflict. The caller holds the stack lock.
func allocateVersion(ctx context.Context, q *db.Queries, stackID string) (int64, error) {
	running, err := q.RunningMutatingUpdates(ctx, stackID)
	if err != nil {
		return 0, fmt.Errorf("count running updates: %w", err)
	}

	if running > 0 {
		return 0, stack.ErrUpdateConflict
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
