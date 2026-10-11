package storetest

import (
	"bytes"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/inventory"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/workspace"
)

// WorkspaceStore is every persistence port the workspace suite exercises:
// namespaces for fixtures, workspace.Store and workspace.StateStore under
// test, and inventory.Reader for the publish contract.
type WorkspaceStore interface {
	namespace.Store
	workspace.Store
	workspace.StateStore
	inventory.Reader
}

// WorkspaceFactory returns a fresh, empty store for one subtest.
type WorkspaceFactory func(t *testing.T) WorkspaceStore

// RunWorkspace executes the conformance suite of the workspace ports
// (workspace.Store, workspace.StateStore) against stores produced by newStore.
func RunWorkspace(t *testing.T, newStore WorkspaceFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s WorkspaceStore)
		name string
	}{
		{name: "create and read by name and by ID", test: testWorkspaceRoundTrip},
		{name: "duplicate workspace name is ErrAlreadyExists", test: testDuplicateWorkspace},
		{name: "create in a missing namespace is namespace.ErrNotFound", test: testWorkspaceMissingNamespace},
		{name: "missing workspace is ErrNotFound", test: testMissingWorkspace},
		{name: "list orders by name and is empty for an unknown namespace", test: testListWorkspaces},
		{name: "stale fence on every write is ErrFenced and writes nothing", test: testWorkspaceWritesFenced},
		{name: "save with a wrong revision is ErrConcurrentModification", test: testSaveWrongRevision},
		{name: "save of a missing workspace is ErrNotFound", test: testSaveMissingWorkspace},
		{name: "save persists a lock and an unlock by the holder", test: testLockRoundTrip},
		{name: "finalize of a workspace that is not locked is ErrNotLocked", test: testFinalizeWithoutLock},
		{name: "finalize under a lock held by another principal is ErrLockedByOther", test: testFinalizeLockedByOther},
		{name: "a pending state version reads back and is not current yet", test: testPendingRoundTrip},
		{name: "create of a state version for a missing workspace is ErrNotFound", test: testCreateStateVersionMissing},
		{name: "finalize makes the highest finalized serial current", test: testFinalizeMakesCurrent},
		{
			name: "a retried finalize of one version is ErrUploadDenied and creates no second version",
			test: testFinalizeRetry,
		},
		{name: "finalize with another lineage is ErrLineageMismatch", test: testFinalizeLineageMismatch},
		{name: "finalize with a stale serial is ErrStaleSerial", test: testFinalizeStaleSerial},
		{name: "force skips the lineage and serial checks but not the lock check", test: testFinalizeForce},
		{name: "a fenced finalize leaves the old state current", test: testFencedFinalizeKeepsCurrent},
		{
			name: "create publishes an inventory entry for opentofu:namespace/name",
			test: testWorkspaceInventoryAfterCreate,
		},
		{name: "lock, unlock and finalize move the inventory entry", test: testInventoryFollowsWrites},
		{name: "a failed write leaves the inventory unchanged", test: testWorkspaceInventoryFailedWrite},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func newWorkspaceNamespace(t *testing.T, s WorkspaceStore, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       now(),
		Name:            namespace.Name(name),
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

func mustWorkspaceName(t *testing.T, name string) workspace.Name {
	t.Helper()

	n, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", name, err)
	}

	return n
}

func newTestWorkspace(ns namespace.Namespace, name workspace.Name) workspace.Workspace {
	return workspace.Workspace{
		CreatedAt: now(),
		UpdatedAt: now(),
		Lock:      clearedLock(),
		ID:        workspace.ID(ident.NewID("ws-")),
		Ref:       workspace.Ref{Namespace: ns.Name, Name: name},
		Revision:  1,
	}
}

func createTestWorkspace(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, name string,
) workspace.Workspace {
	t.Helper()

	w := newTestWorkspace(ns, mustWorkspaceName(t, name))

	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %s: %v", w.Ref, err)
	}

	return w
}

