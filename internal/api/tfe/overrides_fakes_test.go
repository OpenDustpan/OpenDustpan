package tfe_test

import (
	"context"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// SetOverrides is not used by this fake.
func (lockedActionWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

// SetOverrides is not used by this fake.
func (conflictWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

// SetOverrides is not used by this fake.
func (pagingWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

// SetOverrides is not used by this fake.
func (stubWorkspaces) SetOverrides(
	_ context.Context, id workspace.ID, ov namespace.Overrides,
) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Overrides = ov

	return ws, nil
}

// SetOverrides is not used by this fake.
func (errorWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

// SetOverrides is not used by this fake.
func (writeWorkspaces) SetOverrides(
	context.Context, workspace.ID, namespace.Overrides,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}
