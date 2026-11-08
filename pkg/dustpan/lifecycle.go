package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The stored tool codes of the units that the lifecycles serve.
const (
	pulumiTool   = "pulumi"
	opentofuTool = "opentofu"
)

// purgeBlobTimeout bounds the blob deletes of one purge. They run on a
// context that the request cannot cancel, so a client that disconnects does
// not stop them halfway.
const purgeBlobTimeout = 5 * time.Minute

// blobPurger deletes the blobs of one purged unit after its rows are gone
// (ADR 47). It keeps the blob purge record of the unit. A save or an upload
// that passed its checks before the delete can still write a blob after
// this delete, and its SQL commit then fails. So only the sweep finishes the
// record: it deletes the blobs again after a grace period longer than any
// save, so no blob of a purged unit outlives it for good.
type blobPurger struct {
	logger *slog.Logger
}

// deleteBlobs deletes the blobs of the unit unitID of tool with
// deleteUnitBlobs, on a context that the request cannot cancel. It never
// fails the purge: the rows are already gone, and the record keeps the work
// for the sweep. No log line names a blob key.
func (b blobPurger) deleteBlobs(
	ctx context.Context, tool, unitID string, deleteUnitBlobs func(context.Context) error,
) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), purgeBlobTimeout)
	defer cancel()

	if err := deleteUnitBlobs(detached); err != nil {
		if wait, isWait := errors.AsType[*blobguard.WaitError](err); isWait {
			b.logger.InfoContext(ctx, "blobs of a purged unit wait for a running backup or restore",
				slog.String("tool", tool), slog.String("unit_id", unitID),
				slog.String("running", wait.Work.String()))

			return
		}

		b.logger.WarnContext(ctx, "blobs of a purged unit remain for the sweep",
			slog.String("tool", tool), slog.String("unit_id", unitID))
	}
}

// pulumiLifecycle implements inventory.Lifecycle for Pulumi stacks. A purge
// removes the checkpoint blobs after the rows.
type pulumiLifecycle struct {
	stacks  *stack.Service
	updates *stack.Updates
	blobs   blobPurger
}

var _ inventory.Lifecycle = pulumiLifecycle{
	stacks: nil, updates: nil, blobs: blobPurger{logger: nil},
}

// Restore implements inventory.Lifecycle. name is the stack name: the stack
// keeps its project. The answer is the inventory name, project/stack.
func (l pulumiLifecycle) Restore(
	ctx context.Context, ns namespace.Name, unitID, name string, e audit.Entry,
) (string, error) {
	var newName stack.Name

	if name != "" {
		parsed, err := stack.ParseName(name)
		if err != nil {
			return "", fmt.Errorf("%w: %w", inventory.ErrInvalidName, err)
		}

		newName = parsed
	}

	ref, err := l.stacks.RestoreDeleted(ctx, ns, stack.ID(unitID), newName, e)
	if err != nil {
		return "", lifecycleError(err, stack.ErrNotFound, stack.ErrAlreadyExists)
	}

	return ref.Project.String() + "/" + ref.Stack.String(), nil
}

// Purge implements inventory.Lifecycle. It deletes the blobs of the unit
// after the rows and keeps the blob purge record. A late write can still
// leave a blob after this delete: a save or upload that passed its checks
// before the update was canceled and the unit was force-deleted and purged
// writes its blob, and then its SQL commit fails. The retention sweep
// (inventory.Sweeper) deletes the blobs again after inventory.BlobPurgeGrace,
// which is longer than any such write, and only then finishes the record.
func (l pulumiLifecycle) Purge(ctx context.Context, ns namespace.Name, unitID string, e audit.Entry) error {
	return l.purgeUnit(ctx, ns, unitID, time.Time{}, e)
}

// PurgeExpired implements inventory.SweepTool: Purge, only when the
// retention period of the stack ended at or before dueBy.
func (l pulumiLifecycle) PurgeExpired(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	return l.purgeUnit(ctx, ns, unitID, dueBy, e)
}

// purgeUnit removes the deleted stack unitID with the retention check of dueBy,
// which zero turns off, and then deletes its blobs.
func (l pulumiLifecycle) purgeUnit(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	id := stack.ID(unitID)

	nsID, err := l.stacks.PurgeExpired(ctx, ns, id, dueBy, e)
	if err != nil {
		return lifecycleError(err, stack.ErrNotFound, stack.ErrAlreadyExists)
	}

	l.blobs.deleteBlobs(ctx, pulumiTool, unitID, func(c context.Context) error {
		return l.updates.DeleteStackBlobs(c, nsID, id)
	})

	return nil
}

// opentofuLifecycle implements inventory.Lifecycle for OpenTofu workspaces.
type opentofuLifecycle struct {
	workspaces *workspace.Service
	blobs      blobPurger
}

var _ inventory.Lifecycle = opentofuLifecycle{workspaces: nil, blobs: blobPurger{logger: nil}}

// Restore implements inventory.Lifecycle.
func (l opentofuLifecycle) Restore(
	ctx context.Context, ns namespace.Name, unitID, name string, e audit.Entry,
) (string, error) {
	var newName workspace.Name

	if name != "" {
		parsed, err := workspace.ParseName(name)
		if err != nil {
			return "", fmt.Errorf("%w: %w", inventory.ErrInvalidName, err)
		}

		newName = parsed
	}

	ref, err := l.workspaces.RestoreDeleted(ctx, ns, workspace.ID(unitID), newName, e)
	if err != nil {
		return "", lifecycleError(err, workspace.ErrNotFound, workspace.ErrAlreadyExists)
	}

	return ref.Name.String(), nil
}

