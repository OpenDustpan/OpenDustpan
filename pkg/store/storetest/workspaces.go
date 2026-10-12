package storetest

import (
	"bytes"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
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
		{name: "two concurrent finalizes admit exactly one", test: testConcurrentFinalize},
		{name: "a fenced finalize leaves the old state current", test: testFencedFinalizeKeepsCurrent},
		{
			name: "a superseded attempt never finalizes over a newer one",
			test: testFinalizeStaleAttemptDenied,
		},
		{
			name: "create publishes an inventory entry for opentofu:namespace/name",
			test: testWorkspaceInventoryAfterCreate,
		},
		{name: "lock, unlock and finalize move the inventory entry", test: testInventoryFollowsWrites},
		{name: "a stale fence on lock writes no lock", test: testWorkspaceInventoryLockFenced},
		{name: "a failed write leaves the inventory unchanged", test: testWorkspaceInventoryFailedWrite},
		{name: "list returns only finalized versions, newest first", test: testListStateVersionsNewestFirst},
		{
			name: "list pages size 2 over five versions as 2, 2, 1 and empty past the end",
			test: testListStateVersionsPages,
		},
		{
			name: "list caps the page size and normalizes the page",
			test: testListStateVersionsCapsPageSize,
		},
		{
			name: "list sorts a forced lower serial by finalize time, not by serial",
			test: testListStateVersionsForcedLowerSerial,
		},
		{
			name: "restored-from is stored and read back by get and by list",
			test: testListStateVersionsRestoredFrom,
		},
		{
			name: "a restored version keeps lineage, serial, MD5 and size and sets force",
			test: testListStateVersionsRestoredLineage,
		},
		{
			name: "list of one workspace never returns versions of another workspace",
			test: testListStateVersionsIsolatesWorkspaces,
		},
		{
			name: "a stale fence on a failed write leaves the list unchanged",
			test: testListStateVersionsFencedWrite,
		},
		{
			name: "count pending counts only pending versions of one workspace",
			test: testCountPendingStateVersions,
		},
		{
			name: "record upload attempt notes the content key of a pending version",
			test: testRecordUploadAttempt,
		},
		{
			name: "record upload attempt rejects a missing, finalized or fenced version",
			test: testRecordUploadAttemptErrors,
		},
		{
			name: "delete expired pending removes only expired pendings of one workspace",
			test: testDeleteExpiredPending,
		},
		{
			name: "delete expired pending of a missing workspace or stale fence writes nothing",
			test: testDeleteExpiredPendingFence,
		},
		{
			name: "delete expired pending skips a version that finalized first",
			test: testDeleteExpiredPendingSkipsFinalized,
		},
		{
			name: "a finalize racing the sweep never lands in the sweep result",
			test: testFinalizeRacesSweep,
		},
		{
			name: "create refuses past the pending cap with ErrTooManyPending",
			test: testCreateStateVersionCap,
		},
		{
			name: "concurrent creates keep the pending cap",
			test: testCreateStateVersionConcurrentCap,
		},
		{
			name: "referenced content keys lists every named blob of one workspace",
			test: testReferencedContentKeys,
		},
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
		RestoredFrom:    "",
	}
}

// generousPendingCap binds no test that does not probe the cap: those pass
// their own limit to CreateStateVersion.
const generousPendingCap = 1 << 30

