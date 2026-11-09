package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	_ workspace.Store      = (*Store)(nil)
	_ workspace.StateStore = (*Store)(nil)
)

// workspaceInventoryTool names OpenTofu in inventory entries.
const workspaceInventoryTool = workspace.InventoryTool

// CreateWorkspace implements workspace.Store with a fenced insert. The
// inventory entry is written in the same transaction.
func (s *Store) CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return createWorkspaceRows(ctx, q, w)
	})
}

// CreateWorkspacePlaced implements workspace.PlacedStore. The workspace, its
// inventory entry, its membership of the collection and the audit entry of the
// placement share one fenced transaction.
func (s *Store) CreateWorkspacePlaced(
	ctx context.Context, w workspace.Workspace, p workspace.Placement, fence namespace.Fence,
) error {
	// Defense in depth: the service and the handler check the placement too.
	// This check stays so that a new caller of the store cannot make a
	// workspace that no collection holds.
	if !p.Complete(w.Ref.Namespace) {
		return collection.ErrNoCollection
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	e := p.Entry

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if err := createWorkspaceRows(ctx, q, w); err != nil {
			return err
		}

		return setUnitCollection(ctx, q, workspacePlacementMove(w, p), e)
	})
}

// workspacePlacementMove returns the move that puts the new workspace w in the
// collection of p: the workspace has no collection yet.
func workspacePlacementMove(w workspace.Workspace, p workspace.Placement) collection.Move {
	return collection.Move{
		Unit: collection.UnitRef{
			Namespace: w.Ref.Namespace, Tool: workspaceInventoryTool, Name: w.Ref.Name.String(),
		},
		From: "", To: p.Collection, ToID: p.CollectionID, EndsLock: false,
	}
}

// createWorkspaceRows inserts the workspace and its inventory entry in the
// transaction of q.
func createWorkspaceRows(ctx context.Context, q *db.Queries, w workspace.Workspace) error {
	row := codec.ToWorkspaceRow(w)

	if err := refuseUnderNamespaceLock(ctx, q, w.Ref, workspace.LockPassFrom(ctx)); err != nil {
		return err
	}

	n, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		ID:                    row.ID,
		Namespace:             row.Namespace,
		Name:                  row.Name,
		Revision:              row.Revision,
		LockID:                row.LockID,
		LockHolder:            row.LockHolder,
		LockReason:            row.LockReason,
		LockedAtMs:            row.LockedAtMs,
		CurrentStateVersionID: row.CurrentStateVersion,
		AllowForcedWrites:     row.AllowForcedWrites,
		AllowUnlockedWrites:   row.AllowUnlockedWrites,
		CreatedAtMs:           row.CreatedAtMs,
		UpdatedAtMs:           row.UpdatedAtMs,
		LastReleaseLockID:     row.LastReleaseLockID,
		LastReleaseHolder:     row.LastReleaseHolder,
		LastReleaseBy:         row.LastReleaseBy,
		LastReleaseKind:       row.LastReleaseKind,
		LastReleaseAtMs:       row.LastReleaseAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert workspace: %w", err)
	}

	if n == 0 {
		return workspace.ErrAlreadyExists
	}

	return publishWorkspaceInventory(
		ctx,
		q,
		w.Ref,
		unitWrite{unitID: row.ID, atMs: row.UpdatedAtMs, kind: inventory.ChangeCreated},
	)
}

// GetWorkspace implements workspace.Store.
func (s *Store) GetWorkspace(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.ReadWorkspace(ctx, db.ReadWorkspaceParams{
		Namespace: ref.Namespace.String(),
		Name:      ref.Name.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("select workspace: %w", err)
	}

	return workspaceFromRow(row.Workspace, row.NamespaceID), nil
}

// GetWorkspaceByID implements workspace.Store.
func (s *Store) GetWorkspaceByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.ReadWorkspaceByID(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("select workspace: %w", err)
	}

	return workspaceFromRow(row.Workspace, row.NamespaceID), nil
}

// ListWorkspaces implements workspace.Store.
func (s *Store) ListWorkspaces(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ReadWorkspaces(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select workspaces: %w", err)
	}

	out := make([]workspace.Workspace, 0, len(rows))
	for i := range rows {
		out = append(out, workspaceFromRow(rows[i].Workspace, rows[i].NamespaceID))
	}

	return out, nil
}

// ReleaseUnfenced implements workspace.Store. It saves a released unit like
// SaveWorkspace, and refuses the save with ErrOverrideRefused unless the
// forced-write switches of the namespace and of the unit are on, in the same
// transaction. It appends the audit entry e in
// that transaction.
func (s *Store) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		wrow, err := q.GetWorkspaceByID(ctx, w.ID.String())
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read workspace: %w", err)
		}

		if switchErr := checkForcedSwitches(ctx, q, wrow); switchErr != nil {
			return switchErr
		}

		return saveWorkspaceRow(ctx, q, w)
	})
}

// SaveReleased implements workspace.Store. It saves a released unit like
// SaveWorkspace and appends the audit entry e in the same transaction.
func (s *Store) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return saveWorkspaceRow(ctx, q, w)
	})
}

// SaveWorkspace implements workspace.Store: a compare-and-swap on the
// revision. The inventory entry moves with the write in the same transaction.
func (s *Store) SaveWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return saveWorkspaceRow(ctx, q, w)
	})
}

