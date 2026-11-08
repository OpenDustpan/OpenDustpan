package workspace_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// memInventoryTool names OpenTofu in inventory entries.
const memInventoryTool = "opentofu"

// memStore implements namespace.Store, workspace.Store, workspace.StateStore
// and inventory.Reader in memory, following their contracts.
type memStore struct {
	// beforeFence, when set, runs before FenceAndUnlock takes the store lock.
	beforeFence func(sv workspace.StateVersion)
	namespaces  map[namespace.Name]namespace.Namespace
	workspaces  map[workspace.ID]workspace.Workspace
	deleted     map[workspace.ID]inventory.DeletedEntry
	// removed holds each deleted workspace, so a restore brings it back.
	removed  map[workspace.ID]workspace.Workspace
	versions map[workspace.StateVersionID]workspace.StateVersion
	current  map[workspace.ID]workspace.StateVersionID
	entries  map[memInventoryKey]inventory.Entry
	mu       sync.Mutex
	// lastLockPass is the lock pass that the last CreateWorkspace got in its context.
	lastLockPass time.Time
	// lostAck makes the next finalize commit and then report an error, as a
	// dropped connection after COMMIT would.
	lostAck bool
	// failFinalize makes the next finalize report err without writing, as a
	// store failure with an unknown outcome would.
	failFinalize error
	// failReferenced makes ReferencedContentKeys report err, as a store would
	// for a workspace whose row is gone.
	failReferenced error
	// released holds the audit entry of each release, in the order written.
	released []audit.Entry
	// placements holds the placement of each workspace that CreateWorkspacePlaced made.
	placements map[workspace.ID]workspace.Placement
}

type memInventoryKey struct {
	namespace namespace.Name
	tool      string
	name      string
}

var (
	_ storetest.WorkspaceStore = (*memStore)(nil)
	_ workspace.PlacedStore    = (*memStore)(nil)
)

// memCollectionID is the ID of the collection of a placement that memPlacement makes.
const memCollectionID = "col-1"

// memPlacement returns a valid placement of a new workspace in the collection
// "placed" of ns. The collection is not stored: the memory store keeps no
// collections.
func memPlacement(ns namespace.Name) workspace.Placement {
	return workspace.Placement{
		Collection:   collection.Ref{Namespace: ns, Name: "placed"},
		CollectionID: memCollectionID,
		Entry:        placementtest.Entry(ns),
	}
}

func newMemStore() *memStore {
	return &memStore{
		beforeFence:    nil,
		namespaces:     map[namespace.Name]namespace.Namespace{},
		workspaces:     map[workspace.ID]workspace.Workspace{},
		deleted:        map[workspace.ID]inventory.DeletedEntry{},
		removed:        map[workspace.ID]workspace.Workspace{},
		versions:       map[workspace.StateVersionID]workspace.StateVersion{},
		current:        map[workspace.ID]workspace.StateVersionID{},
		entries:        map[memInventoryKey]inventory.Entry{},
		mu:             sync.Mutex{},
		lostAck:        false,
		failFinalize:   nil,
		failReferenced: nil,
		released:       nil,
		lastLockPass:   time.Time{},
		placements:     map[workspace.ID]workspace.Placement{},
	}
}

func (m *memStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memStore) SetOverrides(
	_ context.Context,
	name namespace.Name,
	ov namespace.Overrides,
	fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.ErrNotFound
	}

	ns.Overrides = ov
	m.namespaces[name] = ns

	return nil
}

func (m *memStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]namespace.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		out = append(out, ns)
	}

	slices.SortFunc(out, func(a, b namespace.Namespace) int {
		return strings.Compare(a.Name.String(), b.Name.String())
	})

	return out, nil
}

func (m *memStore) Ping(_ context.Context) error {
	return nil
}

func (m *memStore) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range m.entries {
		if e := m.entries[key]; e.UnitID == unitID {
			return e, nil
		}
	}

	return inventory.Entry{}, inventory.ErrUnitNotFound
}

func (m *memStore) ListUnits(_ context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]inventory.Entry, 0)

	for key := range m.entries {
		if key.namespace == ns {
			out = append(out, m.entries[key])
		}
	}

	slices.SortFunc(out, func(a, b inventory.Entry) int {
		if a.Tool != b.Tool {
			return strings.Compare(a.Tool, b.Tool)
		}

		return strings.Compare(a.Name, b.Name)
	})

	return out, nil
}

// QueryUnits implements inventory.Reader over the entries of ListUnits.
func (m *memStore) QueryUnits(ctx context.Context, q inventory.UnitQuery) ([]inventory.Entry, error) {
	all, err := m.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return inventory.PageUnits(all, q), nil
}

// ListDeletedUnits implements inventory.DeletedReader, ordered by tool, then
// name, then unit ID.
func (m *memStore) ListDeletedUnits(_ context.Context, ns namespace.Name) ([]inventory.DeletedEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]inventory.DeletedEntry, 0)

	for id := range m.deleted {
		if m.deleted[id].Namespace == ns {
			out = append(out, m.deleted[id])
		}
	}

	slices.SortFunc(out, func(a, b inventory.DeletedEntry) int {
		if c := strings.Compare(a.Tool, b.Tool); c != 0 {
			return c
		}

		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}

		return strings.Compare(a.UnitID, b.UnitID)
	})

	return out, nil
}

// QueryDeletedUnits implements inventory.DeletedReader with
// inventory.PageDeleted over ListDeletedUnits.
func (m *memStore) QueryDeletedUnits(ctx context.Context, q inventory.DeletedQuery) (inventory.DeletedPage, error) {
	all, err := m.ListDeletedUnits(ctx, q.Namespace)
	if err != nil {
		return inventory.DeletedPage{}, err
	}

	return inventory.PageDeleted(all, q), nil
}

// GetDeletedUnit implements inventory.DeletedReader.
func (m *memStore) GetDeletedUnit(
	_ context.Context, ns namespace.Name, tool, unitID string,
) (inventory.DeletedEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.deleted[workspace.ID(unitID)]
	if !ok || e.Namespace != ns || e.Tool != tool {
		return inventory.DeletedEntry{}, inventory.ErrNotFound
	}

	return e, nil
}

func (m *memStore) CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastLockPass = workspace.LockPassFrom(ctx)

	if err := m.checkFence(fence); err != nil {
		return err
	}

	for id := range m.workspaces {
		if m.workspaces[id].Ref == w.Ref {
			return workspace.ErrAlreadyExists
		}
	}

	m.workspaces[w.ID] = w
	m.publish(w)

	return nil
}

// CreateWorkspacePlaced creates w as CreateWorkspace does, then records p.
func (m *memStore) CreateWorkspacePlaced(
	ctx context.Context, w workspace.Workspace, p workspace.Placement, fence namespace.Fence,
) error {
	if err := m.CreateWorkspace(ctx, w, fence); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.placements[w.ID] = p

	return nil
}

func (m *memStore) GetWorkspace(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.workspaces {
		if w := m.workspaces[id]; w.Ref == ref {
			return w, nil
		}
	}

	return workspace.Workspace{}, workspace.ErrNotFound
}

func (m *memStore) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.workspaces[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

func (m *memStore) ListWorkspaces(_ context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]workspace.Workspace, 0)

	for id := range m.workspaces {
		if m.workspaces[id].Ref.Namespace == ns {
			out = append(out, m.workspaces[id])
		}
	}

	slices.SortFunc(out, func(a, b workspace.Workspace) int {
		return strings.Compare(a.Ref.Name.String(), b.Ref.Name.String())
	})

	return out, nil
}

// QueryWorkspaces implements workspace.Store over the list of ListWorkspaces.
func (m *memStore) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := m.ListWorkspaces(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (m *memStore) SaveWorkspace(_ context.Context, w workspace.Workspace, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	stored, ok := m.workspaces[w.ID]
	if !ok {
		return workspace.ErrNotFound
	}

	if stored.Revision != w.Revision-1 {
		return workspace.ErrConcurrentModification
	}

	m.workspaces[w.ID] = w
	m.publish(w)

	return nil
}

// DeleteWorkspace implements workspace.Store: it soft-deletes the workspace
// (ADR 47). The workspace moves to deleted, so no lookup or list finds it and
// its name is free, while its state versions stay. It removes the inventory
// entry, refuses a locked workspace with ErrLocked and one with a pending
// state version with ErrPendingState.
func (m *memStore) DeleteWorkspace(
	_ context.Context,
	ref workspace.Ref,
	fence namespace.Fence,
	d inventory.Deletion,
) error {
	e := d.Entry

	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	id, err := deletableWorkspace(m, ref)
	if err != nil {
		return err
	}

	m.deleted[id] = inventory.DeletedEntry{
		DeletedAt:  e.At,
		PurgeAfter: d.PurgeAfter,
		Namespace:  ref.Namespace,
		Tool:       memInventoryTool,
		UnitID:     id.String(),
		Name:       ref.Name.String(),
	}
	m.removed[id] = m.workspaces[id]
	delete(m.workspaces, id)

	for key := range m.entries {
		if key.namespace == ref.Namespace && key.tool == memInventoryTool && key.name == ref.Name.String() {
			delete(m.entries, key)
		}
	}

	return nil
}

// RestoreWorkspace implements workspace.Store: it brings back the deleted
// workspace id of the namespace of fence under newName, or under its old
// name, and publishes its inventory entry again.
func (m *memStore) RestoreWorkspace(
	_ context.Context, id workspace.ID, newName workspace.Name, fence namespace.Fence, e audit.Entry,
) (workspace.Ref, error) {
	if err := validMemEntry(e, fence); err != nil {
		return workspace.Ref{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return workspace.Ref{}, err
	}

	entry, ok := m.deleted[id]
	if !ok || entry.Namespace != fence.Namespace {
		return workspace.Ref{}, workspace.ErrNotFound
	}

	name := newName
	if name == "" {
		name = workspace.Name(entry.Name)
	}

	ref := workspace.Ref{Namespace: fence.Namespace, Name: name}
	for live := range m.workspaces {
		if m.workspaces[live].Ref == ref {
			return workspace.Ref{}, workspace.ErrAlreadyExists
		}
	}

	w := m.removed[id]
	w.Ref = ref
	m.workspaces[id] = w
	m.publish(w)

	delete(m.removed, id)
	delete(m.deleted, id)

	return ref, nil
}

// PurgeWorkspace implements workspace.Store: it removes the deleted
// workspace id of the namespace of fence with its state versions.
func (m *memStore) PurgeWorkspace(
	_ context.Context,
	id workspace.ID,
	fence namespace.Fence,
	dueBy time.Time,
	e audit.Entry,
) error {
	if err := validMemEntry(e, fence); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	entry, ok := m.deleted[id]
	if !ok || entry.Namespace != fence.Namespace {
		return workspace.ErrNotFound
	}

	// A sweep purge (non-zero dueBy) acts only on a workspace whose
	// retention period ended by then.
	if !dueBy.IsZero() && entry.PurgeAfter.After(dueBy) {
		return workspace.ErrNotFound
	}

	for vid := range m.versions {
		if m.versions[vid].Workspace == id {
			delete(m.versions, vid)
		}
	}

	delete(m.current, id)
	delete(m.removed, id)
	delete(m.deleted, id)

	return nil
}

// validMemEntry checks e as the store adapters do: it must pass validation
// and name the namespace of fence.
func validMemEntry(e audit.Entry, fence namespace.Fence) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return nil
}

// deletableWorkspace returns the ID of the live workspace called ref, or
// ErrNotFound, ErrLocked or ErrPendingState. The caller holds m.mu.
func deletableWorkspace(m *memStore, ref workspace.Ref) (workspace.ID, error) {
	for id := range m.workspaces {
		if m.workspaces[id].Ref != ref {
			continue
		}

		if m.workspaces[id].Lock.ID != "" {
			return "", workspace.ErrLocked
		}

		for vid := range m.versions {
			if sv := m.versions[vid]; sv.Workspace == id && sv.Status == workspace.StatusPending {
				return "", workspace.ErrPendingState
			}
		}

		return id, nil
	}

	return "", workspace.ErrNotFound
}

func (m *memStore) CreateStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence, maxPending int,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	w, ok := m.workspaces[sv.Workspace]
	if !ok {
		return workspace.ErrNotFound
	}

	if err := m.checkDeclareSwitches(w, sv); err != nil {
		return err
	}

	pending := 0

	for vid := range m.versions {
		if v := m.versions[vid]; v.Workspace == sv.Workspace && v.Status == workspace.StatusPending {
			pending++
		}
	}

	if pending >= maxPending {
		return fmt.Errorf(
			"%w: %d pending, limit %d",
			workspace.ErrTooManyPending,
			pending,
			maxPending,
		)
	}

	m.versions[sv.ID] = sv

	return nil
}

func (m *memStore) GetStateVersion(_ context.Context, id workspace.StateVersionID) (workspace.StateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sv, ok := m.versions[id]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sv, nil
}

// FenceAndUnlock implements workspace.StateStore: it releases the lock of w
// and makes the fence version current, checking the same guards as the
// adapters: the fence, a held lock and the revision.
//
//nolint:revive // The signature of the workspace.StateStore port.
func (m *memStore) FenceAndUnlock(
	_ context.Context, w workspace.Workspace, sv workspace.StateVersion,
	expect workspace.StateVersionID, fence namespace.Fence, e audit.Entry,
) error {
	// The hook runs before the store takes its lock, so a test can land a
	// write between the caller's read and this fence, as a concurrent
	// finalize would.
	if m.beforeFence != nil {
		m.beforeFence(sv)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if err := checkMemFence(m, w, sv, expect); err != nil {
		return err
	}

	if err := m.keepReleaseEntry(fence, e); err != nil {
		return err
	}

	// The write sets only the finalize columns of the staged row, as the SQL
	// stores do: the staged sealed flag stays, so a staged plain row never
	// finalizes as sealed.
	staged := m.versions[sv.ID]
	staged.Serial = sv.Serial
	staged.Lineage = sv.Lineage
	staged.MD5 = sv.MD5
	staged.Outputs = sv.Outputs
	staged.Status = sv.Status
	staged.FinalizedAt = sv.FinalizedAt
	staged.Size = sv.Size
	m.versions[sv.ID] = staged
	m.current[w.ID] = sv.ID
	m.workspaces[w.ID] = w
	m.publish(w)

	return nil
}

func (m *memStore) FinalizeStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	return memFinalize(m, sv, fence, nil)
}

// FinalizeStateVersionExpecting finalizes as FinalizeStateVersion does, and
// also checks the current version under the same lock.
func (m *memStore) FinalizeStateVersionExpecting(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence, expect workspace.StateVersionID,
) error {
	return memFinalize(m, sv, fence, &expect)
}