func createPendingVersion(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, sv workspace.StateVersion,
) workspace.StateVersion {
	t.Helper()

	if err := s.CreateStateVersion(t.Context(), sv, ns.Fence(), generousPendingCap); err != nil {
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

// finalizeRecorded records the attempt key of sv and finalizes it, returning
// the error of either step. The finalize matches only the recorded key, so
// every test records before it finalizes, as Upload does. A record with a
// stale fence or on a finalized version returns that step's error without
// reaching the finalize.
func finalizeRecorded(
	t *testing.T, s WorkspaceStore, fence namespace.Fence, sv workspace.StateVersion,
) error {
	t.Helper()

	if _, err := s.RecordUploadAttempt(t.Context(), sv.ID, sv.Content, fence); err != nil {
		return err
	}

	return s.FinalizeStateVersion(t.Context(), sv, fence)
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
		got.Force == want.Force && got.Upload == want.Upload && got.RestoredFrom == want.RestoredFrom
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

	assertWorkspaceLock(t, got.Lock, w.Lock)
}

// assertWorkspaceLock checks a projection lock against a workspace lock: an
// unlocked workspace reads no lock, a held one reads its holder with its
// take time and its reason as the operation, or "lock" without one. It fails
// the test on a mismatch, so the holder is always the subject that took the
// lock, never a token or a secret.
func assertWorkspaceLock(t *testing.T, got *inventory.Lock, want workspace.Lock) {
	t.Helper()

	if !want.Locked() {
		if got != nil {
			t.Fatalf("inventory lock = %+v, want none", got)
		}

		return
	}

	operation := want.Reason
	if operation == "" {
		operation = "lock"
	}

	if got == nil {
		t.Fatalf("inventory lock is missing, want holder %q operation %q", want.Holder, operation)
	}

	if got.Holder != want.Holder || !got.Since.Equal(want.LockedAt) || got.Operation != operation {
		t.Fatalf("inventory lock = %+v, want holder %q operation %q since %v",
			got, want.Holder, operation, want.LockedAt)
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

	if err := s.CreateStateVersion(t.Context(), sv, ghost.Fence(), generousPendingCap); !errors.Is(
		err, namespace.ErrNotFound,
	) {
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
	if err := s.CreateStateVersion(t.Context(), stalePending, stale, generousPendingCap); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Errorf("create state version with a stale fence: err = %v, want ErrFenced", err)
	}

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")
	if err := finalizeRecorded(t, s, stale, finalized); !errors.Is(err, namespace.ErrFenced) {
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
	if err := finalizeRecorded(t, s, ns.Fence(), finalized); !errors.Is(err, workspace.ErrNotLocked) {
		t.Errorf("finalize without a lock: err = %v, want ErrNotLocked", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	// The attempt was recorded before the finalize was refused, so the row
	// stays pending with the attempted content and no current state.
	want := pending
	want.Content = finalized.Content
	assertStateVersion(t, stored, want)

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
	if err := finalizeRecorded(t, s, ns.Fence(), finalized); !errors.Is(
		err, workspace.ErrLockedByOther,
	) {
		t.Errorf("finalize under another lock: err = %v, want ErrLockedByOther", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	// The attempt was recorded before the finalize was refused, so the row
	// stays pending with the attempted content and no current state.
	want := pending
	want.Content = finalized.Content
	assertStateVersion(t, stored, want)

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

	if err := s.CreateStateVersion(t.Context(), sv, ns.Fence(), generousPendingCap); !errors.Is(
		err, workspace.ErrNotFound,
	) {
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

		if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
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

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	// A retry calls the finalize itself: the version is finalized, so the
	// conditional write matches no row.
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

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	other := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l2", "v2", 2),
	))
	finalized := finalizedVersion(other, now().Add(3*time.Hour), "v2")

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); !errors.Is(
		err, workspace.ErrLineageMismatch,
	) {
		t.Errorf("finalize with another lineage: err = %v, want ErrLineageMismatch", err)
	}

	stored, err := s.GetStateVersion(t.Context(), other.ID)
	if err != nil {
		t.Fatalf("get state version after a refused finalize: %v", err)
	}

	// The attempt was recorded before the finalize was refused, so the row
	// stays pending with the attempted content.
	wantOther := other
	wantOther.Content = finalized.Content
	assertStateVersion(t, stored, wantOther)

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

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	for _, serial := range []int64{2, 1} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "old", serial),
		))
		finalized := finalizedVersion(pending, now().Add(3*time.Hour), "old")

		if err := finalizeRecorded(t, s, ns.Fence(), finalized); !errors.Is(
			err, workspace.ErrStaleSerial,
		) {
			t.Errorf("finalize with serial %d: err = %v, want ErrStaleSerial", serial, err)
		}

		stored, err := s.GetStateVersion(t.Context(), pending.ID)
		if err != nil {
			t.Fatalf("get state version after a refused finalize: %v", err)
		}

		// The attempt was recorded before the finalize was refused, so the
		// row stays pending with the attempted content.
		wantPending := pending
		wantPending.Content = finalized.Content
		assertStateVersion(t, stored, wantPending)
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

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	// Force skips the lineage and the serial checks.
	forced := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l9", "v1", 1),
	)), now().Add(3*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), forced); err != nil {
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

	if finalErr := finalizeRecorded(t, s, ns.Fence(), unlocked); !errors.Is(
		finalErr, workspace.ErrNotLocked,
	) {
		t.Errorf("forced finalize without a lock: err = %v, want ErrNotLocked", finalErr)
	}

	retaken := lockTestWorkspace(t, s, ns, released, "bob")

	foreign := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, retaken, locked.Lock.ID, forcedInput("l9", "v3", 3),
	)), now().Add(6*time.Hour), "v3")

	if foreignErr := finalizeRecorded(t, s, ns.Fence(), foreign); !errors.Is(
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

// testConcurrentFinalize finalizes two versions with one serial at once:
// exactly one wins, the other sees a stale serial. The row lock on the
// workspace keeps both from passing the serial check together.
func testConcurrentFinalize(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	pendings := []workspace.StateVersion{
		createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
		)),
		createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v2", 1),
		)),
	}

	finalized := make([]workspace.StateVersion, len(pendings))
	attempts := []string{"a1", "a2"}

	for i := range pendings {
		finalized[i] = finalizedVersion(pendings[i], now().Add(time.Duration(i+2)*time.Hour), "v1")
		finalized[i].Content = blob.Key(
			"tofu/" + pendings[i].Workspace.String() + "/" + pendings[i].ID.String() + "/" + attempts[i],
		)

		if _, err := s.RecordUploadAttempt(t.Context(), finalized[i].ID, finalized[i].Content, ns.Fence()); err != nil {
			t.Fatalf("record upload attempt: %v", err)
		}
	}

	errs := make([]error, len(finalized))

	var wg sync.WaitGroup

	for i := range finalized {
		wg.Go(func() {
			errs[i] = s.FinalizeStateVersion(t.Context(), finalized[i], ns.Fence())
		})
	}

	wg.Wait()

	var won []workspace.StateVersion

	for i, err := range errs {
		switch {
		case err == nil:
			won = append(won, finalized[i])
		case !errors.Is(err, workspace.ErrStaleSerial):
			t.Errorf("finalize %d: err = %v, want nil or ErrStaleSerial", i, err)
		default:
		}
	}

	if len(won) != 1 {
		t.Fatalf("%d of %d concurrent finalizes succeeded, want 1", len(won), len(finalized))
	}

	got, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after concurrent finalizes: %v", err)
	}

	assertStateVersion(t, got, won[0])

	stored, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace after concurrent finalizes: %v", err)
	}

	if !stored.UpdatedAt.Equal(won[0].FinalizedAt) {
		t.Fatalf("workspace updated at = %v, want %v", stored.UpdatedAt, won[0].FinalizedAt)
	}
}

func testFencedFinalizeKeepsCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	)), now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	next := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	finalized := finalizedVersion(next, now().Add(3*time.Hour), "v2")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := finalizeRecorded(t, s, stale, finalized); !errors.Is(err, namespace.ErrFenced) {
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

// testFinalizeStaleAttemptDenied proves a superseded upload attempt never
// finalizes over a newer one: after two attempts the stale key reports
// ErrUploadDenied with the row still naming the latest attempt and no
// current state, and the latest attempt still finalizes.
func testFinalizeStaleAttemptDenied(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	first := attemptKeyOf(pending, "a1")
	second := attemptKeyOf(pending, "a2")

	if _, err := s.RecordUploadAttempt(t.Context(), pending.ID, first, ns.Fence()); err != nil {
		t.Fatalf("record first attempt: %v", err)
	}

	superseded, err := s.RecordUploadAttempt(t.Context(), pending.ID, second, ns.Fence())
	if err != nil {
		t.Fatalf("record second attempt: %v", err)
	}

	if superseded != first {
		t.Fatalf("superseded key = %q, want %q", superseded, first)
	}

	stale := finalizedVersion(pending, now().Add(2*time.Hour), "v1")
	stale.Content = first
	stale.Size = 2

	if staleErr := s.FinalizeStateVersion(t.Context(), stale, ns.Fence()); !errors.Is(
		staleErr, workspace.ErrUploadDenied,
	) {
		t.Fatalf("finalize with a superseded attempt: err = %v, want ErrUploadDenied", staleErr)
	}

	assertAttemptContent(t, s, pending.ID, second)

	if _, currentErr := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(currentErr, workspace.ErrNoState) {
		t.Fatalf("current state after a denied stale finalize: err = %v, want ErrNoState", currentErr)
	}

	latest := finalizedVersion(pending, now().Add(2*time.Hour), "v1")
	latest.Content = second
	latest.Size = 2

	if latestErr := s.FinalizeStateVersion(t.Context(), latest, ns.Fence()); latestErr != nil {
		t.Fatalf("finalize with the latest attempt: %v", latestErr)
	}

	got, gotErr := s.CurrentStateVersion(t.Context(), w.ID)
	if gotErr != nil {
		t.Fatalf("current state after the latest finalize: %v", gotErr)
	}

	assertStateVersion(t, got, latest)
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

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
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

// testWorkspaceInventoryLockFenced checks a stale fence fails the lock write and
// writes nothing: the entry holds no lock after. Unlock and force-unlock
// save through the same path, so the fence guards their clear too.
func testWorkspaceInventoryLockFenced(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	locked := w
	locked.Lock = workspace.Lock{
		LockedAt: now(),
		ID:       "lock-alice",
		Holder:   "alice",
		Reason:   "apply",
	}
	locked.UpdatedAt = now().Add(time.Hour)
	locked.Revision++

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.SaveWorkspace(t.Context(), locked, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("lock with a stale fence: err = %v, want ErrFenced", err)
	}

	entries := listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after a fenced lock = %+v, want one", entries)
	}

	assertWorkspaceEntry(t, entries[0], w, w.UpdatedAt)
}

func testWorkspaceInventoryFailedWrite(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	first := finalizedVersion(createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	)), now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
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

	if err := finalizeRecorded(t, s, ns.Fence(), staleSerial); !errors.Is(
		err, workspace.ErrStaleSerial,
	) {
		t.Fatalf("finalize with a stale serial: err = %v, want ErrStaleSerial", err)
	}

	next := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	fenced := finalizedVersion(next, now().Add(4*time.Hour), "v2")

	if err := finalizeRecorded(t, s, stale, fenced); !errors.Is(err, namespace.ErrFenced) {
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

// assertNoHistory checks that id lists no versions with total zero: a
// workspace without state, or one that does not exist, yields empty.
func assertNoHistory(t *testing.T, s WorkspaceStore, id workspace.ID) {
	t.Helper()

	list, total, err := s.ListStateVersions(t.Context(), id, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions of %s: %v", id, err)
	}

	if len(list) != 0 || total != 0 {
		t.Fatalf("list of %s without state = %v, total %d; want empty and 0", id, list, total)
	}
}

// assertHistory checks the total and the newest-first versions against want,
// with a full round trip of each version.
func assertHistory(t *testing.T, list []workspace.StateVersion, total int, want []workspace.StateVersion) {
	t.Helper()

	if total != len(want) {
		t.Fatalf("total state versions = %d, want %d", total, len(want))
	}

	if len(list) != len(want) {
		t.Fatalf("listed state versions = %v, want %v", stateVersionIDs(list), stateVersionIDs(want))
	}

	for i := range want {
		if list[i].ID != want[i].ID {
			t.Fatalf("listed state versions = %v, want %v", stateVersionIDs(list), stateVersionIDs(want))
		}

		assertStateVersion(t, list[i], want[i])
	}
}

// stateVersionIDs returns the IDs of versions in order.
func stateVersionIDs(versions []workspace.StateVersion) []workspace.StateVersionID {
	ids := make([]workspace.StateVersionID, 0, len(versions))
	for i := range versions {
		ids = append(ids, versions[i].ID)
	}

	return ids
}

func testListStateVersionsNewestFirst(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	all := workspace.Page{Limit: 100, Offset: 0}

	assertNoHistory(t, s, w.ID)
	assertNoHistory(t, s, workspace.ID(ident.NewID("ws-")))

	finalized := make([]workspace.StateVersion, 0, 3)

	for i, serial := range []int64{1, 2, 3} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", serial),
		))
		done := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), done); err != nil {
			t.Fatalf("finalize serial %d: %v", serial, err)
		}

		finalized = append(finalized, done)
	}

	// A pending version stays out of the list: the exact-ID match below
	// proves its absence without a second scan.
	createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v9", 9),
	))

	list, total, err := s.ListStateVersions(t.Context(), w.ID, all)
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{finalized[2], finalized[1], finalized[0]})
}

// testListStateVersionsCapsPageSize proves the paging rules of the port: a
// non-positive limit reads as DefaultStatePageSize, a limit above
// MaxStatePageSize is capped, and a negative offset reads as zero. The total
// always counts every finalized version.
func testListStateVersionsCapsPageSize(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	const count = workspace.MaxStatePageSize + 2

	finalized := finalizeSerials(t, s, ns, locked, count)

	// Newest first: the last finalized version leads.
	newest := make([]workspace.StateVersionID, 0, count)
	for i := range slices.Backward(finalized) {
		newest = append(newest, finalized[i].ID)
	}

	for _, tc := range []listPageWant{
		{
			page:  workspace.Page{Limit: 0, Offset: 0},
			want:  newest[:workspace.DefaultStatePageSize],
			total: count,
		},
		{
			page:  workspace.Page{Limit: workspace.MaxStatePageSize + 400, Offset: 0},
			want:  newest[:workspace.MaxStatePageSize],
			total: count,
		},
		{
			page:  workspace.Page{Limit: 2, Offset: -5},
			want:  newest[:2],
			total: count,
		},
	} {
		assertListPage(t, s, w.ID, tc)
	}
}

