package maintenance_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

func TestParseState(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"normal", "maintenance"} {
		if got, err := maintenance.ParseState(s); err != nil || got.String() != s {
			t.Fatalf("parse %q = %q, %v, want %q without error", s, got, err, s)
		}
	}

	if _, err := maintenance.ParseState("offline"); !errors.Is(err, maintenance.ErrInvalidState) {
		t.Fatalf("parse offline: err = %v, want ErrInvalidState", err)
	}
}

// stubSealer is a workspace.Sealer that seals nothing: it passes state and
// outputs through, so the maintenance tests prove their own behavior.
type stubSealer struct{}

func (stubSealer) SealState(
	_ context.Context, _ namespace.Name, _, _ string, r io.Reader,
) (io.Reader, error) {
	return r, nil
}

func (stubSealer) OpenState(
	_ context.Context, _ namespace.Name, _, _ string, r io.Reader,
) (io.ReadCloser, error) {
	return io.NopCloser(r), nil
}

func (stubSealer) SealOutputs(
	_ context.Context, _ namespace.Name, _, _ string, plain []byte,
) ([]byte, error) {
	return plain, nil
}

func (stubSealer) OpenOutputs(
	_ context.Context, _ namespace.Name, _, _ string, sealed []byte,
) ([]byte, error) {
	return sealed, nil
}

// fakeStateStore holds the installation state in memory. onSet runs on each
// write, so race tests seed a start between the enter check and its write.
// onSetIf runs before the conditional write checks its expectation, so a
// cross-instance write lands first and the conditional write loses, as one
// UPDATE would against a committed concurrent write.
type fakeStateStore struct {
	state    maintenance.State
	onSet    func()
	onSetIf  func()
	setIfErr error
}

func (f *fakeStateStore) GetState(context.Context) (maintenance.State, error) {
	return f.state, nil
}

func (f *fakeStateStore) SetState(_ context.Context, state maintenance.State) error {
	if state != maintenance.StateNormal && state != maintenance.StateMaintenance {
		return maintenance.ErrInvalidState
	}

	if f.onSet != nil {
		f.onSet()
	}

	f.state = state

	return nil
}

func (f *fakeStateStore) SetStateIf(_ context.Context, expected, next maintenance.State) error {
	if expected != maintenance.StateNormal && expected != maintenance.StateMaintenance {
		return fmt.Errorf("%w: expected %q", maintenance.ErrInvalidState, expected)
	}

	if next != maintenance.StateNormal && next != maintenance.StateMaintenance {
		return fmt.Errorf("%w: %q", maintenance.ErrInvalidState, next)
	}

	if f.onSetIf != nil {
		f.onSetIf()
	}

	if f.setIfErr != nil {
		return f.setIfErr
	}

	if f.state != expected {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	f.state = next

	return nil
}

// fakeNamespaces answers one namespace whose primary installation is the test installation.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID:                  testNamespaceID,
		CreatedAt:           time.Now(),
		Name:                name,
		PrimaryInstallation: testRegion,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	ns, err := fakeNamespaces{}.GetNamespace(ctx, testNamespace)
	if err != nil {
		return nil, err
	}

	return []namespace.Namespace{ns}, nil
}

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeStacks lists the stacks the fixture holds.
type fakeStacks struct {
	stacks []stack.Stack
}

func (*fakeStacks) CreateStack(context.Context, stack.Stack, namespace.Fence) error {
	return stack.ErrAlreadyExists
}

func (*fakeStacks) GetStack(context.Context, stack.Ref) (stack.Stack, error) {
	return stack.Stack{}, stack.ErrNotFound
}

func (f *fakeStacks) ListStacks(_ context.Context, _ stack.Filter) ([]stack.Stack, error) {
	return append([]stack.Stack(nil), f.stacks...), nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (f *fakeStacks) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, f.ListStacks, q)
}

func (*fakeStacks) DeleteStack(
	context.Context,
	stack.Ref,
	namespace.Fence,
	stack.DeleteGuard,
	inventory.Deletion,
) error {
	return stack.ErrNotFound
}

