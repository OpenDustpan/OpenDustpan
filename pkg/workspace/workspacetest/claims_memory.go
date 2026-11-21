package workspacetest

import (
	"context"
	"fmt"
	"sync"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
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
	locked     map[namespace.Name]bool
	deleted    map[workspace.ID]workspace.Workspace
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
		locked:           make(map[namespace.Name]bool),
		deleted:          make(map[workspace.ID]workspace.Workspace),
	}
	b.MemoryClaimStore = workspace.NewMemoryClaimStore(b)
	b.SetManualLocks(func(ctx context.Context, ws workspace.ID) []manuallock.Lock {
		w, err := b.GetWorkspaceByID(ctx, ws)

		b.mu.Lock()
		defer b.mu.Unlock()

		if err != nil || !b.locked[w.Ref.Namespace] {
			return nil
		}

		return []manuallock.Lock{
			{
				Since:  claimNow(),
				Target: manuallock.NamespaceTarget(w.Ref.Namespace),
				Holder: claimLockHolder,
				Reason: claimLockReason,
			},
		}
	})

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

// SaveWorkspace implements ClaimBackend.
func (b *MemoryClaimBackend) SaveWorkspace(_ context.Context, w workspace.Workspace, _ namespace.Fence) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.workspaces[w.ID] = w

	return nil
}

// DeleteWorkspace implements ClaimBackend: a deleted workspace is missing.
func (b *MemoryClaimBackend) DeleteWorkspace(
	_ context.Context, ref workspace.Ref, _ namespace.Fence, _ inventory.Deletion,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id := range b.workspaces {
		if b.workspaces[id].Ref == ref {
			b.deleted[id] = b.workspaces[id]
			delete(b.workspaces, id)
		}
	}

	return nil
}

// PutLock implements ClaimBackend: a manual lock covers each workspace of its
// namespace.
func (b *MemoryClaimBackend) PutLock(
	_ context.Context, l manuallock.Lock, fence namespace.Fence, _ audit.Entry,
) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.locked[fence.Namespace] = true

	_ = l

	return nil
}

// DeletedWorkspace implements workspace.ClaimDeleted.
func (b *MemoryClaimBackend) DeletedWorkspace(_ context.Context, id workspace.ID) (workspace.Workspace, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	gone, ok := b.deleted[id]

	return gone, ok, nil
}
