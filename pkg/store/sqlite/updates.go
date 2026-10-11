package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

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
		})
		if err != nil {
			return fmt.Errorf("insert update: %w", err)
		}

		return nil
	})
}

// GetUpdate implements stack.UpdateStore.
func (s *Store) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return stack.Update{}, err
	}

	row, err := s.queries.GetUpdate(ctx, db.GetUpdateParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select update: %w", err)
	}

	return codec.FromUpdateRow(fromGetUpdateRow(row), ref)
}

// StartUpdate implements stack.UpdateStore. The write lock taken when the
// transaction begins makes the conflict check and version allocation atomic.
func (s *Store) StartUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) (stack.Update, error) {
	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stackID, err := stackIDFor(ctx, q, u.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return stack.ErrUpdateNotFound
		}

		if err != nil {
			return err
		}

		if u.Mutates() {
			u.Version, err = allocateVersion(ctx, q, stackID)
		} else {
			u.Version, err = q.GetStackVersion(ctx, db.GetStackVersionParams{
				Namespace: u.Ref.Namespace.String(),
				Project:   u.Ref.Project.String(),
				Name:      u.Ref.Stack.String(),
			})
			if errors.Is(err, sql.ErrNoRows) {
				return stack.ErrUpdateNotFound
			}
		}

		if err != nil {
			return err
		}

		return saveUpdate(ctx, q, u, stackID)
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
		stackID, err := stackIDFor(ctx, q, u.Ref)
		if errors.Is(err, stack.ErrNotFound) {
			return stack.ErrUpdateNotFound
		}

		if err != nil {
			return err
		}

		if saveErr := saveUpdate(ctx, q, u, stackID); saveErr != nil {
			return saveErr
		}

		if !u.Mutates() || !u.Status.Terminal() || u.Version == 0 {
			return nil
		}

		err = q.SetStackVersion(ctx, db.SetStackVersionParams{
			Version:     u.Version,
			UpdatedAtMs: codec.Millis(u.EndedAt),
			Namespace:   u.Ref.Namespace.String(),
			Project:     u.Ref.Project.String(),
			Name:        u.Ref.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("set stack version: %w", err)
		}

		return publishStackInventory(ctx, q, u.Ref, stackID, codec.Millis(u.EndedAt))
	})
}

// ActiveUpdate implements stack.UpdateStore.
func (s *Store) ActiveUpdate(ctx context.Context, ref stack.Ref) (stack.Update, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return stack.Update{}, err
	}

	row, err := s.queries.ActiveUpdate(ctx, stackID)
	if errors.Is(err, sql.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select active update: %w", err)
	}

	return codec.FromUpdateRow(fromActiveUpdateRow(row), ref)
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
		Limit:   int64(page.Limit),
		Offset:  int64(page.Offset),
	})
	if err != nil {
		return nil, fmt.Errorf("select updates: %w", err)
	}

	converted := make([]codec.UpdateRow, 0, len(rows))
	for i := range rows {
		converted = append(converted, fromListUpdateRow(rows[i]))
	}

	return codec.FromUpdateRows(converted, ref)
}

// PutCheckpoint implements stack.CheckpointStore. The deployment is stored in
// the row, one row for each mutating update; each write of the running
// update overwrites the row of that update.
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