func (*fakeStacks) ForceDeleteStack(context.Context, stack.Ref, namespace.Fence, inventory.Deletion) error {
	return stack.ErrNotFound
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (*fakeStacks) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (*fakeStacks) PurgeStack(_ context.Context, _ stack.ID, _ namespace.Fence, _ time.Time, _ audit.Entry) error {
	return stack.ErrNotFound
}

func (*fakeStacks) UpdateStackTags(
	context.Context, stack.Ref, map[string]string, time.Time, namespace.Fence,
) error {
	return stack.ErrNotFound
}

func (*fakeStacks) RenameStack(context.Context, stack.Ref, stack.Name, namespace.Fence) error {
	return stack.ErrNotFound
}

// fakeUpdates holds updates by ID and answers the running one per stack.
type fakeUpdates struct {
	updates map[stack.UpdateID]stack.Update
}

func (*fakeUpdates) CreateUpdate(
	ctx context.Context, u stack.Update, key stack.UpdateKey, fence namespace.Fence,
) error {
	item, err := key.RefFor(u.Ref.Namespace, "stk-maintenance", u.ID)
	if err != nil {
		return err
	}

	_, err = key.NewKey(ctx, nil, item, fence)

	return err
}

func (f *fakeUpdates) GetUpdate(_ context.Context, _ stack.Ref, id stack.UpdateID) (stack.Update, error) {
	u, ok := f.updates[id]
	if !ok {
		return stack.Update{}, stack.ErrUpdateNotFound
	}

	return u, nil
}

func (*fakeUpdates) StartUpdate(
	_ context.Context, u stack.Update, _ namespace.Fence,
) (stack.Update, error) {
	return u, nil
}

func (f *fakeUpdates) SaveUpdate(_ context.Context, u stack.Update, _ namespace.Fence) error {
	stored, ok := f.updates[u.ID]
	if !ok {
		return stack.ErrUpdateNotFound
	}

	if u.Revision != stored.Revision+1 {
		return stack.ErrConcurrentModification
	}

	f.updates[u.ID] = u

	return nil
}

func (f *fakeUpdates) ActiveUpdate(_ context.Context, ref stack.Ref) (stack.Update, error) {
	for id := range f.updates {
		u := f.updates[id]
		if u.Ref == ref && u.Status == stack.StatusRunning && u.Mutates() {
			return u, nil
		}
	}

	return stack.Update{}, stack.ErrUpdateNotFound
}

func (*fakeUpdates) ListUpdates(context.Context, stack.Ref, stack.Page) ([]stack.Update, error) {
	return nil, nil
}

// fakeCheckpoints answers no checkpoint. The cancel path writes none.
type fakeCheckpoints struct{}

func (fakeCheckpoints) PutCheckpoint(context.Context, stack.Checkpoint, namespace.Fence) (bool, error) {
	return false, nil
}

func (fakeCheckpoints) LatestCheckpoint(context.Context, stack.Ref) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (fakeCheckpoints) CheckpointByVersion(context.Context, stack.Ref, int64) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (fakeCheckpoints) CheckpointByUpdate(context.Context, stack.Ref, stack.UpdateID) (stack.Checkpoint, error) {
	return stack.Checkpoint{}, stack.ErrNoState
}

func (fakeCheckpoints) ReferencedBlobKeys(context.Context, stack.ID) ([]blob.Key, error) {
	return nil, nil
}

// newTestBlobs opens a blob store in a fresh directory that the test closes
// at cleanup. The cancel path writes no checkpoint, so it stays empty.
func newTestBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// fakeEvents answers no events. The cancel path writes none.
type fakeEvents struct{}

func (fakeEvents) AppendEvents(context.Context, stack.EventBatch, namespace.Fence) error { return nil }

func (fakeEvents) ListEvents(context.Context, stack.Ref, stack.UpdateID, int64, int) ([]stack.Event, error) {
	return nil, nil
}

// fakeWorkspaces holds workspaces by ID.
type fakeWorkspaces struct {
	workspaces map[workspace.ID]workspace.Workspace
	// released holds the audit entry of each release of a lock.
	released []audit.Entry
}

func (*fakeWorkspaces) CreateWorkspace(context.Context, workspace.Workspace, namespace.Fence) error {
	return workspace.ErrAlreadyExists
}

func (*fakeWorkspaces) GetWorkspace(context.Context, workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrNotFound
}

func (f *fakeWorkspaces) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	w, ok := f.workspaces[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

func (f *fakeWorkspaces) ListWorkspaces(_ context.Context, _ namespace.Name) ([]workspace.Workspace, error) {
	out := make([]workspace.Workspace, 0, len(f.workspaces))
	for id := range f.workspaces {
		out = append(out, f.workspaces[id])
	}

	return out, nil
}

// QueryWorkspaces implements workspace.Store over the list of ListWorkspaces.
func (f *fakeWorkspaces) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := f.ListWorkspaces(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (f *fakeWorkspaces) SaveWorkspace(
	_ context.Context, w workspace.Workspace, _ namespace.Fence,
) error {
	stored, ok := f.workspaces[w.ID]
	if !ok {
		return workspace.ErrNotFound
	}

	if w.Revision != stored.Revision+1 {
		return workspace.ErrConcurrentModification
	}

	f.workspaces[w.ID] = w

	return nil
}

// DeleteWorkspace implements workspace.Store.
func (*fakeWorkspaces) DeleteWorkspace(context.Context, workspace.Ref, namespace.Fence, inventory.Deletion) error {
	return workspace.ErrNotFound
}

// RestoreWorkspace implements workspace.Store; this fake holds no deleted
// workspace.
func (*fakeWorkspaces) RestoreWorkspace(
	_ context.Context, _ workspace.ID, _ workspace.Name, _ namespace.Fence, _ audit.Entry,
) (workspace.Ref, error) {
	return workspace.Ref{}, workspace.ErrNotFound
}

// PurgeWorkspace implements workspace.Store; this fake holds no deleted
// workspace.
func (*fakeWorkspaces) PurgeWorkspace(
	_ context.Context,
	_ workspace.ID,
	_ namespace.Fence,
	_ time.Time,
	_ audit.Entry,
) error {
	return workspace.ErrNotFound
}

// fakeStates answers no state version. The force-unlock path reads none.
type fakeStates struct{}

func (fakeStates) CreateStateVersion(
	context.Context, workspace.StateVersion, namespace.Fence, int,
) error {
	return nil
}

func (fakeStates) GetStateVersion(context.Context, workspace.StateVersionID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
}

func (fakeStates) FinalizeStateVersion(context.Context, workspace.StateVersion, namespace.Fence) error {
	return nil
}

func (fakeStates) FinalizeStateVersionExpecting(
	context.Context, workspace.StateVersion, namespace.Fence, workspace.StateVersionID,
) error {
	return nil
}

func (fakeStates) FenceAndUnlock(
	context.Context, workspace.Workspace, workspace.StateVersion, workspace.StateVersionID, namespace.Fence,
	audit.Entry,
) error {
	return nil
}

func (fakeStates) CurrentStateVersion(context.Context, workspace.ID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrNoState
}

func (fakeStates) ListStateVersions(
	context.Context, workspace.ID, workspace.Page,
) ([]workspace.StateVersion, int, error) {
	return nil, 0, nil
}

func (fakeStates) CountPendingStateVersions(context.Context, workspace.ID) (int, error) {
	return 0, nil
}

func (fakeStates) ReferencedContentKeys(context.Context, workspace.ID) ([]blob.Key, error) {
	return nil, nil
}

func (fakeStates) RecordUploadAttempt(
	context.Context, workspace.StateVersionID, blob.Key, namespace.Fence,
) (blob.Key, error) {
	return "", nil
}

func (fakeStates) SaveSensitiveMarks(
	context.Context, workspace.StateVersionID, workspace.SensitiveMarks, namespace.Fence,
) error {
	return nil
}

func (fakeStates) DiscardStateVersion(context.Context, workspace.StateVersionID, namespace.Fence) error {
	return nil
}

func (fakeStates) DeleteExpiredPendingStateVersions(
	context.Context, workspace.ID, time.Time, namespace.Fence,
) ([]workspace.StateVersion, error) {
	return nil, nil
}

// fakeBlobs answers no content. The force-unlock path reads none.
type fakeBlobs struct{}

func (fakeBlobs) Put(context.Context, blob.Key, io.Reader) error { return nil }

func (fakeBlobs) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return nil, blob.ErrNotFound
}

func (fakeBlobs) Delete(context.Context, blob.Key) error { return nil }

// ListPage implements blob.Store: it cuts the page that req selects from the
// objects of list.
func (x fakeBlobs) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	if err := req.Validate(); err != nil {
		return blob.Page{}, err
	}

	objects, err := x.list(ctx, req.Prefix)
	if err != nil {
		return blob.Page{}, err
	}

	slices.SortFunc(objects, func(a, b blob.Object) int { return strings.Compare(a.Key.String(), b.Key.String()) })

	return blob.PageOf(objects, req), nil
}

func (fakeBlobs) Ping(context.Context) error { return nil }

func (fakeBlobs) list(context.Context, string) ([]blob.Object, error) { return nil, nil }

const (
	testRegion    = namespace.InstallationName("eu-west")
	testNamespace = namespace.Name("acme")
	// testNamespaceID is the stable ID of the namespace that fakeNamespaces serves.
	testNamespaceID = namespace.ID("ns-abcdefghijklmnopqrst")
)

// fixture wires a maintenance service over fakes with no active work. It
// keeps the domain services, so tests wire a second maintenance service over
// the same store.
type fixture struct {
	state        *fakeStateStore
	updates      *fakeUpdates
	workspaces   *fakeWorkspaces
	stacks       *fakeStacks
	namespaces   *namespace.Service
	stackSvc     *stack.Service
	updateSvc    *stack.Updates
	workspaceSvc *workspace.Service
	service      *maintenance.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	return newFixtureWithNamespaces(t, fakeNamespaces{})
}

func newFixtureWithNamespaces(t *testing.T, namespacesStore namespace.Store) *fixture {
	t.Helper()

	return newFixtureWith(t, namespacesStore, fakeStates{}, fakeBlobs{})
}

// newFixtureWith builds the fixture with the state and blob stores the test
// needs, so a test can hold a state version whose content it controls.
func newFixtureWith(
	t *testing.T, namespacesStore namespace.Store, states workspace.StateStore, blobs blob.Store,
) *fixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: namespacesStore, Now: nil, Installation: testRegion},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	f := &fixture{
		state:        &fakeStateStore{state: maintenance.StateNormal, onSet: nil, onSetIf: nil, setIfErr: nil},
		updates:      &fakeUpdates{updates: map[stack.UpdateID]stack.Update{}},
		workspaces:   &fakeWorkspaces{workspaces: map[workspace.ID]workspace.Workspace{}, released: nil},
		stacks:       &fakeStacks{stacks: nil},
		namespaces:   namespaces,
		stackSvc:     nil,
		updateSvc:    nil,
		workspaceSvc: nil,
		service:      nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: f.stacks, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            f.updates,
		Tokens:             nil,
		Checkpoints:        fakeCheckpoints{},
		Events:             fakeEvents{},
		Namespaces:         namespaces,
		Stacks:             f.stacks,
		Blobs:              newTestBlobs(t),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.workspaces,
		States:             states,
		Blobs:              blobs,
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             stubSealer{},
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	f.stackSvc = stacks
	f.updateSvc = updates
	f.workspaceSvc = workspaces

	f.service, err = maintenance.NewService(maintenance.Config{
		Store:      f.state,
		Namespaces: namespaces,
		Stacks:     stacks,
		Updates:    updates,
		Workspaces: workspaces,
		Hold:       nil,
	})
	if err != nil {
		t.Fatalf("maintenance service: %v", err)
	}

	return f
}