// lockTestWorkspace saves w holding holder's lock and returns it.
func lockTestWorkspace(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace, holder string,
) workspace.Workspace {
	t.Helper()

	locked := w
	locked.Lock = workspace.Lock{
		LockedAt: now(),
		ID:       "lock-" + holder,
		Holder:   holder,
		Reason:   "apply",
	}
	locked.UpdatedAt = now().Add(time.Hour)
	locked.Revision++

	if err := s.SaveWorkspace(t.Context(), locked, ns.Fence()); err != nil {
		t.Fatalf("save locked workspace %s: %v", w.Ref, err)
	}

	return locked
}

// pendingInput is the content a test declares before uploading it.
type pendingInput struct {
	lineage string
	content string
	serial  int64
	force   bool
}

// stateInput declares content with lineage and serial.
func stateInput(lineage, content string, serial int64) pendingInput {
	return pendingInput{lineage: lineage, content: content, serial: serial, force: false}
}

// forcedInput declares content that skips the lineage and serial checks.
func forcedInput(lineage, content string, serial int64) pendingInput {
	return pendingInput{lineage: lineage, content: content, serial: serial, force: true}
}

// clearedLock is the zero Lock: an unlocked workspace.
func clearedLock() workspace.Lock {
	return workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
}

func newPendingVersion(
	t *testing.T, w workspace.Workspace, lockID string, in pendingInput,
) workspace.StateVersion {
	t.Helper()

	tok, err := token.New()
	if err != nil {
		t.Fatalf("new upload capability: %v", err)
	}

	sum := md5.Sum([]byte(in.content)) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	return workspace.StateVersion{
		CreatedAt:       now(),
		FinalizedAt:     time.Time{},
		UploadExpiresAt: now().Add(workspace.UploadWindow),
		Upload:          tok.Digest(),
		ID:              workspace.StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          w.Lock.Holder,
		LockID:          lockID,
		Lineage:         in.lineage,
		Status:          workspace.StatusPending,
		Content:         "",
		Outputs:         nil,
		MD5:             sum,
		Serial:          in.serial,
		Size:            0,
		Force:           in.force,
	}
}

func createPendingVersion(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, sv workspace.StateVersion,
) workspace.StateVersion {
	t.Helper()

	if err := s.CreateStateVersion(t.Context(), sv, ns.Fence()); err != nil {
		t.Fatalf("create state version %s: %v", sv.ID, err)
	}

	return sv
}

// finalizedVersion returns sv ready to finalize at at: each attempt writes its
// own blob key, so a retry reuses the same key and content size.
func finalizedVersion(sv workspace.StateVersion, at time.Time, content string) workspace.StateVersion {
	sv.Status = workspace.StatusFinalized
	sv.FinalizedAt = at
	sv.Content = blob.Key("tofu/" + sv.Workspace.String() + "/" + sv.ID.String() + "/a1")
	sv.Size = int64(len(content))

	return sv
}

func assertWorkspace(t *testing.T, got, want workspace.Workspace) {
	t.Helper()

	if got.Ref != want.Ref || got.ID != want.ID || got.Revision != want.Revision ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		got.Lock.ID != want.Lock.ID || got.Lock.Holder != want.Lock.Holder ||
		got.Lock.Reason != want.Lock.Reason || !got.Lock.LockedAt.Equal(want.Lock.LockedAt) {
		t.Fatalf("workspace = %+v, want %+v", got, want)
	}
}

func sameStateReference(got, want workspace.StateVersion) bool {
	return got.ID == want.ID && got.Workspace == want.Workspace && got.Holder == want.Holder &&
		got.LockID == want.LockID && got.Lineage == want.Lineage && got.Status == want.Status
}

func sameStateContent(got, want workspace.StateVersion) bool {
	return got.Content == want.Content && bytes.Equal(got.Outputs, want.Outputs) &&
		got.MD5 == want.MD5 && got.Serial == want.Serial && got.Size == want.Size &&
		got.Force == want.Force && got.Upload == want.Upload
}

