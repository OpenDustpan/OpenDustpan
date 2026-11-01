package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// RestoreStack implements stack.Store. In one fenced transaction with the
// audit entry e, it brings back the deleted stack id under newName, or under
// its name before the delete when newName is empty, and moves its inventory
// entry from the deleted units back to the live projection (ADR 47). The
// transaction holds the write lock from the start, so the name check and the
// rename see the same rows.
func (s *Store) RestoreStack(
	ctx context.Context, id stack.ID, newName stack.Name, fence namespace.Fence, e audit.Entry,
) (stack.Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ref stack.Ref

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, getErr := q.GetDeletedStack(
			ctx,
			db.GetDeletedStackParams{Namespace: fence.Namespace.String(), ID: id.String()},
		)
		if errors.Is(getErr, sql.ErrNoRows) {
			return stack.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select deleted stack: %w", getErr)
		}

		name := newName
		if name == "" {
			name = stack.Name(row.DeletedName.String)
		}

		ref = stack.Ref{Namespace: fence.Namespace, Project: stack.ProjectName(row.Project), Stack: name}
		e.ObjectID = id.String()
		e.Object = restoredReference(
			inventoryTool,
			fence.Namespace,
			ref.Project.String()+"/"+ref.Stack.String(),
			e.Object,
		)

		return restoreStackRow(ctx, q, ref, deletedStack{id: id, name: stack.Name(row.DeletedName.String)}, &e)
	})
	if err != nil {
		return stack.Ref{}, err
	}

	return ref, nil
}

// deletedStack names the deleted stack that a restore brings back: its ID and
// its name before the delete.
type deletedStack struct {
	id   stack.ID
	name stack.Name
}

// deletedWorkspace names the deleted workspace that a restore brings back: its
// ID and its name before the delete.
type deletedWorkspace struct {
	id   workspace.ID
	name workspace.Name
}

// restoreStackRow renames the deleted stack id to ref and moves its
// inventory entry back. It returns ErrAlreadyExists if a live stack holds the
// name of ref.
func restoreStackRow(ctx context.Context, q *db.Queries, ref stack.Ref, deleted deletedStack, e *audit.Entry) error {
	id := deleted.id

	_, liveErr := q.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(), Project: ref.Project.String(), Name: ref.Stack.String(),
	})

	switch {
	case liveErr == nil:
		return stack.ErrAlreadyExists
	case !errors.Is(liveErr, sql.ErrNoRows):
		return fmt.Errorf("select stack: %w", liveErr)
	}

	from := unitGate{
		ns:   ref.Namespace,
		tool: inventoryTool,
		name: stackUnitName(stack.Ref{Namespace: ref.Namespace, Project: ref.Project, Stack: deleted.name}),
	}

	moved, err := restoreUnitGate(ctx, q, from, stackUnitName(ref), audit.MaxChanges-len(e.Changes))
	if err != nil {
		return err
	}

	e.Changes = append(e.Changes, moved...)

	n, err := q.RestoreStack(ctx, db.RestoreStackParams{
		Name: ref.Stack.String(), UpdatedAtMs: e.At.UnixMilli(), Namespace: ref.Namespace.String(), ID: id.String(),
	})
	if isUniqueViolation(err) {
		return stack.ErrAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("restore stack: %w", err)
	}

	if n == 0 {
		return stack.ErrNotFound
	}

	// The foreign key keeps every membership on a collection that exists, so
	// this removes nothing that the schema allows. It is defense only.
	if err = q.DeleteMissingCollectionMembership(ctx, id.String()); err != nil {
		return fmt.Errorf("check collection membership: %w", err)
	}

	delParams := db.DeleteDeletedInventoryParams{Tool: inventoryTool, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	pubErr := publishStackInventory(
		ctx,
		q,
		ref,
		unitWrite{unitID: id.String(), atMs: e.At.UnixMilli(), kind: inventory.ChangeRestored},
	)
	if pubErr != nil {
		return pubErr
	}

	return refuseRestoreUnderLock(ctx, q, collection.UnitRef{
		Namespace: ref.Namespace, Tool: inventoryTool, Name: stackUnitName(ref),
	}, "stack", ref.Project.String()+"/"+ref.Stack.String())
}

