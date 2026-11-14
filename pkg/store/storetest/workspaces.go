package storetest

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// WorkspaceStore is every persistence port the workspace suite exercises:
// namespaces for fixtures, workspace.Store and workspace.StateStore under
// test, and inventory.Reader for the publish contract.
type WorkspaceStore interface {
	namespace.Store
	workspace.Store
	workspace.StateStore
	inventory.Reader
	inventory.DeletedReader
}

// WorkspaceFactory returns a fresh, empty store for one subtest.
type WorkspaceFactory func(t *testing.T) WorkspaceStore

// workspaceCase is one case of the workspace suite.
type workspaceCase struct {
	test func(t *testing.T, s WorkspaceStore)
	name string
}

// RunWorkspace executes the conformance suite of the workspace ports
// (workspace.Store, workspace.StateStore) against stores produced by newStore.
func RunWorkspace(t *testing.T, newStore WorkspaceFactory) {
	t.Helper()

	cases := []workspaceCase{
		{name: "create and read by name and by ID", test: testWorkspaceRoundTrip},
		{name: "the change log follows the writes of a workspace", test: testWorkspaceChangeLog},
		{name: "a refused write of a workspace appends no change log row", test: testWorkspaceChangeLogFence},
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
		{name: "finalize sets the declared serial, lineage, MD5 and outputs", test: testFinalizeSetsDeclaredFields},
		{
			name: "finalize under the same lock ID held by another principal is ErrLockedByOther",
			test: testFinalizeSameLockIDOtherHolder,
		},
		{name: "discard removes a pending version and keeps a finalized one", test: testDiscardStateVersion},
		{name: "discard under a stale fence is ErrFenced and keeps the row", test: testDiscardStaleFence},
		{
			name: "the conditional unlock and force-unlock check the lock ID and the holder",
			test: testConditionalUnlock,
		},
		{name: "a placeholder pending version is never current", test: testPlaceholderNeverCurrent},
		{
			name: "a finalize under a stale fence keeps the placeholder pending",
			test: testFinalizeStaleFenceKeepsPlaceholder,
		},
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
		{
			name: "concurrent restores of one version through two services write one version",
			test: testRestoreAcrossServicesWritesOnce,
		},
		{
			name: "finalize of a duplicate restore is ErrAlreadyRestored and writes nothing",
			test: testFinalizeDuplicateRestore,
		},
		{
			name: "finalize that expects a current version refuses another one and writes nothing",
			test: testFinalizeExpectingCurrent,
		},
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
		{name: "a workspace lock reads as the locked state and an unlock clears it", test: testInventoryLockedState},
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
		{
			name: "sealed flag is stored and read back by get, current and list",
			test: testSealedFlagRoundTrip,
		},
		{
			name: "sensitive marks are stored and read back by get, current and list",
			test: testSensitiveMarksRoundTrip,
		},
		{
			name: "delete hides the workspace, keeps its versions and drops its entry",
			test: testDeleteWorkspace,
		},
		{
			name: "a deleted workspace frees its name",
			test: testDeletedWorkspaceNameIsFree,
		},
		{name: "a workspace delete appends its change entry", test: testDeleteWorkspaceAppendsChangeEntry},
		{
			name: "a workspace delete with a refused entry changes nothing",
			test: testDeleteWorkspaceRefusedEntryChangesNothing,
		},
		{
			name: "a workspace delete whose audit append fails changes nothing",
			test: testDeleteWorkspaceFailedAppendChangesNothing,
		},
		{
			name: "the stored name of a deleted workspace finds nothing",
			test: testDeletedWorkspaceStoredNameFindsNothing,
		},
		{
			name: "a workspace with a pending state version refuses the delete",
			test: testDeleteWorkspaceRefusesPendingState,
		},
		{name: "a restore brings a deleted workspace back", test: testRestoreWorkspace},
		{name: "a purge removes a deleted workspace for good", test: testPurgeWorkspace},
		{name: "a sweep purge checks the retention of a workspace", test: testSweepPurgeChecksTheRetentionWorkspace},
		{name: "a purge ends the grants on the workspace", test: testPurgeWorkspaceRevokesItsGrants},
		{name: "a purge removes the context of the workspace", test: testPurgeWorkspaceDropsItsContext},
		{name: "an audit entry on a workspace gets its stable ID", test: testAppendFillsWorkspaceID},
		{name: "a workspace restore and purge race: one wins", test: testRestoreRacesPurgeWorkspace},
		{
			name: "a workspace restore or purge with the fence of another namespace changes nothing",
			test: testRestoreOrPurgeWorkspaceInAnotherNamespace,
		},
		{
			name: "fence and unlock releases the lock and makes the fence current",
			test: testFenceAndUnlock,
		},
		{
			name: "fence and unlock of a version that is not staged is ErrUploadDenied and writes nothing",
			test: testFenceAndUnlockNeedsStagedVersion,
		},
		{
			name: "fence and unlock of a version that is already finalized is ErrUploadDenied",
			test: testFenceAndUnlockRefusesFinalizedVersion,
		},
		{
			name: "a staged fence version names its content and is neither current nor in the history",
			test: testStagedFenceNamesItsContent,
		},
		{
			name: "fence and unlock of an unlocked workspace is ErrNotLocked and writes nothing",
			test: testFenceNotLocked,
		},
		{
			name: "fence and unlock with a stale revision is ErrConcurrentModification and writes nothing",
			test: testFenceStaleRevision,
		},
		{
			name: "fence and unlock with a stale fence is ErrFenced and writes nothing",
			test: testFenceFencedWrite,
		},
		{
			name: "a sealed fence version reads back sealed with its outputs",
			test: testFenceSealedRoundTrip,
		},
		{
			name: "a fence whose serial is not one more than the current serial is ErrStaleSerial",
			test: testFenceSerialMustFollow,
		},
		{
			name: "a fence whose read of the current version is stale is ErrConcurrentModification",
			test: testFenceStaleCurrent,
		},
		{
			name: "a new namespace and a new unit allow no override",
			test: testOverridesDefaultOff,
		},
		{
			name: "a namespace override switch round-trips, and a missing namespace is ErrNotFound",
			test: testNamespaceOverridesRoundTrip,
		},
		{
			name: "a unit switch with the namespace switch off refuses an unlocked and a forced declare",
			test: testNamespaceOffUnitOnIsRefused,
		},
		{
			name: "a namespace switch write with a stale fence is ErrFenced and changes nothing",
			test: testNamespaceOverridesStaleFence,
		},
		{
			name: "a unit override switch round-trips by ID and by name",
			test: testUnitOverridesRoundTrip,
		},
		{
			name: "an unlocked write finalizes on an unlocked unit and keeps its flag",
			test: testUnlockedWriteFinalizes,
		},
		{
			name: "a version without the unlocked-write flag does not finalize on an unlocked unit",
			test: testUnlockedFinalizeNeedsTheFlag,
		},
		{
			name: "an unfenced release is ErrOverrideRefused unless both forced-write switches are on",
			test: testReleaseUnfencedNeedsForcedSwitches,
		},
		{
			name: "a fenced force-unlock keeps the unit switches on",
			test: testFenceAndUnlockKeepsSwitches,
		},
		{
			name: "a forced declare is ErrOverrideRefused while the forced-write switches are off",
			test: testForcedDeclareNeedsSwitches,
		},
		{
			name: "a switch turned off before a forced write finalizes stops the finalize",
			test: testForcedFinalizeNeedsSwitches,
		},
		{
			name: "a forced write into an empty unit needs no override",
			test: testForcedIntoEmptyUnitNeedsNoOverride,
		},
		{
			name: "state appearing before a forced finalize needs the override",
			test: testForcedRaceNeedsSwitches,
		},
		{
			name: "a declare of an unlocked write is ErrOverrideRefused unless both switches are on",
			test: testUnlockedDeclareNeedsSwitches,
		},
		{
			name: "a switch turned off before an unlocked write finalizes stops the finalize",
			test: testUnlockedFinalizeNeedsSwitches,
		},
	}

	for _, tc := range slices.Concat(cases, releaseAuditCases(), lastReleaseCases(), workspacePageCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func newWorkspaceNamespace(t *testing.T, s WorkspaceStore, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
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
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Lock:        clearedLock(),
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: name},
		NamespaceID: ns.ID,
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
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
		Sealed:          true,
		RestoredFrom:    "",
		SensitiveMarks:  nil,
		FenceBy:         "",
		UnlockedWrite:   false, Origin: identity.StateOrigin{Job: "", Commit: ""},
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
	sv.Content = blob.Key(stateBlobDir(sv.Workspace) + sv.ID.String() + "/a1")
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

	same := got.Ref == want.Ref && got.NamespaceID == want.NamespaceID && got.ID == want.ID &&
		got.Revision == want.Revision && got.Overrides == want.Overrides &&
		got.CreatedAt.Equal(want.CreatedAt) && got.UpdatedAt.Equal(want.UpdatedAt) &&
		sameLock(got.Lock, want.Lock) && sameRelease(got.LastRelease, want.LastRelease)
	if !same {
		t.Fatalf("workspace = %+v, want %+v", got, want)
	}
}

// sameLock reports whether two locks are the same.
func sameLock(got, want workspace.Lock) bool {
	return got.ID == want.ID && got.Holder == want.Holder && got.Reason == want.Reason &&
		got.LockedAt.Equal(want.LockedAt)
}

// sameRelease reports whether two records of a release are the same.
func sameRelease(got, want workspace.Release) bool {
	return got.At.Equal(want.At) && got.LockID == want.LockID && got.Holder == want.Holder &&
		got.By == want.By && got.Kind == want.Kind
}

func sameStateReference(got, want workspace.StateVersion) bool {
	return got.ID == want.ID && got.Workspace == want.Workspace && got.Holder == want.Holder &&
		got.LockID == want.LockID && got.Lineage == want.Lineage && got.Status == want.Status
}

func sameStateContent(got, want workspace.StateVersion) bool {
	return got.Content == want.Content && bytes.Equal(got.Outputs, want.Outputs) &&
		got.MD5 == want.MD5 && got.Serial == want.Serial && got.Size == want.Size &&
		got.Force == want.Force && got.Sealed == want.Sealed &&
		got.Upload == want.Upload && got.RestoredFrom == want.RestoredFrom &&
		sameSensitiveMarks(got.SensitiveMarks, want.SensitiveMarks)
}

// sameSensitiveMarks reports whether two mark sets hold the same paths. An
// empty and a missing set read the same: the read masks every attribute
// value for both.
func sameSensitiveMarks(got, want workspace.SensitiveMarks) bool {
	if len(got) != len(want) {
		return false
	}

	for address, paths := range want {
		if !slices.Equal(got[address], paths) {
			return false
		}
	}

	return true
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

	ghost := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "ghost",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
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
		if got[i].Ref.Name.String() != want[i] || got[i].Ref.Namespace != ns.Name || got[i].NamespaceID != ns.ID {
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
	w := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: false})
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
			stateBlobDir(pendings[i].Workspace) + pendings[i].ID.String() + "/" + attempts[i],
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

// testInventoryLockedState checks that the entry which the store reads derives
// the locked state from the lock it carries: locked since the take time while
// the workspace is locked, and no state after the unlock.
func testInventoryLockedState(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	if got := listWorkspaceInventory(t, s, ns.Name)[0].StatesOf(); len(got) != 0 {
		t.Fatalf("states of an unlocked workspace = %v, want none", got)
	}

	locked := lockTestWorkspace(t, s, ns, w, "alice")

	entries := listWorkspaceInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries after lock = %+v, want one", entries)
	}

	states := entries[0].StatesOf()
	if len(states) != 1 || states[0].State != inventory.StateLocked || !states[0].Since.Equal(locked.Lock.LockedAt) {
		t.Fatalf("states after lock = %v, want locked since %v", states, locked.Lock.LockedAt)
	}

	unlocked := locked
	unlocked.Lock = clearedLock()
	unlocked.UpdatedAt = now().Add(2 * time.Hour)
	unlocked.Revision++

	if err := s.SaveWorkspace(t.Context(), unlocked, ns.Fence()); err != nil {
		t.Fatalf("save unlocked workspace: %v", err)
	}

	if got := listWorkspaceInventory(t, s, ns.Name)[0].StatesOf(); len(got) != 0 {
		t.Fatalf("states after unlock = %v, want none", got)
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
	w := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: false})
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