// DeleteWorkspace implements workspace.Store. It soft-deletes the workspace in
// one fenced transaction (ADR 47), with its audit entry e (ADR 60, fail
// closed): it frees the name, keeps the state versions
// and the wrapped data key, and moves the inventory entry to the deleted
// units. It returns
// ErrLocked while the workspace is locked (this includes a manual lock on the
// workspace, its collection or its namespace, which also matches
// manuallock.ErrLocked), ErrPendingState while it has a
// pending state version, and ErrNotFound if no live
// workspace has ref. The transaction holds the write lock from the start, so a
// concurrent lock of the workspace either runs before the check or waits
// until the delete commits.
func (s *Store) DeleteWorkspace(
	ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion,
) error {
	e := d.Entry

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, getErr := q.GetWorkspace(ctx, db.GetWorkspaceParams{
			Namespace: ref.Namespace.String(),
			Name:      ref.Name.String(),
		})
		if errors.Is(getErr, sql.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select workspace: %w", getErr)
		}

		if lockErr := refuseWorkspaceDeleteUnderLock(ctx, q, ref); lockErr != nil {
			return lockErr
		}

		if busyErr := refuseBusyWorkspace(ctx, q, row.LockID, row.ID); busyErr != nil {
			return busyErr
		}

		n, deleteErr := q.SoftDeleteWorkspace(ctx, db.SoftDeleteWorkspaceParams{
			DeletedAtMs: sql.NullInt64{Int64: e.At.UnixMilli(), Valid: true},
			ID:          row.ID,
		})
		if deleteErr != nil {
			return fmt.Errorf("delete workspace: %w", deleteErr)
		}

		if n == 0 {
			return workspace.ErrNotFound
		}

		e.ObjectID = row.ID

		return moveWorkspaceInventoryToDeleted(ctx, q, ref, row.ID, d)
	})
}

// refuseBusyWorkspace returns ErrLocked while the workspace holds a lock, and
// ErrPendingState while it has a pending state version, so a delete never
// leaves an upload without its workspace. The caller runs it in the delete
// transaction, which holds the write lock, so no declare runs between the count and the delete.
func refuseBusyWorkspace(ctx context.Context, q *db.Queries, lockID, workspaceID string) error {
	if lockID != "" {
		return workspace.ErrLocked
	}

	pending, err := q.CountPendingStateVersions(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("count pending state versions: %w", err)
	}

	if pending > 0 {
		return workspace.ErrPendingState
	}

	return nil
}

// moveWorkspaceInventoryToDeleted moves the inventory entry of the deleted
// workspace ref, with the ID workspaceID, to the deleted units in the
// transaction of q (ADR 47). The entry keeps the name before the delete.
func moveWorkspaceInventoryToDeleted(
	ctx context.Context, q *db.Queries, ref workspace.Ref, workspaceID string, d inventory.Deletion,
) error {
	err := q.InsertDeletedInventory(ctx, db.InsertDeletedInventoryParams{
		Namespace:    ref.Namespace.String(),
		Tool:         workspaceInventoryTool,
		UnitID:       workspaceID,
		Name:         ref.Name.String(),
		DeletedAtMs:  d.Entry.At.UnixMilli(),
		PurgeAfterMs: d.PurgeAfter.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("insert deleted inventory entry: %w", err)
	}

	entry := db.DeleteInventoryParams{
		Namespace: ref.Namespace.String(),
		Tool:      workspaceInventoryTool,
		Name:      ref.Name.String(),
	}
	if err := q.DeleteInventory(ctx, entry); err != nil {
		return fmt.Errorf("delete inventory entry: %w", err)
	}

	deleted := unitChange(
		ref.Namespace,
		workspaceInventoryTool,
		ref.Name.String(),
		unitWrite{unitID: workspaceID, atMs: d.Entry.At.UnixMilli(), kind: inventory.ChangeDeleted},
	)

	return appendInventoryChange(ctx, q, deleted)
}

// CreateStateVersion implements workspace.StateStore with a fenced insert. A
// pending version changes no current state, so it writes no inventory entry.
// The pending count and the insert run in one write transaction, so
// concurrent declares never pass the cap together.
func (s *Store) CreateStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToStateVersionRow(sv)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		wrow, err := q.GetWorkspaceByID(ctx, row.WorkspaceID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return workspace.ErrNotFound
			}

			return fmt.Errorf("read workspace: %w", err)
		}

		if switchErr := checkDeclareSwitches(ctx, q, sv, wrow); switchErr != nil {
			return switchErr
		}

		pending, err := q.CountPendingStateVersions(ctx, row.WorkspaceID)
		if err != nil {
			return fmt.Errorf("count pending state versions: %w", err)
		}

		if pending >= int64(maxPending) {
			return fmt.Errorf(
				"%w: %d pending, limit %d",
				workspace.ErrTooManyPending,
				pending,
				maxPending,
			)
		}

		return insertStateVersion(ctx, q, row, sv.Origin.Job)
	})
}