// memFinalize finalizes sv, and checks the current version when expect is set.
func memFinalize(
	m *memStore, sv workspace.StateVersion, fence namespace.Fence, expect *workspace.StateVersionID,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failFinalize != nil {
		err := m.failFinalize
		m.failFinalize = nil

		return err
	}

	if err := m.checkFence(fence); err != nil {
		return err
	}

	stored, ok := m.versions[sv.ID]
	if !ok {
		return workspace.ErrStateVersionNotFound
	}

	if stored.Status != workspace.StatusPending {
		return workspace.ErrUploadDenied
	}

	// The write matches only the recorded attempt key: a superseded
	// attempt never finalizes over a newer one.
	if stored.Content != sv.Content {
		return workspace.ErrUploadDenied
	}

	w, ok := m.workspaces[sv.Workspace]
	if !ok {
		return workspace.ErrNotFound
	}

	if err := checkMemFinalize(m, w, sv); err != nil {
		return err
	}

	if expect != nil && m.current[sv.Workspace] != *expect {
		return workspace.ErrConcurrentModification
	}

	m.versions[sv.ID] = sv
	m.current[sv.Workspace] = sv.ID

	w.UpdatedAt = sv.FinalizedAt
	m.workspaces[sv.Workspace] = w
	m.publish(w)

	if m.lostAck {
		m.lostAck = false

		return errConnectionLost
	}

	return nil
}

// checkMemFence runs the checks of FenceAndUnlock: the workspace is locked at
// the revision before w, the current version is expect, the fence serial is
// the next one, and sv is stored as pending with its content key. The caller
// holds mu.
func checkMemFence(
	m *memStore,
	w workspace.Workspace,
	sv workspace.StateVersion,
	expect workspace.StateVersionID,
) error {
	stored, ok := m.workspaces[w.ID]
	if !ok {
		return workspace.ErrNotFound
	}

	if !stored.Lock.Locked() {
		return workspace.ErrNotLocked
	}

	if stored.Revision != w.Revision-1 {
		return workspace.ErrConcurrentModification
	}

	curID, found := m.current[w.ID]
	if (found && curID != expect) || (!found && expect != "") {
		return workspace.ErrConcurrentModification
	}

	if found && sv.Serial != m.versions[curID].Serial+1 {
		return fmt.Errorf("%w: fence serial %d", workspace.ErrStaleSerial, sv.Serial)
	}

	return checkMemStaged(m, sv)
}

// checkMemStaged refuses a fence version that is not stored as pending with
// its content key. The caller holds mu.
func checkMemStaged(m *memStore, sv workspace.StateVersion) error {
	staged, ok := m.versions[sv.ID]
	if !ok || staged.Status != workspace.StatusPending || staged.Content != sv.Content {
		return workspace.ErrUploadDenied
	}

	return nil
}

// checkMemFinalize runs the lock, lineage and serial checks of a finalize.
// The caller holds mu.
func checkMemFinalize(m *memStore, w workspace.Workspace, sv workspace.StateVersion) error {
	// A version declared without the lock (ADR 44, decision 5) has no lock to keep,
	// but a lock taken by another principal since the declare stops it.
	if sv.UnlockedWrite && w.Lock.Locked() && w.Lock.Holder != sv.Holder {
		return workspace.ErrLockedByOther
	}

	// Its switches must still be on when it finalizes (ADR 44, decision 6).
	if err := m.checkDeclareSwitches(w, sv); err != nil {
		return err
	}

	if !sv.UnlockedWrite {
		switch {
		case !w.Lock.Locked():
			return workspace.ErrNotLocked
		case w.Lock.ID != sv.LockID, w.Lock.Holder != sv.Holder:
			return workspace.ErrLockedByOther
		}
	}

	if cur, ok := m.current[sv.Workspace]; ok && !sv.Force {
		switch current := m.versions[cur]; {
		case current.Lineage != sv.Lineage:
			return workspace.ErrLineageMismatch
		case current.Serial >= sv.Serial:
			return workspace.ErrStaleSerial
		}
	}

	return checkMemRestore(m, sv)
}

// checkMemRestore refuses a restored version when the current state already
// restores the same version for the same holder. The caller holds mu.
func checkMemRestore(m *memStore, sv workspace.StateVersion) error {
	cur, ok := m.current[sv.Workspace]
	if !ok || sv.RestoredFrom == "" {
		return nil
	}

	if current := m.versions[cur]; current.RestoredFrom == sv.RestoredFrom && current.Holder == sv.Holder {
		return workspace.ErrAlreadyRestored
	}

	return nil
}

var errConnectionLost = errors.New("connection lost after commit")

func (m *memStore) CurrentStateVersion(_ context.Context, id workspace.ID) (workspace.StateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.current[id]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	return m.versions[cur], nil
}

// CountPendingStateVersions implements workspace.StateStore: how many
// pending versions the workspace holds. A workspace that does not exist
// holds none.
func (m *memStore) CountPendingStateVersions(_ context.Context, id workspace.ID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for vid := range m.versions {
		if sv := m.versions[vid]; sv.Workspace == id && sv.Status == workspace.StatusPending {
			n++
		}
	}

	return n, nil
}

// ReferencedContentKeys implements workspace.StateStore: every blob key the
// versions of the workspace name, without the empty ones. A workspace that
// does not exist is ErrNotFound; a deleted one still gives its keys.
func (m *memStore) ReferencedContentKeys(_ context.Context, id workspace.ID) ([]blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failReferenced != nil {
		return nil, m.failReferenced
	}

	_, live := m.workspaces[id]
	_, deleted := m.removed[id]

	if !live && !deleted {
		return nil, workspace.ErrNotFound
	}

	var out []blob.Key

	for vid := range m.versions {
		if sv := m.versions[vid]; sv.Workspace == id && sv.Content != "" {
			out = append(out, sv.Content)
		}
	}

	return out, nil
}

// RecordUploadAttempt implements workspace.StateStore: it notes the blob
// key of one upload attempt on a pending version, and returns the
// superseded key.
func (m *memStore) RecordUploadAttempt(
	_ context.Context, id workspace.StateVersionID, key blob.Key, fence namespace.Fence,
) (blob.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return "", err
	}

	sv, ok := m.versions[id]
	if !ok {
		return "", workspace.ErrStateVersionNotFound
	}

	if sv.Status != workspace.StatusPending {
		return "", workspace.ErrUploadDenied
	}

	superseded := sv.Content
	sv.Content = key
	m.versions[id] = sv

	return superseded, nil
}

// SaveSensitiveMarks implements workspace.StateStore: it stores the secret
// paths of the json-state rendering of id. It works on a pending and on a
// finalized version.
func (m *memStore) SaveSensitiveMarks(
	_ context.Context, id workspace.StateVersionID, marks workspace.SensitiveMarks, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	sv, ok := m.versions[id]
	if !ok {
		return workspace.ErrStateVersionNotFound
	}

	sv.SensitiveMarks = marks
	m.versions[id] = sv

	return nil
}

// DiscardStateVersion implements workspace.StateStore: it deletes the pending
// version id, and leaves a finalized one in place.
func (m *memStore) DiscardStateVersion(
	_ context.Context, id workspace.StateVersionID, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if sv, ok := m.versions[id]; ok && sv.Status == workspace.StatusPending {
		delete(m.versions, id)
	}

	return nil
}

// DeleteExpiredPendingStateVersions implements workspace.StateStore: it
// deletes the pending versions of the workspace whose capability expired
// before before, oldest expiry first, and returns them.
func (m *memStore) DeleteExpiredPendingStateVersions(
	_ context.Context, id workspace.ID, before time.Time, fence namespace.Fence,
) ([]workspace.StateVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return nil, err
	}

	var out []workspace.StateVersion

	for vid := range m.versions {
		sv := m.versions[vid]
		if sv.Workspace != id || sv.Status != workspace.StatusPending || !sv.UploadExpiresAt.Before(before) {
			continue
		}

		delete(m.versions, vid)

		out = append(out, sv)
	}

	slices.SortFunc(out, func(a, b workspace.StateVersion) int {
		if c := a.UploadExpiresAt.Compare(b.UploadExpiresAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID.String(), b.ID.String())
	})

	return out, nil
}

// ListStateVersions implements workspace.StateStore: finalized versions of
// the workspace, newest first by finalize time, with the total count.
func (m *memStore) ListStateVersions(
	_ context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := page.Limit
	if limit <= 0 {
		limit = workspace.DefaultStatePageSize
	}

	limit = min(limit, workspace.MaxStatePageSize)
	offset := max(page.Offset, 0)

	all := make([]workspace.StateVersion, 0)

	for vid := range m.versions {
		if sv := m.versions[vid]; sv.Workspace == id && sv.Status == workspace.StatusFinalized {
			all = append(all, sv)
		}
	}

	slices.SortFunc(all, func(a, b workspace.StateVersion) int {
		if c := b.FinalizedAt.Compare(a.FinalizedAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID.String(), b.ID.String())
	})

	total := len(all)
	if offset > total {
		offset = total
	}

	end := min(offset+limit, total)

	return all[offset:end], total, nil
}

// memLastRelease returns the inventory record of r, or nil when r is no record.
func memLastRelease(r workspace.Release) *inventory.LastRelease {
	if !r.Recorded() {
		return nil
	}

	return &inventory.LastRelease{At: r.At, LockID: r.LockID, Holder: r.Holder, By: r.By, Kind: string(r.Kind)}
}

// memLock returns the inventory lock a workspace holds, or nil when it is
// unlocked, as the store adapters publish it.
func memLock(lock workspace.Lock) *inventory.Lock {
	if !lock.Locked() {
		return nil
	}

	operation := lock.Reason
	if operation == "" {
		operation = "lock"
	}

	return &inventory.Lock{Since: lock.LockedAt, Holder: lock.Holder, Operation: operation}
}

type fixture struct {
	svc        *workspace.Service
	store      *memStore
	namespaces *namespace.Service
	secrets    *secret.Service
	blobs      blob.Store
	refusals   *workspacetest.Refusals
	now        *time.Time
	ws         workspace.Workspace
	sealKEK    []byte
	sealKeyID  string
}

// stateDir returns the blob directory of the state of the workspace ws in the
// namespace "acme": namespaces/<namespace ID>/tofu/<workspace ID>/.
func (f fixture) stateDir(t *testing.T, ws workspace.ID) string {
	t.Helper()

	return stateDirOf(t.Context(), t, f.namespaces, ws)
}

// stateDirOf is stateDir for any namespace service.
func stateDirOf(ctx context.Context, t *testing.T, namespaces *namespace.Service, ws workspace.ID) string {
	t.Helper()

	ns, err := namespaces.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	return "namespaces/" + ns.ID.String() + "/tofu/" + ws.String() + "/"
}

// listStateBlobs returns the state blobs of the workspace id in blobs,
// whatever the namespace of the key, so a test also sees a blob in a place
// where it must not be.
func listStateBlobs(t *testing.T, blobs blob.Store, id workspace.ID) []blob.Object {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), blobs, "namespaces/")
	if err != nil {
		t.Fatalf("list state blobs of %s: %v", id, err)
	}

	var found []blob.Object

	for _, obj := range listed {
		if parsed, parseErr := workspace.ParseStateBlobKey(obj.Key); parseErr == nil && parsed.Workspace == id {
			found = append(found, obj)
		}
	}

	return found
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	return newFixtureWithBlobs(t, openFixtureBlobs(t))
}

// openFixtureBlobs opens a blob store in a temporary directory for one test.
func openFixtureBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// newFixtureWithBlobs returns a fixture over blobs, so tests prove what an
// upload leaves behind.
func newFixtureWithBlobs(t *testing.T, blobs blob.Store) fixture {
	t.Helper()

	return newFixtureGuarded(t, blobs, blobguardtest.None{})
}

// newFixtureGuarded is newFixtureWithBlobs with guard as the guard of the
// blob deletes.
func newFixtureGuarded(t *testing.T, blobs blob.Store, guard blobguard.Watch) fixture {
	t.Helper()

	return newFixtureSized(t, blobs, guard, 64)
}

// newFixtureSized is newFixtureGuarded with a state size limit of maxState.
func newFixtureSized(t *testing.T, blobs blob.Store, guard blobguard.Watch, maxState int64) fixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	secrets := newMemSealer(t, namespaces)
	refusals := workspacetest.NewRefusals()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          guard,
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         refusals,
		Sealer:             secrets,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      maxState,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return fixture{
		svc: svc, store: store, namespaces: namespaces, secrets: secrets, blobs: blobs, refusals: refusals,
		now: &now, ws: ws,
		sealKEK: nil, sealKeyID: "",
	}
}