// testFinalizeDuplicateRestore proves a finalize refuses a restored version
// with ErrAlreadyRestored when the current state already restores the same
// version for the same holder, and writes nothing. A restore of another
// version still finalizes.
func testFinalizeDuplicateRestore(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	sources := make([]workspace.StateVersion, 2)

	for i := range sources {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", int64(i+1)),
		))
		sources[i] = finalizedVersion(pending, now().Add(time.Duration(i+1)*time.Hour), "v1")

		if err := finalizeRecorded(t, s, ns.Fence(), sources[i]); err != nil {
			t.Fatalf("finalize source %d: %v", i, err)
		}
	}

	restores := make([]workspace.StateVersion, 3)

	for i := range restores {
		pending := newPendingVersion(t, locked, locked.Lock.ID, forcedInput("l1", "v1", 1))
		pending.RestoredFrom = sources[0].ID

		if i == 2 {
			pending.RestoredFrom = sources[1].ID
		}

		createPendingVersion(t, s, ns, pending)
		restores[i] = finalizedVersion(pending, now().Add(time.Duration(i+3)*time.Hour), "v1")
	}

	if err := finalizeRecorded(t, s, ns.Fence(), restores[0]); err != nil {
		t.Fatalf("finalize first restore: %v", err)
	}

	err := finalizeRecorded(t, s, ns.Fence(), restores[1])
	if !errors.Is(err, workspace.ErrAlreadyRestored) {
		t.Fatalf("finalize duplicate restore: err = %v, want ErrAlreadyRestored", err)
	}

	stored, err := s.GetStateVersion(t.Context(), restores[1].ID)
	if err != nil || stored.Status != workspace.StatusPending {
		t.Fatalf("duplicate restore after the refusal: status %v, err %v; want pending", stored.Status, err)
	}

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state after the duplicate restore: %v", err)
	}

	assertStateVersion(t, current, restores[0])

	if err := finalizeRecorded(t, s, ns.Fence(), restores[2]); err != nil {
		t.Fatalf("finalize restore of another version: %v", err)
	}
}