// GetStateVersion implements workspace.StateStore.
func (s *Store) GetStateVersion(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetStateVersion(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	if err != nil {
		return workspace.StateVersion{}, fmt.Errorf("select state version: %w", err)
	}

	return stateFromRow(row)
}

// CurrentStateVersion implements workspace.StateStore. It follows the
// workspace pointer; a missing workspace reads as no state.
func (s *Store) CurrentStateVersion(
	ctx context.Context, workspaceID workspace.ID,
) (workspace.StateVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetCurrentStateVersion(ctx, workspaceID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	if err != nil {
		return workspace.StateVersion{}, fmt.Errorf("select current state: %w", err)
	}

	return stateFromRow(row)
}

// ListStateVersions implements workspace.StateStore: the finalized versions
// of workspace, newest first, with the total count. A workspace without
// finalized versions, or one that does not exist, yields an empty list and a
// zero total.
func (s *Store) ListStateVersions(
	ctx context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	page = normalizeStatePage(page)

	total, err := s.queries.CountStateVersions(ctx, id.String())
	if err != nil {
		return nil, 0, fmt.Errorf("count state versions: %w", err)
	}

	rows, err := s.queries.ListStateVersions(ctx, db.ListStateVersionsParams{
		WorkspaceID: id.String(),
		Limit:       int64(page.Limit),
		Offset:      int64(page.Offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("select state versions: %w", err)
	}

	out := make([]workspace.StateVersion, 0, len(rows))

	for i := range rows {
		sv, err := stateFromRow(rows[i])
		if err != nil {
			return nil, 0, err
		}

		out = append(out, sv)
	}

	return out, int(total), nil
}

// normalizeStatePage applies the paging rules of the port: a non-positive
// limit reads as the default page size, a limit above the maximum is capped,
// and a negative offset reads as zero.
func normalizeStatePage(page workspace.Page) workspace.Page {
	if page.Limit <= 0 {
		page.Limit = workspace.DefaultStatePageSize
	}

	page.Limit = min(page.Limit, workspace.MaxStatePageSize)
	page.Offset = max(page.Offset, 0)

	return page
}

// CountPendingStateVersions implements workspace.StateStore: how many
// pending versions the workspace holds. A workspace that does not exist
// holds none.
func (s *Store) CountPendingStateVersions(ctx context.Context, id workspace.ID) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.CountPendingStateVersions(ctx, id.String())
	if err != nil {
		return 0, fmt.Errorf("count pending state versions: %w", err)
	}

	return int(n), nil
}

// ReferencedContentKeys implements workspace.StateStore: every blob key the
// versions of the workspace name, pending and finalized, without the empty
// ones. A workspace without versions yields an empty list. A workspace that
// does not exist returns workspace.ErrNotFound. A soft-deleted workspace
// still returns its keys.
func (s *Store) ReferencedContentKeys(ctx context.Context, id workspace.ID) ([]blob.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys, err := s.queries.ListStateContentKeys(ctx, id.String())
	if err != nil {
		return nil, fmt.Errorf("select state content keys: %w", err)
	}

	// No row means no workspace with this ID: fail closed, so a sweep
	// deletes nothing.
	if len(keys) == 0 {
		return nil, workspace.ErrNotFound
	}

	out := make([]blob.Key, 0, len(keys))

	for _, key := range keys {
		// A workspace without keys gives one row with an empty key.
		if key == "" {
			continue
		}

		parsed, err := blob.ParseKey(key)
		if err != nil {
			return nil, fmt.Errorf("parse state content key: %w", err)
		}

		out = append(out, parsed)
	}

	return out, nil
}

// RecordUploadAttempt implements workspace.StateStore: it notes the blob
// key of one upload attempt on a pending version, so a later sweep finds
// the content even when the finalize outcome stays unknown. It returns the
// key the attempt supersedes, so the caller deletes the earlier blob.
func (s *Store) RecordUploadAttempt(
	ctx context.Context, id workspace.StateVersionID, key blob.Key, fence namespace.Fence,
) (blob.Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var superseded blob.Key

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stored, err := q.GetStateVersion(ctx, id.String())
		if errors.Is(err, sql.ErrNoRows) {
			return workspace.ErrStateVersionNotFound
		}

		if err != nil {
			return fmt.Errorf("read state version: %w", err)
		}

		sv, err := stateFromRow(stored)
		if err != nil {
			return err
		}

		if sv.Status != workspace.StatusPending {
			return workspace.ErrUploadDenied
		}

		superseded = sv.Content

		n, err := q.SetPendingContentKey(ctx, db.SetPendingContentKeyParams{
			ContentKey: key.String(),
			ID:         id.String(),
		})
		if err != nil {
			return fmt.Errorf("record upload attempt: %w", err)
		}

		if n == 0 {
			return workspace.ErrUploadDenied
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return superseded, nil
}

// SaveSensitiveMarks implements workspace.StateStore: it stores the secret
// paths of the json-state rendering of id. It works on a pending and on a
// finalized version: the rendering may land after the state upload
// finalizes. A missing version reports ErrStateVersionNotFound.
func (s *Store) SaveSensitiveMarks(
	ctx context.Context, id workspace.StateVersionID, marks workspace.SensitiveMarks, fence namespace.Fence,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := codec.MarshalSensitiveMarks(marks)
	if err != nil {
		return fmt.Errorf("encode sensitive marks: %w", err)
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.SaveSensitiveMarks(ctx, db.SaveSensitiveMarksParams{
			SensitiveMarks: raw,
			ID:             id.String(),
		})
		if err != nil {
			return fmt.Errorf("save sensitive marks: %w", err)
		}

		if n == 0 {
			return workspace.ErrStateVersionNotFound
		}

		return nil
	})
}

// DeleteExpiredPendingStateVersions implements workspace.StateStore: it
// deletes the expired pending versions of the workspace in one fenced
// transaction and returns them, oldest expiry first, with their attempted
// content keys.
func (s *Store) DeleteExpiredPendingStateVersions(
	ctx context.Context, id workspace.ID, before time.Time, fence namespace.Fence,
) ([]workspace.StateVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []workspace.StateVersion

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		rows, err := q.ListExpiredPendingStateVersions(ctx, db.ListExpiredPendingStateVersionsParams{
			WorkspaceID:       id.String(),
			UploadExpiresAtMs: codec.Millis(before),
		})
		if err != nil {
			return fmt.Errorf("select expired pending state: %w", err)
		}

		out = make([]workspace.StateVersion, 0, len(rows))

		for i := range rows {
			sv, err := stateFromRow(rows[i])
			if err != nil {
				return err
			}

			n, err := q.DeleteStateVersion(ctx, sv.ID.String())
			if err != nil {
				return fmt.Errorf("delete expired pending state: %w", err)
			}

			// The version finalized after the listing: the conditional
			// delete matched no row, so it stays out of the result and
			// keeps its content.
			if n == 0 {
				continue
			}

			out = append(out, sv)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// FinalizeStateVersion implements workspace.StateStore. Every check of the
// port comment runs in one transaction with the fence: the stored version is
// still pending, the workspace is held by the lock the version was declared
// under, and, unless forced, the lineage matches and the serial is newer. The
// workspace stamp and the inventory entry move with the finalize.
func (s *Store) FinalizeStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToStateVersionRow(sv)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return finalizeInTx(ctx, q, sv, row, nil)
	})
}

// FinalizeStateVersionExpecting implements workspace.StateStore. It runs the
// checks of FinalizeStateVersion and also checks, in the same transaction,
// that the current state version is expect.
func (s *Store) FinalizeStateVersionExpecting(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, expect workspace.StateVersionID,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToStateVersionRow(sv)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return finalizeInTx(ctx, q, sv, row, &expect)
	})
}