// TestDeleteRecordsRetention proves that a delete records the end of the
// configured retention: the delete time plus Config.Retention (ADR 47,
// decision 2), not the default.
func TestDeleteRecordsRetention(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if err = svc.Delete(t.Context(), f.ws.Ref, retentionEntry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	deleted, err := f.store.ListDeletedUnits(t.Context(), f.ws.Ref.Namespace)
	if err != nil {
		t.Fatalf("list deleted units: %v", err)
	}

	want := f.now.UTC().Add(2 * time.Hour)
	if len(deleted) != 1 || !deleted[0].PurgeAfter.Equal(want) {
		t.Fatalf("deleted units = %+v, want one that is purged after %v", deleted, want)
	}
}

// TestDeleteSweepsExpiredPending proves a stuck pending version blocks a
// workspace delete before its expiry with ErrPendingState, and past the
// upload window plus the sweep grace period the delete sweeps it and succeeds.
func TestDeleteSweepsExpiredPending(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	if err := f.svc.Delete(ctx, f.ws.Ref, retentionEntry()); !errors.Is(err, workspace.ErrPendingState) {
		t.Fatalf("delete before expiry: err = %v, want ErrPendingState", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	if err := f.svc.Delete(ctx, f.ws.Ref, retentionEntry()); err != nil {
		t.Fatalf("delete after expiry: %v", err)
	}

	if _, err := f.svc.Get(ctx, f.ws.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}

	if _, err := f.store.GetStateVersion(ctx, pending.Version.ID); !errors.Is(
		err, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", err)
	}
}

// TestDeleteSweepTreatsGoneRowAsDone proves a delete whose sweep finds the
// pending row already deleted does not fail: a gone row counts as done.
func TestDeleteSweepTreatsGoneRowAsDone(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get pending version: %v", err)
	}

	writable, err := f.namespaces.WritableHere(ctx, "acme")
	if err != nil {
		t.Fatalf("writable namespace: %v", err)
	}

	if discardErr := f.store.DiscardStateVersion(ctx, pending.Version.ID, writable.Fence()); discardErr != nil {
		t.Fatalf("discard pending version: %v", discardErr)
	}

	if stored.Content != "" {
		if deleteErr := f.blobs.Delete(ctx, stored.Content); deleteErr != nil {
			t.Fatalf("delete pending blob: %v", deleteErr)
		}
	}

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             goneStates{StateStore: f.store},
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	if err := svc.Delete(ctx, f.ws.Ref, retentionEntry()); err != nil {
		t.Fatalf("delete with a gone pending row: %v", err)
	}

	if _, err := svc.Get(ctx, f.ws.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}

// goneStates reports the expired pending row as already gone, as an
// overlapping sweep would after the first sweep deletes it.
type goneStates struct {
	workspace.StateStore
}

// DeleteExpiredPendingStateVersions implements workspace.StateStore: it
// reports the row as already gone, with no error for the caller to join.
func (g goneStates) DeleteExpiredPendingStateVersions(
	_ context.Context, _ workspace.ID, _ time.Time, _ namespace.Fence,
) ([]workspace.StateVersion, error) {
	return nil, workspace.ErrStateVersionNotFound
}

// TestDeleteSweepsStuckPendingAfterBackup proves a stuck pending version
// with a content key stays out of the backup and does not fail it, and past
// the upload window plus the sweep grace period the workspace delete sweeps
// it and succeeds.
func TestDeleteSweepsStuckPendingAfterBackup(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newStuckBackupFixture(ctx, t)
	pending, stuckKey := stageStuckPending(ctx, t, f)
	assertBackupOmitsStuckKey(ctx, t, f.store, stuckKey)
	assertDeleteSweepsStuckPending(ctx, t, f, pending.Version.ID)
}

// stuckBackupFixture holds a workspace with a clock the test moves past
// the upload window plus the sweep grace period.
type stuckBackupFixture struct {
	svc        *workspace.Service
	store      *sqlite.Store
	namespaces *namespace.Service
	blobs      *fs.Store
	now        *time.Time
	ws         workspace.Workspace
}

// newStuckBackupFixture opens a blob store and a SQLite store with one
// workspace, so the test stages a stuck pending version against them.
func newStuckBackupFixture(ctx context.Context, t *testing.T) stuckBackupFixture {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	var store *sqlite.Store

	store, err = sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)

	var namespaces *namespace.Service

	namespaces, err = namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	var acme namespace.Namespace

	if acme, err = namespaces.Create(ctx, "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	var svc *workspace.Service

	svc, err = workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             newMemSealer(t, namespaces),
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	var name workspace.Name

	name, err = workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	var ws workspace.Workspace

	ws, err = svc.Create(
		ctx, workspace.Ref{Namespace: "acme", Name: name}, placementtest.Workspace(t, store, acme.Fence()),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return stuckBackupFixture{svc: svc, store: store, namespaces: namespaces, blobs: blobs, now: &now, ws: ws}
}

// stageStuckPending locks the workspace, declares a version, records a
// content key for it without finalizing, and writes that blob: the row
// stays pending with committed content that the backup must leave out.
func stageStuckPending(
	ctx context.Context, t *testing.T, f stuckBackupFixture,
) (workspace.PendingState, blob.Key) {
	t.Helper()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx, f.ws.ID, "alice", newState("l1", 1, "{\"v\":1}"), authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	stuckKey := blob.Key(stateDirOf(ctx, t, f.namespaces, f.ws.ID) + pending.Version.ID.String() + "/a1")

	var writable namespace.Namespace

	writable, err = f.namespaces.WritableHere(ctx, "acme")
	if err != nil {
		t.Fatalf("writable namespace: %v", err)
	}

	if _, err = f.store.RecordUploadAttempt(ctx, pending.Version.ID, stuckKey, writable.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	if err = f.blobs.Put(ctx, stuckKey, strings.NewReader("{\"v\":1}")); err != nil {
		t.Fatalf("put stuck blob: %v", err)
	}

	return pending, stuckKey
}

// assertBackupOmitsStuckKey proves a portable export succeeds without
// naming the stuck pending key: a pending row has no committed content.
func assertBackupOmitsStuckKey(ctx context.Context, t *testing.T, store *sqlite.Store, stuckKey blob.Key) {
	t.Helper()

	sink := func(string) (io.WriteCloser, error) {
		return nopWriteCloser{Writer: io.Discard}, nil
	}

	summary, _, err := store.ExportPortable(ctx, sink)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	for _, ref := range summary.Blobs {
		if ref.Key == stuckKey.String() {
			t.Fatalf("export names stuck key %q, want only finalized content", stuckKey)
		}
	}
}

// assertDeleteSweepsStuckPending proves the delete is refused before the
// expiry with ErrPendingState and sweeps the expired row after it.
func assertDeleteSweepsStuckPending(
	ctx context.Context, t *testing.T, f stuckBackupFixture, pendingID workspace.StateVersionID,
) {
	t.Helper()

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	if err := f.svc.Delete(ctx, f.ws.Ref, retentionEntry()); !errors.Is(err, workspace.ErrPendingState) {
		t.Fatalf("delete before expiry: err = %v, want ErrPendingState", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	if err := f.svc.Delete(ctx, f.ws.Ref, retentionEntry()); err != nil {
		t.Fatalf("delete after expiry: %v", err)
	}

	if _, err := f.svc.Get(ctx, f.ws.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}

	if _, err := f.store.GetStateVersion(ctx, pendingID); !errors.Is(
		err, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", err)
	}
}

// nopWriteCloser discards one portable file: the test checks the summary,
// not the bytes.
type nopWriteCloser struct {
	io.Writer
}

// Close does nothing: the bytes stay discarded.
func (nopWriteCloser) Close() error { return nil }

// retentionEntry returns a valid audit entry for a delete of acme's workspace.
// The service sets its time.
func retentionEntry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "acme",
		Subject:       "alice@example.com",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:manage",
		Object:        "opentofu:acme/e2e-workspace",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "workspace_test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// cappedService returns a service over the fixture stores holding at most
// limit pending versions per workspace.
func (f fixture) cappedService(t *testing.T, limit int) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: limit,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

func sum(content string) string {
	s := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test uses MD5 for integrity.

	return hex.EncodeToString(s[:])
}

func newState(lineage string, serial int64, content string) workspace.NewState {
	return workspace.NewState{Lineage: lineage, MD5: sum(content), Outputs: nil, Serial: serial, Force: false}
}

// push declares and uploads one state version as alice, who must hold the lock.
func (f fixture) push(t *testing.T, in workspace.NewState, content string) error {
	t.Helper()

	pending, err := f.svc.BeginState(
		t.Context(),
		f.ws.ID,
		"alice",
		in,
		authority(forcedOnly(), noRecord),
	)
	if err != nil {
		return err
	}

	_, err = f.svc.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(content))

	return err
}

// TestWorkspaceConformance runs the store conformance suite of the workspace
// ports against the in-memory store, proving the suite before an adapter runs it.
func TestWorkspaceConformance(t *testing.T) {
	t.Parallel()

	storetest.RunWorkspace(t, func(t *testing.T) storetest.WorkspaceStore {
		t.Helper()

		return newMemStore()
	})
}

func TestLocking(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "alice"); !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("unlock unlocked: %v, want ErrNotLocked", err)
	}

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if _, err := f.svc.Lock(ctx, f.ws.ID, "bob", "plan"); !errors.Is(err, workspace.ErrLocked) {
		t.Errorf("second lock: %v, want ErrLocked", err)
	}

	if _, err := f.svc.Unlock(ctx, f.ws.ID, "bob"); !errors.Is(err, workspace.ErrLockedByOther) {
		t.Errorf("unlock by another: %v, want ErrLockedByOther", err)
	}

	if _, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Errorf("force unlock: %v", err)
	}

	if _, err := f.svc.Lock(ctx, f.ws.ID, "bob", "plan"); err != nil {
		t.Errorf("lock after force unlock: %v", err)
	}
}

func TestStateVersions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	enableForced(t, f)

	if err := f.push(t, newState("l1", 1, "{\"v\":1}"), "{\"v\":1}"); !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("push without lock: %v, want ErrNotLocked", err)
	}

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	tests := []struct {
		want    error
		name    string
		in      workspace.NewState
		content string
	}{
		{name: "first state", in: newState("l1", 1, "{\"v\":1}"), content: "{\"v\":1}", want: nil},
		{name: "same serial", in: newState("l1", 1, "v1b"), content: "v1b", want: workspace.ErrStaleSerial},
		{
			name:    "lower serial",
			in:      newState("l1", 0, "{\"v\":0}"),
			content: "{\"v\":0}",
			want:    workspace.ErrStaleSerial,
		},
		{
			name:    "other lineage",
			in:      newState("l2", 2, "{\"v\":2}"),
			content: "{\"v\":2}",
			want:    workspace.ErrLineageMismatch,
		},
		{
			name:    "wrong md5",
			in:      newState("l1", 2, "{\"v\":2}"),
			content: "tampered",
			want:    workspace.ErrChecksumMismatch,
		},
		{
			name: "too large", in: newState("l1", 2, strings.Repeat("x", 65)), content: strings.Repeat("x", 65),
			want: workspace.ErrTooLarge,
		},
		{
			name:    "bad md5",
			in:      workspace.NewState{Lineage: "l1", MD5: "nope", Outputs: nil, Serial: 2, Force: false},
			content: "",
			want:    workspace.ErrInvalidState,
		},
		{name: "next state", in: newState("l1", 2, "{\"v\":2}"), content: "{\"v\":2}", want: nil},
		{
			name:    "forced other lineage",
			in:      workspace.NewState{Lineage: "l9", MD5: sum("{\"v\":9}"), Outputs: nil, Serial: 1, Force: true},
			content: "{\"v\":9}",
			want:    nil,
		},
	}

	for _, tt := range tests {
		if err := f.push(t, tt.in, tt.content); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}

	// The outputs document has its own 16 MiB limit, separate from the state
	// limit the "too large" case above proves.
	oversized := workspace.NewState{
		Lineage: "l1",
		MD5:     sum("{\"v\":3}"),
		Outputs: make([]byte, (16<<20)+1),
		Serial:  3,
		Force:   false,
	}

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", oversized, authority(forcedOnly(), nil))
	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Errorf("oversized outputs: %v, want ErrTooLarge", err)
	}

	checkCurrent(t, f, "l9", "{\"v\":9}")
}

// TestForcedStateNeedsTheLock proves force skips the lineage and serial
// checks but never the lock: a forced begin without the lock fails with
// ErrNotLocked, and a forced version declared under the lock does not
// finalize after the lock is lost.
func TestForcedStateNeedsTheLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	enableForced(t, f)

	forced := workspace.NewState{Lineage: "l9", MD5: sum("{\"v\":9}"), Outputs: nil, Serial: 1, Force: true}

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(forcedOnly(), nil))
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("forced begin without the lock: %v, want ErrNotLocked", err)
	}

	if _, lockErr := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("forced begin: %v", err)
	}

	if _, _, err = f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	_, err = f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":9}"))
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("forced upload after losing the lock: %v, want ErrNotLocked", err)
	}
}

func checkCurrent(t *testing.T, f fixture, lineage, content string) {
	t.Helper()

	current, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	r, err := f.svc.Open(t.Context(), current)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer func() {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if current.Lineage != lineage || string(got) != content {
		t.Errorf("current state: lineage %q content %q, want %q %q", current.Lineage, got, lineage, content)
	}
}

func TestUploadCapability(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	upload := func(id workspace.StateVersionID, tok token.Token, content string) error {
		_, uploadErr := f.svc.Upload(ctx, id, tok, strings.NewReader(content))

		return uploadErr
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	other, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	if err = upload(pending.Version.ID, other, "{\"v\":1}"); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Errorf("foreign capability: %v, want ErrUploadDenied", err)
	}

	if err = upload(pending.Version.ID, pending.Upload, "{\"v\":1}"); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// go-tfe retries a PUT whose response was lost: the same content succeeds.
	if err = upload(pending.Version.ID, pending.Upload, "{\"v\":1}"); err != nil {
		t.Errorf("retried upload: %v", err)
	}

	if err = upload(pending.Version.ID, pending.Upload, "other"); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Errorf("retried upload with other content: %v, want ErrUploadDenied", err)
	}

	late, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow)

	if err = upload(late.Version.ID, late.Upload, "{\"v\":2}"); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Errorf("expired capability: %v, want ErrUploadDenied", err)
	}
}

// A version declared under a lock must not finalize after the lock changed
// hands, and the failed attempt must leave the current state's content intact.
func TestUploadNeedsTheLockStill(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	const doc1 = `{"lineage":"l1","serial":1}`

	if err := f.push(t, newState("l1", 1, doc1), doc1); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	stale, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 5, "{\"v\":5}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin v5: %v", err)
	}

	if _, _, err = f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if _, err = f.svc.Lock(ctx, f.ws.ID, "bob", "apply"); err != nil {
		t.Fatalf("bob locks: %v", err)
	}

	_, err = f.svc.Upload(ctx, stale.Version.ID, stale.Upload, strings.NewReader("{\"v\":5}"))
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Errorf("upload after losing the lock: %v, want ErrLockedByOther", err)
	}

	// The force-unlock fenced v1: the current state is the fence, so the
	// failed upload left it in place.
	cur, curErr := f.svc.Current(ctx, f.ws.ID)
	if curErr != nil {
		t.Fatalf("current: %v", curErr)
	}

	if cur.FenceBy != "admin" || cur.Serial != 2 || cur.Lineage == "l1" {
		t.Errorf("current after the failed upload: fence by %q serial %d lineage %q, want the admin fence",
			cur.FenceBy, cur.Serial, cur.Lineage)
	}
}

// The same principal locking again after a force-unlock holds a new lock;
// work declared under the old one must not land.
func TestUploadNeedsTheSameLockAcquisition(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "ci", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	stale, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"ci",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, _, err = f.svc.ForceUnlock(ctx, f.ws.ID, "admin", noOverrides(), nil, forceEntry()); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if _, err = f.svc.Lock(ctx, f.ws.ID, "ci", "apply again"); err != nil {
		t.Fatalf("relock: %v", err)
	}

	_, err = f.svc.Upload(ctx, stale.Version.ID, stale.Upload, strings.NewReader("{\"v\":1}"))
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Errorf("upload under an older lock: %v, want ErrLockedByOther", err)
	}
}

// When finalize's outcome is unknown, the blob may be the current state's
// content and must survive.
func TestUnknownFinalizeOutcomeKeepsContent(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	f.store.mu.Lock()
	f.store.lostAck = true
	f.store.mu.Unlock()

	if err := f.push(t, newState("l1", 1, "{\"v\":1}"), "{\"v\":1}"); !errors.Is(err, errConnectionLost) {
		t.Fatalf("push: %v, want the lost acknowledgement", err)
	}

	checkCurrent(t, f, "l1", "{\"v\":1}")
}

// checkUnreadable proves ReadState of id fails with
// ErrStateVersionNotFound.
func checkUnreadable(t *testing.T, svc *workspace.Service, id workspace.StateVersionID) {
	t.Helper()

	if _, err := svc.ReadState(t.Context(), id); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Errorf("read of %s: %v, want ErrStateVersionNotFound", id, err)
	}
}

// TestReadState proves a finalized version reads back, while a pending
// version and an unknown ID fail with ErrStateVersionNotFound: a version
// that is not finalized is never readable.
func TestReadState(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	checkUnreadable(t, f.svc, "sv-missing")

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, beginErr := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}

	checkUnreadable(t, f.svc, pending.Version.ID)

	if pushErr := f.push(t, newState("l1", 1, "{\"v\":1}"), "{\"v\":1}"); pushErr != nil {
		t.Fatalf("push: %v", pushErr)
	}

	current, currentErr := f.svc.Current(ctx, f.ws.ID)
	if currentErr != nil {
		t.Fatalf("current: %v", currentErr)
	}

	got, readErr := f.svc.ReadState(ctx, current.ID)
	if readErr != nil {
		t.Fatalf("read of a finalized version: %v", readErr)
	}

	if got.ID != current.ID || got.Status != workspace.StatusFinalized || got.Lineage != "l1" || got.Serial != 1 {
		t.Errorf("read state = %+v, want the finalized version of lineage l1 serial 1", got)
	}

	checkUnreadable(t, f.svc, pending.Version.ID)
}

func TestParseName(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"a", "e2e-workspace", "Prod_1", strings.Repeat("a", 90)} {
		if _, err := workspace.ParseName(ok); err != nil {
			t.Errorf("ParseName(%q): %v", ok, err)
		}
	}

	for _, bad := range []string{"", "has.dot", "has space", "a/b", strings.Repeat("a", 91)} {
		if _, err := workspace.ParseName(bad); !errors.Is(err, workspace.ErrInvalidName) {
			t.Errorf("ParseName(%q): %v, want ErrInvalidName", bad, err)
		}
	}
}

