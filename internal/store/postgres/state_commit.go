package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const committedLockExpiry = 30 * time.Second

// stateCommit wraps fenced state and lock writes in one transaction.
type stateCommit struct {
	store *Store
	fence namespace.Fence
}

// newStateCommit builds the unexported transaction guard for fenced state
// and lock writes. Only it may mark a state version current or a lock held.
func newStateCommit(store *Store, fence namespace.Fence) stateCommit {
	return stateCommit{store: store, fence: fence}
}

func (c stateCommit) FinalizeStateVersion(ctx context.Context, sv workspace.StateVersion) error {
	row, err := codec.FinalizeStateVersionRow(sv)
	if err != nil {
		return err
	}
	return c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		return finalizeInTx(
			ctx, unitStateTx{q: q, logger: c.store.logger, retryFinalize: true}, sv, row, nil,
		)
	})
}

func (c stateCommit) AcquireLock(
	ctx context.Context, id workspace.ID, lock workspace.Lock,
) (workspace.Workspace, error) {
	holder, reason, lockID, at := lock.Holder, lock.Reason, lock.ID, lock.LockedAt

	var got workspace.Workspace
	err := c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		wrow, err := q.LockWorkspaceForFinalize(ctx, id.String())
		if err != nil {
			return err
		}
		w := codec.FromWorkspaceRow(codec.WorkspaceRow{
			Namespace:           wrow.Namespace,
			NamespaceID:         "",
			Name:                wrow.Name,
			ID:                  wrow.ID,
			LockID:              wrow.LockID,
			LockHolder:          wrow.LockHolder,
			LockReason:          wrow.LockReason,
			CurrentStateVersion: wrow.CurrentStateVersionID,
			AllowForcedWrites:   wrow.AllowForcedWrites,
			AllowUnlockedWrites: wrow.AllowUnlockedWrites,
			Revision:            wrow.Revision,
			LockedAtMs:          wrow.LockedAtMs,
			CreatedAtMs:         wrow.CreatedAtMs,
			UpdatedAtMs:         wrow.UpdatedAtMs,
			LastReleaseLockID:   wrow.LastReleaseLockID,
			LastReleaseHolder:   wrow.LastReleaseHolder,
			LastReleaseBy:       wrow.LastReleaseBy,
			LastReleaseKind:     wrow.LastReleaseKind,
			LastReleaseAtMs:     wrow.LastReleaseAtMs,
		})
		if w.Lock.Locked() {
			if w.Lock.Holder == holder && w.Lock.ID == lockID {
				got = w
				return nil
			}
			if at.Sub(w.Lock.LockedAt) < committedLockExpiry || w.Lock.Holder == holder {
				return workspace.ErrLocked
			}
		}
		w.Lock = workspace.Lock{LockedAt: at, ID: lockID, Holder: holder, Reason: reason}
		w.Revision++
		w.UpdatedAt = at
		if err := saveLockRow(ctx, q, w); err != nil {
			return err
		}
		got = w
		return nil
	})
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("acquire lock %s: %w", id, err)
	}
	return got, nil
}

func (c stateCommit) ReleaseLock(
	ctx context.Context, id workspace.ID, holder, lockID string, at time.Time,
) (workspace.Workspace, error) {
	var got workspace.Workspace
	err := c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		wrow, err := q.LockWorkspaceForFinalize(ctx, id.String())
		if err != nil {
			return err
		}
		w := codec.FromWorkspaceRow(codec.WorkspaceRow{
			Namespace:           wrow.Namespace,
			NamespaceID:         "",
			Name:                wrow.Name,
			ID:                  wrow.ID,
			LockID:              wrow.LockID,
			LockHolder:          wrow.LockHolder,
			LockReason:          wrow.LockReason,
			CurrentStateVersion: wrow.CurrentStateVersionID,
			AllowForcedWrites:   wrow.AllowForcedWrites,
			AllowUnlockedWrites: wrow.AllowUnlockedWrites,
			Revision:            wrow.Revision,
			LockedAtMs:          wrow.LockedAtMs,
			CreatedAtMs:         wrow.CreatedAtMs,
			UpdatedAtMs:         wrow.UpdatedAtMs,
			LastReleaseLockID:   wrow.LastReleaseLockID,
			LastReleaseHolder:   wrow.LastReleaseHolder,
			LastReleaseBy:       wrow.LastReleaseBy,
			LastReleaseKind:     wrow.LastReleaseKind,
			LastReleaseAtMs:     wrow.LastReleaseAtMs,
		})
		repeat := workspace.ReleaseRepeat{LockID: lockID, Holder: "", Caller: holder, Kind: workspace.ReleaseNormal}
		if w.RepeatsRelease(at, repeat) {
			got = w
			return nil
		}
		if !w.Lock.Locked() {
			return workspace.ErrNotLocked
		}
		if w.Lock.Holder != holder || (lockID != "" && w.Lock.ID != lockID) {
			return workspace.ErrLockedByOther
		}
		w.LastRelease = workspace.Release{
			At:     at,
			LockID: w.Lock.ID,
			Holder: w.Lock.Holder,
			By:     holder,
			Kind:   workspace.ReleaseNormal,
		}
		w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
		w.Revision++
		w.UpdatedAt = at
		if err := saveLockRow(ctx, q, w); err != nil {
			return err
		}
		got = w
		return nil
	})
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("release lock %s: %w", id, err)
	}
	return got, nil
}

func saveLockRow(ctx context.Context, q *db.Queries, w workspace.Workspace) error {
	row := codec.ToWorkspaceRow(w)
	n, err := q.SaveWorkspace(ctx, db.SaveWorkspaceParams{
		Revision:            row.Revision,
		LockID:              row.LockID,
		LockHolder:          row.LockHolder,
		LockReason:          row.LockReason,
		LockedAtMs:          row.LockedAtMs,
		UpdatedAtMs:         row.UpdatedAtMs,
		ID:                  row.ID,
		ExpectedRevision:    row.Revision - 1,
		AllowForcedWrites:   row.AllowForcedWrites,
		AllowUnlockedWrites: row.AllowUnlockedWrites,
		LastReleaseLockID:   row.LastReleaseLockID,
		LastReleaseHolder:   row.LastReleaseHolder,
		LastReleaseBy:       row.LastReleaseBy,
		LastReleaseKind:     row.LastReleaseKind,
		LastReleaseAtMs:     row.LastReleaseAtMs,
	})
	if err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	if n == 0 {
		return workspace.ErrConcurrentModification
	}
	key := inventoryLockKey{
		namespace: w.Ref.Namespace.String(),
		tool:      inventoryToolOpenTofu,
		name:      w.Ref.Name.String(),
	}
	if err := writeInventoryLock(ctx, q, key, workspaceLock(w.Lock)); err != nil {
		return err
	}
	return writeWorkspaceLastRelease(ctx, q, w)
}