// FenceAndUnlock implements workspace.StateStore: it releases the lock of w,
// finalizes the pending fence version sv and moves the workspace pointer to
// it, in one fenced transaction. The inventory entry moves with the release.
//
//nolint:revive // The signature of the workspace.StateStore port.
func (s *Store) FenceAndUnlock(
	ctx context.Context, w workspace.Workspace, sv workspace.StateVersion,
	expect workspace.StateVersionID, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return fenceAndUnlockInTx(ctx, q, w, codec.ToStateVersionRow(sv), expect)
	})
}

// fenceAndUnlockInTx writes the release of w's lock and finalizes the fence
// version row in the caller's transaction. The workspace must be locked, and its stored
// revision must be w.Revision-1; a write that fails either check changes
// nothing.
func fenceAndUnlockInTx(
	ctx context.Context, q *db.Queries, w workspace.Workspace, row codec.StateVersionRow,
	expect workspace.StateVersionID,
) error {
	wrow, err := workspaceForFinalize(ctx, q, w.ID.String())
	if err != nil {
		return err
	}

	if wrow.LockHolder == "" {
		return workspace.ErrNotLocked
	}

	if fenceErr := checkFenceCurrent(ctx, q, wrow.ID, expect, row.Serial); fenceErr != nil {
		return fenceErr
	}

	released := codec.ToWorkspaceRow(w)

	n, err := q.SaveWorkspace(ctx, db.SaveWorkspaceParams{
		Revision:            released.Revision,
		LockID:              released.LockID,
		LockHolder:          released.LockHolder,
		LockReason:          released.LockReason,
		LockedAtMs:          released.LockedAtMs,
		UpdatedAtMs:         released.UpdatedAtMs,
		ID:                  released.ID,
		ExpectedRevision:    released.Revision - 1,
		AllowForcedWrites:   released.AllowForcedWrites,
		AllowUnlockedWrites: released.AllowUnlockedWrites,
		LastReleaseLockID:   released.LastReleaseLockID,
		LastReleaseHolder:   released.LastReleaseHolder,
		LastReleaseBy:       released.LastReleaseBy,
		LastReleaseKind:     released.LastReleaseKind,
		LastReleaseAtMs:     released.LastReleaseAtMs,
	})
	if err != nil {
		return fmt.Errorf("release workspace lock: %w", err)
	}

	if n == 0 {
		return workspace.ErrConcurrentModification
	}

	row, err = stampAfterLatest(ctx, q, row)
	if err != nil {
		return err
	}

	if finalizeErr := finalizeFenceRow(ctx, q, row); finalizeErr != nil {
		return finalizeErr
	}

	return publishFencedState(ctx, q, w, released.ID, row)
}

// publishFencedState makes the finalized row the current state of the workspace
// w, writes its inventory entry and its lock, and appends the change log row.
// The change log row comes last, after every other lock.
func publishFencedState(
	ctx context.Context, q *db.Queries, w workspace.Workspace, id string, row codec.StateVersionRow,
) error {
	setErr := q.SetWorkspaceCurrent(ctx, db.SetWorkspaceCurrentParams{
		CurrentStateVersionID: row.ID,
		UpdatedAtMs:           row.FinalizedAtMs,
		ID:                    row.WorkspaceID,
	})
	if setErr != nil {
		return fmt.Errorf("set current state: %w", setErr)
	}

	if pubErr := upsertWorkspaceInventory(ctx, q, w.Ref, id, row.FinalizedAtMs); pubErr != nil {
		return pubErr
	}

	lockErr := writeInventoryLock(ctx, q, inventoryLockKey{
		namespace: w.Ref.Namespace.String(),
		tool:      workspaceInventoryTool,
		name:      w.Ref.Name.String(),
	}, workspaceLock(w.Lock))
	if lockErr != nil {
		return lockErr
	}

	if releaseErr := writeWorkspaceLastRelease(ctx, q, w); releaseErr != nil {
		return releaseErr
	}

	// The forced release finalizes the state version that becomes current: a
	// write-back job of the unit closes on it.
	atMs, err := storeNow(ctx, q)
	if err != nil {
		return err
	}

	origin, err := stateVersionOrigin(ctx, q, row.ID)
	if err != nil {
		return err
	}

	pubErr := publishUnitState(ctx, q, unitStateWrite{
		nsName:  w.Ref.Namespace.String(),
		unitID:  id,
		version: row.ID,
		final:   true,
		origin:  origin,
		atMs:    atMs,
	})
	if pubErr != nil {
		return pubErr
	}

	// The change log row comes last, after every other lock.
	return appendWorkspaceChange(
		ctx,
		q,
		w.Ref,
		unitWrite{unitID: id, atMs: row.FinalizedAtMs, kind: inventory.ChangeChanged},
	)
}

// finalizeFenceRow finalizes the pending fence version row in the caller's
// transaction. The write matches only a pending row that names the fence
// content, else ErrUploadDenied: the caller stored the row before it wrote the
// content.
func finalizeFenceRow(ctx context.Context, q *db.Queries, row codec.StateVersionRow) error {
	n, err := q.FinalizeStateVersion(ctx, db.FinalizeStateVersionParams{
		Serial:        row.Serial,
		Lineage:       row.Lineage,
		Md5:           row.MD5,
		Outputs:       row.Outputs,
		Status:        string(workspace.StatusFinalized),
		FinalizedAtMs: row.FinalizedAtMs,
		ContentKey:    row.ContentKey,
		Size:          row.Size,
		ID:            row.ID,
	})
	if err != nil {
		return fmt.Errorf("finalize fence version: %w", err)
	}

	if n == 0 {
		return workspace.ErrUploadDenied
	}

	return nil
}

// finalizeInTx runs the finalize checks and writes in one transaction.
func finalizeInTx(
	ctx context.Context, q *db.Queries, sv workspace.StateVersion, row codec.StateVersionRow,
	expect *workspace.StateVersionID,
) error {
	wrow, err := checkFinalizeGuards(ctx, q, sv, row, expect)
	if err != nil {
		return err
	}

	return writeFinalize(ctx, q, wrow, row)
}