// TestParseNameStatesTheRule proves that a refused workspace name gives the
// rule of a workspace name and an echo of at most 40 characters, and that an
// empty name gives the same text as an invalid one.
func TestParseNameStatesTheRule(t *testing.T) {
	t.Parallel()

	const rule = "use 1 to 90 ASCII letters, digits, - and _"

	for _, tc := range []struct{ in, want string }{
		{in: "", want: `invalid workspace name: "": ` + rule},
		{in: "a/b", want: `invalid workspace name: "a/b": ` + rule},
		{
			in:   strings.Repeat("a", 91),
			want: `invalid workspace name: "` + strings.Repeat("a", 40) + `"... (cut, 91 characters): ` + rule,
		},
	} {
		_, err := workspace.ParseName(tc.in)
		if !errors.Is(err, workspace.ErrInvalidName) || err.Error() != tc.want {
			t.Errorf("ParseName(%.50q) = %v, want %s", tc.in, err, tc.want)
		}
	}
}

// recordingBlobs wraps a blob store and records every deleted key, so tests
// prove a failed upload leaves no blob behind.
type recordingBlobs struct {
	blob.Store

	mu      sync.Mutex
	deleted []blob.Key
}

// Delete removes the object and records its key.
func (r *recordingBlobs) Delete(ctx context.Context, key blob.Key) error {
	err := r.Store.Delete(ctx, key)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.deleted = append(r.deleted, key)

	return err
}

// deletedKeys returns every deleted key.
func (r *recordingBlobs) deletedKeys() []blob.Key {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]blob.Key(nil), r.deleted...)
}

// TestChecksumMismatchDeletesBlob proves an upload whose bytes differ from
// the declared MD5 fails and leaves no blob behind: the attempt key is
// deleted and the version stays pending.
func TestChecksumMismatchDeletesBlob(t *testing.T) {
	t.Parallel()

	rec := &recordingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, deleted: nil}
	f := newFixtureWithBlobs(t, rec)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("tampered"))
	if !errors.Is(uploadErr, workspace.ErrChecksumMismatch) {
		t.Fatalf("upload: %v, want ErrChecksumMismatch", uploadErr)
	}

	deleted := rec.deletedKeys()
	if len(deleted) == 0 {
		t.Fatal("no blob deleted after the checksum mismatch")
	}

	for _, key := range deleted {
		_, getErr := rec.Get(ctx, key)
		if !errors.Is(getErr, blob.ErrNotFound) {
			t.Errorf("get of deleted blob %q: %v, want ErrNotFound", key, getErr)
		}
	}

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if stored.Status != workspace.StatusPending {
		t.Errorf("state version status = %q, want pending", stored.Status)
	}
}

// listBlobDir returns the slash-separated files under dir, sorted, so tests
// prove what an upload leaves behind. Empty directories do not count: only
// objects are blobs.
func listBlobDir(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	walkErr := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return fmt.Errorf("relative blob path: %w", relErr)
		}

		out = append(out, filepath.ToSlash(rel))

		return nil
	})
	if walkErr != nil {
		t.Fatalf("list blob dir: %v", walkErr)
	}

	slices.Sort(out)

	return out
}

// chtimesBlob sets the modification time of the blob key to at, so sweep
// tests control the grace period without waiting for it.
func chtimesBlob(t *testing.T, dir string, key blob.Key, at time.Time) {
	t.Helper()

	if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(key.String())), at, at); err != nil {
		t.Fatalf("chtimes %s: %v", key, err)
	}
}

// TestTooLargeUploadDeletesBlob proves a too-large upload fails with
// ErrTooLarge and leaves no blob behind: the blob directory lists the same
// objects before and after the rejection, and the version stays pending.
func TestTooLargeUploadDeletesBlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	blobs, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err = f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	before := listBlobDir(t, dir)

	content := strings.Repeat("x", 65)

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, content),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if !errors.Is(uploadErr, workspace.ErrTooLarge) {
		t.Fatalf("upload: %v, want ErrTooLarge", uploadErr)
	}

	if after := listBlobDir(t, dir); !slices.Equal(after, before) {
		t.Errorf("blob dir = %v, want %v: the rejected upload left an orphan blob", after, before)
	}

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if stored.Status != workspace.StatusPending {
		t.Errorf("state version status = %q, want pending", stored.Status)
	}
}

// TestDiscardJSONRendering proves the JSON rendering upload: a wrong
// capability is denied, a pending version with the capability succeeds
// without consuming it, a retry after finalize with the same capability and
// other content still succeeds, and an expired capability is denied.
func TestDiscardJSONRendering(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	other, tokenErr := token.New()
	if tokenErr != nil {
		t.Fatalf("token: %v", tokenErr)
	}

	discardErr := f.svc.DiscardJSONRendering(ctx, pending.Version.ID, other, strings.NewReader(`{"rendering":1}`))
	if !errors.Is(discardErr, workspace.ErrUploadDenied) {
		t.Errorf("foreign capability: %v, want ErrUploadDenied", discardErr)
	}

	discardErr = f.svc.DiscardJSONRendering(
		ctx,
		pending.Version.ID,
		pending.Upload,
		strings.NewReader(`{"rendering":1}`),
	)
	if discardErr != nil {
		t.Fatalf("discard: %v", discardErr)
	}

	// The rendering never consumes the capability: the state upload with the
	// same capability still finalizes.
	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if uploadErr != nil {
		t.Fatalf("upload after discard: %v", uploadErr)
	}

	// A retry after finalize with the same capability and other rendering
	// still succeeds: the two PUTs of one upload carry other content.
	discardErr = f.svc.DiscardJSONRendering(
		ctx,
		pending.Version.ID,
		pending.Upload,
		strings.NewReader(`{"rendering":2}`),
	)
	if discardErr != nil {
		t.Errorf("discard after finalize: %v", discardErr)
	}

	late, beginErr := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}

	*f.now = f.now.Add(workspace.UploadWindow)

	discardErr = f.svc.DiscardJSONRendering(ctx, late.Version.ID, late.Upload, strings.NewReader(`{"rendering":3}`))
	if !errors.Is(discardErr, workspace.ErrUploadDenied) {
		t.Errorf("expired capability: %v, want ErrUploadDenied", discardErr)
	}
}

// errFinalizeFailed is a store failure without a commit: the finalize
// outcome stays unknown and the attempt blob must survive.
var errFinalizeFailed = errors.New("finalize failed")

// TestPendingCap proves BeginState sweeps expired pendings first, then
// refuses past the cap with ErrTooManyPending: with at most two pendings,
// two declares work, a third fails, and past the sweep grace period a
// declare sweeps the old ones and works.
func TestPendingCap(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	svc := f.cappedService(t, 2)

	if _, err := svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	for _, serial := range []int64{1, 2} {
		in := newState("l1", serial, "{\"v\":1}")
		if _, beginErr := svc.BeginState(ctx, f.ws.ID, "alice", in, authority(forcedOnly(), nil)); beginErr != nil {
			t.Fatalf("begin serial %d: %v", serial, beginErr)
		}
	}

	_, err := svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 3, "{\"v\":3}"), authority(forcedOnly(), nil))
	if !errors.Is(err, workspace.ErrTooManyPending) {
		t.Fatalf("begin past the cap: %v, want ErrTooManyPending", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	_, err = svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 3, "{\"v\":3}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin after expiry: %v", err)
	}

	n, err := f.store.CountPendingStateVersions(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}

	if n != 1 {
		t.Fatalf("pending state versions = %d, want 1 after the sweep", n)
	}
}

// openSweepBlobs opens a blob store in a temporary directory and returns
// both, so sweep tests prove what an upload leaves behind.
func openSweepBlobs(t *testing.T) (*fs.Store, string) {
	t.Helper()

	dir := t.TempDir()

	blobs, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs, dir
}

// unknownUpload declares and uploads content with a finalize that writes
// nothing and reports errFinalizeFailed, and returns the pending version:
// the row stays pending with the attempted content and the blob stays.
func unknownUpload(t *testing.T, f fixture, in workspace.NewState) workspace.PendingState {
	t.Helper()

	ctx := t.Context()

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", in, authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	f.store.mu.Lock()
	f.store.failFinalize = errFinalizeFailed
	f.store.mu.Unlock()

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if !errors.Is(uploadErr, errFinalizeFailed) {
		t.Fatalf("upload: %v, want the unknown outcome", uploadErr)
	}

	return pending
}

// TestUnknownOutcomeKeepsAttempt proves an upload whose finalize outcome
// stays unknown keeps its blob: the version stays pending with the
// attempted content and the blob directory holds one object.
func TestUnknownOutcomeKeepsAttempt(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	stored, err := f.store.GetStateVersion(t.Context(), pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version after unknown finalize: %v", err)
	}

	if stored.Status != workspace.StatusPending || stored.Content == "" {
		t.Fatalf("pending version = %+v, want pending with attempted content", stored)
	}

	if before := listBlobDir(t, dir); len(before) != 1 {
		t.Fatalf("blob dir = %v, want the one attempted blob", before)
	}
}

// TestSweepExpiredPendingDeletesBlobs proves a declare sweeps expired
// pending versions with their attempted content: within the grace period
// it keeps both, past it the old row and its blob are gone and only the
// kept pendings stay.
func TestSweepExpiredPendingDeletesBlobs(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	*f.now = f.now.Add(workspace.UploadWindow + time.Second)

	_, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin within grace: %v", err)
	}

	if n := countPending(t, f); n != 2 {
		t.Fatalf("pending state versions = %d, want 2 within the grace period", n)
	}

	if before := listBlobDir(t, dir); len(before) != 1 {
		t.Fatalf("blob dir = %v, want the one attempted blob within the grace period", before)
	}

	*f.now = f.now.Add(workspace.SweepGracePeriod)

	_, err = f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 3, "{\"v\":3}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin past grace: %v", err)
	}

	if _, err := f.store.GetStateVersion(ctx, pending.Version.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", err)
	}

	if after := listBlobDir(t, dir); len(after) != 0 {
		t.Fatalf("blob dir = %v, want empty after the sweep", after)
	}

	if n := countPending(t, f); n != 2 {
		t.Fatalf("pending state versions = %d, want the kept version and the new one", n)
	}
}

// countPending returns how many pending versions the fixture workspace holds.
func countPending(t *testing.T, f fixture) int {
	t.Helper()

	n, err := f.store.CountPendingStateVersions(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}

	return n
}

// TestRetryAfterUnknownDeletesSupersededBlob proves a second upload of one
// version with an unknown finalize outcome deletes the first attempt's
// blob: after two unknown uploads only the latest attempt stays, and the
// declare past the grace period sweeps it too, leaving no blob behind.
func TestRetryAfterUnknownDeletesSupersededBlob(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	f.store.mu.Lock()
	f.store.failFinalize = errFinalizeFailed
	f.store.mu.Unlock()

	if _, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}")); !errors.Is(
		err, errFinalizeFailed,
	) {
		t.Fatalf("second upload: %v, want the unknown outcome", err)
	}

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if stored.Status != workspace.StatusPending || stored.Content == "" {
		t.Fatalf("pending version = %+v, want pending with attempted content", stored)
	}

	if blobs := listBlobDir(t, dir); !slices.Equal(blobs, []string{stored.Content.String()}) {
		t.Fatalf("blob dir = %v, want only the latest attempt %q", blobs, stored.Content)
	}

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	_, err = f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin past grace: %v", err)
	}

	if _, err := f.store.GetStateVersion(ctx, pending.Version.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", err)
	}

	if after := listBlobDir(t, dir); len(after) != 0 {
		t.Fatalf("blob dir = %v, want empty after the sweep", after)
	}
}

// TestSupersededDeleteFailureKeepsReference proves an upload whose
// superseded blob delete fails still records the new attempt: the row names
// the new attempt, the old blob stays unreferenced until a later sweep, and
// a retry after healing finalizes, leaving the current blob with the old
// orphan for the next sweep to collect.
func TestSupersededDeleteFailureKeepsReference(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	flaky := &flakyBlobs{Store: blobs, mu: sync.Mutex{}, fails: 100}
	f := newFixtureWithBlobs(t, flaky)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	stored, err := f.store.GetStateVersion(ctx, pending.Version.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	first := stored.Content
	if first == "" {
		t.Fatal("pending version names no attempted content")
	}

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if !errors.Is(uploadErr, errFlakyBlob) {
		t.Fatalf("second upload: %v, want the blob failure", uploadErr)
	}

	assertDanglingAttempt(t, f, dir, pending.Version.ID, first)

	flaky.mu.Lock()
	flaky.fails = 0
	flaky.mu.Unlock()

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if err != nil {
		t.Fatalf("retry upload: %v", err)
	}

	assertSweepCollectsOrphan(t, f, dir, first, sv)
}

// assertDanglingAttempt proves the failed upload recorded its new attempt
// while the old blob stays orphaned on disk: the new blob was never written.
func assertDanglingAttempt(
	t *testing.T, f fixture, dir string, id workspace.StateVersionID, first blob.Key,
) {
	t.Helper()

	stored, err := f.store.GetStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if stored.Content == "" || stored.Content == first {
		t.Fatalf("pending content = %q, want the newly recorded attempt", stored.Content)
	}

	if blobs := listBlobDir(t, dir); !slices.Equal(blobs, []string{first.String()}) {
		t.Fatalf("blob dir = %v, want only the orphaned attempt", blobs)
	}
}

// assertSweepCollectsOrphan proves the retry after healing finalizes with
// the old orphan beside it, and the next sweep past the grace period
// collects the orphan, leaving only the current blob.
func assertSweepCollectsOrphan(
	t *testing.T, f fixture, dir string, first blob.Key, sv workspace.StateVersion,
) {
	t.Helper()

	ctx := t.Context()
	want := []string{first.String(), sv.Content.String()}
	slices.Sort(want)

	if blobs := listBlobDir(t, dir); !slices.Equal(blobs, want) {
		t.Fatalf("blob dir = %v, want the current blob with the orphan %v", blobs, want)
	}

	chtimesBlob(t, dir, first, f.now.Add(-2*workspace.SweepGracePeriod))

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	_, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin past grace: %v", err)
	}

	if blobs := listBlobDir(t, dir); !slices.Equal(blobs, []string{sv.Content.String()}) {
		t.Fatalf("blob dir = %v, want only the current blob %q after the sweep", blobs, sv.Content)
	}
}

// TestRetryAfterUnknownSuccessKeepsOneBlob proves an upload that succeeds
// after an unknown outcome leaves exactly the current blob: the superseded
// attempt is gone, and the current state opens with the uploaded content.
func TestRetryAfterUnknownSuccessKeepsOneBlob(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if err != nil {
		t.Fatalf("retry upload: %v", err)
	}

	if blobs := listBlobDir(t, dir); !slices.Equal(blobs, []string{sv.Content.String()}) {
		t.Fatalf("blob dir = %v, want only the current blob %q", blobs, sv.Content)
	}

	current, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if current.ID != sv.ID || current.Content != sv.Content {
		t.Fatalf("current state = %+v, want version %s", current, sv.ID)
	}

	r, err := f.svc.Open(ctx, current)
	if err != nil {
		t.Fatalf("open current state: %v", err)
	}

	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read current state: %v", err)
	}

	if closeErr := r.Close(); closeErr != nil {
		t.Fatalf("close current state: %v", closeErr)
	}

	if string(content) != "{\"v\":1}" {
		t.Fatalf("current content = %q, want %q", content, "{\"v\":1}")
	}
}