// writeCheckpoint checks the lease, rejects previews, checks the sequence,
// then upserts the checkpoint of its update.
func writeCheckpoint(
	ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string, row codec.CheckpointRow,
) error {
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
		FeaturesJson:  string(row.Features),
		Deployment:    row.Deployment,
		SavedAtMs:     row.SavedAtMs,
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
	if errors.Is(err, sql.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
	}

	return codec.FromCheckpointRow(fromCheckpointRow(row), ref)
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
	if errors.Is(err, sql.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
	}

	return codec.FromCheckpointRow(fromCheckpointRow(row), ref)
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

// ListEvents implements stack.EventStore. Updates are never deleted, so the
// existence check and the read need no common snapshot.
func (s *Store) ListEvents(
	ctx context.Context, ref stack.Ref, id stack.UpdateID, after int64, limit int,
) ([]stack.Event, error) {
	stackID, err := s.stackID(ctx, ref)
	if err != nil {
		return nil, err
	}

	_, err = s.queries.GetUpdateLease(ctx, db.GetUpdateLeaseParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, stack.ErrUpdateNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("select update: %w", err)
	}

	rows, err := s.queries.ListEvents(ctx, db.ListEventsParams{
		StackID:  stackID,
		UpdateID: id.String(),
		Sequence: after,
		Limit:    int64(limit),
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
// does not exist. Reads use it to scope by ID; a missing stack then reads as
// no update, no state or no events, as before.
func (s *Store) stackID(ctx context.Context, ref stack.Ref) (string, error) {
	id, err := s.queries.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", stack.ErrUpdateNotFound
	}

	if err != nil {
		return "", fmt.Errorf("resolve stack: %w", err)
	}

	return id, nil
}

// stackIDFor resolves ref to its stable ID inside a fenced write, or
// ErrNotFound if the stack does not exist and ErrUpdateNotFound where an
// update is expected.
func stackIDFor(ctx context.Context, q *db.Queries, ref stack.Ref) (string, error) {
	id, err := q.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", stack.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("resolve stack: %w", err)
	}

	return id, nil
}

// allocateVersion returns the next version of a stack with no running
// mutating update, or ErrUpdateConflict.
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

	_, err = q.GetUpdateLease(ctx, db.GetUpdateLeaseParams{StackID: stackID, ID: u.ID.String()})

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return stack.ErrUpdateNotFound
	case err != nil:
		return fmt.Errorf("read update: %w", err)
	default:
		return stack.ErrConcurrentModification
	}
}

// checkLease returns ErrWrongUpdateState unless the update runs under lease.
func checkLease(ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID, lease []byte) error {
	row, err := q.GetUpdateLease(ctx, db.GetUpdateLeaseParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
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

// checkMutates returns ErrWrongUpdateState when the update is a preview or a
// dry run, which never saves state.
func checkMutates(ctx context.Context, q *db.Queries, stackID string, id stack.UpdateID) error {
	row, err := q.GetUpdateKind(ctx, db.GetUpdateKindParams{StackID: stackID, ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
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
// reports whether cp repeats it.
func checkSequence(ctx context.Context, q *db.Queries, cp stack.Checkpoint, stackID string) (bool, error) {
	last, err := q.GetCheckpointByUpdate(ctx, db.GetCheckpointByUpdateParams{
		StackID:  stackID,
		UpdateID: cp.Update.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
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

// fromGetUpdateRow converts a GetUpdate join row, with its history fields
// and checkpoint size, into a codec row.
func fromGetUpdateRow(row db.GetUpdateRow) codec.UpdateRow {
	return codec.UpdateRow{
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
		CheckpointBytes:  row.CheckpointBytes,
	}
}

// fromActiveUpdateRow converts an ActiveUpdate join row, with its history
// fields and checkpoint size, into a codec row.
func fromActiveUpdateRow(row db.ActiveUpdateRow) codec.UpdateRow {
	return codec.UpdateRow{
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
		CheckpointBytes:  row.CheckpointBytes,
	}
}

// fromListUpdateRow converts a ListUpdates join row, with its history fields
// and checkpoint size, into a codec row.
func fromListUpdateRow(row db.ListUpdatesRow) codec.UpdateRow {
	return codec.UpdateRow{
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
		CheckpointBytes:  row.CheckpointBytes,
	}
}

func fromCheckpointRow(row db.Checkpoint) codec.CheckpointRow {
	return codec.CheckpointRow{
		StackID:       row.StackID,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		Digest:        row.Digest,
		Features:      []byte(row.FeaturesJson),
		Deployment:    row.Deployment,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		SavedAtMs:     row.SavedAtMs,
		Invalid:       row.Invalid,
	}
}