// finalizeExpecting records the attempt key of sv and finalizes it only if the
// current state version is expect, returning the error of either step.
func finalizeExpecting(
	t *testing.T, s WorkspaceStore, fence namespace.Fence, sv workspace.StateVersion, expect workspace.StateVersionID,
) error {
	t.Helper()

	if _, err := s.RecordUploadAttempt(t.Context(), sv.ID, sv.Content, fence); err != nil {
		return err
	}

	return s.FinalizeStateVersionExpecting(t.Context(), sv, fence, expect)
}

// testFinalizeExpectingCurrent proves a finalize that names the current state
// version it expects checks it in the finalize transaction: another current
// version, also one that the same holder wrote under the same lock after the
// caller read it, gives ErrConcurrentModification and writes nothing. An empty
// expected version means the workspace has no state. The expected version
// does not replace the other checks of the finalize.
func testFinalizeExpectingCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	sources := make([]workspace.StateVersion, 3)

	for i := range sources {
		pending := createPendingVersion(t, s, ns, newPendingVersion(
			t, locked, locked.Lock.ID, stateInput("l1", "v1", int64(i+1)),
		))
		sources[i] = finalizedVersion(pending, now().Add(time.Duration(i+1)*time.Hour), "v1")
	}

	restore := newPendingVersion(t, locked, locked.Lock.ID, forcedInput("l1", "v1", 1))
	restore.RestoredFrom = sources[0].ID
	createPendingVersion(t, s, ns, restore)
	restored := finalizedVersion(restore, now().Add(10*time.Hour), "v1")

	// The workspace has no state: only the empty expected version matches.
	if err := finalizeExpecting(t, s, ns.Fence(), sources[0], sources[1].ID); !errors.Is(
		err, workspace.ErrConcurrentModification,
	) {
		t.Fatalf(
			"finalize expecting a version of a workspace with no state: err = %v, want ErrConcurrentModification",
			err,
		)
	}

	assertPendingAndNoCurrent(t, s, w.ID, sources[0].ID)

	if err := finalizeExpecting(t, s, ns.Fence(), sources[0], ""); err != nil {
		t.Fatalf("finalize expecting no state: %v", err)
	}

	if err := finalizeExpecting(t, s, ns.Fence(), sources[1], sources[0].ID); err != nil {
		t.Fatalf("finalize expecting the current version: %v", err)
	}

	// The caller read sources[1] as current. The same holder then wrote
	// sources[2] under the same lock, so the restore must not finalize.
	if err := finalizeRecorded(t, s, ns.Fence(), sources[2]); err != nil {
		t.Fatalf("finalize the later write: %v", err)
	}

	for _, expect := range []workspace.StateVersionID{sources[1].ID, ""} {
		err := finalizeExpecting(t, s, ns.Fence(), restored, expect)
		if !errors.Is(err, workspace.ErrConcurrentModification) {
			t.Fatalf("finalize expecting %q: err = %v, want ErrConcurrentModification", expect, err)
		}

		stored, getErr := s.GetStateVersion(t.Context(), restored.ID)
		if getErr != nil || stored.Status != workspace.StatusPending {
			t.Fatalf("restore after the refusal: status %v, err %v; want pending", stored.Status, getErr)
		}

		current, curErr := s.CurrentStateVersion(t.Context(), w.ID)
		if curErr != nil {
			t.Fatalf("current state after the refusal: %v", curErr)
		}

		assertStateVersion(t, current, sources[2])
	}

	finishExpectingRestore(t, s, ns, restored, sources[2])
}

// finishExpectingRestore proves that a stale fence still refuses a finalize
// that expects a current version, and that the finalize then succeeds against
// the real current version: the expected version adds a check and removes none.
func finishExpectingRestore(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, restored, current workspace.StateVersion,
) {
	t.Helper()

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := s.FinalizeStateVersionExpecting(t.Context(), restored, stale, current.ID)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("finalize expecting with a stale fence: err = %v, want ErrFenced", err)
	}

	if err = finalizeExpecting(t, s, ns.Fence(), restored, current.ID); err != nil {
		t.Fatalf("finalize expecting the current version: %v", err)
	}

	now, err := s.CurrentStateVersion(t.Context(), restored.Workspace)
	if err != nil {
		t.Fatalf("current state after the restore: %v", err)
	}

	assertStateVersion(t, now, restored)
}

// assertPendingAndNoCurrent fails t unless version id is still pending and the
// workspace has no current state.
func assertPendingAndNoCurrent(t *testing.T, s WorkspaceStore, w workspace.ID, id workspace.StateVersionID) {
	t.Helper()

	stored, err := s.GetStateVersion(t.Context(), id)
	if err != nil || stored.Status != workspace.StatusPending {
		t.Fatalf("version after the refusal: status %v, err %v; want pending", stored.Status, err)
	}

	if _, err = s.CurrentStateVersion(t.Context(), w); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("current state after the refusal: err = %v, want ErrNoState", err)
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
	return blob.Key(stateBlobDir(sv.Workspace) + sv.ID.String() + "/" + attempt)
}

// stateNamespaceID is the namespace ID in the state content keys of this suite.
// The stores keep a content key as an opaque string and never read it, so one
// fixed ID is enough; the workspace service tests check the real layout.
const stateNamespaceID = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")

// stateBlobDir returns the blob directory of the state of the workspace ws:
// namespaces/<namespace ID>/tofu/<workspace ID>/.
func stateBlobDir(ws workspace.ID) string {
	return "namespaces/" + stateNamespaceID.String() + "/tofu/" + ws.String() + "/"
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
// empty ones: another workspace stays out, a workspace without versions yields
// an empty list, and a workspace that does not exist is ErrNotFound with no
// keys. RunKeepSetRules holds the cases of the rename, the soft delete and the
// purge.
func testReferencedContentKeys(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	other := createTestWorkspace(t, s, ns, "other")
	lockedOther := lockTestWorkspace(t, s, ns, other, "alice")

	assertReferencedContentKeys(t, s, w.ID)

	if keys, err := s.ReferencedContentKeys(t.Context(), workspace.ID(ident.NewID("ws-"))); !errors.Is(
		err, workspace.ErrNotFound,
	) || len(keys) != 0 {
		t.Fatalf("referenced content keys of an unknown workspace = %v, %v; want no keys and ErrNotFound", keys, err)
	}

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

// testSealedFlagRoundTrip proves the sealed flag is stored and read back:
// a sealed version reads sealed by get, current and list.
func testSealedFlagRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	sealed := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", 1))
	createPendingVersion(t, s, ns, sealed)

	got, err := s.GetStateVersion(t.Context(), sealed.ID)
	if err != nil {
		t.Fatalf("get sealed state version: %v", err)
	}

	if !got.Sealed {
		t.Fatal("sealed state version reads unsealed, want sealed")
	}

	finalized := finalizedVersion(sealed, now().Add(2*time.Hour), "v1")

	finalizeErr := finalizeRecorded(t, s, ns.Fence(), finalized)
	if finalizeErr != nil {
		t.Fatalf("finalize sealed state: %v", finalizeErr)
	}

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	assertStateVersion(t, current, finalized)

	list, total, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{finalized})
}