// errFlakyBlob is a transient object store failure the sweep retries.
var errFlakyBlob = errors.New("blob store unavailable")

// flakyBlobs fails the first fails Delete calls, then delegates to the
// wrapped store.
type flakyBlobs struct {
	blob.Store

	mu    sync.Mutex
	fails int
}

// Delete fails while fails holds, then deletes the object.
func (f *flakyBlobs) Delete(ctx context.Context, key blob.Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.fails > 0 {
		f.fails--

		return errFlakyBlob
	}

	return f.Store.Delete(ctx, key)
}

// TestSweepRetriesBlobDeletes proves a transient blob failure never leaves
// an orphan and never fails the declare: the sweep retries the delete,
// removes the expired row with its blob, and the declare succeeds.
func TestSweepRetriesBlobDeletes(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	flaky := &flakyBlobs{Store: blobs, mu: sync.Mutex{}, fails: 1}
	f := newFixtureWithBlobs(t, flaky)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	_, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin past grace: %v", err)
	}

	if _, err := f.store.GetStateVersion(ctx, pending.Version.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", err)
	}

	if after := listBlobDir(t, dir); len(after) != 0 {
		t.Fatalf("blob dir = %v, want empty after the retried sweep", after)
	}
}

// TestSweepDeletesUnreferencedBlobs proves a declare deletes the blobs under
// the workspace prefix that no version references once they are older than
// the grace period, and keeps the referenced and the fresh ones: a stray
// object from a superseded attempt or a committed row delete never stays.
func TestSweepDeletesUnreferencedBlobs(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// A finalized version names its blob for ever.
	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	current, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	stray, err := blob.ParseKey(f.stateDir(t, f.ws.ID) + "sv-stray/a1")
	if err != nil {
		t.Fatalf("stray key: %v", err)
	}

	fresh, err := blob.ParseKey(f.stateDir(t, f.ws.ID) + "sv-stray/a2")
	if err != nil {
		t.Fatalf("fresh key: %v", err)
	}

	for _, key := range []blob.Key{stray, fresh} {
		if putErr := blobs.Put(ctx, key, strings.NewReader(key.String())); putErr != nil {
			t.Fatalf("put %s: %v", key, putErr)
		}
	}

	// Everything but the fresh stray is older than the grace period.
	old := f.now.Add(-2 * workspace.SweepGracePeriod)
	chtimesBlob(t, dir, current.Content, old)
	chtimesBlob(t, dir, stray, old)
	chtimesBlob(t, dir, fresh, *f.now)

	_, err = f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	want := []string{current.Content.String(), fresh.String()}
	slices.Sort(want)

	if after := listBlobDir(t, dir); !slices.Equal(after, want) {
		t.Fatalf("blob dir = %v, want the current and the fresh blob %v", after, want)
	}
}

// errKeepSetDown is the failure of a keep set read that is not a missing
// workspace.
var errKeepSetDown = errors.New("keep set read failed")

// TestSweepKeepsBlobsWhenTheKeepSetFails proves the sweep deletes nothing when
// the store cannot read the keep set, also when the workspace row is gone
// (workspace.ErrNotFound): an empty keep set would delete every old blob.
func TestSweepKeepsBlobsWhenTheKeepSetFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		name string
	}{
		{name: "the workspace row is gone", err: workspace.ErrNotFound},
		{name: "the read fails", err: errKeepSetDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blobs, dir := openSweepBlobs(t)
			f := newFixtureWithBlobs(t, blobs)

			stray := putOldStray(t, f, blobs, dir)

			f.store.mu.Lock()
			f.store.failReferenced = tc.err
			f.store.mu.Unlock()

			declareOnce(t, f)

			if after := listBlobDir(t, dir); !slices.Contains(after, stray.String()) {
				t.Fatalf("blob dir = %v, want the old stray blob kept", after)
			}
		})
	}
}

// putOldStray stores a blob under the state directory of the workspace of f
// that no version names, older than the grace period, and returns its key.
func putOldStray(t *testing.T, f fixture, blobs blob.Store, dir string) blob.Key {
	t.Helper()

	stray, err := blob.ParseKey(f.stateDir(t, f.ws.ID) + "sv-stray/a1")
	if err != nil {
		t.Fatalf("stray key: %v", err)
	}

	if putErr := blobs.Put(t.Context(), stray, strings.NewReader("stray")); putErr != nil {
		t.Fatalf("put stray: %v", putErr)
	}

	chtimesBlob(t, dir, stray, f.now.Add(-2*workspace.SweepGracePeriod))

	return stray
}

// declareOnce locks the workspace of f and declares one state. The declare
// treats the sweep as best effort, so it succeeds when the sweep fails.
func declareOnce(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	_, err := f.svc.BeginState(
		t.Context(), f.ws.ID, "alice", newState("l1", 1, "{\"v\":1}"), authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
}

// TestSweepBlobFailureKeepsDeclare proves a persistent blob failure never
// fails the declare: the expired row is gone and the new version declares,
// so cleanup trouble blocks no apply. The blob stays unreferenced until a
// later sweep past the grace period removes it, which the test drives by
// healing the store and aging the orphan.
func TestSweepBlobFailureKeepsDeclare(t *testing.T) {
	t.Parallel()

	blobs, dir := openSweepBlobs(t)
	flaky := &flakyBlobs{Store: blobs, mu: sync.Mutex{}, fails: 100}
	f := newFixtureWithBlobs(t, flaky)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending := unknownUpload(t, f, newState("l1", 1, "{\"v\":1}"))

	*f.now = f.now.Add(workspace.UploadWindow + workspace.SweepGracePeriod + time.Second)

	next, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 2, "{\"v\":2}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin past grace with failing blobs: %v", err)
	}

	if _, getErr := f.store.GetStateVersion(ctx, pending.Version.ID); !errors.Is(
		getErr, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("get swept version: %v, want ErrStateVersionNotFound", getErr)
	}

	if _, getErr := f.store.GetStateVersion(ctx, next.Version.ID); getErr != nil {
		t.Fatalf("get declared version: %v", getErr)
	}

	orphans := listBlobDir(t, dir)
	if len(orphans) != 1 {
		t.Fatalf("blob dir = %v, want the one orphaned blob", orphans)
	}

	orphan, err := blob.ParseKey(orphans[0])
	if err != nil {
		t.Fatalf("orphan key: %v", err)
	}

	// Heal the store and age the orphan past the grace period: the next
	// declare retries it through the unreferenced-blob sweep.
	flaky.mu.Lock()
	flaky.fails = 0
	flaky.mu.Unlock()

	chtimesBlob(t, dir, orphan, *f.now)

	*f.now = f.now.Add(workspace.SweepGracePeriod + time.Second)

	_, err = f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 3, "{\"v\":3}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin after healing: %v", err)
	}

	if after := listBlobDir(t, dir); len(after) != 0 {
		t.Fatalf("blob dir = %v, want empty after the retry", after)
	}
}

// TestConcurrentBeginStateKeepsCap proves concurrent declares never exceed
// the pending cap: exactly the cap succeeds, the rest report
// ErrTooManyPending.
func TestConcurrentBeginStateKeepsCap(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	svc := f.cappedService(t, 2)

	if _, err := svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	const declares = 8

	errs := make([]error, declares)

	var wg sync.WaitGroup

	for i := range declares {
		wg.Go(func() {
			_, errs[i] = svc.BeginState(
				ctx,
				f.ws.ID,
				"alice",
				newState("l1", int64(i+1), "{\"v\":1}"),
				authority(forcedOnly(), nil),
			)
		})
	}

	wg.Wait()

	succeeded := 0

	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workspace.ErrTooManyPending):
		default:
			t.Fatalf("begin: %v, want nil or ErrTooManyPending", err)
		}
	}

	if succeeded != 2 {
		t.Fatalf("successful declares = %d, want 2: the cap", succeeded)
	}

	if n := countPending(t, f); n != 2 {
		t.Fatalf("pending state versions = %d, want 2: the cap", n)
	}
}

// TestConcurrentBeginStateAcrossInstancesKeepsCap proves the cap holds
// across services over one store: two services declaring at once never
// exceed it. The barrier holds both declares through the cap check, so
// both count together: with per-service stripes alone both would pass the
// check together and the workspace would end over the cap. The store
// counts and inserts in one transaction, so exactly one declare wins the
// last slot.
func TestConcurrentBeginStateAcrossInstancesKeepsCap(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	gated := newGateCounter(f.store)

	defer gated.openOne()
	defer gated.openTwo()

	first, second := newGatedService(t, f, gated), newGatedService(t, f, gated)

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// One pending of the two the cap allows, so the two racing declares
	// contend for the last slot.
	_, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("prefill: %v", err)
	}

	errs := make([]error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		_, errs[0] = first.BeginState(
			ctx,
			f.ws.ID,
			"alice",
			newState("l1", 2, "{\"v\":1}"),
			authority(forcedOnly(), nil),
		)
	})
	wg.Go(func() {
		_, errs[1] = second.BeginState(
			ctx,
			f.ws.ID,
			"alice",
			newState("l1", 3, "{\"v\":1}"),
			authority(forcedOnly(), nil),
		)
	})

	gated.awaitCounts(t)
	wg.Wait()
	assertCapResult(t, f, errs, 1, 2)
}

// newGatedService returns a service over the gated store holding at most
// two pending versions per workspace.
func newGatedService(t *testing.T, f fixture, gated *gateCounter) *workspace.Service {
	t.Helper()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         gated,
		States:             gated,
		Blobs:              f.blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 2,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// assertCapResult checks that exactly won declares succeeded with
// ErrTooManyPending for the rest, and that the workspace holds total
// pendings.
func assertCapResult(t *testing.T, f fixture, errs []error, won, total int) {
	t.Helper()

	succeeded := 0

	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workspace.ErrTooManyPending):
		default:
			t.Fatalf("begin: %v, want nil or ErrTooManyPending", err)
		}
	}

	if succeeded != won {
		t.Fatalf("successful declares = %d, want %d", succeeded, won)
	}

	if n := countPending(t, f); n != total {
		t.Fatalf("pending state versions = %d, want %d", n, total)
	}
}

// gateCounter wraps a store and funnels every CountPendingStateVersions
// call through a two-phase barrier: both declares count before either
// proceeds, so both count together whatever the scheduling.
type gateCounter struct {
	*memStore

	arrived chan struct{}
	counted chan struct{}
	passOne chan struct{}
	passTwo chan struct{}

	onceOne sync.Once
	onceTwo sync.Once
}

// newGateCounter returns a barrier over store for two racing declares.
func newGateCounter(store *memStore) *gateCounter {
	return &gateCounter{
		memStore: store,
		arrived:  make(chan struct{}, 2),
		counted:  make(chan struct{}, 2),
		passOne:  make(chan struct{}),
		passTwo:  make(chan struct{}),
		onceOne:  sync.Once{},
		onceTwo:  sync.Once{},
	}
}

// CountPendingStateVersions implements workspace.StateStore: it waits at
// the barrier, then delegates.
func (g *gateCounter) CountPendingStateVersions(
	ctx context.Context, id workspace.ID,
) (int, error) {
	select {
	case g.arrived <- struct{}{}:
	default:
	}

	<-g.passOne

	n, countErr := g.memStore.CountPendingStateVersions(ctx, id)

	select {
	case g.counted <- struct{}{}:
	default:
	}

	<-g.passTwo

	return n, countErr
}

// openOne releases the first barrier phase. It is idempotent, so a
// timeout path releases waiters too.
func (g *gateCounter) openOne() {
	g.onceOne.Do(func() { close(g.passOne) })
}

// openTwo releases the second barrier phase. It is idempotent, so a
// timeout path releases waiters too.
func (g *gateCounter) openTwo() {
	g.onceTwo.Do(func() { close(g.passTwo) })
}

// awaitCounts drives the barrier: it lets both declares reach the cap
// check, then lets both count, then lets both proceed.
func (g *gateCounter) awaitCounts(t *testing.T) {
	t.Helper()

	timeout := time.After(30 * time.Second)

	for range 2 {
		select {
		case <-g.arrived:
		case <-timeout:
			t.Fatal("declares did not reach the cap check together")
		}
	}

	g.openOne()

	for range 2 {
		select {
		case <-g.counted:
		case <-timeout:
			t.Fatal("declares did not count together")
		}
	}

	g.openTwo()
}

// TestConcurrentUploadAcrossServicesKeepsCurrent proves parallel uploads of
// one version from two services keep the current state: each writes its own
// blob and records its own attempt, exactly one finalize wins, and the loser
// deletes only its own blob, so the current content always opens and the
// blob directory holds only it.
func TestConcurrentUploadAcrossServicesKeepsCurrent(t *testing.T) {
	t.Parallel()

	for range 20 {
		concurrentUploadAcrossServicesOnce(t)
	}
}

func concurrentUploadAcrossServicesOnce(t *testing.T) {
	t.Helper()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()
	second := newSecondService(t, f, blobs)

	if _, lockErr := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); lockErr != nil {
		t.Fatalf("lock: %v", lockErr)
	}

	pending, beginErr := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}

	assertOneUploadWins(t, f.svc, second, pending)
	assertCurrentHoldsOnlyBlob(t, f, dir)
}

// newSecondService returns a service over the fixture stores with its own
// lock stripes, so parallel uploads share nothing in memory.
func newSecondService(t *testing.T, f fixture, blobs blob.Store) *workspace.Service {
	t.Helper()

	second, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.store,
		States:             f.store,
		Blobs:              blobs,
		Namespaces:         f.namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Logger:             nil,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("second service: %v", err)
	}

	return second
}

// assertOneUploadWins uploads one version from two services at once and
// proves at least one succeeds with no unexpected error.
func assertOneUploadWins(
	t *testing.T, first, second *workspace.Service, pending workspace.PendingState,
) {
	t.Helper()

	ctx := t.Context()
	errs := make([]error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		_, errs[0] = first.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	})

	wg.Go(func() {
		_, errs[1] = second.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	})

	wg.Wait()

	succeeded := 0

	for _, uploadErr := range errs {
		switch {
		case uploadErr == nil:
			succeeded++
		case errors.Is(uploadErr, workspace.ErrUploadDenied):
		default:
			t.Fatalf("upload: %v, want nil or ErrUploadDenied", uploadErr)
		}
	}

	if succeeded == 0 {
		t.Fatal("no upload succeeded, want at least one")
	}
}

// assertCurrentHoldsOnlyBlob proves the current state opens with the
// uploaded content and the blob directory holds only its blob.
func assertCurrentHoldsOnlyBlob(t *testing.T, f fixture, dir string) {
	t.Helper()

	ctx := t.Context()
	current, currentErr := f.svc.Current(ctx, f.ws.ID)
	if currentErr != nil {
		t.Fatalf("current: %v", currentErr)
	}

	r, openErr := f.svc.Open(ctx, current)
	if openErr != nil {
		t.Fatalf("open current state: %v", openErr)
	}

	content, readErr := io.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil {
		t.Fatalf("close current state: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read current state: %v", readErr)
	}

	if string(content) != "{\"v\":1}" {
		t.Fatalf("current content = %q, want %q", content, "{\"v\":1}")
	}

	if listed := listBlobDir(t, dir); !slices.Equal(listed, []string{current.Content.String()}) {
		t.Fatalf("blob dir = %v, want only the current blob %q", listed, current.Content)
	}
}