// listPageWant is one paging probe: the page to read, the total it must
// report, and the version IDs it must list in order.
type listPageWant struct {
	page  workspace.Page
	want  []workspace.StateVersionID
	total int
}

// finalizeSerials finalizes count versions with serials 1..count and returns
// them in finalize order.
func finalizeSerials(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, locked workspace.Workspace, count int,
) []workspace.StateVersion {
	t.Helper()

	finalized := make([]workspace.StateVersion, 0, count)

	for i := range count {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", int64(i+1)),
		))
		done := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), done); err != nil {
			t.Fatalf("finalize serial %d: %v", i+1, err)
		}

		finalized = append(finalized, done)
	}

	return finalized
}

// assertListPage checks one page: the total counts every finalized version
// and the listed IDs match want in order.
func assertListPage(t *testing.T, s WorkspaceStore, id workspace.ID, tc listPageWant) {
	t.Helper()

	list, got, err := s.ListStateVersions(t.Context(), id, tc.page)
	if err != nil {
		t.Fatalf("list state versions %+v: %v", tc.page, err)
	}

	if got != tc.total {
		t.Errorf("total state versions at %+v = %d, want %d", tc.page, got, tc.total)
	}

	ids := stateVersionIDs(list)

	if len(ids) != len(tc.want) {
		t.Errorf("listed state versions at %+v = %v, want %d versions", tc.page, ids, len(tc.want))

		return
	}

	for i := range tc.want {
		if ids[i] != tc.want[i] {
			t.Errorf("listed state versions at %+v = %v, want %v", tc.page, ids, tc.want)

			break
		}
	}
}

func testListStateVersionsPages(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	finalized := make([]workspace.StateVersion, 0, 5)

	for i, serial := range []int64{1, 2, 3, 4, 5} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", serial),
		))
		done := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), done); err != nil {
			t.Fatalf("finalize serial %d: %v", serial, err)
		}

		finalized = append(finalized, done)
	}

	for _, tc := range []struct {
		page workspace.Page
		want []workspace.StateVersionID
	}{
		{
			page: workspace.Page{Limit: 2, Offset: 0},
			want: []workspace.StateVersionID{finalized[4].ID, finalized[3].ID},
		},
		{
			page: workspace.Page{Limit: 2, Offset: 2},
			want: []workspace.StateVersionID{finalized[2].ID, finalized[1].ID},
		},
		{page: workspace.Page{Limit: 2, Offset: 4}, want: []workspace.StateVersionID{finalized[0].ID}},
		{page: workspace.Page{Limit: 2, Offset: 6}, want: []workspace.StateVersionID{}},
	} {
		list, total, err := s.ListStateVersions(t.Context(), w.ID, tc.page)
		if err != nil {
			t.Fatalf("list state versions %+v: %v", tc.page, err)
		}

		if total != 5 {
			t.Errorf("total state versions at %+v = %d, want 5", tc.page, total)
		}

		got := stateVersionIDs(list)

		if len(got) != len(tc.want) {
			t.Errorf("listed state versions at %+v = %v, want %v", tc.page, got, tc.want)

			continue
		}

		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("listed state versions at %+v = %v, want %v", tc.page, got, tc.want)

				break
			}
		}
	}
}

func testListStateVersionsForcedLowerSerial(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	firstPending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v5", 5),
	))
	first := finalizedVersion(firstPending, now().Add(2*time.Hour), "v5")

	if err := finalizeRecorded(t, s, ns.Fence(), first); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	forcedPending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l1", "v1", 1),
	))
	forced := finalizedVersion(forcedPending, now().Add(3*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), forced); err != nil {
		t.Fatalf("finalize forced state: %v", err)
	}

	list, total, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{forced, first})
}

func testListStateVersionsRestoredFrom(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	oldPending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	old := finalizedVersion(oldPending, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), old); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	if old.RestoredFrom != "" {
		t.Fatalf("restored-from of a plain version = %q, want empty", old.RestoredFrom)
	}

	restoredPending := newPendingVersion(t, locked, locked.Lock.ID, forcedInput("l1", "v1", 1))
	restoredPending.RestoredFrom = old.ID
	createPendingVersion(t, s, ns, restoredPending)

	restored := finalizedVersion(restoredPending, now().Add(3*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), restored); err != nil {
		t.Fatalf("finalize restored state: %v", err)
	}

	got, err := s.GetStateVersion(t.Context(), restored.ID)
	if err != nil {
		t.Fatalf("get restored state version: %v", err)
	}

	assertStateVersion(t, got, restored)

	if got.RestoredFrom != old.ID {
		t.Fatalf("restored-from by get = %q, want %q", got.RestoredFrom, old.ID)
	}

	list, total, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{restored, old})

	if list[0].RestoredFrom != old.ID {
		t.Errorf("restored-from by list = %q, want %q", list[0].RestoredFrom, old.ID)
	}

	if list[1].RestoredFrom != "" {
		t.Errorf("restored-from of the source version = %q, want empty", list[1].RestoredFrom)
	}
}