// testSensitiveMarksRoundTrip proves SaveSensitiveMarks stores the secret
// paths of a version and GetStateVersion, CurrentStateVersion and
// ListStateVersions read them back, on a pending and on a finalized version.
// A missing version reports ErrStateVersionNotFound, and a stale fence
// reports ErrFenced with nothing written.
func testSensitiveMarksRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	marks := workspace.SensitiveMarks{"terraform_data.smoke": {"/input"}}

	if err := s.SaveSensitiveMarks(t.Context(), pending.ID, marks, ns.Fence()); err != nil {
		t.Fatalf("save sensitive marks of a pending version: %v", err)
	}

	want := pending
	want.SensitiveMarks = marks

	got, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version with marks: %v", err)
	}

	assertStateVersion(t, got, want)

	finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v1")
	finalized.SensitiveMarks = marks

	if finalizeErr := finalizeRecorded(t, s, ns.Fence(), finalized); finalizeErr != nil {
		t.Fatalf("finalize state version: %v", finalizeErr)
	}

	// The rendering may land after the state upload finalizes, so saving on
	// a finalized version overwrites the marks.
	updated := workspace.SensitiveMarks{"terraform_data.smoke": {"/input", "/id"}}

	if saveErr := s.SaveSensitiveMarks(t.Context(), pending.ID, updated, ns.Fence()); saveErr != nil {
		t.Fatalf("save sensitive marks of a finalized version: %v", saveErr)
	}

	finalized.SensitiveMarks = updated

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	assertStateVersion(t, current, finalized)

	list, total, err := s.ListStateVersions(t.Context(), w.ID, workspace.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	assertHistory(t, list, total, []workspace.StateVersion{finalized})

	missing := workspace.StateVersionID(ident.NewID("sv-"))

	if missingErr := s.SaveSensitiveMarks(t.Context(), missing, marks, ns.Fence()); !errors.Is(
		missingErr, workspace.ErrStateVersionNotFound,
	) {
		t.Fatalf("save marks of a missing version: err = %v, want ErrStateVersionNotFound", missingErr)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if fenceErr := s.SaveSensitiveMarks(t.Context(), pending.ID, marks, stale); !errors.Is(
		fenceErr, namespace.ErrFenced,
	) {
		t.Fatalf("save marks with a stale fence: err = %v, want ErrFenced", fenceErr)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version after a fenced save: %v", err)
	}

	assertStateVersion(t, stored, finalized)
}

// testDeleteWorkspace proves the soft delete (ADR 47): a locked workspace
// refuses it, and after the unlock the workspace leaves every lookup by name
// or ID, every list and the inventory, while its state versions stay. A
// second delete and a delete of a missing workspace report ErrNotFound, and
// a stale fence refuses it.
func testDeleteWorkspace(t *testing.T, s WorkspaceStore) {
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

	err := s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		workspace.ErrLocked,
	) {
		t.Fatalf("delete a locked workspace: err = %v, want ErrLocked", err)
	}

	unlockTestWorkspace(t, s, ns, w.Ref)

	err = s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	if _, err = s.GetWorkspace(t.Context(), w.Ref); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get deleted workspace: err = %v, want ErrNotFound", err)
	}

	if _, err = s.GetWorkspaceByID(t.Context(), w.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get deleted workspace by ID: err = %v, want ErrNotFound", err)
	}

	if _, err = s.GetStateVersion(t.Context(), pending.ID); err != nil {
		t.Fatalf("get a version of a deleted workspace: %v, want it kept for a restore", err)
	}

	if entries := listWorkspaceInventory(t, s, ns.Name); len(entries) != 0 {
		t.Fatalf("inventory entries after delete = %+v, want none", entries)
	}

	err = s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		workspace.ErrNotFound,
	) {
		t.Fatalf("delete a deleted workspace: err = %v, want ErrNotFound", err)
	}

	ghost := workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, "ghost")}
	err = s.DeleteWorkspace(
		t.Context(),
		ghost,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		workspace.ErrNotFound,
	) {
		t.Fatalf("delete a missing workspace: err = %v, want ErrNotFound", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	other := createTestWorkspace(t, s, ns, "other")

	err = s.DeleteWorkspace(
		t.Context(),
		other.Ref,
		stale,
		inventory.Deletion{Entry: deleteEntry(stale.Namespace), PurgeAfter: purgeAt()},
	)
	if !errors.Is(
		err,
		namespace.ErrFenced,
	) {
		t.Fatalf("delete with a stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetWorkspace(t.Context(), other.Ref); err != nil {
		t.Fatalf("get workspace after a fenced delete: %v", err)
	}
}

// testDeletedWorkspaceNameIsFree proves a new workspace can take the name of a
// deleted one at once, with a new ID and no state, and that the lists and the
// inventory show only live workspaces under their own names. The deleted
// units hold the deleted workspace (ADR 47).
func testDeletedWorkspaceNameIsFree(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	gone := createTestWorkspace(t, s, ns, "network")

	err := s.DeleteWorkspace(
		t.Context(),
		gone.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	again := createTestWorkspace(t, s, ns, "network")
	if again.ID == gone.ID {
		t.Fatalf("new workspace ID = %q, want a new ID", again.ID)
	}

	if _, err = s.CurrentStateVersion(t.Context(), again.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Fatalf("current state of the new workspace: err = %v, want ErrNoState", err)
	}

	list, err := s.ListWorkspaces(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}

	if len(list) != 1 || list[0].ID != again.ID || strings.HasPrefix(list[0].Ref.Name.String(), "deleted:") {
		t.Fatalf("workspaces = %+v, want only %s", list, again.ID)
	}

	entries := listWorkspaceInventory(t, s, ns.Name)
	for i := range entries {
		if entries[i].UnitID == gone.ID.String() || strings.HasPrefix(entries[i].Name, "deleted:") {
			t.Fatalf("inventory entry %+v, want no entry of the deleted workspace", entries[i])
		}
	}

	checkDeletedWorkspaceEntry(t, s, ns.Name, gone)
}

// checkDeletedWorkspaceEntry checks that the deleted units of ns hold exactly
// one entry: the deleted workspace gone, under its name before the delete,
// with the delete time and the end of the retention (ADR 47).
func checkDeletedWorkspaceEntry(t *testing.T, s WorkspaceStore, ns namespace.Name, gone workspace.Workspace) {
	t.Helper()

	deleted, err := s.ListDeletedUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list deleted units: %v", err)
	}

	if len(deleted) != 1 {
		t.Fatalf("deleted units = %+v, want 1", deleted)
	}

	got := deleted[0]
	if got.Tool != "opentofu" || got.UnitID != gone.ID.String() || got.Name != gone.Ref.Name.String() ||
		got.Namespace != ns || !got.DeletedAt.Equal(deletedAt()) ||
		!got.PurgeAfter.Equal(deletedAt().Add(inventory.DeletedRetention)) {
		t.Fatalf("deleted unit = %+v, want opentofu %s (%s) deleted at %v", got, gone.Ref.Name, gone.ID, deletedAt())
	}
}

// unlockTestWorkspace clears the lock of the workspace called ref.
func unlockTestWorkspace(t *testing.T, s WorkspaceStore, ns namespace.Namespace, ref workspace.Ref) {
	t.Helper()

	current, err := s.GetWorkspace(t.Context(), ref)
	if err != nil {
		t.Fatalf("get workspace %s: %v", ref, err)
	}

	current.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	current.Revision++

	if err := s.SaveWorkspace(t.Context(), current, ns.Fence()); err != nil {
		t.Fatalf("unlock workspace %s: %v", ref, err)
	}
}

// releasedWorkspace returns locked as the caller passes it to FenceAndUnlock:
// the lock cleared and the revision one on.
func releasedWorkspace(locked workspace.Workspace, at time.Time) workspace.Workspace {
	released := locked
	released.Lock = clearedLock()
	released.UpdatedAt = at
	released.Revision++

	return released
}

// newFenceVersion returns a finalized fence version of w that FenceAndUnlock
// may make current. It has the new lineage, the serial and the principal that
// forced the unlock.
func newFenceVersion(t *testing.T, w workspace.Workspace, serial int64, at time.Time) workspace.StateVersion {
	t.Helper()

	const content = "fence"

	sum := md5.Sum([]byte(content)) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	return workspace.StateVersion{
		CreatedAt:       at,
		FinalizedAt:     at,
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		ID:              workspace.StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          "",
		LockID:          "",
		Lineage:         "lin-b",
		Status:          workspace.StatusFinalized,
		Content:         blob.Key(stateBlobDir(w.ID) + "fence/a1"),
		Outputs:         nil,
		MD5:             sum,
		Serial:          serial,
		Size:            int64(len(content)),
		Force:           false,
		Sealed:          true,
		RestoredFrom:    "",
		SensitiveMarks:  nil,
		FenceBy:         "admin",
		UnlockedWrite:   false, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// stageFence stores fence as a pending version that names its content, as the
// force-unlock does before it writes the content. FenceAndUnlock finalizes it.
func stageFence(t *testing.T, s WorkspaceStore, ns namespace.Namespace, fence workspace.StateVersion) {
	t.Helper()

	pending := fence
	pending.Status = workspace.StatusPending
	pending.FinalizedAt = time.Time{}
	pending.UploadExpiresAt = fence.CreatedAt.Add(workspace.UploadWindow)

	if err := s.CreateStateVersion(t.Context(), pending, ns.Fence(), generousPendingCap); err != nil {
		t.Fatalf("stage fence version %s: %v", fence.ID, err)
	}
}

// lockedWithState returns a workspace locked by "ops" that holds one finalized
// version, lineage "lin-a" and serial 1, as the current state.
func lockedWithState(
	t *testing.T,
	s WorkspaceStore,
	ns namespace.Namespace,
) (workspace.Workspace, workspace.StateVersion) {
	t.Helper()

	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "ops")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("lin-a", "one", 1),
	))
	v1 := finalizedVersion(pending, now().Add(time.Hour), "one")

	if err := finalizeRecorded(t, s, ns.Fence(), v1); err != nil {
		t.Fatalf("finalize v1: %v", err)
	}

	return locked, v1
}

func testFenceAndUnlock(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, v1 := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, locked.ID),
		ns.Fence(),
		releaseEntry(ns.Name),
	)
	if err != nil {
		t.Fatalf("fence and unlock: %v", err)
	}

	assertFenceReleased(t, s, released)
	assertFenceCurrent(t, s, fence)
	assertFenceHistory(t, s, locked.ID, fence, v1)
}