// TestRejectedFinalizeDeletesBlob proves an upload whose finalize reports
// nothing was written leaves no blob behind, for every rejected sentinel:
// the attempt blob is deleted and the error keeps the sentinel.
func TestRejectedFinalizeDeletesBlob(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{
		workspace.ErrUploadDenied,
		workspace.ErrNotLocked,
		workspace.ErrLockedByOther,
		workspace.ErrLineageMismatch,
		workspace.ErrStaleSerial,
		workspace.ErrNotFound,
		workspace.ErrStateVersionNotFound,
		namespace.ErrNotFound,
		namespace.ErrFenced,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			t.Parallel()
			checkRejectedFinalizeDeletesBlob(t, sentinel)
		})
	}
}

// checkRejectedFinalizeDeletesBlob uploads with a finalize failure holding
// sentinel and proves the attempt blob is deleted with the sentinel kept:
// the blob directory holds no object afterwards.
func checkRejectedFinalizeDeletesBlob(t *testing.T, sentinel error) {
	t.Helper()

	blobs, dir := openSweepBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	f.store.mu.Lock()
	f.store.failFinalize = sentinel
	f.store.mu.Unlock()

	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}"))
	if !errors.Is(uploadErr, sentinel) {
		t.Fatalf("upload: %v, want %v", uploadErr, sentinel)
	}

	if after := listBlobDir(t, dir); len(after) != 0 {
		t.Fatalf("blob dir = %v, want empty: the rejected upload left an orphan blob", after)
	}
}

// TestDiscardJSONRenderingTooLarge proves a rendering over the size limit
// fails with ErrTooLarge without consuming the capability: a small rendering
// with the same capability still succeeds, and the state upload still
// finalizes.
func TestDiscardJSONRenderingTooLarge(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	oversized := strings.Repeat("x", 65)

	discardErr := f.svc.DiscardJSONRendering(ctx, pending.Version.ID, pending.Upload, strings.NewReader(oversized))
	if !errors.Is(discardErr, workspace.ErrTooLarge) {
		t.Errorf("oversized rendering: %v, want ErrTooLarge", discardErr)
	}

	discardErr = f.svc.DiscardJSONRendering(
		ctx,
		pending.Version.ID,
		pending.Upload,
		strings.NewReader(`{"rendering":1}`),
	)
	if discardErr != nil {
		t.Errorf("discard after oversized: %v", discardErr)
	}

	if _, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}")); err != nil {
		t.Errorf("upload after oversized discard: %v", err)
	}
}

// TestDiscardJSONRenderingExpiryAfterFinalize proves the expiry applies
// whatever the version status: a finalized version with its own capability
// is denied once expired.
func TestDiscardJSONRenderingExpiryAfterFinalize(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("{\"v\":1}")); err != nil {
		t.Fatalf("upload: %v", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow)

	discardErr := f.svc.DiscardJSONRendering(
		ctx,
		pending.Version.ID,
		pending.Upload,
		strings.NewReader(`{"rendering":2}`),
	)
	if !errors.Is(discardErr, workspace.ErrUploadDenied) {
		t.Errorf("expired capability after finalize: %v, want ErrUploadDenied", discardErr)
	}
}

// pushVersion pushes one state version as alice with its own finalize time,
// so the history order is deterministic, and returns the current version.
func pushVersion(t *testing.T, f fixture, serial int64, content string) workspace.StateVersion {
	t.Helper()

	*f.now = f.now.Add(time.Second)

	if err := f.push(t, newState("l1", serial, content), content); err != nil {
		t.Fatalf("push serial %d: %v", serial, err)
	}

	current, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	return current
}

// mustLock locks the fixture workspace for holder, failing the test on error.
func mustLock(t *testing.T, f fixture, holder string) {
	t.Helper()

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, holder, "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}
}