func testListStateVersionsRestoredLineage(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	oldPending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "state-bytes", 3),
	))
	old := finalizedVersion(oldPending, now().Add(2*time.Hour), "state-bytes")

	if err := finalizeRecorded(t, s, ns.Fence(), old); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	restoredPending := newPendingVersion(t, locked, locked.Lock.ID, forcedInput("l1", "state-bytes", 3))
	restoredPending.RestoredFrom = old.ID
	createPendingVersion(t, s, ns, restoredPending)

	restored := finalizedVersion(restoredPending, now().Add(3*time.Hour), "state-bytes")

	if err := finalizeRecorded(t, s, ns.Fence(), restored); err != nil {
		t.Fatalf("finalize restored state: %v", err)
	}

	got, err := s.GetStateVersion(t.Context(), restored.ID)
	if err != nil {
		t.Fatalf("get restored state version: %v", err)
	}

	assertStateVersion(t, got, restored)

	if got.Lineage != old.Lineage {
		t.Errorf("restored lineage = %q, want %q", got.Lineage, old.Lineage)
	}

	if got.Serial != old.Serial {
		t.Errorf("restored serial = %d, want %d", got.Serial, old.Serial)
	}

	if got.MD5 != old.MD5 {
		t.Errorf("restored MD5 = %x, want %x", got.MD5, old.MD5)
	}

	if got.Size != old.Size {
		t.Errorf("restored size = %d, want %d", got.Size, old.Size)
	}

	if !got.Force {
		t.Error("restored force = false, want true")
	}

	if got.RestoredFrom != old.ID {
		t.Errorf("restored-from = %q, want %q", got.RestoredFrom, old.ID)
	}

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after a restore: %v", err)
	}

	assertStateVersion(t, current, restored)
}

func testListStateVersionsIsolatesWorkspaces(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	first := createTestWorkspace(t, s, ns, "one")
	second := createTestWorkspace(t, s, ns, "two")
	lockedFirst := lockTestWorkspace(t, s, ns, first, "alice")
	lockedSecond := lockTestWorkspace(t, s, ns, second, "alice")

	firstVersions := make([]workspace.StateVersion, 0, 2)

	for i, serial := range []int64{1, 2} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, lockedFirst, lockedFirst.Lock.ID, stateInput("l1", "v1", serial),
		))
		done := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), done); err != nil {
			t.Fatalf("finalize serial %d: %v", serial, err)
		}

		firstVersions = append(firstVersions, done)
	}

	otherPending := createPendingVersion(t, s, ns, newPendingVersion(
		t, lockedSecond, lockedSecond.Lock.ID, stateInput("l1", "v1", 1),
	))
	other := finalizedVersion(otherPending, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), other); err != nil {
		t.Fatalf("finalize state of the second workspace: %v", err)
	}

	list, total, err := s.ListStateVersions(t.Context(), first.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions of the first workspace: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{firstVersions[1], firstVersions[0]})

	for i := range list {
		if list[i].ID == other.ID {
			t.Fatalf("list of one workspace holds version %s of another workspace", other.ID)
		}
	}

	otherList, otherTotal, err := s.ListStateVersions(
		t.Context(), second.ID, workspace.Page{Limit: 100, Offset: 0},
	)
	if err != nil {
		t.Fatalf("list state versions of the second workspace: %v", err)
	}

	assertHistory(t, otherList, otherTotal, []workspace.StateVersion{other})
}

func testListStateVersionsFencedWrite(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	for i, serial := range []int64{1, 2} {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", serial),
		))
		done := finalizedVersion(pending, now().Add(time.Duration(i+2)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), done); err != nil {
			t.Fatalf("finalize serial %d: %v", serial, err)
		}
	}

	before, beforeTotal, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if beforeTotal != 2 {
		t.Fatalf("total state versions = %d, want 2", beforeTotal)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	stalePending := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v3", 3))
	createErr := s.CreateStateVersion(t.Context(), stalePending, stale, generousPendingCap)
	if !errors.Is(createErr, namespace.ErrFenced) {
		t.Fatalf("create state version with a stale fence: err = %v, want ErrFenced", createErr)
	}

	next := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v3", 3),
	))
	fenced := finalizedVersion(next, now().Add(4*time.Hour), "v3")

	if finalizeErr := finalizeRecorded(t, s, stale, fenced); !errors.Is(finalizeErr, namespace.ErrFenced) {
		t.Fatalf("finalize with a stale fence: err = %v, want ErrFenced", finalizeErr)
	}

	after, afterTotal, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions after fenced writes: %v", err)
	}

	assertHistory(t, after, afterTotal, before)

	stored, err := s.GetStateVersion(t.Context(), next.ID)
	if err != nil {
		t.Fatalf("get state version after a fenced finalize: %v", err)
	}

	assertStateVersion(t, stored, next)
}

// assertPendingCount checks that workspace holds n pending versions.
func assertPendingCount(t *testing.T, s WorkspaceStore, id workspace.ID, n int) {
	t.Helper()

	got, err := s.CountPendingStateVersions(t.Context(), id)
	if err != nil {
		t.Fatalf("count pending state of %s: %v", id, err)
	}

	if got != n {
		t.Fatalf("pending state versions of %s = %d, want %d", id, got, n)
	}
}

