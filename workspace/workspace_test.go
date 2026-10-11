package workspace_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/blob/fs"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/workspace"
)

type oneNamespace struct{}

func (oneNamespace) CreateNamespace(context.Context, namespace.Namespace) error {
	return namespace.ErrAlreadyExists
}

func (oneNamespace) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{CreatedAt: time.Time{}, Name: name, HomeRegion: "local", FailoverVersion: 1}, nil
}

func (oneNamespace) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

// memStore implements Store and StateStore in memory, following their contracts.
type memStore struct {
	workspaces map[workspace.ID]workspace.Workspace
	versions   map[workspace.StateVersionID]workspace.StateVersion
	current    map[workspace.ID]workspace.StateVersionID
	mu         sync.Mutex
	// lostAck makes the next finalize commit and then report an error, as a
	// dropped connection after COMMIT would.
	lostAck bool
}

func newMemStore() *memStore {
	return &memStore{
		workspaces: map[workspace.ID]workspace.Workspace{},
		versions:   map[workspace.StateVersionID]workspace.StateVersion{},
		current:    map[workspace.ID]workspace.StateVersionID{},
		mu:         sync.Mutex{},
		lostAck:    false,
	}
}

func (m *memStore) CreateWorkspace(_ context.Context, w workspace.Workspace, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.workspaces {
		if m.workspaces[id].Ref == w.Ref {
			return workspace.ErrAlreadyExists
		}
	}

	m.workspaces[w.ID] = w

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

func (m *memStore) ListWorkspaces(context.Context, namespace.Name) ([]workspace.Workspace, error) {
	return nil, nil
}

func (m *memStore) SaveWorkspace(_ context.Context, w workspace.Workspace, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.workspaces[w.ID].Revision != w.Revision-1 {
		return workspace.ErrConcurrentModification
	}

	m.workspaces[w.ID] = w

	return nil
}

func (m *memStore) CreateStateVersion(_ context.Context, sv workspace.StateVersion, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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

func (m *memStore) FinalizeStateVersion(_ context.Context, sv workspace.StateVersion, _ namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.versions[sv.ID].Status != workspace.StatusPending {
		return workspace.ErrUploadDenied
	}

	switch lock := m.workspaces[sv.Workspace].Lock; {
	case !lock.Locked():
		return workspace.ErrNotLocked
	case lock.ID != sv.LockID:
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

type fixture struct {
	svc   *workspace.Service
	store *memStore
	now   *time.Time
	ws    workspace.Workspace
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: oneNamespace{}, Now: nil, Region: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()

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

	checkCurrent(t, f, "l9", "v9")
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
