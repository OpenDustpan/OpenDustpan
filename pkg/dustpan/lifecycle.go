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

// blobPurger deletes the blobs of one purged unit after its rows are gone,
// and finishes the blob purge record only when every blob is gone (ADR 47).
// A record that stays is for the sweep, so no blob of a purged unit outlives
// it for good.
type blobPurger struct {
	purges inventory.BlobPurges
	logger *slog.Logger
}

// finish deletes the blobs of the unit unitID of tool with deleteBlobs and
// then finishes its record. It never fails the purge: the rows are already
// gone, and the record keeps the work for the sweep. No log line names a
// blob key.
func (b blobPurger) finish(
	ctx context.Context, tool, unitID string, deleteBlobs func(context.Context) error,
) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), purgeBlobTimeout)
	defer cancel()

	if err := deleteBlobs(detached); err != nil {
		b.logger.WarnContext(ctx, "blobs of a purged unit remain for the sweep",
			slog.String("tool", tool), slog.String("unit_id", unitID))

		return
	}

	if err := b.purges.FinishBlobPurge(detached, tool, unitID); err != nil {
		b.logger.WarnContext(ctx, "finish blob purge record",
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
	stacks: nil, updates: nil, blobs: blobPurger{purges: nil, logger: nil},
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

// Purge implements inventory.Lifecycle.
func (l pulumiLifecycle) Purge(ctx context.Context, ns namespace.Name, unitID string, e audit.Entry) error {
	id := stack.ID(unitID)

	if err := l.stacks.PurgeDeleted(ctx, ns, id, e); err != nil {
		return lifecycleError(err, stack.ErrNotFound, stack.ErrAlreadyExists)
	}

	l.blobs.finish(ctx, pulumiTool, unitID, func(c context.Context) error {
		return l.updates.DeleteStackBlobs(c, id)
	})

	return nil
}

// opentofuLifecycle implements inventory.Lifecycle for OpenTofu workspaces.
type opentofuLifecycle struct {
	workspaces *workspace.Service
	blobs      blobPurger
}

var _ inventory.Lifecycle = opentofuLifecycle{workspaces: nil, blobs: blobPurger{purges: nil, logger: nil}}

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

// Purge implements inventory.Lifecycle.
func (l opentofuLifecycle) Purge(ctx context.Context, ns namespace.Name, unitID string, e audit.Entry) error {
	id := workspace.ID(unitID)

	if err := l.workspaces.PurgeDeleted(ctx, ns, id, e); err != nil {
		return lifecycleError(err, workspace.ErrNotFound, workspace.ErrAlreadyExists)
	}

	l.blobs.finish(ctx, opentofuTool, unitID, func(c context.Context) error {
		return l.workspaces.DeletePurgedBlobs(c, id)
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
// of unit:read that an access.Authorizer lists (ADR 52). Another authorizer
// lists no scope, so it fails closed: an administrator reads every deleted
// unit, and any other caller reads none.
type deletedReadScope struct {
	authz identity.Authorizer
}

var _ inventoryapi.DeletedReadScope = deletedReadScope{authz: nil}

// ReadableUnits implements inventoryapi.DeletedReadScope. A grant of
// unit:read on the installation or on ns covers every deleted unit of ns. A
// grant on a unit covers that unit while it exists, so it gives nothing on a
// deleted unit here: ListObjects leaves out the units that do not exist.
// Collections (#478) are not wired yet, so a grant on a collection covers
// no deleted unit.
func (s deletedReadScope) ReadableUnits(
	ctx context.Context, p identity.Principal, ns namespace.Name,
) (bool, []string, error) {
	authz, ok := s.authz.(*access.Authorizer)
	if !ok {
		return p.Admin, nil, nil
	}

	scope, err := authz.ListObjects(ctx, p, identity.ActionUnitRead)
	if err != nil {
		return false, nil, fmt.Errorf("list readable units: %w", err)
	}

	if scope.All {
		return true, nil, nil
	}

	for _, obj := range scope.Namespaces {
		if obj.Namespace() == ns {
			return true, nil, nil
		}
	}

	ids := []string{}

	for _, obj := range scope.Units {
		if obj.Namespace() == ns && obj.UnitID() != "" {
			ids = append(ids, obj.UnitID())
		}
	}

	return false, ids, nil
}