// assertFenceReleased checks that the workspace holds the released state.
func assertFenceReleased(t *testing.T, s WorkspaceStore, released workspace.Workspace) {
	t.Helper()

	got, err := s.GetWorkspaceByID(t.Context(), released.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, released)
}

// assertFenceCurrent checks that the fence is the current version, with its
// lineage, serial and principal, and that it reads back by ID.
func assertFenceCurrent(t *testing.T, s WorkspaceStore, fence workspace.StateVersion) {
	t.Helper()

	cur, err := s.CurrentStateVersion(t.Context(), fence.Workspace)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if cur.ID != fence.ID || cur.Lineage != "lin-b" || cur.Serial != 2 || cur.FenceBy != "admin" {
		t.Fatalf("current = %s lineage %q serial %d fence by %q, want the fence",
			cur.ID, cur.Lineage, cur.Serial, cur.FenceBy)
	}

	stored, err := s.GetStateVersion(t.Context(), fence.ID)
	if err != nil {
		t.Fatalf("get fence version: %v", err)
	}

	if stored.FenceBy != "admin" || stored.Status != workspace.StatusFinalized {
		t.Fatalf("stored fence by %q status %s, want admin and finalized", stored.FenceBy, stored.Status)
	}
}

// assertFenceHistory checks that the history lists the fence, then v1.
func assertFenceHistory(
	t *testing.T, s WorkspaceStore, id workspace.ID, fence, v1 workspace.StateVersion,
) {
	t.Helper()

	list, total, err := s.ListStateVersions(t.Context(), id, workspace.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != 2 || len(list) != 2 || list[0].ID != fence.ID || list[1].ID != v1.ID {
		t.Fatalf("history total %d, %d versions, want the fence then v1", total, len(list))
	}
}

func testFenceNotLocked(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	released := releasedWorkspace(w, now().Add(time.Hour))
	fence := newFenceVersion(t, w, 1, now().Add(time.Hour))
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, w.ID),
		ns.Fence(),
		releaseEntry(ns.Name),
	)
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("fence an unlocked workspace: err = %v, want ErrNotLocked", err)
	}

	got, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, w)

	if _, curErr := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a refused fence: err = %v, want ErrNoState", curErr)
	}
}

func testFenceStaleRevision(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)

	stale := locked
	stale.Revision += 3
	released := releasedWorkspace(stale, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, locked.ID),
		ns.Fence(),
		releaseEntry(ns.Name),
	)
	if !errors.Is(err, workspace.ErrConcurrentModification) {
		t.Fatalf("fence with a stale revision: err = %v, want ErrConcurrentModification", err)
	}

	got, getErr := s.GetWorkspaceByID(t.Context(), locked.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	assertWorkspace(t, got, locked)

	cur, curErr := s.CurrentStateVersion(t.Context(), locked.ID)
	if curErr != nil || cur.Lineage != "lin-a" {
		t.Fatalf("current after a refused fence: lineage %q, err %v, want lin-a", cur.Lineage, curErr)
	}
}

func testFenceFencedWrite(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, locked.ID),
		stale,
		releaseEntry(stale.Namespace),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("fence with a stale fence: err = %v, want ErrFenced", err)
	}

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, locked)

	stored, getErr := s.GetStateVersion(t.Context(), fence.ID)
	if getErr != nil || stored.Status != workspace.StatusPending {
		t.Fatalf("stored fence after a stale fence: status %v, err %v; want pending", stored.Status, getErr)
	}
}

