package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/internal/codec"
	"opendustpan.org/opendustpan/store/postgres/internal/db"
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
	row := codec.ToUpdateRow(u)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		exists, err := q.StackExists(ctx, db.StackExistsParams{
			Namespace: row.Namespace,
			Project:   row.Project,
			Name:      row.Stack,
		})
		if err != nil {
			return fmt.Errorf("check stack: %w", err)
		}

		if !exists {
			return stack.ErrNotFound
		}

		err = q.CreateUpdate(ctx, db.CreateUpdateParams{
			Namespace:        row.Namespace,
			Project:          row.Project,
			Stack:            row.Stack,
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
		})
		if err != nil {
			return fmt.Errorf("insert update: %w", err)
		}

		return nil
	})
}

// GetUpdate implements stack.UpdateStore.
func (s *Store) GetUpdate(ctx context.Context, ref stack.Ref, id stack.UpdateID) (stack.Update, error) {
	row, err := s.queries.GetUpdate(ctx, db.GetUpdateParams(updateParams(ref, id)))
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select update: %w", err)
	}

	return codec.FromUpdateRow(fromUpdateRow(&row))
}

// StartUpdate implements stack.UpdateStore.
func (s *Store) StartUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) (stack.Update, error) {
	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		current, err := lockStack(ctx, q, u.Ref)
		if err != nil {
			return err
		}

		u.Version = current
		if u.Mutates() {
			if u.Version, err = allocateVersion(ctx, q, u.Ref); err != nil {
				return err
			}
		}

		return saveUpdate(ctx, q, u)
	})
	if err != nil {
		return stack.Update{}, err
	}

	return u, nil
}

// SaveUpdate implements stack.UpdateStore.
func (s *Store) SaveUpdate(ctx context.Context, u stack.Update, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if _, err := lockStack(ctx, q, u.Ref); err != nil {
			return err
		}

		if err := saveUpdate(ctx, q, u); err != nil {
			return err
		}

		if !u.Mutates() || !u.Status.Terminal() || u.Version == 0 {
			return nil
		}

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

		return nil
	})
}