// PurgeStack implements stack.Store. In one fenced transaction with the
// audit entry e, it removes the deleted stack id with every child row, its
// data key and its deleted inventory entry (ADR 47). A live stack is not a
// deleted stack, so it answers ErrNotFound and stays.
func (s *Store) PurgeStack(
	ctx context.Context,
	id stack.ID,
	fence namespace.Fence,
	dueBy time.Time,
	e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.purgeUnit(ctx, fence, e, purgedUnit{tool: inventoryTool, id: id.String()}, func(q *db.Queries) error {
		return purgeStackRows(ctx, q, fence.Namespace, id, dueBy)
	})
}

// purgeStackRows checks that the namespace has the deleted stack id, then
// deletes its child rows in foreign-key order, the stack row and its deleted
// inventory entry. It returns ErrNotFound for a live or unknown stack, and
// for a stack whose retention period ends after a non-zero dueBy, before it
// deletes anything.
func purgeStackRows(ctx context.Context, q *db.Queries, ns namespace.Name, id stack.ID, dueBy time.Time) error {
	_, err := q.GetDeletedStack(ctx, db.GetDeletedStackParams{Namespace: ns.String(), ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
		return stack.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select deleted stack: %w", err)
	}

	due, err := retentionEnded(ctx, q, ns, purgedUnit{tool: inventoryTool, id: id.String()}, dueBy)
	if err != nil {
		return err
	}

	if !due {
		return stack.ErrNotFound
	}

	children := []struct {
		del  func(context.Context, string) error
		name string
	}{
		{del: q.DeleteEventsOfStack, name: "events"},
		{del: q.DeleteCheckpointsOfStack, name: "checkpoint rows"},
		{del: q.DeleteJournalSequencesOfStack, name: "journal sequences"},
		{del: q.DeleteJournalOfStack, name: "journal"},
		{del: q.DeleteUpdatesOfStack, name: "updates"},
		{del: q.DeleteStackTags, name: "tags"},
		{del: q.DeleteInventoryLabels, name: "labels"},
		{del: q.DeleteSecretKeysOfStack, name: "data key"},
	}

	for _, child := range children {
		if err = child.del(ctx, id.String()); err != nil {
			return fmt.Errorf("delete %s of stack: %w", child.name, err)
		}
	}

	n, err := q.PurgeStack(ctx, db.PurgeStackParams{Namespace: ns.String(), ID: id.String()})
	if err != nil {
		return fmt.Errorf("purge stack: %w", err)
	}

	if n == 0 {
		return stack.ErrNotFound
	}

	delParams := db.DeleteDeletedInventoryParams{Tool: inventoryTool, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	return purgeUnitContext(ctx, q, ns, id.String())
}

// RestoreWorkspace implements workspace.Store. In one fenced transaction
// with the audit entry e, it brings back the deleted workspace id under
// newName, or under its name before the delete when newName is empty, and
// moves its inventory entry back (ADR 47).
func (s *Store) RestoreWorkspace(
	ctx context.Context, id workspace.ID, newName workspace.Name, fence namespace.Fence, e audit.Entry,
) (workspace.Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ref workspace.Ref

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		deletedName, getErr := q.GetDeletedWorkspace(ctx, db.GetDeletedWorkspaceParams{
			Namespace: fence.Namespace.String(), ID: id.String(),
		})
		if errors.Is(getErr, sql.ErrNoRows) {
			return workspace.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select deleted workspace: %w", getErr)
		}

		name := newName
		if name == "" {
			name = workspace.Name(deletedName.String)
		}

		ref = workspace.Ref{Namespace: fence.Namespace, Name: name}
		e.ObjectID = id.String()
		e.Object = restoredReference(workspaceInventoryTool, fence.Namespace, name.String(), e.Object)

		return restoreWorkspaceRow(ctx, q, ref, deletedWorkspace{id: id, name: workspace.Name(deletedName.String)}, &e)
	})
	if err != nil {
		return workspace.Ref{}, err
	}

	return ref, nil
}