// testFenceAndUnlockNeedsStagedVersion proves FenceAndUnlock finalizes only a
// pending fence version that names the fence content: a version that was
// never stored, or that names other content, is ErrUploadDenied, and the lock
// and the current version stay.
func testFenceAndUnlockNeedsStagedVersion(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, v1 := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))

	unstaged := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))

	staged := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, staged)

	other := staged
	other.Content = blob.Key(stateBlobDir(locked.ID) + "fence/other")

	refused := []workspace.StateVersion{unstaged, other}

	for i := range refused {
		err := s.FenceAndUnlock(t.Context(), released, refused[i], v1.ID, ns.Fence(), releaseEntry(ns.Name))
		if !errors.Is(err, workspace.ErrUploadDenied) {
			t.Fatalf("fence of %s naming %s: err = %v, want ErrUploadDenied", refused[i].ID, refused[i].Content, err)
		}
	}

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, locked)

	if id := currentVersionID(t, s, locked.ID); id != v1.ID {
		t.Fatalf("current version after refused fences = %s, want %s", id, v1.ID)
	}
}

// testFenceAndUnlockRefusesFinalizedVersion proves FenceAndUnlock refuses a
// fence that names a version that is already finalized, also with its own
// content key: only a pending row finalizes. The lock and the current version
// stay.
func testFenceAndUnlockRefusesFinalizedVersion(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, v1 := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))

	again := v1
	again.Lineage = "lin-b"
	again.Serial = v1.Serial + 1
	again.FinalizedAt = now().Add(2 * time.Hour)
	again.FenceBy = "admin"

	err := s.FenceAndUnlock(t.Context(), released, again, v1.ID, ns.Fence(), releaseEntry(ns.Name))
	if !errors.Is(err, workspace.ErrUploadDenied) {
		t.Fatalf("fence naming the finalized version %s: err = %v, want ErrUploadDenied", v1.ID, err)
	}

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, locked)

	stored, err := s.GetStateVersion(t.Context(), v1.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	assertStateVersion(t, stored, v1)
}

// testStagedFenceNamesItsContent proves a staged fence version names its
// content key before FenceAndUnlock runs, so a sweep keeps the blob, and that
// the staged version is not current and is not in the history.
func testStagedFenceNamesItsContent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, v1 := lockedWithState(t, s, ns)
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)

	refs, err := s.ReferencedContentKeys(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("referenced content keys: %v", err)
	}

	if !slices.Contains(refs, fence.Content) {
		t.Fatalf("referenced keys %v, want the staged fence key %s", refs, fence.Content)
	}

	if id := currentVersionID(t, s, locked.ID); id != v1.ID {
		t.Fatalf("current version with a staged fence = %s, want %s", id, v1.ID)
	}

	_, total, err := s.ListStateVersions(t.Context(), locked.ID, workspace.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != 1 {
		t.Fatalf("history with a staged fence holds %d versions, want 1", total)
	}
}

func testFenceSealedRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	fence.Sealed = true
	fence.Outputs = []byte("sealed-outputs")
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, locked.ID),
		ns.Fence(),
		releaseEntry(ns.Name),
	)
	if err != nil {
		t.Fatalf("fence and unlock of a sealed version: %v", err)
	}

	stored, err := s.GetStateVersion(t.Context(), fence.ID)
	if err != nil {
		t.Fatalf("get sealed fence: %v", err)
	}

	if !stored.Sealed || string(stored.Outputs) != "sealed-outputs" || stored.FenceBy != "admin" {
		t.Fatalf("stored sealed=%t outputs %q fence by %q, want sealed, the outputs and admin",
			stored.Sealed, stored.Outputs, stored.FenceBy)
	}
}

func testFenceSerialMustFollow(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))

	for _, serial := range []int64{1, 3} {
		fence := newFenceVersion(t, locked, serial, now().Add(2*time.Hour))
		stageFence(t, s, ns, fence)

		err := s.FenceAndUnlock(
			t.Context(),
			released,
			fence,
			currentVersionID(t, s, locked.ID),
			ns.Fence(),
			releaseEntry(ns.Name),
		)
		if !errors.Is(err, workspace.ErrStaleSerial) {
			t.Fatalf("fence at serial %d: err = %v, want ErrStaleSerial", serial, err)
		}
	}

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, locked)

	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)
	err = s.FenceAndUnlock(
		t.Context(),
		released,
		fence,
		currentVersionID(t, s, locked.ID),
		ns.Fence(),
		releaseEntry(ns.Name),
	)
	if err != nil {
		t.Fatalf("fence at serial 2: %v", err)
	}
}

