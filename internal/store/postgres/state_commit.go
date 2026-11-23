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

// stateCommit wraps fenced state and lock writes in one transaction.
type stateCommit struct {
	store *Store
	fence namespace.Fence
	// retryFinalize lets a repeated finalize report the result that already
	// committed. Without it, a finalize of a version that is not pending is
	// refused as before.
	retryFinalize bool
}

// newStateCommit builds the unexported transaction guard for fenced state
// and lock writes. It marks a state version current or a lock held only
// inside one fenced transaction.
func newStateCommit(store *Store, fence namespace.Fence) stateCommit {
	return stateCommit{store: store, fence: fence, retryFinalize: false}
}

func (c stateCommit) FinalizeStateVersion(ctx context.Context, sv workspace.StateVersion) error {
	return c.commitFinalize(ctx, sv, nil)
}

func (c stateCommit) FinalizeStateVersionExpecting(
	ctx context.Context, sv workspace.StateVersion, expect workspace.StateVersionID,
) error {
	return c.commitFinalize(ctx, sv, &expect)
}

func (c stateCommit) AcquireLock(
	ctx context.Context, id workspace.ID, lock workspace.Lock,
) (workspace.Workspace, error) {
	holder, reason, lockID, at := lock.Holder, lock.Reason, lock.ID, lock.LockedAt

	var got workspace.Workspace
	err := c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		wrow, err := lockWorkspace(ctx, q, id.String())
		if err != nil {
			return err
		}
		w := workspaceFromRow(wrow, "")
		if w.Lock.Locked() {
			if w.Lock.Holder == holder && w.Lock.ID == lockID {
				got = w
				return nil
			}
			return workspace.ErrLocked
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
		wrow, err := lockWorkspace(ctx, q, id.String())
		if err != nil {
			return err
		}
		w := workspaceFromRow(wrow, "")
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

// withRetry returns the guard with the idempotent retry of a finalize.
func (c stateCommit) withRetry() stateCommit {
	c.retryFinalize = true
	return c
}

// commitFinalize finalizes sv in one fenced transaction, with the expected
// current version when expect is set.
func (c stateCommit) commitFinalize(
	ctx context.Context, sv workspace.StateVersion, expect *workspace.StateVersionID,
) error {
	row, err := codec.FinalizeStateVersionRow(sv)
	if err != nil {
		return err
	}

	return c.store.inFencedTx(ctx, c.fence, func(q *db.Queries) error {
		return finalizeInTx(
			ctx,
			unitStateTx{q: q, logger: c.store.logger, retryFinalize: c.retryFinalize},
			sv,
			row,
			expect,
		)
	})
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
