package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Finalize holds the workspace row FOR UPDATE for the whole transaction, so
// two finalizes on one workspace cannot both pass the serial check. Every
// write first takes the namespace row FOR SHARE (the fence).

var (
	_ workspace.Store      = (*Store)(nil)
	_ workspace.StateStore = (*Store)(nil)
)

// CreateWorkspace implements workspace.Store with a fenced insert. The
// inventory entry is written in the same transaction.
func (s *Store) CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	row := codec.ToWorkspaceRow(w)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
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
			CreatedAtMs:           row.CreatedAtMs,
			UpdatedAtMs:           row.UpdatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("insert workspace: %w", err)
		}

		if n == 0 {
			return workspace.ErrAlreadyExists
		}

		return publishWorkspaceInventory(ctx, q, w.Ref, row.ID, row.UpdatedAtMs)
	})
}

// GetWorkspace implements workspace.Store.
func (s *Store) GetWorkspace(ctx context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	row, err := s.queries.GetWorkspace(ctx, db.GetWorkspaceParams{
		Namespace: ref.Namespace.String(),
		Name:      ref.Name.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("select workspace: %w", err)
	}

	return workspaceFromRow(row), nil
}

// GetWorkspaceByID implements workspace.Store.
func (s *Store) GetWorkspaceByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	row, err := s.queries.GetWorkspaceByID(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("select workspace: %w", err)
	}

	return workspaceFromRow(row), nil
}

// ListWorkspaces implements workspace.Store.
func (s *Store) ListWorkspaces(ctx context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	rows, err := s.queries.ListWorkspaces(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select workspaces: %w", err)
	}

	out := make([]workspace.Workspace, 0, len(rows))
	for i := range rows {
		out = append(out, workspaceFromRow(rows[i]))
	}

	return out, nil
}

// SaveWorkspace implements workspace.Store: a compare-and-swap on the
// revision. The inventory entry moves with the write in the same transaction.
func (s *Store) SaveWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	row := codec.ToWorkspaceRow(w)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.SaveWorkspace(ctx, db.SaveWorkspaceParams{
			Revision:         row.Revision,
			LockID:           row.LockID,
			LockHolder:       row.LockHolder,
			LockReason:       row.LockReason,
			LockedAtMs:       row.LockedAtMs,
			UpdatedAtMs:      row.UpdatedAtMs,
			ID:               row.ID,
			ExpectedRevision: row.Revision - 1,
		})
		if err != nil {
			return fmt.Errorf("update workspace: %w", err)
		}

		if n > 0 {
			if pubErr := publishWorkspaceInventory(ctx, q, w.Ref, row.ID, row.UpdatedAtMs); pubErr != nil {
				return pubErr
			}

			// The lock moves with the write in the same transaction: lock,
			// unlock and force-unlock all save here.
			return writeInventoryLock(
				ctx, q, inventoryLockKey{
					namespace: w.Ref.Namespace.String(),
					tool:      inventoryToolOpenTofu,
					name:      w.Ref.Name.String(),
				}, workspaceLock(w.Lock),
			)
		}

		exists, err := q.WorkspaceExists(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("check workspace: %w", err)
		}

		if !exists {
			return workspace.ErrNotFound
		}

		return workspace.ErrConcurrentModification
	})
}

// DeleteWorkspace implements workspace.Store: it removes the workspace with
// its state versions, its wrapped data key and its inventory entry in one
// fenced transaction. The data key shares the secret_keys table with stack
// scopes, keyed by the workspace ID, so DeleteSecretKeysOfStack removes it.
func (s *Store) DeleteWorkspace(ctx context.Context, ref workspace.Ref, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		row, getErr := q.GetWorkspace(ctx, db.GetWorkspaceParams{
			Namespace: ref.Namespace.String(),
			Name:      ref.Name.String(),
		})
		if errors.Is(getErr, pgx.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select workspace: %w", getErr)
		}

		if keyErr := q.DeleteSecretKeysOfStack(ctx, row.ID); keyErr != nil {
			return fmt.Errorf("delete secret key: %w", keyErr)
		}

		if versionsErr := q.DeleteStateVersionsOfWorkspace(ctx, row.ID); versionsErr != nil {
			return fmt.Errorf("delete state versions: %w", versionsErr)
		}

		n, deleteErr := q.DeleteWorkspace(ctx, db.DeleteWorkspaceParams{
			Namespace: ref.Namespace.String(),
			Name:      ref.Name.String(),
		})
		if deleteErr != nil {
			return fmt.Errorf("delete workspace: %w", deleteErr)
		}

		if n == 0 {
			return workspace.ErrNotFound
		}

		entry := db.DeleteInventoryParams{
			Namespace: ref.Namespace.String(),
			Tool:      inventoryToolOpenTofu,
			Name:      ref.Name.String(),
		}
		if inventoryErr := q.DeleteInventory(ctx, entry); inventoryErr != nil {
			return fmt.Errorf("delete inventory entry: %w", inventoryErr)
		}

		return nil
	})
}