// checkFinalizeGuards runs the port checks of a finalize and returns the
// workspace row the finalize stamps.
func checkFinalizeGuards(
	ctx context.Context, q *db.Queries, sv workspace.StateVersion, row codec.StateVersionRow,
	expect *workspace.StateVersionID,
) (db.Workspace, error) {
	if pendingErr := checkFinalizePending(ctx, q, row.ID); pendingErr != nil {
		return db.Workspace{}, pendingErr
	}

	wrow, err := workspaceForFinalize(ctx, q, row.WorkspaceID)
	if err != nil {
		return db.Workspace{}, err
	}

	if lockErr := checkFinalizeHolder(ctx, q, row, wrow); lockErr != nil {
		return db.Workspace{}, lockErr
	}

	// The expected current version is read from the locked workspace row, so
	// a write by the same holder under the same lock cannot slip in between.
	if expect != nil && wrow.CurrentStateVersionID != expect.String() {
		return db.Workspace{}, workspace.ErrConcurrentModification
	}

	if forcedErr := checkFinalizeForced(ctx, q, sv, wrow); forcedErr != nil {
		return db.Workspace{}, forcedErr
	}

	if !sv.Force {
		if orderErr := checkFinalizeOrder(ctx, q, row); orderErr != nil {
			return db.Workspace{}, orderErr
		}
	}

	if restoreErr := checkFinalizeRestore(ctx, q, row); restoreErr != nil {
		return db.Workspace{}, restoreErr
	}

	return wrow, nil
}

// checkFinalizeHolder checks the lock of a finalize. A version declared under
// the lock keeps it. A version declared without the lock (ADR 44, decision 5)
// has no lock to keep, and a lock that another principal took after the
// declare stops it.
func checkFinalizeHolder(
	ctx context.Context, q *db.Queries, row codec.StateVersionRow, wrow db.Workspace,
) error {
	if !row.UnlockedWrite {
		return checkFinalizeLock(wrow, row.LockID, row.Holder)
	}

	if wrow.LockHolder != "" && wrow.LockHolder != row.Holder {
		return workspace.ErrLockedByOther
	}

	return checkUnlockedSwitches(ctx, q, wrow)
}

// checkFinalizeForced checks the forced-write switches of a forced finalize. A
// forced version finalizes only while the switches are on (ADR 44, decisions 4
// and 6). A restore sets Force itself, so it is not a client's forced write, and
// a unit with no current state needs no override.
func checkFinalizeForced(
	ctx context.Context, q *db.Queries, sv workspace.StateVersion, wrow db.Workspace,
) error {
	if !sv.Force || sv.RestoredFrom != "" || wrow.CurrentStateVersionID == "" {
		return nil
	}

	return checkForcedSwitches(ctx, q, wrow)
}

// stampAfterLatest returns row with a finalize time after the latest finalize
// time of its workspace: the requested time, or one millisecond after the
// latest one when the requested time is not later. The caller runs it in the
// finalize transaction, after the workspace row is locked, so the version
// that becomes current always has the latest finalize time of its workspace,
// also when two finalizes share a millisecond or a clock steps back.
func stampAfterLatest(
	ctx context.Context, q *db.Queries, row codec.StateVersionRow,
) (codec.StateVersionRow, error) {
	latest, err := q.LatestWorkspaceStateFinalize(ctx, row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return row, nil
	}

	if err != nil {
		return codec.StateVersionRow{}, fmt.Errorf("select latest finalize time: %w", err)
	}

	row.FinalizedAtMs = max(row.FinalizedAtMs, latest+1)

	return row, nil
}

// writeFinalize marks the version finalized, moves the workspace pointer and
// publishes the inventory entry. The write matches only the recorded attempt
// key, so a superseded attempt reports ErrUploadDenied instead of
// finalizing over a newer one.
func writeFinalize(
	ctx context.Context, q *db.Queries, wrow db.Workspace, row codec.StateVersionRow,
) error {
	row, err := stampAfterLatest(ctx, q, row)
	if err != nil {
		return err
	}

	n, err := q.FinalizeStateVersion(ctx, db.FinalizeStateVersionParams{
		Serial:        row.Serial,
		Lineage:       row.Lineage,
		Md5:           row.MD5,
		Outputs:       row.Outputs,
		Status:        string(workspace.StatusFinalized),
		FinalizedAtMs: row.FinalizedAtMs,
		ContentKey:    row.ContentKey,
		Size:          row.Size,
		ID:            row.ID,
	})
	if err != nil {
		return fmt.Errorf("finalize state version: %w", err)
	}

	if n == 0 {
		return workspace.ErrUploadDenied
	}

	setParams := db.SetWorkspaceCurrentParams{
		CurrentStateVersionID: row.ID,
		UpdatedAtMs:           row.FinalizedAtMs,
		ID:                    row.WorkspaceID,
	}

	if setErr := q.SetWorkspaceCurrent(ctx, setParams); setErr != nil {
		return fmt.Errorf("set current state: %w", setErr)
	}

	// The finalized version is the current state version of the unit. A write
	// that the workspace allows without a lock is final: it has no release
	// that marks it, and a write-back job of the unit would never close.
	atMs, err := storeNow(ctx, q)
	if err != nil {
		return err
	}

	origin, err := stateVersionOrigin(ctx, q, row.ID)
	if err != nil {
		return err
	}

	pubErr := publishUnitState(ctx, q, unitStateWrite{
		nsName:  wrow.Namespace,
		unitID:  wrow.ID,
		version: row.ID,
		final:   row.UnlockedWrite,
		origin:  origin,
		atMs:    atMs,
	})
	if pubErr != nil {
		return pubErr
	}

	return publishWorkspaceInventory(
		ctx,
		q,
		workspaceRef(wrow),
		unitWrite{unitID: wrow.ID, atMs: row.FinalizedAtMs, kind: inventory.ChangeChanged},
	)
}