func sameStateTimes(got, want workspace.StateVersion) bool {
	return got.CreatedAt.Equal(want.CreatedAt) && got.FinalizedAt.Equal(want.FinalizedAt) &&
		got.UploadExpiresAt.Equal(want.UploadExpiresAt)
}

func assertStateVersion(t *testing.T, got, want workspace.StateVersion) {
	t.Helper()

	if !sameStateReference(got, want) || !sameStateContent(got, want) || !sameStateTimes(got, want) {
		t.Fatalf("state version = %+v, want %+v", got, want)
	}
}

// assertWorkspaceEntry checks one entry against its workspace and change time.
func assertWorkspaceEntry(t *testing.T, got inventory.Entry, w workspace.Workspace, changed time.Time) {
	t.Helper()

	name := w.Ref.Name.String()
	wantRef := "opentofu:" + w.Ref.Namespace.String() + "/" + name

	if got.Tool != "opentofu" || got.Namespace != w.Ref.Namespace || got.Name != name ||
		got.UnitID != w.ID.String() || got.DisplayName != name ||
		!got.LastChanged.Equal(changed) || got.Status != "" || got.ResourceCount != nil ||
		got.Reference() != wantRef {
		t.Fatalf("inventory entry = %+v, want opentofu unit %q changed %v", got, wantRef, changed)
	}
}

func listWorkspaceInventory(t *testing.T, s WorkspaceStore, ns namespace.Name) []inventory.Entry {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list inventory of %q: %v", ns, err)
	}

	return entries
}

func testWorkspaceRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	want := createTestWorkspace(t, s, ns, "network")

	byName, err := s.GetWorkspace(t.Context(), want.Ref)
	if err != nil {
		t.Fatalf("get workspace %s: %v", want.Ref, err)
	}

	assertWorkspace(t, byName, want)

	byID, err := s.GetWorkspaceByID(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get workspace %s: %v", want.ID, err)
	}

	assertWorkspace(t, byID, want)
}

func testDuplicateWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	first := createTestWorkspace(t, s, ns, "network")
	second := newTestWorkspace(ns, first.Ref.Name)

	if err := s.CreateWorkspace(t.Context(), second, ns.Fence()); !errors.Is(err, workspace.ErrAlreadyExists) {
		t.Fatalf("create duplicate workspace: err = %v, want ErrAlreadyExists", err)
	}

	got, err := s.GetWorkspace(t.Context(), first.Ref)
	if err != nil {
		t.Fatalf("get workspace after a refused duplicate: %v", err)
	}

	assertWorkspace(t, got, first)
}

func testWorkspaceMissingNamespace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ghost := namespace.Namespace{CreatedAt: now(), Name: "ghost", HomeRegion: "eu-west", FailoverVersion: 1}
	w := newTestWorkspace(ghost, mustWorkspaceName(t, "network"))

	if err := s.CreateWorkspace(t.Context(), w, ghost.Fence()); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("create workspace in a missing namespace: err = %v, want namespace.ErrNotFound", err)
	}

	sv := newPendingVersion(t, w, "lock-alice", stateInput("l1", "v1", 1))

	if err := s.CreateStateVersion(t.Context(), sv, ghost.Fence()); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("create state version in a missing namespace: err = %v, want namespace.ErrNotFound", err)
	}
}

func testMissingWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	ref := workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, "ghost")}

	if _, err := s.GetWorkspace(t.Context(), ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get missing workspace: err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetWorkspaceByID(t.Context(), workspace.ID(ident.NewID("ws-"))); !errors.Is(
		err, workspace.ErrNotFound,
	) {
		t.Fatalf("get missing workspace by ID: err = %v, want ErrNotFound", err)
	}
}