// restoreWorkspaceRow renames the deleted workspace id to ref and moves its
// inventory entry back. It returns ErrAlreadyExists if a live workspace holds
// the name of ref.
func restoreWorkspaceRow(
	ctx context.Context, q *db.Queries, ref workspace.Ref, deleted deletedWorkspace, e *audit.Entry,
) error {
	id := deleted.id

	_, liveErr := q.GetWorkspace(ctx, db.GetWorkspaceParams{Namespace: ref.Namespace.String(), Name: ref.Name.String()})

	switch {
	case liveErr == nil:
		return workspace.ErrAlreadyExists
	case !errors.Is(liveErr, sql.ErrNoRows):
		return fmt.Errorf("select workspace: %w", liveErr)
	}

	from := unitGate{ns: ref.Namespace, tool: workspaceInventoryTool, name: deleted.name.String()}

	moved, err := restoreUnitGate(ctx, q, from, ref.Name.String(), audit.MaxChanges-len(e.Changes))
	if err != nil {
		return err
	}

	e.Changes = append(e.Changes, moved...)

	n, err := q.RestoreWorkspace(ctx, db.RestoreWorkspaceParams{
		Name: ref.Name.String(), UpdatedAtMs: e.At.UnixMilli(), Namespace: ref.Namespace.String(), ID: id.String(),
	})
	if isUniqueViolation(err) {
		return workspace.ErrAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("restore workspace: %w", err)
	}

	if n == 0 {
		return workspace.ErrNotFound
	}

	// The foreign key keeps every membership on a collection that exists, so
	// this removes nothing that the schema allows. It is defense only.
	if err = q.DeleteMissingCollectionMembership(ctx, id.String()); err != nil {
		return fmt.Errorf("check collection membership: %w", err)
	}

	delParams := db.DeleteDeletedInventoryParams{Tool: workspaceInventoryTool, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	pubErr := publishWorkspaceInventory(
		ctx,
		q,
		ref,
		unitWrite{unitID: id.String(), atMs: e.At.UnixMilli(), kind: inventory.ChangeRestored},
	)
	if pubErr != nil {
		return pubErr
	}

	return refuseRestoreUnderLock(ctx, q, collection.UnitRef{
		Namespace: ref.Namespace, Tool: workspaceInventoryTool, Name: ref.Name.String(),
	}, "workspace", ref.Name.String())
}

// PurgeWorkspace implements workspace.Store. In one fenced transaction with
// the audit entry e, it removes the deleted workspace id with its blame, its state
// versions, its data key and its deleted inventory entry (ADR 47).
func (s *Store) PurgeWorkspace(
	ctx context.Context,
	id workspace.ID,
	fence namespace.Fence,
	dueBy time.Time,
	e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.purgeUnit(
		ctx,
		fence,
		e,
		purgedUnit{tool: workspaceInventoryTool, id: id.String()},
		func(q *db.Queries) error {
			return purgeWorkspaceRows(ctx, q, fence.Namespace, id, dueBy)
		},
	)
}

// purgeWorkspaceRows checks that the namespace has the deleted workspace id,
// then deletes its state versions, its data key, the workspace row and its
// deleted inventory entry. It returns ErrNotFound for a live or unknown
// workspace, and for a workspace whose retention period ends after a
// non-zero dueBy, before it deletes anything.
func purgeWorkspaceRows(
	ctx context.Context, q *db.Queries, ns namespace.Name, id workspace.ID, dueBy time.Time,
) error {
	_, err := q.GetDeletedWorkspace(ctx, db.GetDeletedWorkspaceParams{Namespace: ns.String(), ID: id.String()})
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select deleted workspace: %w", err)
	}

	due, err := retentionEnded(ctx, q, ns, purgedUnit{tool: workspaceInventoryTool, id: id.String()}, dueBy)
	if err != nil {
		return err
	}

	if !due {
		return workspace.ErrNotFound
	}

	err = deleteWorkspaceBlame(ctx, q, id)
	if err != nil {
		return err
	}

	if err = q.DeleteStateVersionsOfWorkspace(ctx, id.String()); err != nil {
		return fmt.Errorf("delete state versions of workspace: %w", err)
	}

	if err = q.DeleteSecretKeysOfStack(ctx, id.String()); err != nil {
		return fmt.Errorf("delete data key of workspace: %w", err)
	}

	n, err := q.PurgeWorkspace(ctx, db.PurgeWorkspaceParams{Namespace: ns.String(), ID: id.String()})
	if err != nil {
		return fmt.Errorf("purge workspace: %w", err)
	}

	if n == 0 {
		return workspace.ErrNotFound
	}

	delParams := db.DeleteDeletedInventoryParams{Tool: workspaceInventoryTool, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	return purgeUnitContext(ctx, q, ns, id.String())
}

// deleteWorkspaceBlame deletes the resource change rows, the blame items and
// the blame cursor of the workspace id.
func deleteWorkspaceBlame(ctx context.Context, q *db.Queries, id workspace.ID) error {
	if err := q.DeleteWorkspaceResourceChanges(ctx, id.String()); err != nil {
		return fmt.Errorf("delete resource changes of workspace: %w", err)
	}

	if err := q.DeleteWorkspaceBlameItems(ctx, id.String()); err != nil {
		return fmt.Errorf("delete blame items of workspace: %w", err)
	}

	if err := q.DeleteWorkspaceBlameCursor(ctx, id.String()); err != nil {
		return fmt.Errorf("delete blame cursor of workspace: %w", err)
	}

	return nil
}

// retentionEnded reports whether the retention period of the deleted unit
// ended at or before dueBy, from its deleted inventory entry, in the purge
// transaction. A zero dueBy skips the check: an admin purge acts before the
// end. The retention sweep sets it, so a unit that a restore and a new
// delete gave a new retention period after the sweep listed it stays.
func retentionEnded(
	ctx context.Context,
	q *db.Queries,
	ns namespace.Name,
	u purgedUnit,
	dueBy time.Time,
) (bool, error) {
	if dueBy.IsZero() {
		return true, nil
	}

	row, err := q.GetDeletedInventory(ctx, db.GetDeletedInventoryParams{
		Namespace: ns.String(), Tool: u.tool, UnitID: u.id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("select deleted inventory entry: %w", err)
	}

	return row.PurgeAfterMs <= dueBy.UnixMilli(), nil
}

// revokeUnitGrants ends every current grant on the purged unit id, at the
// time of e and by its author, in the purge transaction (ADR 47). A grant
// on a unit matches its stable ID, so a grant that stays would point at no
// unit. The rows stay for the access-review history (ADR 52, decision 10).
// A restore keeps the grants, because it undoes the delete.
func revokeUnitGrants(ctx context.Context, q *db.Queries, id string, e audit.Entry) error {
	_, err := q.RevokeGrantsOnTarget(ctx, db.RevokeGrantsOnTargetParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		RevokedBy:   e.Subject,
		Target:      unitTargetPrefix + id,
	})
	if err != nil {
		return fmt.Errorf("revoke grants of the purged unit: %w", err)
	}

	return nil
}

// purgedUnit names the unit of a purge: its stored tool code and its stable
// ID.
type purgedUnit struct {
	tool string
	id   string
}

// purgeUnit runs the purge of the unit u in one fenced transaction with the
// audit entry e (ADR 47): rows deletes the rows of the unit, then the purge
// records the blob purge and ends the grants on the unit. The audit entry
// carries the stable ID of the unit.
func (s *Store) purgeUnit(
	ctx context.Context, fence namespace.Fence, e audit.Entry, u purgedUnit, rows func(q *db.Queries) error,
) error {
	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		e.ObjectID = u.id

		// The purge row names the unit as the delete left it. The read
		// comes first, because rows deletes the deleted inventory entry.
		deleted, nameErr := q.GetDeletedInventory(ctx, db.GetDeletedInventoryParams{
			Namespace: fence.Namespace.String(), Tool: u.tool, UnitID: u.id,
		})
		if nameErr != nil && !errors.Is(nameErr, sql.ErrNoRows) {
			return fmt.Errorf("select deleted inventory entry: %w", nameErr)
		}

		if err := rows(q); err != nil {
			return err
		}

		removed, gateErr := purgeUnitGate(
			ctx, q, unitGate{ns: fence.Namespace, tool: u.tool, name: deleted.Name}, audit.MaxChanges-len(e.Changes),
		)
		if gateErr != nil {
			return gateErr
		}

		e.Changes = append(e.Changes, removed...)

		if err := deleteUnitLinks(ctx, q, fence.Namespace, u.id); err != nil {
			return err
		}

		if err := insertBlobPurge(ctx, q, fence.Namespace, u, e.At); err != nil {
			return err
		}

		if revokeErr := revokeUnitGrants(ctx, q, u.id, e); revokeErr != nil {
			return revokeErr
		}

		purged := unitChange(
			fence.Namespace,
			u.tool,
			deleted.Name,
			unitWrite{unitID: u.id, atMs: codec.Millis(e.At), kind: inventory.ChangePurged},
		)

		return appendInventoryChange(ctx, q, purged)
	})
}