// CreateStateVersion implements workspace.StateStore with a fenced insert. A
// pending version changes no current state, so it writes no inventory entry.
// The workspace row stays locked while the pending count and the insert
// run, so concurrent declares never pass the cap together.
func (s *Store) CreateStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	row := codec.ToStateVersionRow(sv)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if _, err := q.LockWorkspaceForDeclare(ctx, row.WorkspaceID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return workspace.ErrNotFound
			}

			return fmt.Errorf("lock workspace: %w", err)
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

		err = q.CreateStateVersion(ctx, db.CreateStateVersionParams{
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
		})
		if err != nil {
			return fmt.Errorf("insert state version: %w", err)
		}

		return nil
	})
}

// GetStateVersion implements workspace.StateStore.
func (s *Store) GetStateVersion(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	row, err := s.queries.GetStateVersion(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
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
	row, err := s.queries.GetCurrentStateVersion(ctx, workspaceID.String())
	if errors.Is(err, pgx.ErrNoRows) {
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
	page = normalizeStatePage(page)

	total, err := s.queries.CountStateVersions(ctx, id.String())
	if err != nil {
		return nil, 0, fmt.Errorf("count state versions: %w", err)
	}

	rows, err := s.queries.ListStateVersions(ctx, db.ListStateVersionsParams{
		WorkspaceID: id.String(),
		Limit:       clampInt32(page.Limit),
		Offset:      clampInt32(page.Offset),
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
	n, err := s.queries.CountPendingStateVersions(ctx, id.String())
	if err != nil {
		return 0, fmt.Errorf("count pending state versions: %w", err)
	}

	return int(n), nil
}

// ReferencedContentKeys implements workspace.StateStore: every blob key the
// versions of the workspace name, pending and finalized, without the empty
// ones. A workspace without versions, or one that does not exist, yields an
// empty list.
func (s *Store) ReferencedContentKeys(ctx context.Context, id workspace.ID) ([]blob.Key, error) {
	keys, err := s.queries.ListStateContentKeys(ctx, id.String())
	if err != nil {
		return nil, fmt.Errorf("select state content keys: %w", err)
	}

	out := make([]blob.Key, 0, len(keys))

	for _, key := range keys {
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
	var superseded blob.Key

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		stored, err := q.GetStateVersion(ctx, id.String())
		if errors.Is(err, pgx.ErrNoRows) {
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

// FinalizeStateVersion implements workspace.StateStore. The workspace row
// stays locked FOR UPDATE for the whole transaction: the stored version is
// still pending, the workspace is held by the lock the version was declared
// under, and, unless forced, the lineage matches and the serial is newer. The
// workspace stamp and the inventory entry move with the finalize.
func (s *Store) FinalizeStateVersion(
	ctx context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	row := codec.ToStateVersionRow(sv)

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return finalizeInTx(ctx, q, sv, row)
	})
}

// ListUnsealedStateVersions implements workspace.StateStore: the finalized
// versions that still hold plain content, ordered by version ID.
func (s *Store) ListUnsealedStateVersions(ctx context.Context) ([]workspace.StateVersion, error) {
	rows, err := s.queries.ListUnsealedStateVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("select unsealed state versions: %w", err)
	}

	out := make([]workspace.StateVersion, 0, len(rows))

	for i := range rows {
		sv, err := stateFromRow(rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, sv)
	}

	return out, nil
}

// SealStateVersion implements workspace.StateStore: it moves one unsealed
// version to its sealed blob with its sealed outputs and marks it sealed in
// one fenced transaction. A version that is already sealed succeeds and
// changes nothing.
func (s *Store) SealStateVersion(
	ctx context.Context,
	id workspace.StateVersionID,
	content blob.Key,
	outputs []byte,
	fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.SealStateVersion(ctx, db.SealStateVersionParams{
			ContentKey: content.String(),
			Outputs:    outputs,
			ID:         id.String(),
		})
		if err != nil {
			return fmt.Errorf("seal state version: %w", err)
		}

		if n > 0 {
			return nil
		}

		_, getErr := q.GetStateVersion(ctx, id.String())
		if errors.Is(getErr, pgx.ErrNoRows) {
			return workspace.ErrStateVersionNotFound
		}

		if getErr != nil {
			return fmt.Errorf("read sealed state version: %w", getErr)
		}

		return nil
	})
}

// finalizeInTx runs the finalize checks and writes in one transaction, holding
// the workspace row FOR UPDATE throughout.
func finalizeInTx(
	ctx context.Context, q *db.Queries, sv workspace.StateVersion, row codec.StateVersionRow,
) error {
	wrow, err := checkFinalizeGuards(ctx, q, sv, row)
	if err != nil {
		return err
	}

	return writeFinalize(ctx, q, wrow, row)
}

// checkFinalizeGuards locks the workspace row and runs the port checks of a
// finalize, returning the locked workspace row the finalize stamps.
func checkFinalizeGuards(
	ctx context.Context, q *db.Queries, sv workspace.StateVersion, row codec.StateVersionRow,
) (db.Workspace, error) {
	wrow, err := lockWorkspace(ctx, q, row.WorkspaceID)
	if err != nil {
		return db.Workspace{}, err
	}

	if pendingErr := checkFinalizePending(ctx, q, row.ID); pendingErr != nil {
		return db.Workspace{}, pendingErr
	}

	if lockErr := checkFinalizeLock(wrow, row.LockID); lockErr != nil {
		return db.Workspace{}, lockErr
	}

	if !sv.Force {
		if orderErr := checkFinalizeOrder(ctx, q, row); orderErr != nil {
			return db.Workspace{}, orderErr
		}
	}

	return wrow, nil
}

// writeFinalize marks the version finalized, moves the workspace pointer and
// publishes the inventory entry. The write matches only the recorded attempt
// key, so a superseded attempt reports ErrUploadDenied instead of
// finalizing over a newer one.
func writeFinalize(
	ctx context.Context, q *db.Queries, wrow db.Workspace, row codec.StateVersionRow,
) error {
	n, err := q.FinalizeStateVersion(ctx, db.FinalizeStateVersionParams{
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

	return publishWorkspaceInventory(ctx, q, workspaceRef(wrow), wrow.ID, row.FinalizedAtMs)
}

// lockWorkspace locks the workspace row for a finalize and returns it.
func lockWorkspace(ctx context.Context, q *db.Queries, workspaceID string) (db.Workspace, error) {
	wrow, err := q.LockWorkspaceForFinalize(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Workspace{}, workspace.ErrNotFound
	}

	if err != nil {
		return db.Workspace{}, fmt.Errorf("lock workspace: %w", err)
	}

	return wrow, nil
}

// checkFinalizePending returns ErrUploadDenied unless the stored version is
// still pending, and ErrStateVersionNotFound when it does not exist.
func checkFinalizePending(ctx context.Context, q *db.Queries, id string) error {
	stored, err := q.GetStateVersion(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
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

// checkFinalizeLock checks that the workspace is still held by the lock the
// version was declared under.
func checkFinalizeLock(wrow db.Workspace, lockID string) error {
	switch {
	case wrow.LockHolder == "":
		return workspace.ErrNotLocked
	case wrow.LockID != lockID:
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
	if errors.Is(err, pgx.ErrNoRows) {
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

// workspaceFromRow converts a workspace row into a domain workspace. The
// current state pointer stays in SQL; the domain reads current state
// through CurrentStateVersion.
func workspaceFromRow(row db.Workspace) workspace.Workspace {
	return codec.FromWorkspaceRow(codec.WorkspaceRow{
		Namespace:           row.Namespace,
		Name:                row.Name,
		ID:                  row.ID,
		LockID:              row.LockID,
		LockHolder:          row.LockHolder,
		LockReason:          row.LockReason,
		CurrentStateVersion: row.CurrentStateVersionID,
		Revision:            row.Revision,
		LockedAtMs:          row.LockedAtMs,
		CreatedAtMs:         row.CreatedAtMs,
		UpdatedAtMs:         row.UpdatedAtMs,
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