func testListWorkspaces(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	other := newWorkspaceNamespace(t, s, "other")
	empty := newWorkspaceNamespace(t, s, "empty")

	for _, name := range []string{"zeta", "acme", "beta"} {
		createTestWorkspace(t, s, ns, name)
	}

	createTestWorkspace(t, s, other, "network")

	got, err := s.ListWorkspaces(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}

	want := []string{"acme", "beta", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("listed workspaces = %v, want %v", workspaceNames(got), want)
	}

	for i := range want {
		if got[i].Ref.Name.String() != want[i] || got[i].Ref.Namespace != ns.Name {
			t.Fatalf("listed workspaces = %v, want %v", workspaceNames(got), want)
		}
	}

	if list, err := s.ListWorkspaces(t.Context(), empty.Name); err != nil || len(list) != 0 {
		t.Fatalf("list of an empty namespace = %v, %v; want empty", list, err)
	}

	if list, err := s.ListWorkspaces(t.Context(), "ghost"); err != nil || len(list) != 0 {
		t.Fatalf("list of a missing namespace = %v, %v; want empty", list, err)
	}
}

func workspaceNames(workspaces []workspace.Workspace) []string {
	names := make([]string, 0, len(workspaces))
	for i := range workspaces {
		names = append(names, workspaces[i].Ref.Name.String())
	}

	return names
}

func testWorkspaceWritesFenced(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	ghost := newTestWorkspace(ns, mustWorkspaceName(t, "ghost"))
	if err := s.CreateWorkspace(t.Context(), ghost, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create with a stale fence: err = %v, want ErrFenced", err)
	}

	moved := locked
	moved.UpdatedAt = now().Add(2 * time.Hour)
	moved.Revision++

	if err := s.SaveWorkspace(t.Context(), moved, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("save with a stale fence: err = %v, want ErrFenced", err)
	}

	stalePending := newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	)
	if err := s.CreateStateVersion(t.Context(), stalePending, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("create state version with a stale fence: err = %v, want ErrFenced", err)
	}

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")
	if err := s.FinalizeStateVersion(t.Context(), finalized, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("finalize with a stale fence: err = %v, want ErrFenced", err)
	}

	unknown := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}
	if err := s.CreateWorkspace(t.Context(), ghost, unknown); !errors.Is(err, namespace.ErrNotFound) {
		t.Errorf("create in a missing namespace: err = %v, want namespace.ErrNotFound", err)
	}

	if _, err := s.GetWorkspace(t.Context(), ghost.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("fenced workspace was written: err = %v, want ErrNotFound", err)
	}

	got, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get workspace after fenced writes: %v", err)
	}

	assertWorkspace(t, got, locked)

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after fenced writes: %v", err)
	}

	assertStateVersion(t, stored, pending)

	if _, err := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Errorf("fenced finalize wrote a current state: err = %v, want ErrNoState", err)
	}
}

func testSaveWrongRevision(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	next := w
	next.UpdatedAt = now().Add(time.Hour)
	next.Revision++

	if err := s.SaveWorkspace(t.Context(), next, ns.Fence()); err != nil {
		t.Fatalf("save workspace: %v", err)
	}

	stale := w
	stale.UpdatedAt = now().Add(2 * time.Hour)
	stale.Revision++

	if err := s.SaveWorkspace(t.Context(), stale, ns.Fence()); !errors.Is(err, workspace.ErrConcurrentModification) {
		t.Errorf("save from a stale read: err = %v, want ErrConcurrentModification", err)
	}

	skipped := next
	skipped.UpdatedAt = now().Add(3 * time.Hour)
	skipped.Revision += 2

	if err := s.SaveWorkspace(t.Context(), skipped, ns.Fence()); !errors.Is(
		err, workspace.ErrConcurrentModification,
	) {
		t.Errorf("save with a skipped revision: err = %v, want ErrConcurrentModification", err)
	}

	got, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get workspace after refused saves: %v", err)
	}

	assertWorkspace(t, got, next)
}

func testSaveMissingWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := newTestWorkspace(ns, mustWorkspaceName(t, "ghost"))

	if err := s.SaveWorkspace(t.Context(), w, ns.Fence()); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("save missing workspace: err = %v, want ErrNotFound", err)
	}
}

func testLockRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	got, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get locked workspace: %v", err)
	}

	assertWorkspace(t, got, locked)

	unlocked := locked
	unlocked.Lock = clearedLock()
	unlocked.UpdatedAt = now().Add(2 * time.Hour)
	unlocked.Revision++

	if saveErr := s.SaveWorkspace(t.Context(), unlocked, ns.Fence()); saveErr != nil {
		t.Fatalf("save unlocked workspace: %v", saveErr)
	}

	cleared, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get unlocked workspace: %v", err)
	}

	assertWorkspace(t, cleared, unlocked)

	if cleared.Lock.Locked() {
		t.Fatalf("workspace lock = %+v, want unlocked", cleared.Lock)
	}
}

func testFinalizeWithoutLock(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	// A force-unlock leaves the workspace unlocked: a version declared under
	// the old lock must not finalize afterwards.
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	released := locked
	released.Lock = clearedLock()
	released.UpdatedAt = now().Add(2 * time.Hour)
	released.Revision++

	if err := s.SaveWorkspace(t.Context(), released, ns.Fence()); err != nil {
		t.Fatalf("release the lock: %v", err)
	}

	finalized := finalizedVersion(pending, now().Add(3*time.Hour), "v1")
	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("finalize without a lock: err = %v, want ErrNotLocked", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	assertStateVersion(t, stored, pending)

	if _, err := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Errorf("refused finalize wrote a current state: err = %v, want ErrNoState", err)
	}
}

func testFinalizeLockedByOther(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	taken := locked
	taken.Lock = workspace.Lock{LockedAt: now().Add(time.Hour), ID: "lock-bob", Holder: "bob", Reason: "apply"}
	taken.UpdatedAt = now().Add(2 * time.Hour)
	taken.Revision++

	if err := s.SaveWorkspace(t.Context(), taken, ns.Fence()); err != nil {
		t.Fatalf("take the lock as another principal: %v", err)
	}

	finalized := finalizedVersion(pending, now().Add(3*time.Hour), "v1")
	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); !errors.Is(
		err, workspace.ErrLockedByOther,
	) {
		t.Errorf("finalize under another lock: err = %v, want ErrLockedByOther", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	assertStateVersion(t, stored, pending)

	if _, err := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Errorf("refused finalize wrote a current state: err = %v, want ErrNoState", err)
	}
}

func testPendingRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	want := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	got, err := s.GetStateVersion(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get state version %s: %v", want.ID, err)
	}

	assertStateVersion(t, got, want)

	if _, err := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("current state of a workspace without finalized state: err = %v, want ErrNoState", err)
	}

	if _, err := s.GetStateVersion(t.Context(), workspace.StateVersionID(ident.NewID("sv-"))); !errors.Is(
		err, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("get missing state version: err = %v, want ErrStateVersionNotFound", err)
	}

	if _, err := s.CurrentStateVersion(t.Context(), workspace.ID(ident.NewID("ws-"))); !errors.Is(
		err, workspace.ErrNoState,
	) {
		t.Fatalf("current state of a missing workspace: err = %v, want ErrNoState", err)
	}
}

func testCreateStateVersionMissing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := newTestWorkspace(ns, mustWorkspaceName(t, "network"))
	sv := newPendingVersion(t, w, "lock-alice", stateInput("l1", "v1", 1))

	if err := s.CreateStateVersion(t.Context(), sv, ns.Fence()); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("create state version for a missing workspace: err = %v, want ErrNotFound", err)
	}
}

func testFinalizeMakesCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	var current workspace.StateVersion

	for i, serial := range []int64{1, 2, 3} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", serial),
		))
		finalized := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); err != nil {
			t.Fatalf("finalize serial %d: %v", serial, err)
		}

		current = finalized
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	assertStateVersion(t, got, current)

	if got.Serial != 3 {
		t.Fatalf("current serial = %d, want 3", got.Serial)
	}

	stored, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace after finalize: %v", err)
	}

	if !stored.UpdatedAt.Equal(current.FinalizedAt) {
		t.Fatalf("workspace updated at = %v, want %v", stored.UpdatedAt, current.FinalizedAt)
	}
}