// mustRef parses acme/web/prod as the stack reference of the tests.
func mustRef(t *testing.T) stack.Ref {
	t.Helper()

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	return ref
}

// seedRunningUpdate registers a running mutating update on ref with the
// service's stack list.
func seedRunningUpdate(t *testing.T, f *fixture, ref stack.Ref) {
	t.Helper()

	addRunningUpdate(f, ref, "u1", "stk-test00000000000001")
}

// addRunningUpdate registers a running mutating update with id on ref with
// the service's stack list.
func addRunningUpdate(f *fixture, ref stack.Ref, id stack.UpdateID, stackID stack.ID) {
	f.stacks.stacks = append(f.stacks.stacks, stack.Stack{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Tags:        map[string]string{},
		Ref:         ref,
		ID:          stackID,
		Version:     0,
	})

	f.updates.updates[id] = stack.Update{
		TokenID:         "",
		JournalVersion:  0,
		CreatedAt:       time.Now(),
		StartedAt:       time.Now(),
		EndedAt:         time.Now(),
		Lease:           stack.Lease{ExpiresAt: time.Now().Add(time.Hour), Digest: token.Digest{}},
		ID:              id,
		Kind:            stack.KindUpdate,
		Status:          stack.StatusRunning,
		Ref:             ref,
		DryRun:          false,
		Version:         1,
		Revision:        2,
		CreatedBy:       "admin",
		RestoredFrom:    0,
		CheckpointBytes: 0,
		JobID:           "",
		GitHead:         "",
	}
}