// insertBlobPurge records the blob purge of the unit u under the ID of the
// namespace ns. The fenced transaction holds the namespace row, so a missing
// row is a store fault: the purge fails and rolls back, and no record is
// written with an empty ID.
func insertBlobPurge(ctx context.Context, q *db.Queries, ns namespace.Name, u purgedUnit, at time.Time) error {
	nsID, found, err := namespaceID(ctx, q, ns)
	if err != nil {
		return err
	}

	if !found {
		return fmt.Errorf("record blob purge: %w", namespace.ErrNotFound)
	}

	err = q.InsertBlobPurge(ctx, db.InsertBlobPurgeParams{
		Tool: u.tool, UnitID: u.id, NamespaceID: nsID, PurgedAtMs: codec.Millis(at),
	})
	if err != nil {
		return fmt.Errorf("record blob purge: %w", err)
	}

	return nil
}

// deleteUnitLinks deletes the rows that other features keep for the unit unitID
// of the namespace ns: its collection membership, its manual locks and its
// latest drift result, and its unit settings.
func deleteUnitLinks(ctx context.Context, q *db.Queries, ns namespace.Name, unitID string) error {
	if err := q.DeleteCollectionMembersOfUnit(ctx, unitID); err != nil {
		return fmt.Errorf("delete collection membership: %w", err)
	}

	err := q.DeleteManualLocksOfObject(ctx, db.DeleteManualLocksOfObjectParams{
		Kind: string(manuallock.KindUnit), ObjectID: unitID,
	})
	if err != nil {
		return fmt.Errorf("delete manual lock: %w", err)
	}

	err = q.DeleteDriftResult(ctx, db.DeleteDriftResultParams{UnitID: unitID, Namespace: ns.String()})
	if err != nil {
		return fmt.Errorf("delete drift result: %w", err)
	}

	err = q.DeleteUnitSettingsOfUnit(ctx, db.DeleteUnitSettingsOfUnitParams{UnitID: unitID, Namespace: ns.String()})
	if err != nil {
		return fmt.Errorf("delete unit settings: %w", err)
	}

	return nil
}