// checkFinalizePending returns ErrUploadDenied unless the stored version is
// still pending, and ErrStateVersionNotFound when it does not exist.
func checkFinalizePending(ctx context.Context, q *db.Queries, id string) error {
	stored, err := q.GetStateVersion(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrStateVersionNotFound
	}

	if err != nil {
		return fmt.Errorf("read state version: %w", err)
	}

	if stored.Status != string(workspace.StatusPending) {
		return workspace.ErrUploadDenied
	}

	return nil
}

// stateVersionOrigin returns the origin of the write that declared the state
// version id: the job whose token declared it, or an empty origin when the
// declare named none. The commit stays empty: a job write takes it from the
// spec of the job when it publishes the version.
func stateVersionOrigin(ctx context.Context, q *db.Queries, id string) (identity.StateOrigin, error) {
	stored, err := q.GetStateVersion(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.StateOrigin{Job: "", Commit: ""}, workspace.ErrStateVersionNotFound
	}

	if err != nil {
		return identity.StateOrigin{Job: "", Commit: ""}, fmt.Errorf("read the origin of state version %s: %w", id, err)
	}

	return identity.StateOrigin{Job: stored.OriginJob, Commit: ""}, nil
}

// workspaceForFinalize reads the workspace of a finalize inside its
// transaction, or ErrNotFound when it does not exist.
func workspaceForFinalize(ctx context.Context, q *db.Queries, workspaceID string) (db.Workspace, error) {
	wrow, err := q.GetWorkspaceByID(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return db.Workspace{}, fmt.Errorf("read workspace: %w", err)
	}

	return wrow, nil
}

// checkForcedSwitches refuses the release of a client-encrypted state unless the forced-write switches of the namespace
// and of the unit are
// on. It reads both switches in the caller's transaction (ADR 44, decisions 4
// and 6).
func checkForcedSwitches(ctx context.Context, q *db.Queries, wrow db.Workspace) error {
	ns, err := q.GetNamespace(ctx, wrow.Namespace)
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read namespace: %w", err)
	}

	if !ns.AllowForcedWrites || !wrow.AllowForcedWrites {
		return workspace.NewOverrideRefusedError(workspace.OverrideForced, fmt.Sprintf(
			"%s: the forced-write switch of the namespace or of the workspace is off",
			workspace.ErrOverrideRefused,
		))
	}

	return nil
}

// checkDeclareSwitches checks the switches of an unlocked or a forced declare in
// the caller's transaction (ADR 44, decisions 5, 4 and 6). A restore sets Force
// itself, so it is not checked as a client's forced write.
func checkDeclareSwitches(ctx context.Context, q *db.Queries, sv workspace.StateVersion, wrow db.Workspace) error {
	if sv.UnlockedWrite {
		if err := checkUnlockedSwitches(ctx, q, wrow); err != nil {
			return err
		}
	}

	// A forced write needs the override only into a unit that has state (ADR 44,
	// decision 4).
	if sv.Force && sv.RestoredFrom == "" && wrow.CurrentStateVersionID != "" {
		return checkForcedSwitches(ctx, q, wrow)
	}

	return nil
}

// checkUnlockedSwitches refuses an unlocked write when the namespace or the
// unit does not allow it. It reads both switches in the caller's transaction,
// so a switch that is turned off before the write commits stops the write
// (ADR 44, decisions 5 and 6).
func checkUnlockedSwitches(ctx context.Context, q *db.Queries, wrow db.Workspace) error {
	ns, err := q.GetNamespace(ctx, wrow.Namespace)
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read namespace: %w", err)
	}

	if !ns.AllowUnlockedWrites || !wrow.AllowUnlockedWrites {
		return workspace.NewOverrideRefusedError(workspace.OverrideUnlocked, fmt.Sprintf(
			"%s: the unlocked-write switch of the namespace or of the workspace is off",
			workspace.ErrOverrideRefused,
		))
	}

	return nil
}

// checkFinalizeLock checks that the workspace is still held by the lock ID and
// the holder the version was declared under. A lock ID alone is not enough: a
// lock ID the client chose can repeat after a force-unlock.
func checkFinalizeLock(wrow db.Workspace, lockID, holder string) error {
	switch {
	case wrow.LockHolder == "":
		return workspace.ErrNotLocked
	case wrow.LockID != lockID || wrow.LockHolder != holder:
		return workspace.ErrLockedByOther
	default:
		return nil
	}
}

// checkFinalizeOrder checks the lineage and the serial against the current
// state. With no current state any lineage and serial are valid. The caller
// skips this check for a forced finalize.
func checkFinalizeOrder(ctx context.Context, q *db.Queries, row codec.StateVersionRow) error {
	current, err := q.GetCurrentStateVersion(ctx, row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read current state: %w", err)
	}

	switch {
	case current.Lineage != row.Lineage:
		return workspace.ErrLineageMismatch
	case current.Serial >= row.Serial:
		return workspace.ErrStaleSerial
	default:
		return nil
	}
}

// checkFinalizeRestore refuses a restored version when the current state
// already restores the same version for the same holder. The read runs in the
// finalize transaction, so of two concurrent restores of one version only the
// first becomes current.
func checkFinalizeRestore(ctx context.Context, q *db.Queries, row codec.StateVersionRow) error {
	if row.RestoredFrom == "" {
		return nil
	}

	current, err := q.GetCurrentStateVersion(ctx, row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read current state: %w", err)
	}

	if current.RestoredFrom == row.RestoredFrom && current.Holder == row.Holder {
		return workspace.ErrAlreadyRestored
	}

	return nil
}