// seedLockedWorkspace registers a locked workspace called name.
func seedLockedWorkspace(t *testing.T, f *fixture, name string) {
	t.Helper()

	parsed, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: testNamespaceID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Lock: workspace.Lock{
			LockedAt: time.Now(),
			ID:       "lock-1",
			Holder:   "admin",
			Reason:   "apply",
		},
		LastRelease: workspace.NoRelease(),
		ID:          "ws-1",
		Ref:         workspace.Ref{Namespace: testNamespace, Name: parsed},
		Revision:    2,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
	f.workspaces.workspaces[w.ID] = w
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: testRegion},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(
		stack.Config{Store: f.stacks, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer:             secrettest.NewMemorySealer(),
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Updates:            f.updates,
		Tokens:             nil,
		Checkpoints:        fakeCheckpoints{},
		Events:             fakeEvents{},
		Namespaces:         namespaces,
		Stacks:             f.stacks,
		Blobs:              newTestBlobs(t),
		Refusals:           stacktest.NewRefusals(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("update service: %v", err)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         f.workspaces,
		States:             fakeStates{},
		Blobs:              fakeBlobs{},
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             stubSealer{},
		Logger:             nil,
		Now:                nil,
		MaxStateBytes:      0,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("workspace service: %v", err)
	}

	complete := maintenance.Config{
		Store:      f.state,
		Namespaces: namespaces,
		Stacks:     stacks,
		Updates:    updates,
		Workspaces: workspaces,
		Hold:       nil,
	}

	cases := []struct {
		name string
		drop func(*maintenance.Config)
	}{
		{name: "store", drop: func(c *maintenance.Config) { c.Store = nil }},
		{name: "namespaces", drop: func(c *maintenance.Config) { c.Namespaces = nil }},
		{name: "stacks", drop: func(c *maintenance.Config) { c.Stacks = nil }},
		{name: "updates", drop: func(c *maintenance.Config) { c.Updates = nil }},
		{name: "workspaces", drop: func(c *maintenance.Config) { c.Workspaces = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := complete
			tc.drop(&cfg)

			if _, err := maintenance.NewService(cfg); !errors.Is(err, maintenance.ErrMissingDependency) {
				t.Fatalf("NewService without %s: err = %v, want ErrMissingDependency", tc.name, err)
			}
		})
	}
}

func TestEnterAndLeave(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get state = %q, %v, want normal without error", got, err)
	}

	if _, err := f.service.Enter(ctx, false); err != nil {
		t.Fatalf("enter: %v", err)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("get state = %q, %v, want maintenance without error", got, err)
	}

	if res, err := f.service.Enter(ctx, false); err != nil || len(res.Canceled) != 0 {
		t.Fatalf("enter again = %+v, %v, want no names without error", res, err)
	}

	if err := f.service.Leave(ctx); err != nil {
		t.Fatalf("leave: %v", err)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("get state = %q, %v, want normal without error", got, err)
	}

	if err := f.service.Leave(ctx); err != nil {
		t.Fatalf("leave again: %v", err)
	}
}

func TestLeaveRefusedWhileHeld(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	var (
		mu      sync.Mutex
		held    = true
		holdErr error
	)

	svc, err := maintenance.NewService(maintenance.Config{
		Store:      f.state,
		Namespaces: f.namespaces,
		Stacks:     f.stackSvc,
		Updates:    f.updateSvc,
		Workspaces: f.workspaceSvc,
		Hold: func(context.Context) (bool, error) {
			mu.Lock()
			defer mu.Unlock()

			return held, holdErr
		},
	})
	if err != nil {
		t.Fatalf("maintenance service: %v", err)
	}

	if err := svc.Leave(ctx); err != nil {
		t.Fatalf("leave in normal operation: %v, want nil: a hold keeps only maintenance mode", err)
	}

	if _, err := svc.Enter(ctx, false); err != nil {
		t.Fatalf("enter: %v", err)
	}

	if err := svc.Leave(ctx); !errors.Is(err, maintenance.ErrHeld) {
		t.Fatalf("leave while held: %v, want ErrHeld", err)
	}

	mu.Lock()
	held, holdErr = false, errTestHold
	mu.Unlock()

	if err := svc.Leave(ctx); !errors.Is(err, errTestHold) {
		t.Fatalf("leave with a failed hold: %v, want the hold error", err)
	}

	if got, err := svc.Get(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after refused leaves = %q, %v, want maintenance", got, err)
	}

	mu.Lock()
	holdErr = nil
	mu.Unlock()

	if err := svc.Leave(ctx); err != nil {
		t.Fatalf("leave after the hold ends: %v", err)
	}
}

// errTestHold is the failure of the hold in TestLeaveRefusedWhileHeld.
var errTestHold = errors.New("hold unavailable")

func TestEnterRefusesRunningUpdate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	ref := mustRef(t)
	seedRunningUpdate(t, f, ref)

	_, err := f.service.Enter(ctx, false)
	if !errors.Is(err, maintenance.ErrActiveWork) {
		t.Fatalf("enter with a running update: err = %v, want ErrActiveWork", err)
	}

	if !strings.Contains(err.Error(), "pulumi:acme/web/prod") {
		t.Fatalf("enter with a running update: err = %v, want it to name pulumi:acme/web/prod", err)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("state after a refused enter = %q, %v, want normal without error", got, err)
	}
}

func TestEnterForceCancelsRunningUpdate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	ref := mustRef(t)
	seedRunningUpdate(t, f, ref)

	res, err := f.service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("force enter: %v", err)
	}

	if len(res.Canceled) != 1 || res.Canceled[0] != "pulumi:acme/web/prod" {
		t.Fatalf("force enter canceled = %v, want [pulumi:acme/web/prod]", res.Canceled)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after a forced enter = %q, %v, want maintenance without error", got, err)
	}

	u := f.updates.updates["u1"]
	if u.Status != stack.StatusCanceled {
		//nolint:misspell // The Pulumi CLI's wire value; it must not change.
		t.Fatalf("canceled update status = %q, want cancelled", u.Status)
	}
}