// testCountPendingStateVersions proves the pending count holds only pending
// versions of one workspace: a finalized version and another workspace stay
// out, and a missing workspace counts none.
func testCountPendingStateVersions(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	other := createTestWorkspace(t, s, ns, "other")
	lockedOther := lockTestWorkspace(t, s, ns, other, "alice")

	assertPendingCount(t, s, w.ID, 0)
	assertPendingCount(t, s, workspace.ID(ident.NewID("ws-")), 0)

	first := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	createPendingVersion(t, s, ns, newPendingVersion(
		t, lockedOther, lockedOther.Lock.ID, stateInput("l1", "v1", 1),
	))

	assertPendingCount(t, s, w.ID, 2)
	assertPendingCount(t, s, other.ID, 1)

	finalized := finalizedVersion(first, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	assertPendingCount(t, s, w.ID, 1)
	assertPendingCount(t, s, other.ID, 1)
}

// attemptKeyOf returns the blob key of one upload attempt of sv.
func attemptKeyOf(sv workspace.StateVersion, attempt string) blob.Key {
	return blob.Key("tofu/" + sv.Workspace.String() + "/" + sv.ID.String() + "/" + attempt)
}

// assertAttemptContent checks that id is pending with content.
func assertAttemptContent(t *testing.T, s WorkspaceStore, id workspace.StateVersionID, content blob.Key) {
	t.Helper()

	stored, err := s.GetStateVersion(t.Context(), id)
	if err != nil {
		t.Fatalf("get state version %s: %v", id, err)
	}

	if stored.Content != content || stored.Status != workspace.StatusPending {
		t.Fatalf(
			"attempt %s = content %q status %q, want content %q pending",
			id,
			stored.Content,
			stored.Status,
			content,
		)
	}
}

// testRecordUploadAttempt proves an attempt notes its blob key on a pending
// version and reads back, returns no superseded key for the first attempt,
// and returns the earlier key when a later attempt overwrites it: the row
// names the latest content, and the caller deletes the superseded blob.
func testRecordUploadAttempt(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	key := attemptKeyOf(pending, "a1")

	superseded, recordErr := s.RecordUploadAttempt(t.Context(), pending.ID, key, ns.Fence())
	if recordErr != nil {
		t.Fatalf("record upload attempt: %v", recordErr)
	}

	if superseded != "" {
		t.Fatalf("superseded key = %q, want empty for the first attempt", superseded)
	}

	assertAttemptContent(t, s, pending.ID, key)

	later := attemptKeyOf(pending, "a2")

	superseded, recordErr = s.RecordUploadAttempt(t.Context(), pending.ID, later, ns.Fence())
	if recordErr != nil {
		t.Fatalf("record second attempt: %v", recordErr)
	}

	if superseded != key {
		t.Fatalf("superseded key = %q, want %q", superseded, key)
	}

	assertAttemptContent(t, s, pending.ID, later)
}

// testRecordUploadAttemptErrors proves a record on a missing version reports
// ErrStateVersionNotFound, on a finalized version ErrUploadDenied, and with
// a stale fence ErrFenced with nothing written.
func testRecordUploadAttemptErrors(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	key := attemptKeyOf(pending, "a1")
	missing := workspace.StateVersionID(ident.NewID("sv-"))

	if _, recordErr := s.RecordUploadAttempt(t.Context(), missing, key, ns.Fence()); !errors.Is(
		recordErr, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("record on a missing version: err = %v, want ErrStateVersionNotFound", recordErr)
	}

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")

	if finalizeErr := finalizeRecorded(t, s, ns.Fence(), finalized); finalizeErr != nil {
		t.Fatalf("finalize state version: %v", finalizeErr)
	}

	if _, recordErr := s.RecordUploadAttempt(t.Context(), pending.ID, key, ns.Fence()); !errors.Is(
		recordErr, workspace.ErrUploadDenied,
	) {
		t.Fatalf("record on a finalized version: err = %v, want ErrUploadDenied", recordErr)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	fresh := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v9", 9),
	))

	if _, recordErr := s.RecordUploadAttempt(t.Context(), fresh.ID, key, stale); !errors.Is(
		recordErr, namespace.ErrFenced,
	) {
		t.Fatalf("record with a stale fence: err = %v, want ErrFenced", recordErr)
	}

	assertAttemptContent(t, s, fresh.ID, "")
}

// expiredPendingVersion returns a pending version whose capability
// expired at expiry.
func expiredPendingVersion(
	t *testing.T, w workspace.Workspace, lockID string, in pendingInput, expiry time.Time,
) workspace.StateVersion {
	t.Helper()

	sv := newPendingVersion(t, w, lockID, in)
	sv.UploadExpiresAt = expiry

	return sv
}

// testDeleteExpiredPending proves the sweep deletes only the expired pending
// versions of one workspace, oldest expiry first, with their attempted
// content: a fresh pending, a finalized version with an old expiry and
// another workspace stay.
func testDeleteExpiredPending(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	other := createTestWorkspace(t, s, ns, "other")
	lockedOther := lockTestWorkspace(t, s, ns, other, "alice")

	older := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "old", 1), now().Add(-2*time.Hour),
	))
	newer := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "new", 2), now().Add(-time.Hour),
	))
	content := attemptKeyOf(newer, "a1")

	if _, recordErr := s.RecordUploadAttempt(t.Context(), newer.ID, content, ns.Fence()); recordErr != nil {
		t.Fatalf("record upload attempt: %v", recordErr)
	}

	fresh := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v9", 9),
	))
	otherExpired := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, lockedOther, lockedOther.Lock.ID, stateInput("l1", "v1", 1), now().Add(-2*time.Hour),
	))

	// A finalized version keeps its old capability expiry: the sweep only
	// reads pending versions.
	oldPending := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v3", 3), now().Add(-3*time.Hour),
	))
	old := finalizedVersion(oldPending, now().Add(2*time.Hour), "v3")

	if finalizeErr := finalizeRecorded(t, s, ns.Fence(), old); finalizeErr != nil {
		t.Fatalf("finalize old state: %v", finalizeErr)
	}

	swept, sweepErr := s.DeleteExpiredPendingStateVersions(t.Context(), w.ID, now(), ns.Fence())
	if sweepErr != nil {
		t.Fatalf("delete expired pending: %v", sweepErr)
	}

	if len(swept) != 2 || swept[0].ID != older.ID || swept[1].ID != newer.ID {
		t.Fatalf("swept versions = %v, want [%s %s]", stateVersionIDs(swept), older.ID, newer.ID)
	}

	if swept[1].Content != content {
		t.Fatalf("swept content = %q, want %q", swept[1].Content, content)
	}

	assertSweptGone(t, s, older.ID, newer.ID)
	assertPendingCount(t, s, w.ID, 1)
	assertPendingCount(t, s, other.ID, 1)

	if _, getErr := s.GetStateVersion(t.Context(), fresh.ID); getErr != nil {
		t.Fatalf("get fresh pending after sweep: %v", getErr)
	}

	if _, getErr := s.GetStateVersion(t.Context(), otherExpired.ID); getErr != nil {
		t.Fatalf("get other workspace pending after sweep: %v", getErr)
	}

	if _, getErr := s.CurrentStateVersion(t.Context(), w.ID); getErr != nil {
		t.Fatalf("current state after sweep: %v", getErr)
	}
}

