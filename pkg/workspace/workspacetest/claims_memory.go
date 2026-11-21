package workspacetest

import (
	"context"
	"fmt"
	"sync"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// MemoryClaimBackend is a ClaimBackend over workspace.MemoryClaimStore. It
// keeps the namespaces, the workspaces and the state versions of the suite in
// memory, as a SQL store keeps them in tables.
type MemoryClaimBackend struct {
	*workspace.MemoryClaimStore

	mu         sync.Mutex
	namespaces map[namespace.Name]namespace.Namespace
	workspaces map[workspace.ID]workspace.Workspace
	versions   map[workspace.StateVersionID]workspace.StateVersion
}

var _ ClaimBackend = (*MemoryClaimBackend)(nil)

// NewMemoryClaimBackend returns an empty MemoryClaimBackend.
func NewMemoryClaimBackend() *MemoryClaimBackend {
	b := &MemoryClaimBackend{
		MemoryClaimStore: nil,
		mu:               sync.Mutex{},
		namespaces:       make(map[namespace.Name]namespace.Namespace),
		workspaces:       make(map[workspace.ID]workspace.Workspace),
		versions:         make(map[workspace.StateVersionID]workspace.StateVersion),
	}
	b.MemoryClaimStore = workspace.NewMemoryClaimStore(b)

	return b
}

// CreateNamespace implements ClaimBackend.
func (b *MemoryClaimBackend) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.namespaces[ns.Name] = ns

	return nil
}

// CreateWorkspace implements ClaimBackend.
func (b *MemoryClaimBackend) CreateWorkspace(_ context.Context, w workspace.Workspace, fence namespace.Fence) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.namespaces[fence.Namespace]; !ok || w.Ref.Namespace != fence.Namespace {
		return fmt.Errorf("%w: %s", namespace.ErrNotFound, fence.Namespace)
	}

	b.workspaces[w.ID] = w

	return nil
}

// CreateStateVersion implements ClaimBackend.
func (b *MemoryClaimBackend) CreateStateVersion(
	_ context.Context, sv workspace.StateVersion, _ namespace.Fence, _ int,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.versions[sv.ID] = sv

	return nil
}

// GetWorkspaceByID implements workspace.ClaimWorkspaces.
func (b *MemoryClaimBackend) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	w, ok := b.workspaces[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

// GetWorkspaceStateVersion implements workspace.ClaimVersions: a version of
// another workspace reads as missing, as in the SQL stores.
func (b *MemoryClaimBackend) GetWorkspaceStateVersion(
	_ context.Context, ns namespace.Name, ws workspace.ID, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sv, ok := b.versions[id]
	w, wsOK := b.workspaces[ws]

	if !ok || !wsOK || sv.Workspace != ws || w.Ref.Namespace != ns {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sv, nil
}
