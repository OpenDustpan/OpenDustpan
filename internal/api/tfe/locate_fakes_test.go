package tfe_test

import (
	"context"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// The fakes below answer the lookup of the workspace of a state version from
// their own ReadState, so the lookup and the read agree on which versions
// exist. The lookup opens no output.

func locateVia(
	ctx context.Context, read func(context.Context, workspace.StateVersionID) (workspace.StateVersion, error),
	id workspace.StateVersionID,
) (workspace.ID, error) {
	sv, err := read(ctx, id)
	if err != nil {
		return "", err
	}

	return sv.Workspace, nil
}

func (s sensitiveOutputsWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, s.ReadState, id)
}

func (o olderStateWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, o.ReadState, id)
}

func (r revealWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, r.ReadState, id)
}

func (f foreignRestoreWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, f.ReadState, id)
}

func (c canaryRestoreWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, c.ReadState, id)
}

func (stubWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, stubWorkspaces{}.ReadState, id)
}

func (errorWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, errorWorkspaces{}.ReadState, id)
}

func (p pagingWorkspaces) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	return locateVia(ctx, p.ReadState, id)
}

func (p ciStateProbe) StateVersionWorkspace(
	ctx context.Context, id workspace.StateVersionID,
) (workspace.ID, error) {
	p.record(p.reads, "locate "+id.String())

	return p.WorkspaceService.StateVersionWorkspace(ctx, id)
}