// Purge implements inventory.Lifecycle. It deletes the blobs of the unit
// after the rows and keeps the blob purge record. A late write can still
// leave a blob after this delete: a state upload that passed its checks
// before the force-delete and purge writes its blob, and then its SQL commit
// fails. The retention sweep (inventory.Sweeper) deletes the blobs again
// after inventory.BlobPurgeGrace, which is longer than any such write, and
// only then finishes the record.
func (l opentofuLifecycle) Purge(ctx context.Context, ns namespace.Name, unitID string, e audit.Entry) error {
	return l.purgeUnit(ctx, ns, unitID, time.Time{}, e)
}

// PurgeExpired implements inventory.SweepTool: Purge, only when the
// retention period of the workspace ended at or before dueBy.
func (l opentofuLifecycle) PurgeExpired(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	return l.purgeUnit(ctx, ns, unitID, dueBy, e)
}

// purgeUnit removes the deleted workspace unitID with the retention check of
// dueBy, which zero turns off, and then deletes its blobs.
func (l opentofuLifecycle) purgeUnit(
	ctx context.Context, ns namespace.Name, unitID string, dueBy time.Time, e audit.Entry,
) error {
	id := workspace.ID(unitID)

	nsID, err := l.workspaces.PurgeExpired(ctx, ns, id, dueBy, e)
	if err != nil {
		return lifecycleError(err, workspace.ErrNotFound, workspace.ErrAlreadyExists)
	}

	l.blobs.deleteBlobs(ctx, opentofuTool, unitID, func(c context.Context) error {
		return l.workspaces.DeletePurgedBlobs(c, nsID, id)
	})

	return nil
}

// lifecycleError adds the tool-neutral inventory error to a tool error:
// notFound becomes inventory.ErrNotFound and taken becomes
// inventory.ErrNameTaken. Other errors pass unchanged.
func lifecycleError(err, notFound, taken error) error {
	switch {
	case errors.Is(err, notFound):
		return fmt.Errorf("%w: %w", inventory.ErrNotFound, err)
	case errors.Is(err, taken):
		return fmt.Errorf("%w: %w", inventory.ErrNameTaken, err)
	default:
		return err
	}
}

// deletedReadScope implements inventoryapi.DeletedReadScope with the scope
// of unit:read that the authorizer lists.
//
// units lists the units of a collection. It is nil on a server that holds no
// collections: a grant on a collection then covers no deleted unit.
type deletedReadScope struct {
	authz identity.Authorizer
	units access.CollectionUnits
}

var _ inventoryapi.DeletedReadScope = deletedReadScope{authz: nil, units: nil}

// ReadableUnits implements inventoryapi.DeletedReadScope. A grant of
// unit:read on the installation or on ns covers every deleted unit of ns. A
// grant on a unit covers that unit while it exists, so it gives nothing on a
// deleted unit here: ListObjects leaves out the units that do not exist.
// A grant on a collection of ns covers each deleted unit whose last
// collection it was: the membership of a deleted unit stays until the
// collection is deleted. A grant on a collection of another namespace
// covers nothing here.
func (s deletedReadScope) ReadableUnits(
	ctx context.Context, p identity.Principal, ns namespace.Name,
) (bool, []string, error) {
	scope, err := s.authz.ListObjects(ctx, p, identity.ActionUnitRead)
	if err != nil {
		return false, nil, fmt.Errorf("list readable units: %w", err)
	}

	if scope.CoversEveryObject() {
		return true, nil, nil
	}

	for i := range scope.Namespaces {
		obj := scope.Namespaces[i]
		if obj.Namespace() == ns {
			return true, nil, nil
		}
	}

	ids := []string{}

	for i := range scope.Units {
		obj := scope.Units[i]
		if obj.Namespace() == ns && obj.UnitID() != "" {
			ids = append(ids, obj.UnitID())
		}
	}

	ids, err = s.collectionUnits(ctx, scope.Collections, ns, ids)
	if err != nil {
		return false, nil, err
	}

	return false, ids, nil
}

// collectionUnits adds to ids the units of each collection of cols that is in
// ns, without a repeat. It fails closed: a collection of another namespace, a
// collection that no longer exists and a server with no collection port add
// nothing, and a store failure returns an error so that the list is refused.
func (s deletedReadScope) collectionUnits(
	ctx context.Context, cols []identity.Object, ns namespace.Name, ids []string,
) ([]string, error) {
	if s.units == nil {
		return ids, nil
	}

	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}

	for i := range cols {
		col := cols[i]
		// Defense in depth: the store query keys the collection by namespace
		// too; this check stays so that a scope of another namespace never
		// reaches the store.
		if col.Namespace() != ns {
			continue
		}

		members, err := s.units.CollectionUnitIDs(ctx, col)
		if errors.Is(err, collection.ErrNotFound) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("list units of collection: %w", err)
		}

		for _, id := range members {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}

	return ids, nil
}

// newDeletedReadScope returns the read scope of the deleted-units list. A nil
// collection service gives a scope with no collection port, not an interface
// that holds a nil pointer, so that a grant on a collection covers nothing.
func newDeletedReadScope(authz identity.Authorizer, collections *collection.Service) deletedReadScope {
	if collections == nil {
		return deletedReadScope{authz: authz, units: nil}
	}

	return deletedReadScope{authz: authz, units: collections}
}