func TestEnterRefusesLockedWorkspace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	seedLockedWorkspace(t, f, "network")

	_, err := f.service.Enter(ctx, false)
	if !errors.Is(err, maintenance.ErrActiveWork) {
		t.Fatalf("enter with a locked workspace: err = %v, want ErrActiveWork", err)
	}

	if !strings.Contains(err.Error(), "opentofu:acme/network") {
		t.Fatalf("enter with a locked workspace: err = %v, want it to name opentofu:acme/network", err)
	}
}

// encryptedStates holds one finalized version, as the state store of a workspace
// whose state the client encrypted.
type encryptedStates struct {
	fakeStates

	version workspace.StateVersion
}

func (s encryptedStates) CurrentStateVersion(context.Context, workspace.ID) (workspace.StateVersion, error) {
	return s.version, nil
}

func (s encryptedStates) GetStateVersion(context.Context, workspace.StateVersionID) (workspace.StateVersion, error) {
	return s.version, nil
}

// encryptedBlobs returns a client-encrypted state document for any key.
type encryptedBlobs struct {
	fakeBlobs
}

func (encryptedBlobs) Get(context.Context, blob.Key) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(`{"meta":{},"encrypted_data":"c2VjcmV0","encryption_version":"v0"}`)), nil
}

// TestEnterForceLeavesClientEncryptedWorkspaceLocked checks that a force enter
// skips a workspace whose state the client encrypted. The server cannot fence
// that state, so the lock stays, the unit is not reported as released, and the
// enter still succeeds.
func TestEnterForceLeavesClientEncryptedWorkspaceLocked(t *testing.T) {
	t.Parallel()

	states := encryptedStates{
		fakeStates: fakeStates{},
		version: workspace.StateVersion{
			CreatedAt:       time.Time{},
			FinalizedAt:     time.Time{},
			UploadExpiresAt: time.Time{},
			Upload:          token.Digest{},
			ID:              "sv-1",
			Workspace:       "ws-1",
			Holder:          "",
			LockID:          "",
			Lineage:         "l1",
			Status:          workspace.StatusFinalized,
			Content:         blob.Key("namespaces/" + testNamespaceID.String() + "/tofu/ws-1/sv-1/a1"),
			Outputs:         nil,
			// The size and MD5 of the document encryptedBlobs serves,
			// as a stored row names them.
			MD5: [16]byte{
				0x64, 0x20, 0xd9, 0x71, 0x90, 0x04, 0x23, 0xf8,
				0x43, 0x33, 0x65, 0x89, 0xb9, 0xb1, 0x95, 0x80,
			},
			Serial:         1,
			Size:           65,
			Force:          false,
			Sealed:         false,
			RestoredFrom:   "",
			SensitiveMarks: nil,
			FenceBy:        "",
			UnlockedWrite:  false, Origin: identity.StateOrigin{Job: "", Commit: ""},
		},
	}

	f := newFixtureWith(t, fakeNamespaces{}, states, encryptedBlobs{fakeBlobs: fakeBlobs{}})
	ctx := t.Context()
	seedLockedWorkspace(t, f, "network")

	res, err := f.service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("force enter: %v", err)
	}

	if len(res.Canceled) != 0 {
		t.Fatalf("force enter canceled = %v, want nothing: the encrypted workspace stays locked", res.Canceled)
	}

	if w := f.workspaces.workspaces["ws-1"]; w.Lock.Holder != "admin" {
		t.Fatalf("lock holder = %q, want admin: the lock stays", w.Lock.Holder)
	}
}

func TestEnterForceReleasesLockedWorkspace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	seedLockedWorkspace(t, f, "network")

	res, err := f.service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("force enter: %v", err)
	}

	if len(res.Canceled) != 1 || res.Canceled[0] != "opentofu:acme/network" {
		t.Fatalf("force enter canceled = %v, want [opentofu:acme/network]", res.Canceled)
	}

	if w := f.workspaces.workspaces["ws-1"]; w.Lock.Holder != "" {
		t.Fatalf("workspace lock holder = %q, want empty after force enter", w.Lock.Holder)
	}

	// The release is a force-unlock like any other: it writes one change entry
	// that names the unit, the principal and the reason.
	if len(f.workspaces.released) != 1 {
		t.Fatalf("release entries = %+v, want one", f.workspaces.released)
	}

	got := f.workspaces.released[0]
	if got.Kind != audit.KindChange || got.Subject != "maintenance" || got.Object != "opentofu:acme/network" ||
		got.Action != "unit:write-forced" || got.Reason == "" {
		t.Fatalf("release entry = %+v, want a change by maintenance on the unit with a reason", got)
	}

	if err := got.Validate(); err != nil {
		t.Fatalf("release entry fails validation: %v", err)
	}
}

// errTestUnavailable reports a failed state read or write in tests.
var errTestUnavailable = errors.New("test database unavailable")

// errStore fails every read, so the watcher proves its fallback.
type errStore struct{}

func (errStore) GetState(context.Context) (maintenance.State, error) {
	return "", errTestUnavailable
}

func (errStore) SetState(context.Context, maintenance.State) error {
	return errTestUnavailable
}

func (errStore) SetStateIf(context.Context, maintenance.State, maintenance.State) error {
	return errTestUnavailable
}

// countStore counts its reads, so the watcher proves its cache limit.
type countStore struct {
	state maintenance.State
	reads int
}

func (s *countStore) GetState(context.Context) (maintenance.State, error) {
	s.reads++

	return s.state, nil
}