// ActiveUpdate implements stack.UpdateStore.
func (s *Store) ActiveUpdate(ctx context.Context, ref stack.Ref) (stack.Update, error) {
	row, err := s.queries.ActiveUpdate(ctx, db.ActiveUpdateParams(stackParams(ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	if err != nil {
		return stack.Update{}, fmt.Errorf("select active update: %w", err)
	}

	return codec.FromUpdateRow(fromUpdateRow(&row))
}

// ListUpdates implements stack.UpdateStore.
func (s *Store) ListUpdates(ctx context.Context, ref stack.Ref, page stack.Page) ([]stack.Update, error) {
	rows, err := s.queries.ListUpdates(ctx, db.ListUpdatesParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Stack:     ref.Stack.String(),
		Limit:     clampInt32(page.Limit),
		Offset:    clampInt32(page.Offset),
	})
	if err != nil {
		return nil, fmt.Errorf("select updates: %w", err)
	}

	converted := make([]codec.UpdateRow, 0, len(rows))
	for i := range rows {
		converted = append(converted, fromUpdateRow(&rows[i]))
	}

	return codec.FromUpdateRows(converted)
}

// PutCheckpoint implements stack.CheckpointStore. The deployment is stored in
// the row; only the stack's latest checkpoint is kept.
func (s *Store) PutCheckpoint(ctx context.Context, cp stack.Checkpoint, fence namespace.Fence) error {
	row, err := codec.ToCheckpointRow(cp)
	if err != nil {
		return err
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := checkLease(ctx, q, cp.Ref, cp.Update, row.LeaseDigest); err != nil {
			return err
		}

		if cp.Sequence > 0 {
			retried, err := checkSequence(ctx, q, cp)
			if err != nil || retried {
				return err
			}
		}

		err := q.PutCheckpoint(ctx, db.PutCheckpointParams{
			Namespace:     row.Namespace,
			Project:       row.Project,
			Stack:         row.Stack,
			UpdateID:      row.UpdateID,
			LeaseDigest:   row.LeaseDigest,
			SchemaVersion: row.SchemaVersion,
			Sequence:      row.Sequence,
			Digest:        row.Digest,
			Invalid:       row.Invalid,
			Features:      row.Features,
			Deployment:    row.Deployment,
			SavedAtMs:     row.SavedAtMs,
		})
		if err != nil {
			return fmt.Errorf("upsert checkpoint: %w", err)
		}

		return nil
	})
}

// LatestCheckpoint implements stack.CheckpointStore.
func (s *Store) LatestCheckpoint(ctx context.Context, ref stack.Ref) (stack.Checkpoint, error) {
	row, err := s.queries.GetCheckpoint(ctx, db.GetCheckpointParams(stackParams(ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.Checkpoint{}, stack.ErrNoState
	}

	if err != nil {
		return stack.Checkpoint{}, fmt.Errorf("select checkpoint: %w", err)
	}

	return codec.FromCheckpointRow(codec.CheckpointRow{
		Namespace:     row.Namespace,
		Project:       row.Project,
		Stack:         row.Stack,
		UpdateID:      row.UpdateID,
		LeaseDigest:   row.LeaseDigest,
		Digest:        row.Digest,
		Features:      row.Features,
		Deployment:    row.Deployment,
		SchemaVersion: row.SchemaVersion,
		Sequence:      row.Sequence,
		SavedAtMs:     row.SavedAtMs,
		Invalid:       row.Invalid,
	})
}

// AppendEvents implements stack.EventStore.
func (s *Store) AppendEvents(ctx context.Context, batch stack.EventBatch, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := checkLease(ctx, q, batch.Ref, batch.Update, codec.EncodeDigest(batch.Lease)); err != nil {
			return err
		}

		for _, e := range batch.Events {
			err := q.AppendEvent(ctx, db.AppendEventParams{
				Namespace:   batch.Ref.Namespace.String(),
				Project:     batch.Ref.Project.String(),
				Stack:       batch.Ref.Stack.String(),
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
	exists, err := s.queries.UpdateExists(ctx, db.UpdateExistsParams(updateParams(ref, id)))
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}

	if !exists {
		return nil, stack.ErrUpdateNotFound
	}

	rows, err := s.queries.ListEvents(ctx, db.ListEventsParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Stack:     ref.Stack.String(),
		UpdateID:  id.String(),
		Sequence:  after,
		Limit:     clampInt32(limit),
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

// lockStack locks the stack row for a start or end of one of its updates and
// returns the stack's version.
func lockStack(ctx context.Context, q *db.Queries, ref stack.Ref) (int64, error) {
	version, err := q.LockStackForUpdates(ctx, db.LockStackForUpdatesParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, stack.ErrUpdateNotFound
	}

	if err != nil {
		return 0, fmt.Errorf("lock stack: %w", err)
	}

	return version, nil
}

// allocateVersion returns the next version of a stack with no running
// mutating update, or ErrUpdateConflict. The caller holds the stack lock.
func allocateVersion(ctx context.Context, q *db.Queries, ref stack.Ref) (int64, error) {
	running, err := q.RunningMutatingUpdates(ctx, db.RunningMutatingUpdatesParams(stackParams(ref)))
	if err != nil {
		return 0, fmt.Errorf("count running updates: %w", err)
	}

	if running > 0 {
		return 0, stack.ErrUpdateConflict
	}

	highest, err := q.HighestUpdateVersion(ctx, db.HighestUpdateVersionParams(stackParams(ref)))
	if err != nil {
		return 0, fmt.Errorf("read highest update version: %w", err)
	}

	return highest + 1, nil
}

// saveUpdate writes u if the store holds u.Revision-1.
func saveUpdate(ctx context.Context, q *db.Queries, u stack.Update) error {
	row := codec.ToUpdateRow(u)

	n, err := q.SaveUpdate(ctx, db.SaveUpdateParams{
		Status:           row.Status,
		Version:          row.Version,
		Revision:         row.Revision,
		LeaseDigest:      row.LeaseDigest,
		LeaseExpiresAtMs: row.LeaseExpiresAtMs,
		StartedAtMs:      row.StartedAtMs,
		EndedAtMs:        row.EndedAtMs,
		Namespace:        row.Namespace,
		Project:          row.Project,
		Stack:            row.Stack,
		ID:               row.ID,
		ExpectedRevision: row.Revision - 1,
	})
	if err != nil {
		return fmt.Errorf("update update: %w", err)
	}

	if n > 0 {
		return nil
	}

	exists, err := q.UpdateExists(ctx, db.UpdateExistsParams(updateParams(u.Ref, u.ID)))

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
func checkLease(ctx context.Context, q *db.Queries, ref stack.Ref, id stack.UpdateID, lease []byte) error {
	row, err := q.GetUpdateLeaseForShare(ctx, db.GetUpdateLeaseForShareParams(updateParams(ref, id)))
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

// checkSequence compares cp with the last checkpoint of its update and
// reports whether cp repeats it. The caller holds the update FOR SHARE and
// the update runs, so no other checkpoint of the stack can be written
// concurrently: one mutating update runs per stack.
func checkSequence(ctx context.Context, q *db.Queries, cp stack.Checkpoint) (bool, error) {
	last, err := q.GetCheckpoint(ctx, db.GetCheckpointParams(stackParams(cp.Ref)))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read last checkpoint: %w", err)
	}

	if last.UpdateID != cp.Update.String() {
		return false, nil
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

// stackKey matches every generated parameter struct that names only a stack.
type stackKey struct {
	Namespace string
	Project   string
	Stack     string
}

func stackParams(ref stack.Ref) stackKey {
	return stackKey{Namespace: ref.Namespace.String(), Project: ref.Project.String(), Stack: ref.Stack.String()}
}

// updateKey matches every generated parameter struct that names one update.
type updateKey struct {
	Namespace string
	Project   string
	Stack     string
	ID        string
}

func updateParams(ref stack.Ref, id stack.UpdateID) updateKey {
	return updateKey{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Stack:     ref.Stack.String(),
		ID:        id.String(),
	}
}

func fromUpdateRow(row *db.Update) codec.UpdateRow {
	return codec.UpdateRow{
		Namespace:        row.Namespace,
		Project:          row.Project,
		Stack:            row.Stack,
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
	}
}

// clampInt32 bounds n to the range of a PostgreSQL integer parameter.
func clampInt32(n int) int32 {
	return int32(max(min(n, math.MaxInt32), 0))
}