// maxBlobPurgeList bounds one list of blob purges.
const maxBlobPurgeList = 10000

// ListBlobPurges implements inventory.BlobPurges. It reads at most
// maxBlobPurgeList records.
func (s *Store) ListBlobPurges(
	ctx context.Context, after *inventory.BlobPurgeCursor, limit int,
) ([]inventory.BlobPurge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	size := int64(max(0, min(limit, maxBlobPurgeList)))

	if after == nil {
		rows, err := s.queries.ListBlobPurges(ctx, size)
		if err != nil {
			return nil, fmt.Errorf("list blob purges: %w", err)
		}

		out := make([]inventory.BlobPurge, 0, len(rows))
		for _, r := range rows {
			out = append(out, blobPurgeOf(r.Tool, r.UnitID, r.NamespaceID, r.PurgedAtMs))
		}

		return out, nil
	}

	rows, err := s.queries.ListBlobPurgesAfter(ctx, db.ListBlobPurgesAfterParams{
		AfterMs: codec.Millis(after.PurgedAt), AfterTool: after.Tool, AfterUnitID: after.UnitID, PageLimit: size,
	})
	if err != nil {
		return nil, fmt.Errorf("list blob purges: %w", err)
	}

	out := make([]inventory.BlobPurge, 0, len(rows))
	for _, r := range rows {
		out = append(out, blobPurgeOf(r.Tool, r.UnitID, r.NamespaceID, r.PurgedAtMs))
	}

	return out, nil
}

