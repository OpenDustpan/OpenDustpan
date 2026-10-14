package dustpan

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
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

// pulumiLifecycle implements inventory.Lifecycle for Pulumi stacks. A purge
// removes the checkpoint blobs after the rows.
type pulumiLifecycle struct {
	stacks  *stack.Service
	updates *stack.Updates
}

var _ inventory.Lifecycle = pulumiLifecycle{stacks: nil, updates: nil}

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

	l.updates.DeleteStackBlobs(ctx, stack.Ref{Namespace: ns, Project: "", Stack: ""}, id)

	return nil
}

// opentofuLifecycle implements inventory.Lifecycle for OpenTofu workspaces.
type opentofuLifecycle struct {
	workspaces *workspace.Service
}

var _ inventory.Lifecycle = opentofuLifecycle{workspaces: nil}

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
	if err := l.workspaces.PurgeDeleted(ctx, ns, workspace.ID(unitID), e); err != nil {
		return lifecycleError(err, workspace.ErrNotFound, workspace.ErrAlreadyExists)
	}

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