// assertSweptGone checks that every id reads as ErrStateVersionNotFound.
func assertSweptGone(t *testing.T, s WorkspaceStore, ids ...workspace.StateVersionID) {
	t.Helper()

	for _, id := range ids {
		if _, err := s.GetStateVersion(t.Context(), id); !errors.Is(err, workspace.ErrStateVersionNotFound) {
			t.Fatalf("get swept version %s: err = %v, want ErrStateVersionNotFound", id, err)
		}
	}
}

// testDeleteExpiredPendingFence proves a sweep of a missing workspace yields
// an empty list, and with a stale fence reports ErrFenced with nothing
// written.
func testDeleteExpiredPendingFence(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	expired := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1), now().Add(-time.Hour),
	))

	empty, sweepErr := s.DeleteExpiredPendingStateVersions(
		t.Context(), workspace.ID(ident.NewID("ws-")), now(), ns.Fence(),
	)
	if sweepErr != nil {
		t.Fatalf("delete expired of a missing workspace: %v", sweepErr)
	}

	if len(empty) != 0 {
		t.Fatalf("swept of a missing workspace = %v, want empty", stateVersionIDs(empty))
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if _, sweepErr := s.DeleteExpiredPendingStateVersions(t.Context(), w.ID, now(), stale); !errors.Is(
		sweepErr, namespace.ErrFenced,
	) {
		t.Fatalf("delete expired with a stale fence: err = %v, want ErrFenced", sweepErr)
	}

	assertPendingCount(t, s, w.ID, 1)

	if _, getErr := s.GetStateVersion(t.Context(), expired.ID); getErr != nil {
		t.Fatalf("get pending after a fenced sweep: %v", getErr)
	}
}

// testDeleteExpiredPendingSkipsFinalized proves a version that finalized
// before the sweep stays out of the result with its content untouched, even
// when its capability expired: the conditional delete matches only versions
// that are still pending.
func testDeleteExpiredPendingSkipsFinalized(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1), now().Add(-2*time.Hour),
	))
	content := attemptKeyOf(pending, "a1")

	if _, recordErr := s.RecordUploadAttempt(t.Context(), pending.ID, content, ns.Fence()); recordErr != nil {
		t.Fatalf("record upload attempt: %v", recordErr)
	}

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	swept, sweepErr := s.DeleteExpiredPendingStateVersions(t.Context(), w.ID, now(), ns.Fence())
	if sweepErr != nil {
		t.Fatalf("delete expired pending: %v", sweepErr)
	}

	if len(swept) != 0 {
		t.Fatalf("swept versions = %v, want none: the finalized version stays", stateVersionIDs(swept))
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get finalized version after sweep: %v", err)
	}

	assertStateVersion(t, stored, finalized)

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after sweep: %v", err)
	}

	assertStateVersion(t, current, finalized)
}

// testFinalizeRacesSweep finalizes an expired pending version while the
// sweep runs: the finalized version never lands in the sweep result, so a
// caller that deletes the swept content keys can never remove the current
// state. The conditional delete decides the winner inside the store: either
// the finalize commits first and the sweep skips the version, or the sweep
// deletes first and the finalize reports the version as gone.
func testFinalizeRacesSweep(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	for i := range 20 {
		finalizeRacesSweepOnce(t, s, ns, i)
	}
}

func finalizeRacesSweepOnce(t *testing.T, s WorkspaceStore, ns namespace.Namespace, i int) {
	t.Helper()

	w := createTestWorkspace(t, s, ns, fmt.Sprintf("race-%d", i))
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, expiredPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1), now().Add(-2*time.Hour),
	))
	content := attemptKeyOf(pending, "a1")

	if _, recordErr := s.RecordUploadAttempt(t.Context(), pending.ID, content, ns.Fence()); recordErr != nil {
		t.Fatalf("record upload attempt: %v", recordErr)
	}

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")

	var wg sync.WaitGroup

	var finalizeErr error

	var swept []workspace.StateVersion

	var sweepErr error

	wg.Go(func() {
		finalizeErr = s.FinalizeStateVersion(t.Context(), finalized, ns.Fence())
	})

	wg.Go(func() {
		swept, sweepErr = s.DeleteExpiredPendingStateVersions(t.Context(), w.ID, now(), ns.Fence())
	})

	wg.Wait()

	if sweepErr != nil {
		t.Fatalf("delete expired pending: %v", sweepErr)
	}

	switch {
	case finalizeErr == nil:
		for i := range swept {
			if swept[i].ID == pending.ID {
				t.Fatalf("swept versions = %v, want without %s", stateVersionIDs(swept), pending.ID)
			}
		}

		stored, err := s.GetStateVersion(t.Context(), pending.ID)
		if err != nil {
			t.Fatalf("get finalized version after the race: %v", err)
		}

		assertStateVersion(t, stored, finalized)
	case errors.Is(finalizeErr, workspace.ErrStateVersionNotFound) ||
		errors.Is(finalizeErr, workspace.ErrUploadDenied):
		// The sweep deleted first: either before the finalize read the
		// version, or between its guard check and its conditional write.
		if len(swept) != 1 || swept[0].ID != pending.ID {
			t.Fatalf("swept versions = %v, want [%s]: the sweep won the race", stateVersionIDs(swept), pending.ID)
		}

		assertSweptGone(t, s, pending.ID)
	default:
		t.Fatalf(
			"finalize during sweep: err = %v, want nil, ErrStateVersionNotFound or ErrUploadDenied",
			finalizeErr,
		)
	}
}