// blobPurgeOf builds the record of a purged unit from the namespace ID that the
// row holds.
func blobPurgeOf(tool, unitID, nsID string, purgedAtMs int64) inventory.BlobPurge {
	return inventory.BlobPurge{
		PurgedAt: codec.FromMillis(purgedAtMs), NamespaceID: namespace.ID(nsID), Tool: tool, UnitID: unitID,
	}
}

// CountBlobPurges implements inventory.BlobPurgeCounter.
func (s *Store) CountBlobPurges(ctx context.Context) (inventory.BlobPurgeBacklog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.CountBlobPurges(ctx)
	if err != nil {
		return inventory.BlobPurgeBacklog{}, fmt.Errorf("count blob purges: %w", err)
	}

	return blobPurgeBacklogOf(row.Pending, row.OldestMs), nil
}

// blobPurgeBacklogOf builds the backlog from the count and the purge time of
// the oldest record in milliseconds. The time stays zero when no record exists.
func blobPurgeBacklogOf(pending, oldestMs int64) inventory.BlobPurgeBacklog {
	out := inventory.BlobPurgeBacklog{Pending: pending, OldestPurgedAt: time.Time{}}
	if pending > 0 {
		out.OldestPurgedAt = codec.FromMillis(oldestMs)
	}

	return out
}

// FinishBlobPurge implements inventory.BlobPurges.
func (s *Store) FinishBlobPurge(ctx context.Context, tool, unitID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.inTx(ctx, func(q *db.Queries) error {
		return q.DeleteBlobPurge(ctx, db.DeleteBlobPurgeParams{Tool: tool, UnitID: unitID})
	})
	if err != nil {
		return fmt.Errorf("finish blob purge: %w", err)
	}

	return nil
}

// restoredReference returns the unit reference of a restored unit under its
// name after the restore, so the audit entry of the restore records the new
// name. The object ID links the entry to the delete entry, which keeps the
// old name. It returns fallback when the name makes no valid reference.
func restoredReference(tool string, ns namespace.Name, name, fallback string) string {
	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		return fallback
	}

	return obj.String()
}