// mustUnlock releases alice's lock on the fixture workspace.
func mustUnlock(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.svc.Unlock(t.Context(), f.ws.ID, "alice"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}

// openDirBlobs opens a blob store in a temporary directory and returns the
// store with its path, so tests prove what a restore leaves behind.
func openDirBlobs(t *testing.T) (*fs.Store, string) {
	t.Helper()

	dir := t.TempDir()

	blobs, err := fs.Open(dir)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs, dir
}

// checkCurrentID proves the workspace's current state version is want.
func checkCurrentID(t *testing.T, f fixture, want workspace.StateVersionID) {
	t.Helper()

	current, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if current.ID != want {
		t.Errorf("current = %s, want %s", current.ID, want)
	}
}

// checkUnlocked proves the fixture workspace holds no lock.
func checkUnlocked(t *testing.T, f fixture) {
	t.Helper()

	w, err := f.svc.GetByID(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if w.Lock.Locked() {
		t.Error("workspace still locked, want it unlocked")
	}
}

// checkHistoryTotal proves the workspace history holds want versions.
func checkHistoryTotal(t *testing.T, f fixture, want int) {
	t.Helper()

	_, total, err := f.svc.History(t.Context(), f.ws.ID, workspace.Page{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if total != want {
		t.Errorf("history total = %d, want %d", total, want)
	}
}

// checkHistory proves list holds want in order.
func checkHistory(t *testing.T, list, want []workspace.StateVersion) {
	t.Helper()

	if len(list) != len(want) {
		t.Fatalf("history holds %d versions, want %d", len(list), len(want))
	}

	for i := range want {
		if list[i].ID != want[i].ID || list[i].Serial != want[i].Serial {
			t.Errorf(
				"history [%d] = %s serial %d, want %s serial %d",
				i,
				list[i].ID,
				list[i].Serial,
				want[i].ID,
				want[i].Serial,
			)
		}
	}
}

// checkRestored proves restored carries src as a new, forced, finalized
// version with its own blob key.
func checkRestored(t *testing.T, restored, src workspace.StateVersion) {
	t.Helper()

	if restored.Lineage != src.Lineage || restored.Serial != src.Serial {
		t.Errorf(
			"restored = lineage %q serial %d, want lineage %q serial %d",
			restored.Lineage,
			restored.Serial,
			src.Lineage,
			src.Serial,
		)
	}

	if restored.MD5 != src.MD5 || restored.Size != src.Size {
		t.Errorf("restored = md5 %x size %d, want md5 %x size %d", restored.MD5, restored.Size, src.MD5, src.Size)
	}

	if !restored.Force {
		t.Error("restored force = false, want true")
	}

	if restored.RestoredFrom != src.ID {
		t.Errorf("restored-from = %q, want %q", restored.RestoredFrom, src.ID)
	}

	if restored.ID == src.ID {
		t.Error("restored id repeats the source id, want a new version")
	}

	if restored.Content == src.Content {
		t.Error("restored blob repeats the source key, want its own key")
	}

	if restored.Status != workspace.StatusFinalized {
		t.Errorf("restored status = %q, want finalized", restored.Status)
	}

	if restored.Holder != "alice" {
		t.Errorf("restored holder = %q, want alice", restored.Holder)
	}
}

// readContent opens sv through the service and returns its bytes.
func readContent(t *testing.T, f fixture, sv workspace.StateVersion) string {
	t.Helper()

	r, err := f.svc.Open(t.Context(), sv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	defer func() {
		if closeErr := r.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	return string(got)
}

// TestHistory proves the service lists finalized versions newest first with
// the total count, through paging and for an unknown workspace.
func TestHistory(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")
	second := pushVersion(t, f, 2, "{\"v\":2}")

	list, total, err := f.svc.History(ctx, f.ws.ID, workspace.Page{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if total != 2 {
		t.Errorf("history total = %d, want 2", total)
	}

	checkHistory(t, list, []workspace.StateVersion{second, first})

	paged, pagedTotal, err := f.svc.History(ctx, f.ws.ID, workspace.Page{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("paged history: %v", err)
	}

	if pagedTotal != 2 {
		t.Errorf("paged history total = %d, want 2", pagedTotal)
	}

	checkHistory(t, paged, []workspace.StateVersion{first})

	empty, emptyTotal, err := f.svc.History(ctx, "ws-missing", workspace.Page{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("history of an unknown workspace: %v", err)
	}

	if emptyTotal != 0 {
		t.Errorf("history total of an unknown workspace = %d, want 0", emptyTotal)
	}

	checkHistory(t, empty, nil)
}

// TestRestoreMiddleVersion proves a restore of an older version makes it
// current again as a forced version with its own blob key, keeps its lineage,
// serial, MD5 and size, records its source, releases the taken lock, and
// repeats its serial in the history.
func TestRestoreMiddleVersion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")
	second := pushVersion(t, f, 2, "{\"v\":2}")
	third := pushVersion(t, f, 3, "{\"v\":3}")

	mustUnlock(t, f)

	*f.now = f.now.Add(time.Second)

	restored, err := f.svc.Restore(ctx, f.ws.ID, second.ID, "alice")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	checkRestored(t, restored, second)

	if got := readContent(t, f, restored); got != "{\"v\":2}" {
		t.Errorf("restored content = %q, want %q", got, "{\"v\":2}")
	}

	checkCurrentID(t, f, restored.ID)
	checkUnlocked(t, f)

	list, total, err := f.svc.History(ctx, f.ws.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if total != 4 {
		t.Errorf("history total = %d, want 4", total)
	}

	checkHistory(t, list, []workspace.StateVersion{restored, third, second, first})

	if list[0].RestoredFrom != second.ID {
		t.Errorf("restored-from in history = %q, want %q", list[0].RestoredFrom, second.ID)
	}

	for _, sv := range list[1:] {
		if sv.RestoredFrom != "" {
			t.Errorf("restored-from of %s = %q, want empty", sv.ID, sv.RestoredFrom)
		}
	}
}

// TestRestoreLockedByOther proves a restore while another principal holds
// the lock fails with ErrLockedByOther, names the holder, and changes
// nothing: the lock stays and no version or blob appears.
func TestRestoreLockedByOther(t *testing.T) {
	t.Parallel()

	blobs, dir := openDirBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)
	mustLock(t, f, "bob")

	before := listBlobDir(t, dir)

	_, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Fatalf("restore: %v, want ErrLockedByOther", err)
	}

	if !strings.Contains(err.Error(), "bob") {
		t.Errorf("restore error = %q, want it to name the holder bob", err)
	}

	w, err := f.svc.GetByID(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if !w.Lock.Locked() || w.Lock.Holder != "bob" {
		t.Errorf("lock = %+v, want the lock still held by bob", w.Lock)
	}

	checkCurrentID(t, f, first.ID)
	checkHistoryTotal(t, f, 1)

	if after := listBlobDir(t, dir); !slices.Equal(after, before) {
		t.Errorf("blob dir = %v, want %v: the refused restore left a blob", after, before)
	}
}

// TestRestoreWhileCallerHoldsLock proves a restore under the caller's own
// lock succeeds and the lock stays with the same acquisition.
func TestRestoreWhileCallerHoldsLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")
	pushVersion(t, f, 2, "{\"v\":2}")

	before, err := f.svc.GetByID(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	*f.now = f.now.Add(time.Second)

	restored, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if restored.RestoredFrom != first.ID {
		t.Errorf("restored-from = %q, want %q", restored.RestoredFrom, first.ID)
	}

	after, err := f.svc.GetByID(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if !after.Lock.Locked() || after.Lock.Holder != "alice" || after.Lock.ID != before.Lock.ID {
		t.Errorf("lock = %+v, want the lock still held by alice under %q", after.Lock, before.Lock.ID)
	}

	if got := readContent(t, f, restored); got != "{\"v\":1}" {
		t.Errorf("restored content = %q, want %q", got, "{\"v\":1}")
	}
}

// TestRestoreConcurrentSameHolderWritesOnce proves that concurrent restores
// of one version by the holder of the lock write one restored version: every
// caller gets that version, and the history grows by one.
func TestRestoreConcurrentSameHolderWritesOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")
	pushVersion(t, f, 2, "{\"v\":2}")

	*f.now = f.now.Add(time.Second)

	const callers = 8

	var wg sync.WaitGroup

	ids := make([]workspace.StateVersionID, callers)
	errs := make([]error, callers)

	for i := range callers {
		wg.Go(func() {
			restored, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
			ids[i], errs[i] = restored.ID, err
		})
	}

	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("restore %d: %v", i, errs[i])
		}

		if ids[i] != ids[0] {
			t.Fatalf("restore %d answered %s, restore 0 answered %s: want one restored version", i, ids[i], ids[0])
		}
	}

	_, total, err := f.svc.History(ctx, f.ws.ID, workspace.Page{Limit: 0, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if total != 3 {
		t.Fatalf("history holds %d versions, want 3: two pushes and one restore", total)
	}
}

// TestRestoreUnknownVersion proves a restore of a missing version, a pending
// version or a version of another workspace fails with
// ErrStateVersionNotFound.
func TestRestoreUnknownVersion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	_, err := f.svc.Restore(ctx, f.ws.ID, "sv-missing", "alice")
	if !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Errorf("restore of a missing version: %v, want ErrStateVersionNotFound", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	_, pendingErr := f.svc.Restore(ctx, f.ws.ID, pending.Version.ID, "alice")
	if !errors.Is(pendingErr, workspace.ErrStateVersionNotFound) {
		t.Errorf("restore of a pending version: %v, want ErrStateVersionNotFound", pendingErr)
	}

	otherName, err := workspace.ParseName("other")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	other, err := f.svc.Create(ctx, workspace.Ref{Namespace: "acme", Name: otherName}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create other workspace: %v", err)
	}

	if _, err = f.svc.Lock(ctx, other.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock other: %v", err)
	}

	otherPending, err := f.svc.BeginState(
		ctx,
		other.ID,
		"alice",
		newState("l1", 1, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin other: %v", err)
	}

	_, err = f.svc.Upload(ctx, otherPending.Version.ID, otherPending.Upload, strings.NewReader("{\"v\":1}"))
	if err != nil {
		t.Fatalf("upload other: %v", err)
	}

	foreign, err := f.svc.Current(ctx, other.ID)
	if err != nil {
		t.Fatalf("current other: %v", err)
	}

	_, foreignErr := f.svc.Restore(ctx, f.ws.ID, foreign.ID, "alice")
	if !errors.Is(foreignErr, workspace.ErrStateVersionNotFound) {
		t.Errorf("restore of another workspace's version: %v, want ErrStateVersionNotFound", foreignErr)
	}
}

// TestRestoreFailedCopyKeepsCurrent proves a restore whose source content no
// longer matches its MD5 fails, leaves the old state current and leaves no
// blob behind.
func TestRestoreFailedCopyKeepsCurrent(t *testing.T) {
	t.Parallel()

	blobs, dir := openDirBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)

	// Replace the source content with other sealed bytes under the same key,
	// as a damaged object would read: the open succeeds, and the MD5 check
	// of the copy fails.
	if err := blobs.Delete(ctx, first.Content); err != nil {
		t.Fatalf("delete source blob: %v", err)
	}

	sealed, err := f.secrets.SealState(
		ctx, f.ws.Ref.Namespace, f.ws.ID.String(), string(first.ID), strings.NewReader("tampered"),
	)
	if err != nil {
		t.Fatalf("seal tampered content: %v", err)
	}

	tampered, err := io.ReadAll(sealed)
	if err != nil {
		t.Fatalf("read tampered content: %v", err)
	}

	if err = blobs.Put(ctx, first.Content, bytes.NewReader(tampered)); err != nil {
		t.Fatalf("tamper source blob: %v", err)
	}

	before := listBlobDir(t, dir)

	*f.now = f.now.Add(time.Second)

	_, err = f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if !errors.Is(err, workspace.ErrChecksumMismatch) {
		t.Fatalf("restore: %v, want ErrChecksumMismatch", err)
	}

	checkCurrentID(t, f, first.ID)
	checkHistoryTotal(t, f, 1)
	checkUnlocked(t, f)

	if after := listBlobDir(t, dir); !slices.Equal(after, before) {
		t.Errorf("blob dir = %v, want %v: the failed copy left a stray blob", after, before)
	}
}

// fencedWorkspaces rejects every write with namespace.ErrFenced, proving a
// restore with a stale fence writes nothing.
type fencedWorkspaces struct {
	*memStore
}

// CreateWorkspace rejects the write with namespace.ErrFenced.
func (f fencedWorkspaces) CreateWorkspace(
	_ context.Context, _ workspace.Workspace, _ namespace.Fence,
) error {
	return namespace.ErrFenced
}

// CreateWorkspacePlaced rejects the write with namespace.ErrFenced.
func (f fencedWorkspaces) CreateWorkspacePlaced(
	_ context.Context, _ workspace.Workspace, _ workspace.Placement, _ namespace.Fence,
) error {
	return namespace.ErrFenced
}

// SaveWorkspace rejects the write with namespace.ErrFenced.
func (f fencedWorkspaces) SaveWorkspace(
	_ context.Context, _ workspace.Workspace, _ namespace.Fence,
) error {
	return namespace.ErrFenced
}

// fencedStates rejects every write with namespace.ErrFenced, proving a
// restore with a stale fence writes nothing.
type fencedStates struct {
	*memStore
}

// CreateStateVersion rejects the write with namespace.ErrFenced.
func (f fencedStates) CreateStateVersion(
	_ context.Context, _ workspace.StateVersion, _ namespace.Fence, _ int,
) error {
	return namespace.ErrFenced
}

// FinalizeStateVersion rejects the write with namespace.ErrFenced.
//
//nolint:revive // The signature of the workspace.StateStore port.
func (f fencedStates) FenceAndUnlock(
	_ context.Context, _ workspace.Workspace, _ workspace.StateVersion, _ workspace.StateVersionID, _ namespace.Fence,
	_ audit.Entry,
) error {
	return namespace.ErrFenced
}

func (f fencedStates) FinalizeStateVersion(
	_ context.Context, _ workspace.StateVersion, _ namespace.Fence,
) error {
	return namespace.ErrFenced
}

func (f fencedStates) FinalizeStateVersionExpecting(
	_ context.Context, _ workspace.StateVersion, _ namespace.Fence, _ workspace.StateVersionID,
) error {
	return namespace.ErrFenced
}

// TestRestoreStaleFenceWritesNothing proves a restore whose writes the store
// fences fails with ErrFenced and changes nothing: no lock, no version and
// no blob.
func TestRestoreStaleFenceWritesNothing(t *testing.T) {
	t.Parallel()

	blobs, dir := openDirBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)

	namespaces, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	fenced, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         fencedWorkspaces{memStore: f.store},
		States:             fencedStates{memStore: f.store},
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		Logger:             nil,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	before := listBlobDir(t, dir)

	*f.now = f.now.Add(time.Second)

	if _, err = fenced.Restore(ctx, f.ws.ID, first.ID, "alice"); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("restore: %v, want ErrFenced", err)
	}

	checkCurrentID(t, f, first.ID)
	checkUnlocked(t, f)
	checkHistoryTotal(t, f, 1)

	if after := listBlobDir(t, dir); !slices.Equal(after, before) {
		t.Errorf("blob dir = %v, want %v: the fenced restore left a blob", after, before)
	}
}

// TestRestoreRetryMakesNoSecondVersion proves a retried restore answers the
// version the first call made current and writes no second version.
func TestRestoreRetryMakesNoSecondVersion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)

	*f.now = f.now.Add(time.Second)

	restored, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	retried, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if err != nil {
		t.Fatalf("retried restore: %v", err)
	}

	if retried.ID != restored.ID {
		t.Errorf("retried restore = %s, want the first restore %s", retried.ID, restored.ID)
	}

	list, total, err := f.svc.History(ctx, f.ws.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	if total != 2 {
		t.Errorf("history total = %d, want 2", total)
	}

	checkHistory(t, list, []workspace.StateVersion{restored, first})
}

// TestRestoreRetryLockedByOther proves a retried restore while another
// principal holds the lock fails with ErrLockedByOther and writes nothing,
// even though the first call already made the requested source current.
func TestRestoreRetryLockedByOther(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)

	*f.now = f.now.Add(time.Second)

	restored, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	mustLock(t, f, "bob")

	_, err = f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Fatalf("retried restore: %v, want ErrLockedByOther", err)
	}

	if !strings.Contains(err.Error(), "bob") {
		t.Errorf("retried restore error = %q, want it to name the holder bob", err)
	}

	checkCurrentID(t, f, restored.ID)
	checkHistoryTotal(t, f, 2)
}

// raceWorkspaces lets a concurrent locker win the restore lock race: the
// first save of alice's restore lock finds bob already holding the lock.
type raceWorkspaces struct {
	*memStore

	raced bool
}

// SaveWorkspace injects bob's lock before alice's restore take lands, so the
// take fails on the revision and the service must map it to ErrLockedByOther.
func (r *raceWorkspaces) SaveWorkspace(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence,
) error {
	if !r.raced && w.Lock.Holder == "alice" && w.Lock.Reason == "restore" {
		r.raced = true

		r.mu.Lock()
		stored := r.workspaces[w.ID]
		stored.Lock = workspace.Lock{LockedAt: w.UpdatedAt, ID: "lock-bob", Holder: "bob", Reason: "apply"}
		stored.Revision++
		r.workspaces[w.ID] = stored
		r.mu.Unlock()
	}

	return r.memStore.SaveWorkspace(ctx, w, fence)
}

// TestRestoreLockRaceNamesHolder proves a restore whose lock take loses a
// race to another principal fails with ErrLockedByOther naming the winner,
// not with the raw error of the lost take.
func TestRestoreLockRaceNamesHolder(t *testing.T) {
	t.Parallel()

	blobs := openFixtureBlobs(t)
	f := newFixtureWithBlobs(t, blobs)
	ctx := t.Context()

	mustLock(t, f, "alice")

	first := pushVersion(t, f, 1, "{\"v\":1}")

	mustUnlock(t, f)

	namespaces, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	raced, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         &raceWorkspaces{memStore: f.store, raced: false},
		States:             f.store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             f.secrets,
		Now:                func() time.Time { return *f.now },
		MaxStateBytes:      64,
		Logger:             nil,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	*f.now = f.now.Add(time.Second)

	_, err = raced.Restore(ctx, f.ws.ID, first.ID, "alice")
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Fatalf("restore: %v, want ErrLockedByOther", err)
	}

	if !strings.Contains(err.Error(), "bob") {
		t.Errorf("restore error = %q, want it to name the holder bob", err)
	}

	checkCurrentID(t, f, first.ID)
	checkHistoryTotal(t, f, 1)
}

// sealedKeys is a secret.KeyStore in memory for sealed fixtures.
type sealedKeys struct {
	keys map[string]secret.WrappedKey
	mu   sync.Mutex
}

func newSealedKeys() *sealedKeys {
	return &sealedKeys{mu: sync.Mutex{}, keys: map[string]secret.WrappedKey{}}
}

func (m *sealedKeys) CreateKey(
	_ context.Context, k secret.WrappedKey, _ namespace.Fence,
) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.keys[k.Scope.String()]; ok {
		return existing, nil
	}

	m.keys[k.Scope.String()] = k

	return k, nil
}

func (m *sealedKeys) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[scope.String()]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

func (m *sealedKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := map[string]bool{}

	for _, k := range m.keys {
		seen[k.WrapperID] = true
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}

	slices.Sort(out)

	return out, nil
}

// sealedWrapper wraps data keys with a fresh key-encryption key for one test.
type sealedWrapper struct {
	aead cipher.AEAD
	id   string
	kek  []byte
}

func newSealedWrapper(t *testing.T) *sealedWrapper {
	t.Helper()

	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("generate key-encryption key: %v", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("new GCM: %v", err)
	}

	return &sealedWrapper{aead: aead, id: "kek-test", kek: kek}
}

func (w *sealedWrapper) ID() string { return w.id }

func (w *sealedWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	nonce := make([]byte, w.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return w.aead.Seal(nonce, nonce, key, aad), nil
}

func (w *sealedWrapper) Unwrap(_ context.Context, sealed, aad []byte) ([]byte, error) {
	n := w.aead.NonceSize()
	if len(sealed) < n {
		return nil, secret.ErrDecrypt
	}

	key, err := w.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, secret.ErrDecrypt
	}

	return key, nil
}

// newSealedFixture returns a fixture whose service seals state with a fresh
// key-encryption key and holds larger states, so sealing tests prove what an
// upload leaves behind.
func newSealedFixture(t *testing.T) fixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	wrapper := newSealedWrapper(t)

	secrets, err := secret.NewService(secret.Config{
		Keys: newSealedKeys(), Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	blobs := openFixtureBlobs(t)
	refusals := workspacetest.NewRefusals()

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         refusals,
		Sealer:             secrets,
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      4 << 20,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return fixture{
		svc: svc, store: store, namespaces: namespaces, secrets: secrets, blobs: blobs, refusals: refusals,
		now: &now, ws: ws,
		sealKEK: wrapper.kek, sealKeyID: wrapper.id,
	}
}

// sealedOutputsDoc is the outputs document of the sealing tests: one
// sensitive output with a known value.
func sealedOutputsDoc(value string) []byte {
	return []byte(`{"greeting":{"sensitive":true,"value":` + strconv.Quote(value) + `,"type":"string"}}`)
}

// TestSealedUploadLeavesNoPlaintext proves a sealed upload keeps no sensitive
// byte sequence in the blob store or in the outputs column, while the
// download and the outputs read back the plain bytes.
func TestSealedUploadLeavesNoPlaintext(t *testing.T) {
	t.Parallel()

	f := newSealedFixture(t)

	// The outputs come from the body (ADR 49, decision 8), so the body carries
	// the output value the test checks.
	content := `{"version":4,"outputs":{"secret":{"sensitive":true,"value":"s3cr3t-output-value"}},` +
		`"resources":[{"attr":"s3cr3t-state-attribute"}]}`
	sensitive := "s3cr3t-state-attribute"
	outputValue := "s3cr3t-output-value"

	sv := uploadSealedState(t, f, content, outputValue)

	assertBlobSealed(t, f, sv, sensitive)
	assertOutputsColumnSealed(t, f, sv, outputValue)
	assertDownloadRoundTrip(t, f, sv, content)
	assertCurrentOutputsPlain(t, f, outputValue)
}

// TestNewServiceWithoutSealerFails proves NewService without a Sealer fails:
// every service seals state, so none runs without one.
func TestNewServiceWithoutSealerFails(t *testing.T) {
	t.Parallel()

	store := newMemStore()

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	_, err = workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              openFixtureBlobs(t),
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             nil,
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if !errors.Is(err, workspace.ErrMissingSealer) {
		t.Fatalf("NewService without a sealer: err = %v, want ErrMissingSealer", err)
	}
}

// uploadSealedState locks the fixture workspace and uploads content with one
// sensitive output, returning the sealed version.
func uploadSealedState(t *testing.T, f fixture, content, outputValue string) workspace.StateVersion {
	t.Helper()

	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: sealedOutputsDoc(outputValue),
		Serial:  1,
		Force:   false,
	}, authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	if !sv.Sealed {
		t.Fatal("uploaded version reads unsealed, want sealed")
	}

	return sv
}

// assertBlobSealed proves the blob store holds sealed bytes only: no
// sensitive attribute value.
func assertBlobSealed(t *testing.T, f fixture, sv workspace.StateVersion, sensitive string) {
	t.Helper()

	ctx := t.Context()

	raw, err := f.blobs.Get(ctx, sv.Content)
	if err != nil {
		t.Fatalf("get blob: %v", err)
	}

	sealed, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read blob: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close blob: %v", closeErr)
	}

	if bytes.Contains(sealed, []byte(sensitive)) {
		t.Fatal("blob store holds the sensitive attribute value, want sealed bytes only")
	}
}

// assertOutputsColumnSealed proves the outputs column holds the sealed
// document: no sensitive output value. The mem store returns the stored
// version, so its outputs are the sealed document.
func assertOutputsColumnSealed(t *testing.T, f fixture, sv workspace.StateVersion, outputValue string) {
	t.Helper()

	stored, err := f.store.GetStateVersion(t.Context(), sv.ID)
	if err != nil {
		t.Fatalf("get stored version: %v", err)
	}

	if bytes.Contains(stored.Outputs, []byte(outputValue)) {
		t.Fatal("outputs column holds the sensitive output value, want sealed bytes only")
	}
}

// assertDownloadRoundTrip proves the download answers the same bytes the CLI
// sent.
func assertDownloadRoundTrip(t *testing.T, f fixture, sv workspace.StateVersion, content string) {
	t.Helper()

	ctx := t.Context()

	r, err := f.svc.Open(ctx, sv)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	got, readErr := io.ReadAll(r)
	closeErr := r.Close()

	if readErr != nil {
		t.Fatalf("read download: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close download: %v", closeErr)
	}

	if string(got) != content {
		t.Fatalf("download = %q, want %q", got, content)
	}
}

// assertCurrentOutputsPlain proves the current version carries plain outputs.
func assertCurrentOutputsPlain(t *testing.T, f fixture, outputValue string) {
	t.Helper()

	current, err := f.svc.Current(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if !bytes.Contains(current.Outputs, []byte(outputValue)) {
		t.Fatalf("current outputs lack the output value: %q", current.Outputs)
	}
}

// TestSealedDownloadTamper proves a changed byte, a dropped tail, swapped
// chunks and an extension of a sealed blob each fail the download with an
// error that holds no key, scope or content.
func TestSealedDownloadTamper(t *testing.T) {
	t.Parallel()

	f := newSealedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	content := `{"resources":[` + strings.Repeat(`{"sensitive":"s3cr3t-state-attribute"},`, 4999) +
		`{"sensitive":"s3cr3t-state-attribute"}]}`

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", workspace.NewState{
		Lineage: "l1",
		MD5:     sum(content),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, authority(forcedOnly(), nil))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	sealed := readSealedBlob(t, f, sv)

	for name, bad := range sealedMutants(t, sealed) {
		assertTamperedDownloadFails(t, &tamperCase{f: f, sv: sv, name: name, bad: bad})
	}
}

// readSealedBlob returns the sealed bytes the upload stored.
func readSealedBlob(t *testing.T, f fixture, sv workspace.StateVersion) []byte {
	t.Helper()

	ctx := t.Context()

	raw, err := f.blobs.Get(ctx, sv.Content)
	if err != nil {
		t.Fatalf("get blob: %v", err)
	}

	sealed, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil {
		t.Fatalf("read blob: %v", readErr)
	}

	if closeErr != nil {
		t.Fatalf("close blob: %v", closeErr)
	}

	return sealed
}

// sealedMutants returns the tampered blobs the download must refuse: a
// changed byte, a dropped tail, swapped chunks and an extension.
func sealedMutants(t *testing.T, sealed []byte) map[string][]byte {
	t.Helper()

	changed := bytes.Clone(sealed)
	changed[len(changed)/2] ^= 1

	mutants := map[string][]byte{
		"changed byte": changed,
		"dropped tail": bytes.Clone(sealed[:len(sealed)-10]),
		"extended":     append(bytes.Clone(sealed), 0x00),
	}

	if swapped := swapSealedFrames(sealed); swapped != nil {
		mutants["swapped chunks"] = swapped
	} else {
		t.Fatal("sealed blob holds less than two chunks, want at least two for the swap case")
	}

	return mutants
}

// tamperCase groups one tampered blob with the version to open it as.
type tamperCase struct {
	f    fixture
	sv   workspace.StateVersion
	name string
	bad  []byte
}

// assertTamperedDownloadFails proves the download of a tampered blob fails
// with ErrSealedContent and that the error holds no content, no namespace
// name, no workspace ID, no key ID and no key-encryption key.
func assertTamperedDownloadFails(t *testing.T, c *tamperCase) {
	t.Helper()

	ctx := t.Context()

	if err := c.f.blobs.Delete(ctx, c.sv.Content); err != nil {
		t.Fatalf("%s: delete blob: %v", c.name, err)
	}

	if err := c.f.blobs.Put(ctx, c.sv.Content, bytes.NewReader(c.bad)); err != nil {
		t.Fatalf("%s: rewrite blob: %v", c.name, err)
	}

	r, err := c.f.svc.Open(ctx, c.sv)
	if err != nil {
		t.Fatalf("%s: open: %v", c.name, err)
	}

	_, err = io.ReadAll(r)
	closeErr := r.Close()

	if err == nil {
		t.Errorf("%s: tampered download succeeded, want ErrSealedContent", c.name)

		return
	}

	if !errors.Is(err, workspace.ErrSealedContent) {
		t.Errorf("%s: err = %v, want ErrSealedContent", c.name, err)
	}

	if len(c.f.sealKEK) == 0 || c.f.sealKeyID == "" {
		t.Fatalf("%s: fixture has no key-encryption key or key ID, want a sealed fixture", c.name)
	}

	secrets := []string{
		"s3cr3t", "acme", c.f.ws.ID.String(), c.f.sealKeyID,
		string(c.f.sealKEK),
		hex.EncodeToString(c.f.sealKEK),
		base64.StdEncoding.EncodeToString(c.f.sealKEK),
		base64.RawURLEncoding.EncodeToString(c.f.sealKEK),
	}

	for _, leaked := range leakedSecrets(err.Error(), secrets) {
		t.Errorf("%s: error %q holds %q, want no key, scope or content", c.name, err, leaked)
	}

	if closeErr != nil && !errors.Is(closeErr, workspace.ErrSealedContent) {
		t.Logf("%s: close: %v", c.name, closeErr)
	}
}

// leakedSecrets returns each value of secrets that the text holds. An empty
// value is never a match.
func leakedSecrets(text string, secrets []string) []string {
	var leaked []string

	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			leaked = append(leaked, s)
		}
	}

	return leaked
}