// testFenceStaleCurrent checks the identity check of a fence: a finalize that
// lands after the caller read the current version refuses the fence, and the
// fence writes nothing.
func testFenceStaleCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, v1 := lockedWithState(t, s, ns)

	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("lin-a", "two", 2),
	))
	v2 := finalizedVersion(pending, now().Add(2*time.Hour), "two")

	if err := finalizeRecorded(t, s, ns.Fence(), v2); err != nil {
		t.Fatalf("finalize v2: %v", err)
	}

	// The finalize stamped the workspace, so the caller's copy is the one
	// the fence must not change.
	before, getErr := s.GetWorkspaceByID(t.Context(), locked.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	released := releasedWorkspace(before, now().Add(3*time.Hour))
	fence := newFenceVersion(t, locked, 3, now().Add(3*time.Hour))
	stageFence(t, s, ns, fence)

	err := s.FenceAndUnlock(t.Context(), released, fence, v1.ID, ns.Fence(), releaseEntry(ns.Name))
	if !errors.Is(err, workspace.ErrConcurrentModification) {
		t.Fatalf("fence after a finalize: err = %v, want ErrConcurrentModification", err)
	}

	got, getErr := s.GetWorkspaceByID(t.Context(), locked.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	assertWorkspace(t, got, before)

	cur, curErr := s.CurrentStateVersion(t.Context(), locked.ID)
	if curErr != nil || cur.ID != v2.ID {
		t.Fatalf("current after a refused fence: %s, err %v, want v2 %s", cur.ID, curErr, v2.ID)
	}
}

// currentVersionID returns the ID of the current version of id, or the empty ID
// when the workspace has no state. It reads the version the way a caller does
// before it fences.
func currentVersionID(t *testing.T, s WorkspaceStore, id workspace.ID) workspace.StateVersionID {
	t.Helper()

	cur, err := s.CurrentStateVersion(t.Context(), id)
	if errors.Is(err, workspace.ErrNoState) {
		return ""
	}

	if err != nil {
		t.Fatalf("current state of %s: %v", id, err)
	}

	return cur.ID
}

// placeholderVersion returns the row a streamed write creates before its
// first byte: serial -1, an empty lineage and an all-zero MD5. The row stays
// pending until its finalize sets the declared fields (ADR 49, decision 7).
func placeholderVersion(t *testing.T, w workspace.Workspace) workspace.StateVersion {
	t.Helper()

	sv := newPendingVersion(t, w, w.Lock.ID, stateInput("", "", -1))
	sv.MD5 = [md5.Size]byte{}

	return sv
}

// testFinalizeSetsDeclaredFields proves a finalize sets the serial, lineage,
// MD5 and outputs that the streamed write declares, in the same transaction as
// the status change.
func testFinalizeSetsDeclaredFields(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	pending := createPendingVersion(t, s, ns, placeholderVersion(t, locked))

	declared := finalizedVersion(pending, now().Add(time.Hour), "v1")
	declared.Serial = 1
	declared.Lineage = "lin-1"
	declared.MD5 = md5.Sum([]byte("v1")) //nolint:gosec // The TFE protocol names state by MD5 for integrity.
	declared.Outputs = []byte(`{"greeting":{"value":"hello"}}`)

	if err := finalizeRecorded(t, s, ns.Fence(), declared); err != nil {
		t.Fatalf("finalize with the declared fields: %v", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get finalized state version: %v", err)
	}

	if stored.Status != workspace.StatusFinalized || stored.Serial != 1 || stored.Lineage != "lin-1" ||
		stored.MD5 != declared.MD5 || !bytes.Equal(stored.Outputs, declared.Outputs) {
		t.Fatalf("finalized row = status %s, serial %d, lineage %q, outputs %s; want the declared fields",
			stored.Status, stored.Serial, stored.Lineage, stored.Outputs)
	}

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil || current.ID != pending.ID {
		t.Fatalf("current state = %v, %v; want the finalized version", current.ID, err)
	}
}

// testPlaceholderNeverCurrent proves a pending placeholder row is not current,
// and does not replace the finalized version that is current.
func testPlaceholderNeverCurrent(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	first := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("lin-1", "v1", 1),
	))

	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(first, now().Add(time.Hour), "v1")); err != nil {
		t.Fatalf("finalize the first version: %v", err)
	}

	placeholder := createPendingVersion(t, s, ns, placeholderVersion(t, locked))

	current, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state with a placeholder pending: %v", err)
	}

	if current.ID != first.ID || current.Serial != 1 {
		t.Fatalf("current state = %s serial %d, want the first version serial 1", current.ID, current.Serial)
	}

	stored, err := s.GetStateVersion(t.Context(), placeholder.ID)
	if err != nil {
		t.Fatalf("get placeholder: %v", err)
	}

	if stored.Status != workspace.StatusPending {
		t.Fatalf("placeholder status = %s, want pending", stored.Status)
	}
}

// testFinalizeStaleFenceKeepsPlaceholder proves a finalize under a stale
// namespace fence is refused, and leaves the placeholder row pending with its
// placeholder fields.
func testFinalizeStaleFenceKeepsPlaceholder(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	pending := createPendingVersion(t, s, ns, placeholderVersion(t, locked))

	declared := finalizedVersion(pending, now().Add(time.Hour), "v1")
	declared.Serial = 1
	declared.Lineage = "lin-1"

	if _, err := s.RecordUploadAttempt(t.Context(), declared.ID, declared.Content, ns.Fence()); err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	if err := s.FinalizeStateVersion(t.Context(), declared, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("finalize with a stale fence: err = %v, want ErrFenced", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get placeholder after a refused finalize: %v", err)
	}

	if stored.Status != workspace.StatusPending || stored.Serial != -1 || stored.Lineage != "" {
		t.Fatalf("placeholder after a refused finalize = status %s, serial %d, lineage %q; want pending, -1, empty",
			stored.Status, stored.Serial, stored.Lineage)
	}
}

// testFinalizeSameLockIDOtherHolder proves a finalize checks the holder as well
// as the lock ID. A client-chosen lock ID can repeat after a force-unlock, so a
// version declared by alice under lock X finalizes against nothing when bob holds
// lock X.
func testFinalizeSameLockIDOtherHolder(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	taken := locked
	taken.Lock = workspace.Lock{LockedAt: now().Add(time.Hour), ID: locked.Lock.ID, Holder: "bob", Reason: "apply"}
	taken.UpdatedAt = now().Add(2 * time.Hour)
	taken.Revision++

	if err := s.SaveWorkspace(t.Context(), taken, ns.Fence()); err != nil {
		t.Fatalf("take the same lock ID as another principal: %v", err)
	}

	finalized := finalizedVersion(pending, now().Add(3*time.Hour), "v1")
	if err := finalizeRecorded(t, s, ns.Fence(), finalized); !errors.Is(err, workspace.ErrLockedByOther) {
		t.Errorf("finalize under the same lock ID, other holder: err = %v, want ErrLockedByOther", err)
	}

	if _, err := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(err, workspace.ErrNoState) {
		t.Errorf("a refused finalize made a version current: %v", err)
	}
}

// testDiscardStateVersion proves a discard removes a pending row, and that a
// finalized row, or one that does not exist, stays or matches nothing.
func testDiscardStateVersion(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))

	if err := s.DiscardStateVersion(t.Context(), pending.ID, ns.Fence()); err != nil {
		t.Fatalf("discard pending: %v", err)
	}

	if _, err := s.GetStateVersion(t.Context(), pending.ID); !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("discarded pending version: err = %v, want ErrStateVersionNotFound", err)
	}

	if err := s.DiscardStateVersion(t.Context(), pending.ID, ns.Fence()); err != nil {
		t.Fatalf("discard of a missing version: %v", err)
	}

	second := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v2", 2),
	))

	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(second, now().Add(time.Hour), "v2")); err != nil {
		t.Fatalf("finalize second: %v", err)
	}

	if err := s.DiscardStateVersion(t.Context(), second.ID, ns.Fence()); err != nil {
		t.Fatalf("discard of a finalized version: %v", err)
	}

	stored, err := s.GetStateVersion(t.Context(), second.ID)
	if err != nil || stored.Status != workspace.StatusFinalized {
		t.Fatalf("finalized version after a discard: status %v, err %v; want finalized and kept", stored.Status, err)
	}
}

// testDiscardStaleFence proves a discard under a stale fence is refused with
// ErrFenced, and the pending row stays.
func testDiscardStaleFence(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, stateInput("l1", "v1", 1),
	))
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.DiscardStateVersion(t.Context(), pending.ID, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("discard under a stale fence: err = %v, want ErrFenced", err)
	}

	stored, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil || stored.Status != workspace.StatusPending {
		t.Fatalf(
			"pending version after a refused discard: status %v, err %v; want pending and kept",
			stored.Status,
			err,
		)
	}
}

// serviceOver returns the namespace and workspace services over s, with blobs in
// a temporary directory. The conditional unlock cases drive the services.
func serviceOver(t *testing.T, s WorkspaceStore) *workspace.Service {
	t.Helper()

	return servicesOver(t, s, 1)[0]
}