// workspaceFromRow converts a workspace row into a domain workspace. The
// current state pointer stays in SQL; the domain reads current state
// through CurrentStateVersion. The namespace ID comes from the join of the
// read with the namespace row, so the caller needs no second read.
func workspaceFromRow(row db.Workspace, namespaceID string) workspace.Workspace {
	return codec.FromWorkspaceRow(codec.WorkspaceRow{
		Namespace:           row.Namespace,
		NamespaceID:         namespaceID,
		Name:                row.Name,
		ID:                  row.ID,
		LockID:              row.LockID,
		LockHolder:          row.LockHolder,
		LockReason:          row.LockReason,
		CurrentStateVersion: row.CurrentStateVersionID,
		AllowForcedWrites:   row.AllowForcedWrites,
		AllowUnlockedWrites: row.AllowUnlockedWrites,
		Revision:            row.Revision,
		LockedAtMs:          row.LockedAtMs,
		CreatedAtMs:         row.CreatedAtMs,
		UpdatedAtMs:         row.UpdatedAtMs,
		LastReleaseLockID:   row.LastReleaseLockID,
		LastReleaseHolder:   row.LastReleaseHolder,
		LastReleaseBy:       row.LastReleaseBy,
		LastReleaseKind:     row.LastReleaseKind,
		LastReleaseAtMs:     row.LastReleaseAtMs,
	})
}

// stateFromRow converts a state version row into a domain state version.
func stateFromRow(row db.StateVersion) (workspace.StateVersion, error) {
	sv, err := codec.FromStateVersionRow(codec.StateVersionRow{
		ID:                row.ID,
		WorkspaceID:       row.WorkspaceID,
		Holder:            row.Holder,
		LockID:            row.LockID,
		Lineage:           row.Lineage,
		Status:            row.Status,
		ContentKey:        row.ContentKey,
		UploadDigest:      row.UploadDigest,
		Outputs:           row.Outputs,
		MD5:               row.Md5,
		Serial:            row.Serial,
		Size:              row.Size,
		Force:             row.Force,
		Sealed:            row.Sealed,
		RestoredFrom:      row.RestoredFrom,
		FencedBy:          row.FencedBy,
		UnlockedWrite:     row.UnlockedWrite,
		SensitiveMarks:    row.SensitiveMarks,
		CreatedAtMs:       row.CreatedAtMs,
		FinalizedAtMs:     row.FinalizedAtMs,
		UploadExpiresAtMs: row.UploadExpiresAtMs,
	})
	if err != nil {
		return workspace.StateVersion{}, err
	}

	return sv, nil
}

// workspaceRef returns the domain reference of a workspace row.
func workspaceRef(row db.Workspace) workspace.Ref {
	return workspace.Ref{
		Namespace: namespace.Name(row.Namespace),
		Name:      workspace.Name(row.Name),
	}
}

// publishWorkspaceInventory writes the OpenTofu entry for a workspace with
// the given change time, and appends the change log row of the kind kind.
// Status and resource count stay unknown: OpenTofu reports no status
// vocabulary and the server never reads state. The lock columns stay
// untouched: only writeInventoryLock changes them, in the same transaction as
// the lock, unlock or force-unlock that they describe. The caller runs it as
// the last write of its transaction.
func publishWorkspaceInventory(ctx context.Context, q *db.Queries, ref workspace.Ref, w unitWrite) error {
	if err := upsertWorkspaceInventory(ctx, q, ref, w.unitID, w.atMs); err != nil {
		return err
	}

	return appendWorkspaceChange(ctx, q, ref, w)
}

// appendWorkspaceChange appends the change log row of the workspace ref.
func appendWorkspaceChange(ctx context.Context, q *db.Queries, ref workspace.Ref, w unitWrite) error {
	return appendInventoryChange(ctx, q, unitChange(ref.Namespace, workspaceInventoryTool, ref.Name.String(), w))
}

// upsertWorkspaceInventory writes the OpenTofu entry for a workspace and
// appends no change log row. A write that changes only the lock uses it,
// because the lock is not history.
func upsertWorkspaceInventory(
	ctx context.Context, q *db.Queries, ref workspace.Ref, unitID string, lastChangedMs int64,
) error {
	name := ref.Name.String()

	err := q.UpsertInventory(ctx, db.UpsertInventoryParams{
		Namespace:     ref.Namespace.String(),
		Tool:          workspaceInventoryTool,
		Name:          name,
		UnitID:        unitID,
		DisplayName:   name,
		LastChangedMs: lastChangedMs,
		Status:        "",
		ResourceCount: sql.NullInt64{Int64: 0, Valid: false},
	})
	if err != nil {
		return fmt.Errorf("upsert inventory entry: %w", err)
	}

	return nil
}

// workspaceLock returns the inventory lock a workspace holds, or nil when it
// is unlocked. The holder is the subject of the principal that took the
// lock, never a token or a secret.
func workspaceLock(lock workspace.Lock) *inventory.Lock {
	if !lock.Locked() {
		return nil
	}

	return &inventory.Lock{Since: lock.LockedAt, Holder: lock.Holder, Operation: lockOperation(lock.Reason)}
}

// lockOperation maps a workspace lock reason to the inventory operation: the
// CLI's own word when it locked with one, else "lock". The TFE protocol
// carries only the reason, so the reason is the operation word.
func lockOperation(reason string) string {
	if reason == "" {
		return "lock"
	}

	return reason
}

// insertStateVersion writes one state version row in the caller's transaction.
func insertStateVersion(ctx context.Context, q *db.Queries, row codec.StateVersionRow, originJob string) error {
	err := q.CreateStateVersion(ctx, db.CreateStateVersionParams{
		ID:                row.ID,
		WorkspaceID:       row.WorkspaceID,
		Status:            row.Status,
		Serial:            row.Serial,
		Lineage:           row.Lineage,
		Md5:               row.MD5,
		Size:              row.Size,
		ContentKey:        row.ContentKey,
		Outputs:           row.Outputs,
		Force:             row.Force,
		Sealed:            row.Sealed,
		SensitiveMarks:    row.SensitiveMarks,
		UploadDigest:      row.UploadDigest,
		UploadExpiresAtMs: row.UploadExpiresAtMs,
		Holder:            row.Holder,
		LockID:            row.LockID,
		CreatedAtMs:       row.CreatedAtMs,
		FinalizedAtMs:     row.FinalizedAtMs,
		RestoredFrom:      row.RestoredFrom,
		FencedBy:          row.FencedBy,
		UnlockedWrite:     row.UnlockedWrite,
		OriginJob:         originJob,
	})
	if err != nil {
		return fmt.Errorf("insert state version: %w", err)
	}

	return nil
}