// TestLeakedSecretsIgnoresRandomID proves that the leak check reports a secret
// only when the secret is in the text, and not when a random ID holds the
// letters of a fixed word.
func TestLeakedSecretsIgnoresRandomID(t *testing.T) {
	t.Parallel()

	kek := bytes.Repeat([]byte{0x01}, 32)
	shared := []string{
		"kek-test",
		string(kek),
		hex.EncodeToString(kek),
		base64.StdEncoding.EncodeToString(kek),
		base64.RawURLEncoding.EncodeToString(kek),
	}

	tests := []struct {
		name    string
		text    string
		secrets []string
		want    []string
	}{
		{
			name:    "random ID holds kek",
			text:    fmt.Errorf("open state %s: %w", "sv-kekkekkekkekkekkekkekkek", workspace.ErrSealedContent).Error(),
			secrets: nil,
			want:    nil,
		},
		{
			name:    "key ID",
			text:    "open state sv-abc: kek-test",
			secrets: nil,
			want:    []string{"kek-test"},
		},
		{
			name:    "hex key",
			text:    "open state sv-abc: " + hex.EncodeToString(kek),
			secrets: nil,
			want:    []string{hex.EncodeToString(kek)},
		},
		{
			name:    "base64 key",
			text:    "open state sv-abc: " + base64.StdEncoding.EncodeToString(kek),
			secrets: nil,
			want:    []string{base64.StdEncoding.EncodeToString(kek), base64.RawURLEncoding.EncodeToString(kek)},
		},
		{
			name:    "empty secret",
			text:    "open state sv-abc",
			secrets: []string{""},
			want:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			secrets := shared
			if tc.secrets != nil {
				secrets = tc.secrets
			}

			got := leakedSecrets(tc.text, secrets)
			if !slices.Equal(got, tc.want) {
				t.Errorf("leakedSecrets(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// swapSealedFrames swaps the first two chunks of a sealed blob, or returns
// nil when the blob holds less than two chunks. The sealed stream is one
// version byte and an 8-byte nonce, then frames of a 4-byte big-endian
// ciphertext length, a final-chunk flag byte and the ciphertext.
func swapSealedFrames(sealed []byte) []byte {
	// The version byte and the 32-byte salt of a version 3 stream header.
	const header = 1 + 32

	frames := [][]byte{}
	rest := sealed[header:]

	for len(rest) > 0 {
		if len(rest) < 5 {
			return nil
		}

		length := int(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
		if len(rest) < 5+length {
			return nil
		}

		frames = append(frames, rest[:5+length])
		rest = rest[5+length:]
	}

	if len(frames) < 2 {
		return nil
	}

	frames[0], frames[1] = frames[1], frames[0]

	out := append([]byte{}, sealed[:header]...)
	for _, f := range frames {
		out = append(out, f...)
	}

	return out
}

// TestOpenStateBlobRejectsForeignWorkspace proves a key whose workspace
// segment names another workspace opens nothing, even when the blob exists
// under that key: the open fails with ErrStateVersionNotFound and the
// version's own key still opens.
func TestOpenStateBlobRejectsForeignWorkspace(t *testing.T) {
	t.Parallel()

	f := newSealedFixture(t)

	content := `{"version":4,"outputs":{"secret":{"sensitive":true,"value":"s3cr3t-state-attribute"}}}`

	sv := uploadSealedState(t, f, content, "s3cr3t-output-value")
	foreign := foreignWorkspaceKey(t, f, sv)
	plantForeignBlob(t, f, sv, foreign)
	assertForeignKeyDenied(t, f, foreign)
	assertVersionKeyOpens(t, f, sv, content)
}

// foreignWorkspaceKey creates the other workspace and returns the key that
// copies the version and attempt segments of sv.Content under its ID: a key
// whose workspace segment names another workspace.
func foreignWorkspaceKey(t *testing.T, f fixture, sv workspace.StateVersion) blob.Key {
	t.Helper()

	ctx := t.Context()

	otherName, nameErr := workspace.ParseName("other")
	if nameErr != nil {
		t.Fatalf("parse name: %v", nameErr)
	}

	other, createErr := f.svc.Create(ctx, workspace.Ref{Namespace: "acme", Name: otherName}, memPlacement("acme"))
	if createErr != nil {
		t.Fatalf("create other workspace: %v", createErr)
	}

	parts := strings.Split(sv.Content.String(), "/")
	if len(parts) != 6 {
		t.Fatalf("content key = %q, want namespaces/<namespace>/tofu/<workspace>/<version>/<attempt>", sv.Content)
	}

	foreign, keyErr := blob.ParseKey(f.stateDir(t, other.ID) + parts[4] + "/" + parts[5])
	if keyErr != nil {
		t.Fatalf("foreign key: %v", keyErr)
	}

	return foreign
}

// plantForeignBlob stores the sealed bytes of sv under foreign, so the
// denial below proves the segment check, not a missing blob.
func plantForeignBlob(t *testing.T, f fixture, sv workspace.StateVersion, foreign blob.Key) {
	t.Helper()

	ctx := t.Context()

	sealed, readErr := readSealedBlobBytes(t, f, sv)
	if readErr != nil {
		t.Fatalf("read sealed blob: %v", readErr)
	}

	if putErr := f.blobs.Put(ctx, foreign, bytes.NewReader(sealed)); putErr != nil {
		t.Fatalf("put foreign blob: %v", putErr)
	}
}

// assertForeignKeyDenied proves the open of foreign fails with
// ErrStateVersionNotFound and opens nothing.
func assertForeignKeyDenied(t *testing.T, f fixture, foreign blob.Key) {
	t.Helper()

	foreignReader, foreignErr := f.svc.OpenStateBlob(t.Context(), foreign)
	if errors.Is(foreignErr, workspace.ErrStateVersionNotFound) {
		return
	}

	if foreignErr == nil {
		if closeErr := foreignReader.Close(); closeErr != nil {
			t.Errorf("close foreign reader: %v", closeErr)
		}
	}

	t.Fatalf(
		"open of a foreign workspace key: err = %v, want ErrStateVersionNotFound",
		foreignErr,
	)
}

// assertVersionKeyOpens proves the version's own key still opens with the
// uploaded content.
func assertVersionKeyOpens(t *testing.T, f fixture, sv workspace.StateVersion, content string) {
	t.Helper()

	opened, openErr := f.svc.OpenStateBlob(t.Context(), sv.Content)
	if openErr != nil {
		t.Fatalf("open of the version key: %v", openErr)
	}

	got, versionReadErr := io.ReadAll(opened)
	closeErr := opened.Close()

	if versionReadErr != nil {
		t.Fatalf("read version key: %v", versionReadErr)
	}

	if closeErr != nil {
		t.Fatalf("close version key: %v", closeErr)
	}

	if string(got) != content {
		t.Fatalf("version content = %q, want %q", got, content)
	}
}

// readSealedBlobBytes returns the sealed bytes the upload stored.
func readSealedBlobBytes(t *testing.T, f fixture, sv workspace.StateVersion) ([]byte, error) {
	t.Helper()

	ctx := t.Context()

	raw, err := f.blobs.Get(ctx, sv.Content)
	if err != nil {
		return nil, err
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close blob: %v", closeErr)
		}
	}()

	data, err := io.ReadAll(raw)
	if err != nil {
		return nil, fmt.Errorf("read sealed blob: %w", err)
	}

	return data, nil
}

// ReleaseUnfenced refuses the save unless the forced-write switches of the
// namespace and of the unit are on (ADR 44, decisions 4 and 6).
func (m *memStore) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	ns, nsOK := m.namespaces[w.Ref.Namespace]
	stored, ok := m.workspaces[w.ID]
	m.mu.Unlock()

	switch {
	case !ok:
		return workspace.ErrNotFound
	case !nsOK:
		return namespace.ErrNotFound
	case !ns.Overrides.Forced || !stored.Overrides.Forced:
		return workspace.NewOverrideRefusedError(
			workspace.OverrideForced,
			"write refused: the forced-write switch is off",
		)
	}

	return m.SaveReleased(ctx, w, fence, e)
}

// SaveReleased saves a released unit like SaveWorkspace and keeps the audit
// entry of the release, as the adapters append it in the same transaction.
func (m *memStore) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	err := m.keepReleaseEntry(fence, e)
	m.mu.Unlock()

	if err != nil {
		return err
	}

	if err := m.SaveWorkspace(ctx, w, fence); err != nil {
		m.mu.Lock()
		m.released = m.released[:len(m.released)-1]
		m.mu.Unlock()

		return err
	}

	return nil
}

// keepReleaseEntry validates the audit entry of a release like the adapters
// and keeps it. The caller holds mu.
func (m *memStore) keepReleaseEntry(fence namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	m.released = append(m.released, e)

	return nil
}

// releaseEntries returns the audit entries of the releases written so far.
func (m *memStore) releaseEntries() []audit.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.released)
}

// checkFence rejects a write to an unknown namespace or a stale failover
// version. The caller holds mu.
func (m *memStore) checkFence(fence namespace.Fence) error {
	ns, ok := m.namespaces[fence.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// publish writes the inventory entry for w. The caller holds mu.
func (m *memStore) publish(w workspace.Workspace) {
	name := w.Ref.Name.String()

	m.entries[memInventoryKey{namespace: w.Ref.Namespace, tool: memInventoryTool, name: name}] = inventory.Entry{
		LastRelease:   memLastRelease(w.LastRelease),
		ManualLock:    nil,
		Collection:    "",
		LastChanged:   w.UpdatedAt,
		Namespace:     w.Ref.Namespace,
		Tool:          memInventoryTool,
		Name:          name,
		UnitID:        w.ID.String(),
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
		Lock:          memLock(w.Lock),
		Labels:        nil,
	}
}

// checkUnlockedSwitches refuses an unlocked write when the namespace or the unit
// switch is off. The caller holds mu.
func (m *memStore) checkUnlockedSwitches(w workspace.Workspace) error {
	ns, ok := m.namespaces[w.Ref.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if !ns.Overrides.Unlocked || !w.Overrides.Unlocked {
		return workspace.NewOverrideRefusedError(
			workspace.OverrideUnlocked,
			"write refused: the unlocked-write switch is off",
		)
	}

	return nil
}

// checkForcedSwitches refuses a forced write when the namespace or the unit
// forced-write switch is off. The caller holds mu.
func (m *memStore) checkForcedSwitches(w workspace.Workspace) error {
	ns, ok := m.namespaces[w.Ref.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if !ns.Overrides.Forced || !w.Overrides.Forced {
		return workspace.NewOverrideRefusedError(
			workspace.OverrideForced,
			"write refused: the forced-write switch is off",
		)
	}

	return nil
}

// enableForced turns on the forced-write switches of the namespace and of the unit.
func enableForced(t *testing.T, f fixture) {
	t.Helper()

	if _, err := f.namespaces.SetOverrides(t.Context(), f.ws.Ref.Namespace, forcedOnly()); err != nil {
		t.Fatalf("turn on the namespace switch: %v", err)
	}

	if _, err := f.svc.SetOverrides(t.Context(), f.ws.ID, forcedOnly()); err != nil {
		t.Fatalf("turn on the unit switch: %v", err)
	}
}

// checkDeclareSwitches mirrors the store's declare and finalize checks for the
// unlocked and the forced override. The caller holds mu.
func (m *memStore) checkDeclareSwitches(w workspace.Workspace, sv workspace.StateVersion) error {
	if sv.UnlockedWrite {
		if err := m.checkUnlockedSwitches(w); err != nil {
			return err
		}
	}

	_, hasState := m.current[w.ID]
	if sv.Force && sv.RestoredFrom == "" && hasState {
		return m.checkForcedSwitches(w)
	}

	return nil
}

// pendingCount returns the number of state versions of m that are pending.
func pendingCount(m *memStore) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for id := range m.versions {
		if m.versions[id].Status == workspace.StatusPending {
			n++
		}
	}

	return n
}
