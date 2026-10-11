package workspace_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// memInventoryTool names OpenTofu in inventory entries.
const memInventoryTool = "opentofu"

// memStore implements namespace.Store, workspace.Store, workspace.StateStore
// and inventory.Reader in memory, following their contracts.
type memStore struct {
	namespaces map[namespace.Name]namespace.Namespace
	workspaces map[workspace.ID]workspace.Workspace
	versions   map[workspace.StateVersionID]workspace.StateVersion
	current    map[workspace.ID]workspace.StateVersionID
	entries    map[memInventoryKey]inventory.Entry
	mu         sync.Mutex
	// lostAck makes the next finalize commit and then report an error, as a
	// dropped connection after COMMIT would.
	lostAck bool
}

type memInventoryKey struct {
	namespace namespace.Name
	tool      string
	name      string
}

var _ storetest.WorkspaceStore = (*memStore)(nil)

func newMemStore() *memStore {
	return &memStore{
		namespaces: map[namespace.Name]namespace.Namespace{},
		workspaces: map[workspace.ID]workspace.Workspace{},
		versions:   map[workspace.StateVersionID]workspace.StateVersion{},
		current:    map[workspace.ID]workspace.StateVersionID{},
		entries:    map[memInventoryKey]inventory.Entry{},
		mu:         sync.Mutex{},
		lostAck:    false,
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

func (m *memStore) CreateWorkspace(_ context.Context, w workspace.Workspace, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

func (m *memStore) CreateStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	if _, ok := m.workspaces[sv.Workspace]; !ok {
		return workspace.ErrNotFound
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

func (m *memStore) FinalizeStateVersion(
	_ context.Context, sv workspace.StateVersion, fence namespace.Fence,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

	w, ok := m.workspaces[sv.Workspace]
	if !ok {
		return workspace.ErrNotFound
	}

	switch {
	case !w.Lock.Locked():
		return workspace.ErrNotLocked
	case w.Lock.ID != sv.LockID:
		return workspace.ErrLockedByOther
	}

	if cur, ok := m.current[sv.Workspace]; ok && !sv.Force {
		switch current := m.versions[cur]; {
		case current.Lineage != sv.Lineage:
			return workspace.ErrLineageMismatch
		case current.Serial >= sv.Serial:
			return workspace.ErrStaleSerial
		}
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
		LastChanged:   w.UpdatedAt,
		Namespace:     w.Ref.Namespace,
		Tool:          memInventoryTool,
		Name:          name,
		UnitID:        w.ID.String(),
		DisplayName:   name,
		Status:        "",
		ResourceCount: nil,
	}
}

type fixture struct {
	svc   *workspace.Service
	store *memStore
	now   *time.Time
	ws    workspace.Workspace
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

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{CreatedAt: now, Name: "acme", HomeRegion: "local", FailoverVersion: 1}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	svc, err := workspace.NewService(workspace.Config{
		Workspaces:    store,
		States:        store,
		Blobs:         blobs,
		Namespaces:    namespaces,
		Now:           func() time.Time { return now },
		MaxStateBytes: 64,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return fixture{svc: svc, store: store, now: &now, ws: ws}
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

	pending, err := f.svc.BeginState(t.Context(), f.ws.ID, "alice", in)
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

	if _, err := f.svc.ForceUnlock(ctx, f.ws.ID); err != nil {
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

	if err := f.push(t, newState("l1", 1, "v1"), "v1"); !errors.Is(err, workspace.ErrNotLocked) {
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
		{name: "first state", in: newState("l1", 1, "v1"), content: "v1", want: nil},
		{name: "same serial", in: newState("l1", 1, "v1b"), content: "v1b", want: workspace.ErrStaleSerial},
		{name: "lower serial", in: newState("l1", 0, "v0"), content: "v0", want: workspace.ErrStaleSerial},
		{name: "other lineage", in: newState("l2", 2, "v2"), content: "v2", want: workspace.ErrLineageMismatch},
		{name: "wrong md5", in: newState("l1", 2, "v2"), content: "tampered", want: workspace.ErrChecksumMismatch},
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
		{name: "next state", in: newState("l1", 2, "v2"), content: "v2", want: nil},
		{
			name:    "forced other lineage",
			in:      workspace.NewState{Lineage: "l9", MD5: sum("v9"), Outputs: nil, Serial: 1, Force: true},
			content: "v9",
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
		MD5:     sum("v3"),
		Outputs: make([]byte, (16<<20)+1),
		Serial:  3,
		Force:   false,
	}

	if _, err := f.svc.BeginState(ctx, f.ws.ID, "alice", oversized); !errors.Is(err, workspace.ErrTooLarge) {
		t.Errorf("oversized outputs: %v, want ErrTooLarge", err)
	}

	checkCurrent(t, f, "l9", "v9")
}

// TestForcedStateNeedsTheLock proves force skips the lineage and serial
// checks but never the lock: a forced begin without the lock fails with
// ErrNotLocked, and a forced version declared under the lock does not
// finalize after the lock is lost.
func TestForcedStateNeedsTheLock(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	forced := workspace.NewState{Lineage: "l9", MD5: sum("v9"), Outputs: nil, Serial: 1, Force: true}

	if _, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced); !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("forced begin without the lock: %v, want ErrNotLocked", err)
	}

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced)
	if err != nil {
		t.Fatalf("forced begin: %v", err)
	}

	if _, err = f.svc.ForceUnlock(ctx, f.ws.ID); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	_, err = f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("v9"))
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	other, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	if err = upload(pending.Version.ID, other, "v1"); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Errorf("foreign capability: %v, want ErrUploadDenied", err)
	}

	if err = upload(pending.Version.ID, pending.Upload, "v1"); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// go-tfe retries a PUT whose response was lost: the same content succeeds.
	if err = upload(pending.Version.ID, pending.Upload, "v1"); err != nil {
		t.Errorf("retried upload: %v", err)
	}

	if err = upload(pending.Version.ID, pending.Upload, "other"); !errors.Is(err, workspace.ErrUploadDenied) {
		t.Errorf("retried upload with other content: %v, want ErrUploadDenied", err)
	}

	late, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 2, "v2"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	*f.now = f.now.Add(workspace.UploadWindow)

	if err = upload(late.Version.ID, late.Upload, "v2"); !errors.Is(err, workspace.ErrUploadDenied) {
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

	if err := f.push(t, newState("l1", 1, "v1"), "v1"); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	stale, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 5, "v5"))
	if err != nil {
		t.Fatalf("begin v5: %v", err)
	}

	if _, err = f.svc.ForceUnlock(ctx, f.ws.ID); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if _, err = f.svc.Lock(ctx, f.ws.ID, "bob", "apply"); err != nil {
		t.Fatalf("bob locks: %v", err)
	}

	_, err = f.svc.Upload(ctx, stale.Version.ID, stale.Upload, strings.NewReader("v5"))
	if !errors.Is(err, workspace.ErrLockedByOther) {
		t.Errorf("upload after losing the lock: %v, want ErrLockedByOther", err)
	}

	checkCurrent(t, f, "l1", "v1")
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

	stale, err := f.svc.BeginState(ctx, f.ws.ID, "ci", newState("l1", 1, "v1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = f.svc.ForceUnlock(ctx, f.ws.ID); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if _, err = f.svc.Lock(ctx, f.ws.ID, "ci", "apply again"); err != nil {
		t.Fatalf("relock: %v", err)
	}

	_, err = f.svc.Upload(ctx, stale.Version.ID, stale.Upload, strings.NewReader("v1"))
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

	if err := f.push(t, newState("l1", 1, "v1"), "v1"); !errors.Is(err, errConnectionLost) {
		t.Fatalf("push: %v, want the lost acknowledgement", err)
	}

	checkCurrent(t, f, "l1", "v1")
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

	pending, beginErr := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}

	checkUnreadable(t, f.svc, pending.Version.ID)

	if pushErr := f.push(t, newState("l1", 1, "v1"), "v1"); pushErr != nil {
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 2, "v2"))
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, content))
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
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
	_, uploadErr := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("v1"))
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

	late, beginErr := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 2, "v2"))
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}

	*f.now = f.now.Add(workspace.UploadWindow)

	discardErr = f.svc.DiscardJSONRendering(ctx, late.Version.ID, late.Upload, strings.NewReader(`{"rendering":3}`))
	if !errors.Is(discardErr, workspace.ErrUploadDenied) {
		t.Errorf("expired capability: %v, want ErrUploadDenied", discardErr)
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
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

	if _, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("v1")); err != nil {
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader("v1")); err != nil {
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

	first := pushVersion(t, f, 1, "v1")
	second := pushVersion(t, f, 2, "v2")

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

	first := pushVersion(t, f, 1, "v1")
	second := pushVersion(t, f, 2, "v2")
	third := pushVersion(t, f, 3, "v3")

	mustUnlock(t, f)

	*f.now = f.now.Add(time.Second)

	restored, err := f.svc.Restore(ctx, f.ws.ID, second.ID, "alice")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	checkRestored(t, restored, second)

	if got := readContent(t, f, restored); got != "v2" {
		t.Errorf("restored content = %q, want %q", got, "v2")
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

	first := pushVersion(t, f, 1, "v1")

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

	first := pushVersion(t, f, 1, "v1")
	pushVersion(t, f, 2, "v2")

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

	if got := readContent(t, f, restored); got != "v1" {
		t.Errorf("restored content = %q, want %q", got, "v1")
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

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", newState("l1", 1, "v1"))
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

	other, err := f.svc.Create(ctx, workspace.Ref{Namespace: "acme", Name: otherName})
	if err != nil {
		t.Fatalf("create other workspace: %v", err)
	}

	if _, err = f.svc.Lock(ctx, other.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock other: %v", err)
	}

	otherPending, err := f.svc.BeginState(ctx, other.ID, "alice", newState("l1", 1, "v1"))
	if err != nil {
		t.Fatalf("begin other: %v", err)
	}

	_, err = f.svc.Upload(ctx, otherPending.Version.ID, otherPending.Upload, strings.NewReader("v1"))
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

	first := pushVersion(t, f, 1, "v1")

	mustUnlock(t, f)

	// Replace the source content with other bytes under the same key, as a
	// damaged object would read.
	if err := blobs.Delete(ctx, first.Content); err != nil {
		t.Fatalf("delete source blob: %v", err)
	}

	if err := blobs.Put(ctx, first.Content, strings.NewReader("tampered")); err != nil {
		t.Fatalf("tamper source blob: %v", err)
	}

	before := listBlobDir(t, dir)

	*f.now = f.now.Add(time.Second)

	_, err := f.svc.Restore(ctx, f.ws.ID, first.ID, "alice")
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
	_ context.Context, _ workspace.StateVersion, _ namespace.Fence,
) error {
	return namespace.ErrFenced
}

// FinalizeStateVersion rejects the write with namespace.ErrFenced.
func (f fencedStates) FinalizeStateVersion(
	_ context.Context, _ workspace.StateVersion, _ namespace.Fence,
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

	first := pushVersion(t, f, 1, "v1")

	mustUnlock(t, f)

	namespaces, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	fenced, err := workspace.NewService(workspace.Config{
		Workspaces:    fencedWorkspaces{memStore: f.store},
		States:        fencedStates{memStore: f.store},
		Blobs:         blobs,
		Namespaces:    namespaces,
		Now:           func() time.Time { return *f.now },
		MaxStateBytes: 64,
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

	first := pushVersion(t, f, 1, "v1")

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

	first := pushVersion(t, f, 1, "v1")

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

	first := pushVersion(t, f, 1, "v1")

	mustUnlock(t, f)

	namespaces, err := namespace.NewService(namespace.Config{Store: f.store, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	raced, err := workspace.NewService(workspace.Config{
		Workspaces:    &raceWorkspaces{memStore: f.store, raced: false},
		States:        f.store,
		Blobs:         blobs,
		Namespaces:    namespaces,
		Now:           func() time.Time { return *f.now },
		MaxStateBytes: 64,
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
