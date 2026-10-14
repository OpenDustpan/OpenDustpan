package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// RestoreStack implements stack.Store. In one fenced transaction with the
// audit entry e, it brings back the deleted stack id under newName, or under
// its name before the delete when newName is empty, and moves its inventory
// entry from the deleted units back to the live projection (ADR 47). The
// transaction holds the namespace row FOR UPDATE, as every audited write
// does, so a restore and a purge of the same stack run one after the other,
// and the unique key on the name refuses a concurrent create.
func (s *Store) RestoreStack(
	ctx context.Context, id stack.ID, newName stack.Name, fence namespace.Fence, e audit.Entry,
) (stack.Ref, error) {
	var ref stack.Ref

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, getErr := q.GetDeletedStack(
			ctx,
			db.GetDeletedStackParams{Namespace: fence.Namespace.String(), ID: id.String()},
		)
		if errors.Is(getErr, pgx.ErrNoRows) {
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

		return restoreStackRow(ctx, q, ref, id, e)
	})
	if err != nil {
		return stack.Ref{}, err
	}

	return ref, nil
}

// restoreStackRow renames the deleted stack id to ref and moves its
// inventory entry back. It returns ErrAlreadyExists if a live stack holds the
// name of ref.
func restoreStackRow(ctx context.Context, q *db.Queries, ref stack.Ref, id stack.ID, e audit.Entry) error {
	_, liveErr := q.GetStackID(ctx, db.GetStackIDParams{
		Namespace: ref.Namespace.String(), Project: ref.Project.String(), Name: ref.Stack.String(),
	})

	switch {
	case liveErr == nil:
		return stack.ErrAlreadyExists
	case !errors.Is(liveErr, pgx.ErrNoRows):
		return fmt.Errorf("select stack: %w", liveErr)
	}

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

	delParams := db.DeleteDeletedInventoryParams{Tool: inventoryTool, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	return publishStackInventory(ctx, q, ref, id.String(), e.At.UnixMilli())
}

// PurgeStack implements stack.Store. In one fenced transaction with the
// audit entry e, it removes the deleted stack id with every child row, its
// data key and its deleted inventory entry (ADR 47). A live stack is not a
// deleted stack, so it answers ErrNotFound and stays.
func (s *Store) PurgeStack(ctx context.Context, id stack.ID, fence namespace.Fence, e audit.Entry) error {
	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		e.ObjectID = id.String()

		if err := purgeStackRows(ctx, q, fence.Namespace, id); err != nil {
			return err
		}

		return revokeUnitGrants(ctx, q, id.String(), e)
	})
}

// purgeStackRows checks that the namespace has the deleted stack id, then
// deletes its child rows in foreign-key order, the stack row and its deleted
// inventory entry. It returns ErrNotFound for a live or unknown stack before
// it deletes anything.
func purgeStackRows(ctx context.Context, q *db.Queries, ns namespace.Name, id stack.ID) error {
	_, err := q.GetDeletedStack(ctx, db.GetDeletedStackParams{Namespace: ns.String(), ID: id.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select deleted stack: %w", err)
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

	return nil
}

// RestoreWorkspace implements workspace.Store. In one fenced transaction
// with the audit entry e, it brings back the deleted workspace id under
// newName, or under its name before the delete when newName is empty, and
// moves its inventory entry back (ADR 47).
func (s *Store) RestoreWorkspace(
	ctx context.Context, id workspace.ID, newName workspace.Name, fence namespace.Fence, e audit.Entry,
) (workspace.Ref, error) {
	var ref workspace.Ref

	err := s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		deletedName, getErr := q.GetDeletedWorkspace(ctx, db.GetDeletedWorkspaceParams{
			Namespace: fence.Namespace.String(), ID: id.String(),
		})
		if errors.Is(getErr, pgx.ErrNoRows) {
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

		return restoreWorkspaceRow(ctx, q, ref, id, e)
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
	ctx context.Context, q *db.Queries, ref workspace.Ref, id workspace.ID, e audit.Entry,
) error {
	_, liveErr := q.GetWorkspace(ctx, db.GetWorkspaceParams{Namespace: ref.Namespace.String(), Name: ref.Name.String()})

	switch {
	case liveErr == nil:
		return workspace.ErrAlreadyExists
	case !errors.Is(liveErr, pgx.ErrNoRows):
		return fmt.Errorf("select workspace: %w", liveErr)
	}

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

	delParams := db.DeleteDeletedInventoryParams{Tool: inventoryToolOpenTofu, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	return publishWorkspaceInventory(ctx, q, ref, id.String(), e.At.UnixMilli())
}

// PurgeWorkspace implements workspace.Store. In one fenced transaction with
// the audit entry e, it removes the deleted workspace id with its state
// versions, its data key and its deleted inventory entry (ADR 47).
func (s *Store) PurgeWorkspace(ctx context.Context, id workspace.ID, fence namespace.Fence, e audit.Entry) error {
	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		e.ObjectID = id.String()

		if err := purgeWorkspaceRows(ctx, q, fence.Namespace, id); err != nil {
			return err
		}

		return revokeUnitGrants(ctx, q, id.String(), e)
	})
}

// purgeWorkspaceRows checks that the namespace has the deleted workspace id,
// then deletes its state versions, its data key, the workspace row and its
// deleted inventory entry. It returns ErrNotFound for a live or unknown
// workspace before it deletes anything.
func purgeWorkspaceRows(ctx context.Context, q *db.Queries, ns namespace.Name, id workspace.ID) error {
	_, err := q.GetDeletedWorkspace(ctx, db.GetDeletedWorkspaceParams{Namespace: ns.String(), ID: id.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return workspace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select deleted workspace: %w", err)
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

	delParams := db.DeleteDeletedInventoryParams{Tool: inventoryToolOpenTofu, UnitID: id.String()}
	if _, err = q.DeleteDeletedInventory(ctx, delParams); err != nil {
		return fmt.Errorf("delete deleted inventory entry: %w", err)
	}

	return nil
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