// purgeUnitContext deletes the context entries of the unit and of its
// resources. The owner rows go with them by cascade.
func purgeUnitContext(ctx context.Context, q *db.Queries, ns namespace.Name, unitID string) error {
	nsID, err := q.GetNamespaceID(ctx, ns.String())
	if err != nil {
		return fmt.Errorf("select namespace ID: %w", err)
	}

	if err = q.DeleteUnitContext(ctx, db.DeleteUnitContextParams{NamespaceID: nsID, ObjectID: unitID}); err != nil {
		return fmt.Errorf("delete unit context: %w", err)
	}

	return nil
}

// refuseRestoreUnderLock returns an error that matches manuallock.ErrLocked
// when a manual lock covers the unit that a restore has just brought back, on
// the unit, on its collection or on its namespace. It runs in the transaction
// of the restore, after the unit is live again, so the error rolls the restore
// back: a lock stops every change, and a restore is a change.
//
// Defense in depth: the service reads the lock of the namespace before it calls
// the store. This check stays so that the store does not rely on its caller: it
// reads the lock of the unit, of its collection and of its namespace in the same
// transaction as the write, and a lock that appears after the service check
// still stops the restore.
func refuseRestoreUnderLock(
	ctx context.Context, q *db.Queries, unit collection.UnitRef, noun, name string,
) error {
	l, err := coveringLock(ctx, q, unit)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of restored %s: %w", noun, err)
	}

	return &manuallock.LockedError{Lock: l, Unit: unit, Noun: noun, Name: unit.Namespace.String() + "/" + name}
}

// deleteRefusal names what a refused delete says: base is the error of the
// tool that the refusal matches, noun and name describe the unit.
type deleteRefusal struct {
	base error
	noun string
	name string
}

// refuseDeleteUnderLock returns an error that matches base and
// manuallock.ErrLocked when a manual lock covers the unit that a soft delete is
// about to remove, on the unit, on its collection or on its namespace. It runs
// in the transaction of the delete, before the unit row changes, so the error
// rolls the delete back: a lock stops every change, and a delete is a change.
// Without it, a lock that commits after the service check leaves a deleted unit
// with a lock that no route can remove.
//
// Defense in depth: the service reads the covering lock before it calls the
// store. This check stays so that the store does not rely on its caller: it
// reads the locks in the same transaction as the write, which holds the row of
// the namespace, so a lock write of the same namespace either commits before
// this read or waits until the delete commits.
func refuseDeleteUnderLock(ctx context.Context, q *db.Queries, unit collection.UnitRef, refusal deleteRefusal) error {
	l, err := coveringLock(ctx, q, unit)
	if errors.Is(err, manuallock.ErrNotLocked) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read manual lock of %s: %w", refusal.noun, err)
	}

	return fmt.Errorf("%w: %w", refusal.base, &manuallock.LockedError{
		Lock: l, Unit: unit, Noun: refusal.noun, Name: unit.Namespace.String() + "/" + refusal.name,
	})
}

// refuseStackDeleteUnderLock is refuseDeleteUnderLock for the stack ref. The
// error matches stack.ErrUpdateConflict, as the refusal of the service does.
func refuseStackDeleteUnderLock(ctx context.Context, q *db.Queries, ref stack.Ref) error {
	return refuseDeleteUnderLock(ctx, q, collection.UnitRef{
		Namespace: ref.Namespace, Tool: inventoryTool, Name: stackUnitName(ref),
	}, deleteRefusal{base: stack.ErrUpdateConflict, noun: "stack", name: ref.Project.String() + "/" + ref.Stack.String()})
}

// refuseWorkspaceDeleteUnderLock is refuseDeleteUnderLock for the workspace
// ref. The error matches workspace.ErrLocked, as the refusal of the service does.
func refuseWorkspaceDeleteUnderLock(ctx context.Context, q *db.Queries, ref workspace.Ref) error {
	return refuseDeleteUnderLock(ctx, q, collection.UnitRef{
		Namespace: ref.Namespace, Tool: workspaceInventoryTool, Name: ref.Name.String(),
	}, deleteRefusal{base: workspace.ErrLocked, noun: "workspace", name: ref.Name.String()})
}