// servicesOver returns n workspace services over s that share one blob store
// in a temporary directory. Each service has its own in-process lock stripes,
// like the services of n instances over one database.
func servicesOver(t *testing.T, s WorkspaceStore, n int) []*workspace.Service {
	t.Helper()

	nsSvc, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
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

	services := make([]*workspace.Service, n)
	sealer := testSealer(t, nsSvc)

	// The cases build every blob key inside the scope of its namespace, so
	// no service records an out-of-scope refusal.
	refusals := workspacetest.NewRefusals()

	t.Cleanup(func() {
		if got := refusals.All(); len(got) != 0 {
			t.Errorf("out-of-scope refusals = %+v, want none", got)
		}
	})

	for i := range services {
		svc, err := workspace.NewService(workspace.Config{
			ManualLocks:        nil,
			BlobGuard:          blobguardtest.None{},
			Workspaces:         s,
			States:             s,
			Blobs:              blobs,
			Namespaces:         nsSvc,
			OutOfScope:         refusals,
			Sealer:             sealer,
			Logger:             nil,
			Now:                now,
			MaxStateBytes:      0,
			MaxPendingVersions: 0,
			Retention:          0,
		})
		if err != nil {
			t.Fatalf("workspace service: %v", err)
		}

		services[i] = svc
	}

	return services
}

// testSealer returns a sealer with a fixed key-encryption key, so the
// workspace services of the conformance suite seal state.
func testSealer(t *testing.T, namespaces *namespace.Service) *secret.Service {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys:          newTestKeys(),
		Wrapper:       wrapper,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}

	return secrets
}

// testKeys is a secret.KeyStore in memory for the conformance suite.
type testKeys struct {
	mu   sync.Mutex
	keys map[string]secret.WrappedKey
}

// newTestKeys returns an empty key store.
func newTestKeys() *testKeys {
	return &testKeys{mu: sync.Mutex{}, keys: map[string]secret.WrappedKey{}}
}

// CreateKey implements secret.KeyStore, keeping the first key of a scope.
func (m *testKeys) CreateKey(
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

// GetKey implements secret.KeyStore.
func (m *testKeys) GetKey(_ context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.keys[scope.String()]
	if !ok {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	return k, nil
}

// ListWrapperIDs implements secret.KeyStore.
func (m *testKeys) ListWrapperIDs(_ context.Context) ([]string, error) {
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

// testRestoreAcrossServicesWritesOnce proves that concurrent restores of one
// version through two services over one store write one restored version:
// every caller gets that version, and the history grows by one. The services
// share no lock stripe, so only the store can refuse the duplicate.
func testRestoreAcrossServicesWritesOnce(t *testing.T, s WorkspaceStore) {
	t.Helper()

	services := servicesOver(t, s, 2)
	ctx := t.Context()
	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	if _, err := services[0].Lock(ctx, w.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	first := pushThrough(t, services[0], w.ID, 1, "{\"v\":1}")
	pushThrough(t, services[0], w.ID, 2, "{\"v\":2}")

	const callers = 8

	ids := make([]workspace.StateVersionID, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Go(func() {
			restored, err := services[i%len(services)].Restore(ctx, w.ID, first.ID, "alice")
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

	_, total, err := s.ListStateVersions(ctx, w.ID, workspace.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != 3 {
		t.Fatalf("history holds %d versions, want 3: two pushes and one restore", total)
	}
}

// pushThrough declares and uploads one state version through svc as alice,
// who must hold the lock, and returns the finalized version.
func pushThrough(
	t *testing.T, svc *workspace.Service, id workspace.ID, serial int64, content string,
) workspace.StateVersion {
	t.Helper()

	sum := md5.Sum([]byte(content)) //nolint:gosec // The TFE protocol names state by MD5 for integrity.
	in := workspace.NewState{
		Lineage: "l1", MD5: hex.EncodeToString(sum[:]), Outputs: nil, Serial: serial, Force: false,
	}

	pending, err := svc.BeginState(t.Context(), id, "alice", in, workspace.Authority{
		Allow:  namespace.Overrides{Forced: false, Unlocked: false},
		Record: nil,
	})
	if err != nil {
		t.Fatalf("begin serial %d: %v", serial, err)
	}

	sv, err := svc.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload serial %d: %v", serial, err)
	}

	return sv
}

// testConditionalUnlock proves UnlockLock and ForceUnlockLock release only the
// lock they name: the lock ID and, for a force-unlock, the holder must match. A
// lock ID a client chose can repeat after a force-unlock, so the holder decides
// (ADR 49, decision 6).
func testConditionalUnlock(t *testing.T, s WorkspaceStore) {
	t.Helper()

	svc := serviceOver(t, s)
	ctx := t.Context()
	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	if _, err := svc.LockWithID(ctx, w.ID, "bob", "apply", "lock-x"); err != nil {
		t.Fatalf("lock as bob: %v", err)
	}

	if _, err := svc.UnlockLock(ctx, w.ID, "lock-y", "bob"); !errors.Is(err, workspace.ErrLockMismatch) {
		t.Fatalf("unlock with another lock ID: err = %v, want ErrLockMismatch", err)
	}

	_, _, err := svc.ForceUnlockLock(
		ctx,
		w.ID,
		workspace.Lock{LockedAt: time.Time{}, ID: "lock-x", Holder: "alice", Reason: ""},
		"admin",
		namespace.Overrides{Forced: true, Unlocked: false},
		nil,
		releaseEntry(ns.Name),
	)
	if !errors.Is(err, workspace.ErrLockMismatch) {
		t.Fatalf("force-unlock naming another holder: err = %v, want ErrLockMismatch", err)
	}

	held, err := s.GetWorkspaceByID(ctx, w.ID)
	if err != nil || held.Lock.Holder != "bob" {
		t.Fatalf("after refused releases: holder %q, err %v; want bob still holding", held.Lock.Holder, err)
	}

	_, _, err = svc.ForceUnlockLock(
		ctx,
		w.ID,
		workspace.Lock{LockedAt: time.Time{}, ID: "lock-x", Holder: "bob", Reason: ""},
		"admin",
		namespace.Overrides{Forced: true, Unlocked: false},
		nil,
		releaseEntry(ns.Name),
	)
	if err != nil {
		t.Fatalf("force-unlock of the lock as seen: %v", err)
	}

	if _, lockErr := svc.LockWithID(ctx, w.ID, "carol", "apply", "lock-x"); lockErr != nil {
		t.Fatalf("lock as carol with the same ID: %v", lockErr)
	}

	// A caller that read bob's lock before the force-unlock must not release
	// carol's lock, though the lock ID repeats.
	_, _, err = svc.ForceUnlockLock(
		ctx,
		w.ID,
		workspace.Lock{LockedAt: time.Time{}, ID: "lock-x", Holder: "bob", Reason: ""},
		"admin",
		namespace.Overrides{Forced: true, Unlocked: false},
		nil,
		releaseEntry(ns.Name),
	)
	if !errors.Is(err, workspace.ErrLockMismatch) {
		t.Fatalf("stale force-unlock of bob's lock: err = %v, want ErrLockMismatch", err)
	}

	after, err := s.GetWorkspaceByID(ctx, w.ID)
	if err != nil || after.Lock.Holder != "carol" {
		t.Fatalf("after the stale force-unlock: holder %q, err %v; want carol still holding", after.Lock.Holder, err)
	}
}