func (s *countStore) SetState(_ context.Context, state maintenance.State) error {
	s.state = state

	return nil
}

func (s *countStore) SetStateIf(_ context.Context, expected, next maintenance.State) error {
	if s.state != expected {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	s.state = next

	return nil
}

func TestWatcherCachesWithinLimit(t *testing.T) {
	t.Parallel()

	now := time.Now()
	store := &countStore{state: maintenance.StateNormal, reads: 0}
	watcher := maintenance.NewWatcher(store, func() time.Time { return now }, time.Minute)

	if got := watcher.State(t.Context()); got != maintenance.StateNormal {
		t.Fatalf("watcher state = %q, want normal", got)
	}

	store.state = maintenance.StateMaintenance

	if got := watcher.State(t.Context()); got != maintenance.StateNormal {
		t.Fatalf("watcher state within the limit = %q, want the cached normal", got)
	}

	if store.reads != 1 {
		t.Fatalf("watcher reads = %d, want 1 within the limit", store.reads)
	}

	now = now.Add(2 * time.Minute)

	if got := watcher.State(t.Context()); got != maintenance.StateMaintenance {
		t.Fatalf("watcher state past the limit = %q, want maintenance", got)
	}

	if store.reads != 2 {
		t.Fatalf("watcher reads = %d, want 2 past the limit", store.reads)
	}
}

// flakyStore answers state, or errTestUnavailable while fail is set.
type flakyStore struct {
	state maintenance.State
	fail  bool
}

func (s *flakyStore) GetState(context.Context) (maintenance.State, error) {
	if s.fail {
		return "", errTestUnavailable
	}

	return s.state, nil
}

func (s *flakyStore) SetState(_ context.Context, state maintenance.State) error {
	s.state = state

	return nil
}

func (s *flakyStore) SetStateIf(_ context.Context, _, next maintenance.State) error {
	s.state = next

	return nil
}

// TestWatcherFailsClosedBeforeTheFirstRead proves that a watcher whose
// reads all failed answers maintenance, and that after a good read a
// failed read keeps the last state it read.
func TestWatcherFailsClosedBeforeTheFirstRead(t *testing.T) {
	t.Parallel()

	watcher := maintenance.NewWatcher(errStore{}, nil, 0)
	if got := watcher.State(t.Context()); got != maintenance.StateMaintenance {
		t.Fatalf("watcher state with no good read = %q, want maintenance", got)
	}

	for _, last := range []maintenance.State{maintenance.StateNormal, maintenance.StateMaintenance} {
		now := time.Unix(0, 0)
		store := &flakyStore{state: last, fail: true}
		flaky := maintenance.NewWatcher(store, func() time.Time { return now }, time.Minute)

		if got := flaky.State(t.Context()); got != maintenance.StateMaintenance {
			t.Fatalf("flaky watcher state before a good read = %q, want maintenance", got)
		}

		store.fail = false

		if got := flaky.State(t.Context()); got != last {
			t.Fatalf("flaky watcher state after a good read = %q, want %q", got, last)
		}

		store.fail = true
		now = now.Add(2 * time.Minute)

		if got := flaky.State(t.Context()); got != last {
			t.Fatalf("flaky watcher state after a failed read = %q, want the last read %q", got, last)
		}
	}
}

// TestConcurrentForceEnterCancelsOnce proves concurrent enters run one after
// another: exactly one names the running update, the rest answer empty, and
// the installation ends in maintenance mode.
func TestConcurrentForceEnterCancelsOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	seedRunningUpdate(t, f, mustRef(t))

	const enters = 8

	var mu sync.Mutex

	named := 0

	var wg sync.WaitGroup

	for range enters {
		wg.Go(func() {
			res, err := f.service.Enter(ctx, true)
			if err != nil {
				t.Errorf("concurrent force enter: %v", err)

				return
			}

			mu.Lock()
			defer mu.Unlock()

			if len(res.Canceled) == 1 && res.Canceled[0] == "pulumi:acme/web/prod" {
				named++
			} else if len(res.Canceled) != 0 {
				t.Errorf("concurrent force enter canceled = %v, want empty or [pulumi:acme/web/prod]", res.Canceled)
			}
		})
	}

	wg.Wait()

	if named != 1 {
		t.Fatalf("enters naming the update = %d, want 1", named)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after concurrent enters = %q, %v, want maintenance without error", got, err)
	}
}