// checkFenceCurrent checks that a fence was made from the current version the
// caller read: the current version is expect, and the fence serial is one more
// than its serial. The read runs in the same transaction as the write, so a
// finalize that landed after the caller read the current version fails here.
// A workspace with no current state matches the empty expect, and accepts any
// serial.
func checkFenceCurrent(
	ctx context.Context, q *db.Queries, workspaceID string, expect workspace.StateVersionID, serial int64,
) error {
	current, err := q.GetCurrentStateVersion(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		if expect != "" {
			return workspace.ErrConcurrentModification
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("read current state: %w", err)
	}

	if current.ID != expect.String() {
		return workspace.ErrConcurrentModification
	}

	if serial != current.Serial+1 {
		return fmt.Errorf("%w: fence serial %d, current %d", workspace.ErrStaleSerial, serial, current.Serial)
	}

	return nil
}

// releasesLock reports whether the save of w releases the lock of its stored
// workspace: the stored row holds a lock and w holds none. A release is the
// lock that ends, not the record of a release: a write that carries the record
// of an earlier release releases nothing.
func releasesLock(ctx context.Context, q *db.Queries, w workspace.Workspace) (bool, error) {
	if w.Lock.Locked() {
		return false, nil
	}

	stored, err := q.GetWorkspaceByID(ctx, w.ID.String())
	if errors.Is(err, sql.ErrNoRows) {
		// The update reports the row that does not exist.
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("read workspace: %w", err)
	}

	return stored.LockHolder != "", nil
}

// saveWorkspaceRow writes the workspace row in the caller's transaction, with
// its inventory entry and lock. It is the compare-and-swap of SaveWorkspace. A
// write that releases the lock also marks the current state version of the
// unit final.
func saveWorkspaceRow(ctx context.Context, q *db.Queries, w workspace.Workspace) error {
	row := codec.ToWorkspaceRow(w)

	released, err := releasesLock(ctx, q, w)
	if err != nil {
		return err
	}

	n, err := q.SaveWorkspace(ctx, db.SaveWorkspaceParams{
		Revision:            row.Revision,
		LockID:              row.LockID,
		LockHolder:          row.LockHolder,
		LockReason:          row.LockReason,
		LockedAtMs:          row.LockedAtMs,
		UpdatedAtMs:         row.UpdatedAtMs,
		ID:                  row.ID,
		AllowForcedWrites:   row.AllowForcedWrites,
		AllowUnlockedWrites: row.AllowUnlockedWrites,
		LastReleaseLockID:   row.LastReleaseLockID,
		LastReleaseHolder:   row.LastReleaseHolder,
		LastReleaseBy:       row.LastReleaseBy,
		LastReleaseKind:     row.LastReleaseKind,
		LastReleaseAtMs:     row.LastReleaseAtMs,
		ExpectedRevision:    row.Revision - 1,
	})
	if err != nil {
		return fmt.Errorf("update workspace: %w", err)
	}

	if n == 0 {
		return lostWorkspaceSave(ctx, q, row.ID)
	}

	if invErr := upsertWorkspaceInventory(ctx, q, w.Ref, row.ID, row.UpdatedAtMs); invErr != nil {
		return invErr
	}

	// The lock moves with the write in the same transaction: lock, unlock and
	// force-unlock all save here.
	lockErr := writeInventoryLock(
		ctx, q, inventoryLockKey{
			namespace: w.Ref.Namespace.String(),
			tool:      workspaceInventoryTool,
			name:      w.Ref.Name.String(),
		}, workspaceLock(w.Lock),
	)
	if lockErr != nil {
		return lockErr
	}

	if releaseErr := writeWorkspaceLastRelease(ctx, q, w); releaseErr != nil {
		return releaseErr
	}

	if released {
		return publishReleasedState(ctx, q, w.Ref.Namespace.String(), row.ID, w.Origin)
	}

	return nil
}

// lostWorkspaceSave answers a compare-and-swap of a workspace row that wrote
// no row: the row does not exist, or another write moved its revision.
func lostWorkspaceSave(ctx context.Context, q *db.Queries, id string) error {
	_, err := q.GetWorkspaceByID(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return workspace.ErrNotFound
	case err != nil:
		return fmt.Errorf("read workspace: %w", err)
	default:
		return workspace.ErrConcurrentModification
	}
}

// DiscardStateVersion implements workspace.StateStore: it deletes the pending
// version id in one fenced transaction. A version that is not pending matches
// no row and stays.
func (s *Store) DiscardStateVersion(
	ctx context.Context, id workspace.StateVersionID, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if _, err := q.DeleteStateVersion(ctx, id.String()); err != nil {
			return fmt.Errorf("discard pending state: %w", err)
		}

		return nil
	})
}

// writeWorkspaceLastRelease publishes the last released lock of w to its
// inventory entry, in the caller's transaction. A workspace with no record
// publishes empty columns.
func writeWorkspaceLastRelease(ctx context.Context, q *db.Queries, w workspace.Workspace) error {
	r := w.LastRelease
	if !r.Recorded() {
		r = workspace.NoRelease()
	}

	err := q.UpdateInventoryLastRelease(ctx, db.UpdateInventoryLastReleaseParams{
		LastReleaseLockID: r.LockID,
		LastReleaseHolder: r.Holder,
		LastReleaseBy:     r.By,
		LastReleaseKind:   string(r.Kind),
		LastReleaseAtMs:   codec.OptionalMillis(r.At),
		Namespace:         w.Ref.Namespace.String(),
		Tool:              workspaceInventoryTool,
		Name:              w.Ref.Name.String(),
	})
	if err != nil {
		return fmt.Errorf("update inventory last release: %w", err)
	}

	return nil
}