// testCreateStateVersionCap proves the pending cap binds one workspace: at
// most limit pending versions declare, the next reports ErrTooManyPending,
// another workspace is unaffected, a finalize frees a slot, a missing
// workspace reports ErrNotFound, and a non-positive limit refuses every
// version.
func testCreateStateVersionCap(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	other := createTestWorkspace(t, s, ns, "other")
	lockedOther := lockTestWorkspace(t, s, ns, other, "alice")

	create := func(w workspace.Workspace, serial int64, limit int) (workspace.StateVersion, error) {
		sv := newPendingVersion(t, w, w.Lock.ID, stateInput("l1", "v1", serial))

		return sv, s.CreateStateVersion(t.Context(), sv, ns.Fence(), limit)
	}

	first, err := create(locked, 1, 2)
	if err != nil {
		t.Fatalf("create first pending: %v", err)
	}

	if _, err := create(locked, 2, 2); err != nil {
		t.Fatalf("create second pending: %v", err)
	}

	if _, err := create(locked, 3, 2); !errors.Is(err, workspace.ErrTooManyPending) {
		t.Fatalf("create past the cap: err = %v, want ErrTooManyPending", err)
	}

	if _, err := create(lockedOther, 1, 1); err != nil {
		t.Fatalf("create in another workspace: %v", err)
	}

	finalized := finalizedVersion(first, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	if _, err := create(locked, 3, 2); err != nil {
		t.Fatalf("create after a finalize freed a slot: %v", err)
	}

	ghost := newTestWorkspace(ns, mustWorkspaceName(t, "ghost"))
	ghostSV := newPendingVersion(t, ghost, "lock-alice", stateInput("l1", "v1", 1))

	if err := s.CreateStateVersion(t.Context(), ghostSV, ns.Fence(), 2); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("create for a missing workspace: err = %v, want ErrNotFound", err)
	}

	for _, limit := range []int{0, -1} {
		sv := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v9", 9))

		if err := s.CreateStateVersion(t.Context(), sv, ns.Fence(), limit); !errors.Is(
			err, workspace.ErrTooManyPending,
		) {
			t.Fatalf("create with limit %d: err = %v, want ErrTooManyPending", limit, err)
		}
	}
}

// testCreateStateVersionConcurrentCap declares several versions at once with
// a low cap: exactly the cap succeeds and the rest report ErrTooManyPending.
// The count and the insert run in one store transaction, so no two declares
// pass the cap together whatever service fronts them.
func testCreateStateVersionConcurrentCap(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	const declares = 8

	const limit = 3

	pendings := make([]workspace.StateVersion, declares)

	for i := range pendings {
		pendings[i] = newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", int64(i+1)))
	}

	errs := make([]error, declares)

	var wg sync.WaitGroup

	for i := range pendings {
		wg.Go(func() {
			errs[i] = s.CreateStateVersion(t.Context(), pendings[i], ns.Fence(), limit)
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
			t.Fatalf("concurrent create: err = %v, want nil or ErrTooManyPending", err)
		}
	}

	if succeeded != limit {
		t.Fatalf("successful concurrent creates = %d, want %d: the cap", succeeded, limit)
	}

	assertPendingCount(t, s, w.ID, limit)
}

// testReferencedContentKeys proves the referenced set holds every blob key
// the versions of one workspace name, pending and finalized, without the
// empty ones: another workspace stays out, and a workspace without versions,
// or one that does not exist, yields an empty list.
func testReferencedContentKeys(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	other := createTestWorkspace(t, s, ns, "other")
	lockedOther := lockTestWorkspace(t, s, ns, other, "alice")

	assertReferencedContentKeys(t, s, w.ID)
	assertReferencedContentKeys(t, s, workspace.ID(ident.NewID("ws-")))

	first := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	// A version without an attempt names nothing.
	assertReferencedContentKeys(t, s, w.ID)

	firstKey := attemptKeyOf(first, "a1")

	if _, err := s.RecordUploadAttempt(t.Context(), first.ID, firstKey, ns.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	assertReferencedContentKeys(t, s, w.ID, firstKey)

	second := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))
	secondKey := attemptKeyOf(second, "a2")

	if _, err := s.RecordUploadAttempt(t.Context(), second.ID, secondKey, ns.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	assertReferencedContentKeys(t, s, w.ID, firstKey, secondKey)

	// A finalize keeps its content in the set.
	finalized := finalizedVersion(first, now().Add(2*time.Hour), "v1")

	if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
		t.Fatalf("finalize first state: %v", err)
	}

	assertReferencedContentKeys(t, s, w.ID, firstKey, secondKey)

	foreign := createPendingVersion(t, s, ns, newPendingVersion(
		t, lockedOther, lockedOther.Lock.ID, stateInput("l1", "v1", 1),
	))
	foreignKey := attemptKeyOf(foreign, "a1")

	if _, err := s.RecordUploadAttempt(t.Context(), foreign.ID, foreignKey, ns.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	assertReferencedContentKeys(t, s, w.ID, firstKey, secondKey)
	assertReferencedContentKeys(t, s, other.ID, foreignKey)
}

// assertReferencedContentKeys checks the referenced set of id against want,
// order aside.
func assertReferencedContentKeys(t *testing.T, s WorkspaceStore, id workspace.ID, want ...blob.Key) {
	t.Helper()

	got, err := s.ReferencedContentKeys(t.Context(), id)
	if err != nil {
		t.Fatalf("referenced content keys of %s: %v", id, err)
	}

	gotKeys := make([]string, 0, len(got))
	for i := range got {
		gotKeys = append(gotKeys, got[i].String())
	}

	wantKeys := make([]string, 0, len(want))
	for i := range want {
		wantKeys = append(wantKeys, want[i].String())
	}

	slices.Sort(gotKeys)
	slices.Sort(wantKeys)

	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("referenced content keys of %s = %v, want %v", id, gotKeys, wantKeys)
	}
}