// TestEnterCatchesRacedStart proves the re-check after the write: an update
// that starts between the enter check and its write is refused without
// force, and the installation moves back to normal.
func TestEnterCatchesRacedStart(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	ref := mustRef(t)

	f.state.onSetIf = func() {
		if f.state.state == maintenance.StateNormal {
			seedRunningUpdate(t, f, ref)
		}
	}

	_, err := f.service.Enter(ctx, false)
	if !errors.Is(err, maintenance.ErrActiveWork) {
		t.Fatalf("enter with a raced start: err = %v, want ErrActiveWork", err)
	}

	if !strings.Contains(err.Error(), "pulumi:acme/web/prod") {
		t.Fatalf("enter with a raced start: err = %v, want it to name pulumi:acme/web/prod", err)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateNormal {
		t.Fatalf("state after a raced refused enter = %q, %v, want normal without error", got, err)
	}
}

// TestForceEnterRetriesRacedStart proves the re-check after the write with
// force: maintenance mode refuses the cancel of an update that starts between
// the enter check and the write, so the attempt moves back to normal. The
// forced enter tries again, cancels the new update before the write, and
// answers both the update of the first attempt and the new one.
func TestForceEnterRetriesRacedStart(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	seedRunningUpdate(t, f, mustRef(t))

	other, err := stack.ParseRef("acme", "web", "staging")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	raced := true

	f.state.onSetIf = func() {
		if raced && f.state.state == maintenance.StateNormal {
			raced = false

			addRunningUpdate(f, other, "u2", "stk-test00000000000002")
		}
	}

	res, err := f.service.Enter(ctx, true)
	if err != nil {
		t.Fatalf("force enter with a raced start: %v", err)
	}

	want := []string{"pulumi:acme/web/prod", "pulumi:acme/web/staging"}
	if !slices.Equal(res.Canceled, want) {
		t.Fatalf("force enter canceled = %v, want %v", res.Canceled, want)
	}

	assertState(t, f, maintenance.StateMaintenance)
}

// TestForceEnterGivesUpAfterBoundedRetries proves that a writer which keeps
// starting work cannot hold a forced enter in a loop: after the bounded
// attempts the enter answers ErrActiveWork, leaves the state normal, and the
// error names the work that blocked it and every unit that the attempts
// canceled.
func TestForceEnterGivesUpAfterBoundedRetries(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	seedRunningUpdate(t, f, mustRef(t))

	starts := 0

	f.state.onSetIf = func() {
		if f.state.state != maintenance.StateNormal {
			return
		}

		starts++

		name := fmt.Sprintf("racer%d", starts)

		ref, err := stack.ParseRef("acme", "web", name)
		if err != nil {
			t.Errorf("parse stack reference: %v", err)

			return
		}

		addRunningUpdate(
			f,
			ref,
			stack.UpdateID(fmt.Sprintf("u%d", starts+1)),
			stack.ID(fmt.Sprintf("stk-test%014d", starts+1)),
		)
	}

	_, err := f.service.Enter(ctx, true)
	if !errors.Is(err, maintenance.ErrActiveWork) {
		t.Fatalf("force enter against a steady writer: err = %v, want ErrActiveWork", err)
	}

	var blocked *maintenance.ActiveWorkError
	if !errors.As(err, &blocked) {
		t.Fatalf("force enter error %v is not an *ActiveWorkError", err)
	}

	if len(blocked.Work) != 1 || blocked.Work[0] != "pulumi:acme/web/racer3" {
		t.Fatalf("blocking work = %v, want [pulumi:acme/web/racer3]", blocked.Work)
	}

	wantCanceled := []string{"pulumi:acme/web/prod", "pulumi:acme/web/racer1", "pulumi:acme/web/racer2"}
	if !slices.Equal(blocked.Canceled, wantCanceled) {
		t.Fatalf("canceled work = %v, want %v", blocked.Canceled, wantCanceled)
	}

	assertState(t, f, maintenance.StateNormal)
}

// TestNonForceEnterReportsNoCanceledWork proves that a plain enter that is
// refused names the blocking work and no canceled work.
func TestNonForceEnterReportsNoCanceledWork(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	seedRunningUpdate(t, f, mustRef(t))

	_, err := f.service.Enter(t.Context(), false)

	var blocked *maintenance.ActiveWorkError
	if !errors.As(err, &blocked) || len(blocked.Canceled) != 0 {
		t.Fatalf("enter without force: err = %v, want an *ActiveWorkError with no canceled work", err)
	}
}

// assertState fails the test when the stored state is not want.
func assertState(t *testing.T, f *fixture, want maintenance.State) {
	t.Helper()

	if got, err := f.service.Get(t.Context()); err != nil || got != want {
		t.Fatalf("state = %q, %v, want %q without error", got, err, want)
	}
}

// gateNamespaces blocks ListNamespaces until released, so one enter stays
// inside its critical section while a leave starts.
type gateNamespaces struct {
	fakeNamespaces

	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func (g *gateNamespaces) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	g.enterOnce.Do(func() { close(g.entered) })

	select {
	case <-g.release:
		return g.fakeNamespaces.ListNamespaces(ctx)
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for test release: %w", ctx.Err())
	}
}

// overlapState holds one caller's result and the state it reads back after
// both overlapping calls finished.
type overlapState struct {
	callErr error
	back    maintenance.State
	readErr error
}

// overlapReport runs op, waits for the other caller past both, then reads
// the installation state back, so the test compares what each caller
// observed after both calls finished.
func overlapReport(
	ctx context.Context,
	both *sync.WaitGroup,
	get func(context.Context) (maintenance.State, error),
	op func(context.Context) error,
	out chan<- overlapState,
) {
	var r overlapState

	r.callErr = op(ctx)

	both.Done()
	both.Wait()

	if r.callErr == nil {
		r.back, r.readErr = get(ctx)
	}

	out <- r
}

// TestOverlappingEnterAndLeaveSerializes proves Enter and Leave share one
// critical section: a leave that starts while an enter runs waits for it,
// and each caller then reports the final state.
func TestOverlappingEnterAndLeaveSerializes(t *testing.T) {
	t.Parallel()

	gate := &gateNamespaces{
		fakeNamespaces: fakeNamespaces{},
		enterOnce:      sync.Once{},
		entered:        make(chan struct{}),
		release:        make(chan struct{}),
	}
	f := newFixtureWithNamespaces(t, gate)
	ctx := t.Context()

	var both sync.WaitGroup

	both.Add(2)

	enterReport := make(chan overlapState, 1)
	leaveReport := make(chan overlapState, 1)

	go overlapReport(ctx, &both, f.service.Get, func(ctx context.Context) error {
		_, err := f.service.Enter(ctx, false)

		return err
	}, enterReport)

	// Wait until the enter holds the critical section, then start the leave.
	<-gate.entered

	go overlapReport(ctx, &both, f.service.Get, f.service.Leave, leaveReport)

	// The leave must wait for the enter: it answers only after the release.
	select {
	case r := <-leaveReport:
		t.Fatalf("leave answered %+v while an enter held the critical section", r)
	case <-time.After(100 * time.Millisecond):
	}

	close(gate.release)

	enter := <-enterReport
	leave := <-leaveReport

	if enter.callErr != nil {
		t.Fatalf("enter: %v", enter.callErr)
	}

	if leave.callErr != nil {
		t.Fatalf("leave: %v", leave.callErr)
	}

	if enter.readErr != nil {
		t.Fatalf("read back after enter: %v", enter.readErr)
	}

	if leave.readErr != nil {
		t.Fatalf("read back after leave: %v", leave.readErr)
	}

	final, err := f.service.Get(ctx)
	if err != nil {
		t.Fatalf("get final state: %v", err)
	}

	// The leave ran second, so every read reports normal.
	if enter.back != maintenance.StateNormal || leave.back != maintenance.StateNormal ||
		final != maintenance.StateNormal {
		t.Fatalf(
			"enter read %q, leave read %q, final %q, want all normal",
			enter.back,
			leave.back,
			final,
		)
	}
}

// TestTwoInstancesKeepForceEnter proves a non-force enter never undoes
// another instance's force enter on one store: the force enter lands between
// the first enter's check and its write, so the first enter's compare-and-set
// loses, it rolls nothing back, and the installation stays in maintenance
// mode. Without the compare-and-set the first enter rolls back to normal and
// loses the force enter.
func TestTwoInstancesKeepForceEnter(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	second, err := maintenance.NewService(maintenance.Config{
		Store:      f.state,
		Namespaces: f.namespaces,
		Stacks:     f.stackSvc,
		Updates:    f.updateSvc,
		Workspaces: f.workspaceSvc,
		Hold:       nil,
	})
	if err != nil {
		t.Fatalf("second maintenance service: %v", err)
	}

	ref := mustRef(t)

	other, err := stack.ParseRef("acme", "web", "staging")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	var (
		forced    maintenance.EnterResult
		forcedErr error
		hooked    bool
	)

	// The hook runs the second instance's force enter between the first
	// enter's check and its write, then starts one more update after the
	// force enter finished. It covers both write paths: the conditional
	// write runs it before its expectation check, the plain write before it
	// assigns, so the test fails before the compare-and-set and passes with
	// it. The guard keeps the nested force enter from running it again.
	hook := func() {
		if hooked {
			return
		}

		hooked = true

		addRunningUpdate(f, ref, "u1", "stk-test00000000000001")

		forced, forcedErr = second.Enter(ctx, true)

		addRunningUpdate(f, other, "u2", "stk-test00000000000002")
	}
	f.state.onSet = hook
	f.state.onSetIf = hook

	res, err := f.service.Enter(ctx, false)
	if err != nil {
		t.Fatalf("first enter: %v", err)
	}

	if forcedErr != nil {
		t.Fatalf("force enter of the second instance: %v", forcedErr)
	}

	if len(forced.Canceled) != 1 || forced.Canceled[0] != "pulumi:acme/web/prod" {
		t.Fatalf("force enter canceled = %v, want [pulumi:acme/web/prod]", forced.Canceled)
	}

	if len(res.Canceled) != 0 {
		t.Fatalf("first enter canceled = %v, want empty: it lost the compare-and-set", res.Canceled)
	}

	if got, err := f.service.Get(ctx); err != nil || got != maintenance.StateMaintenance {
		t.Fatalf("state after the two enters = %q, %v, want maintenance without error", got, err)
	}

	if u := f.updates.updates["u2"]; u.Status != stack.StatusRunning {
		t.Fatalf("second raced update status = %q, want running: no rollback canceled it", u.Status)
	}
}

// SetOverrides is not used by this fake.
func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// ReleaseUnfenced is not used by this fake: it saves like SaveWorkspace.
func (f *fakeWorkspaces) ReleaseUnfenced(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, _ audit.Entry,
) error {
	return f.SaveWorkspace(ctx, w, fence)
}

// SaveReleased saves like SaveWorkspace and keeps the audit entry of the release.
func (f *fakeWorkspaces) SaveReleased(
	ctx context.Context, w workspace.Workspace, fence namespace.Fence, e audit.Entry,
) error {
	f.released = append(f.released, e)

	return f.SaveWorkspace(ctx, w, fence)
}

// TestForceEnterNamesCanceledWorkOnOtherFailures proves that a forced enter
// which canceled work and then failed on an error other than ErrActiveWork
// still names what it canceled, and that the error keeps its class: the
// cancel stays, so the caller must be able to tell the operator what stopped.
func TestForceEnterNamesCanceledWorkOnOtherFailures(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	seedRunningUpdate(t, f, mustRef(t))

	f.state.setIfErr = maintenance.ErrUnavailable

	_, err := f.service.Enter(t.Context(), true)
	if !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("force enter against a failing store: err = %v, want ErrUnavailable", err)
	}

	var failed *maintenance.CanceledError
	if !errors.As(err, &failed) {
		t.Fatalf("force enter error %v is not a *CanceledError", err)
	}

	if want := []string{"pulumi:acme/web/prod"}; !slices.Equal(failed.Canceled, want) {
		t.Fatalf("canceled work = %v, want %v", failed.Canceled, want)
	}

	if !strings.Contains(err.Error(), "pulumi:acme/web/prod") {
		t.Fatalf("error text %q does not name the canceled unit", err.Error())
	}

	assertState(t, f, maintenance.StateNormal)
}

// TestPlainEnterFailureCarriesNoCanceledWork proves that a plain enter which
// fails has canceled nothing and returns the bare error.
func TestPlainEnterFailureCarriesNoCanceledWork(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.state.setIfErr = maintenance.ErrUnavailable

	_, err := f.service.Enter(t.Context(), false)

	var failed *maintenance.CanceledError
	if !errors.Is(err, maintenance.ErrUnavailable) || errors.As(err, &failed) {
		t.Fatalf("plain enter against a failing store: err = %v, want a bare ErrUnavailable", err)
	}
}