func testFinalizeRetry(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); !errors.Is(
		err, workspace.ErrUploadDenied,
	) {
		t.Errorf("retried finalize: err = %v, want ErrUploadDenied", err)
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after a retried finalize: %v", err)
	}

	assertStateVersion(t, got, finalized)

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a retried finalize: %v", err)
	}

	assertStateVersion(t, stored, finalized)
}

func testFinalizeLineageMismatch(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	)), now().Add(2*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	other := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l2", "v2", 2),
	))
	finalized := finalizedVersion(other, now().Add(3*time.Hour), "v2")

	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); !errors.Is(
		err, workspace.ErrLineageMismatch,
	) {
		t.Errorf("finalize with another lineage: err = %v, want ErrLineageMismatch", err)
	}

	stored, err := s.GetStateVersion(t.Context(), other.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	assertStateVersion(t, stored, other)

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after a refused finalize: %v", err)
	}

	assertStateVersion(t, got, first)
}

func testFinalizeStaleSerial(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	)), now().Add(2*time.Hour), "v2")

	if err := s.FinalizeStateVersion(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	for _, serial := range []int64{2, 1} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "old", serial),
		))
		finalized := finalizedVersion(pending, now().Add(3*time.Hour), "old")

		if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); !errors.Is(
			err, workspace.ErrStaleSerial,
		) {
			t.Errorf("finalize with serial %d: err = %v, want ErrStaleSerial", serial, err)
		}

		stored, err := s.GetStateVersion(t.Context(), pending.ID)
		if err != nil {
			t.Fatalf("get state version after a refused finalize: %v", err)
		}

		assertStateVersion(t, stored, pending)
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after refused finalizes: %v", err)
	}

	assertStateVersion(t, got, first)
}

func testFinalizeForce(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v5", 5),
	)), now().Add(2*time.Hour), "v5")

	if err := s.FinalizeStateVersion(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	// Force skips the lineage and the serial checks.
	forced := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l9", "v1", 1),
	)), now().Add(3*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), forced, ns.Fence()); err != nil {
		t.Fatalf("finalize with force: %v", err)
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after a forced finalize: %v", err)
	}

	assertStateVersion(t, got, forced)

	// Force never skips the lock check: released or foreign locks still fail.
	released := locked
	released.Lock = clearedLock()
	released.UpdatedAt = now().Add(4 * time.Hour)
	released.Revision++

	if saveErr := s.SaveWorkspace(t.Context(), released, ns.Fence()); saveErr != nil {
		t.Fatalf("release the lock: %v", saveErr)
	}

	unlocked := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, released, locked.Lock.ID, forcedInput("l9", "v2", 2),
	)), now().Add(5*time.Hour), "v2")

	if finalErr := s.FinalizeStateVersion(t.Context(), unlocked, ns.Fence()); !errors.Is(
		finalErr, workspace.ErrNotLocked,
	) {
		t.Errorf("forced finalize without a lock: err = %v, want ErrNotLocked", finalErr)
	}

	retaken := lockTestWorkspace(t, s, ns, released, "bob")

	foreign := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, retaken, locked.Lock.ID, forcedInput("l9", "v3", 3),
	)), now().Add(6*time.Hour), "v3")

	if foreignErr := s.FinalizeStateVersion(t.Context(), foreign, ns.Fence()); !errors.Is(
		foreignErr, workspace.ErrLockedByOther,
	) {
		t.Errorf("forced finalize under another lock: err = %v, want ErrLockedByOther", foreignErr)
	}

	got, err = s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after refused forced finalizes: %v", err)
	}

	assertStateVersion(t, got, forced)
}

func testFencedFinalizeKeepsCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	)), now().Add(2*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	next := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	finalized := finalizedVersion(next, now().Add(3*time.Hour), "v2")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.FinalizeStateVersion(t.Context(), finalized, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("finalize with a stale fence: err = %v, want ErrFenced", err)
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after a fenced finalize: %v", err)
	}

	assertStateVersion(t, got, first)

	stored, err := s.GetStateVersion(t.Context(), next.ID)
	if err != nil {
		t.Fatalf("get state version after a fenced finalize: %v", err)
	}

	assertStateVersion(t, stored, next)

	stamped, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace after a fenced finalize: %v", err)
	}

	if !stamped.UpdatedAt.Equal(first.FinalizedAt) {
		t.Fatalf("workspace updated at = %v, want %v", stamped.UpdatedAt, first.FinalizedAt)
	}
}

func testWorkspaceInventoryAfterCreate(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	entries := listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertWorkspaceEntry(t, entries[0], w, w.UpdatedAt)

	if entries := listWorkspaceInventory(t, s, "ghost"); len(entries) != 0 {
		t.Fatalf("inventory of a missing namespace = %+v, want none", entries)
	}
}

func testInventoryFollowsWrites(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	locked := lockTestWorkspace(t, s, ns, w, "alice")

	entries := listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after lock = %+v, want one", entries)
	}

	assertWorkspaceEntry(t, entries[0], locked, locked.UpdatedAt)

	unlocked := locked
	unlocked.Lock = clearedLock()
	unlocked.UpdatedAt = now().Add(2 * time.Hour)
	unlocked.Revision++

	if err := s.SaveWorkspace(t.Context(), unlocked, ns.Fence()); err != nil {
		t.Fatalf("save unlocked workspace: %v", err)
	}

	entries = listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after unlock = %+v, want one", entries)
	}

	assertWorkspaceEntry(t, entries[0], unlocked, unlocked.UpdatedAt)

	relocked := lockTestWorkspace(t, s, ns, unlocked, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, relocked, relocked.Lock.ID, stateInput("l1", "v1", 1),
	))
	finalized := finalizedVersion(pending, now().Add(3*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), finalized, ns.Fence()); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	entries = listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after finalize = %+v, want one", entries)
	}

	stored, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace after finalize: %v", err)
	}

	assertWorkspaceEntry(t, entries[0], stored, finalized.FinalizedAt)
}

func testWorkspaceInventoryFailedWrite(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	)), now().Add(2*time.Hour), "v1")

	if err := s.FinalizeStateVersion(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	duplicate := newTestWorkspace(ns, w.Ref.Name)
	if err := s.CreateWorkspace(t.Context(), duplicate, ns.Fence()); !errors.Is(err, workspace.ErrAlreadyExists) {
		t.Fatalf("create duplicate workspace: err = %v, want ErrAlreadyExists", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	ghost := newTestWorkspace(ns, mustWorkspaceName(t, "ghost"))
	if err := s.CreateWorkspace(t.Context(), ghost, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence: err = %v, want ErrFenced", err)
	}

	moved := locked
	moved.UpdatedAt = now().Add(4 * time.Hour)
	moved.Revision++

	if err := s.SaveWorkspace(t.Context(), moved, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("save with a stale fence: err = %v, want ErrFenced", err)
	}

	staleSerial := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "old", 1),
	)), now().Add(4*time.Hour), "old")

	if err := s.FinalizeStateVersion(t.Context(), staleSerial, ns.Fence()); !errors.Is(
		err, workspace.ErrStaleSerial,
	) {
		t.Fatalf("finalize with a stale serial: err = %v, want ErrStaleSerial", err)
	}

	next := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	fenced := finalizedVersion(next, now().Add(4*time.Hour), "v2")

	if err := s.FinalizeStateVersion(t.Context(), fenced, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("finalize with a stale fence: err = %v, want ErrFenced", err)
	}

	entries := listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after failed writes = %+v, want one", entries)
	}

	stored, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace after failed writes: %v", err)
	}

	assertWorkspaceEntry(t, entries[0], stored, first.FinalizedAt)

	if got := entries[0].Reference(); got != "opentofu:"+ns.Name.String()+"/"+w.Ref.Name.String() {
		t.Fatalf("inventory reference = %q, want opentofu:%s/%s", got, ns.Name, w.Ref.Name)
	}
}
