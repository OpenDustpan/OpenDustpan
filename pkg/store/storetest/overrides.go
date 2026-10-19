package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// testOverridesDefaultOff checks that a namespace and a unit allow no
// override until a switch is set (ADR 44, decision 6).
func testOverridesDefaultOff(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	gotNS, err := s.GetNamespace(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if gotNS.Overrides != (namespace.Overrides{Forced: false, Unlocked: false}) {
		t.Fatalf("namespace overrides = %+v, want all off", gotNS.Overrides)
	}

	gotW, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if gotW.Overrides != (namespace.Overrides{Forced: false, Unlocked: false}) {
		t.Fatalf("unit overrides = %+v, want all off", gotW.Overrides)
	}
}

// testNamespaceOverridesRoundTrip checks that a namespace switch is stored and
// read back, and that a missing namespace is ErrNotFound.
func testNamespaceOverridesRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	want := namespace.Overrides{Forced: true, Unlocked: false}

	if err := s.SetOverrides(t.Context(), ns.Name, want, ns.Fence()); err != nil {
		t.Fatalf("set namespace overrides: %v", err)
	}

	got, err := s.GetNamespace(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.Overrides != want {
		t.Fatalf("namespace overrides = %+v, want %+v", got.Overrides, want)
	}

	err = s.SetOverrides(
		t.Context(),
		namespace.Name("missing"),
		want,
		namespace.Fence{Namespace: namespace.Name("missing"), FailoverVersion: 1},
	)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("set overrides of a missing namespace: err = %v, want ErrNotFound", err)
	}
}

// testUnitOverridesRoundTrip checks that a unit switch is saved with the
// fence of its namespace and read back by ID and by name.
func testUnitOverridesRoundTrip(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	updated := w
	updated.Overrides = namespace.Overrides{Forced: false, Unlocked: true}
	updated.Revision = w.Revision + 1
	updated.UpdatedAt = now().Add(time.Hour)

	if err := s.SaveWorkspace(t.Context(), updated, ns.Fence()); err != nil {
		t.Fatalf("save unit overrides: %v", err)
	}

	byID, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace by ID: %v", err)
	}

	assertWorkspace(t, byID, updated)

	if byID.Overrides != updated.Overrides {
		t.Fatalf("unit overrides by ID = %+v, want %+v", byID.Overrides, updated.Overrides)
	}

	byName, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get workspace by name: %v", err)
	}

	if byName.Overrides != updated.Overrides {
		t.Fatalf("unit overrides by name = %+v, want %+v", byName.Overrides, updated.Overrides)
	}
}

// testUnlockedWriteFinalizes checks that a version declared under the
// unlocked-write override finalizes on an unlocked unit, and that the flag
// survives the store. A version without the flag is ErrNotLocked there.
func testUnlockedWriteFinalizes(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := turnOnUnlockedSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"))

	in := newPendingVersion(t, w, "", stateInput("l1", "v1", 1))
	in.Holder = "ci"
	in.UnlockedWrite = true
	pending := createPendingVersion(t, s, ns, in)

	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(pending, now().Add(time.Hour), "v1")); err != nil {
		t.Fatalf("finalize an unlocked write: %v", err)
	}

	got, err := s.GetStateVersion(t.Context(), pending.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if !got.UnlockedWrite {
		t.Fatal("unlocked write flag = false after finalize, want true")
	}

	cur, err := s.CurrentStateVersion(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if cur.ID != pending.ID {
		t.Fatalf("current = %s, want the unlocked write %s", cur.ID, pending.ID)
	}
}

// testUnlockedFinalizeNeedsTheFlag checks that a version declared without the
// unlocked-write flag does not finalize on an unlocked unit.
func testUnlockedFinalizeNeedsTheFlag(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	pending := createPendingVersion(t, s, ns, newPendingVersion(t, w, "", stateInput("l1", "v1", 1)))

	err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(pending, now().Add(time.Hour), "v1"))
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("finalize without the flag on an unlocked unit: err = %v, want ErrNotLocked", err)
	}

	if _, curErr := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a refused finalize: err = %v, want ErrNoState", curErr)
	}
}

// testUnlockedDeclareNeedsSwitches checks that the store refuses a declare of an
// unlocked write when the namespace or the unit switch is off, so the check
// does not depend on the service that read the switches (ADR 44, decision 6).
func testUnlockedDeclareNeedsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	in := newPendingVersion(t, w, "", stateInput("l1", "v1", 1))
	in.Holder = "ci"
	in.UnlockedWrite = true

	err := s.CreateStateVersion(t.Context(), in, ns.Fence(), generousPendingCap)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("declare an unlocked write with the switches off: err = %v, want ErrOverrideRefused", err)
	}

	setErr := s.SetOverrides(t.Context(), ns.Name, namespace.Overrides{Forced: false, Unlocked: true}, ns.Fence())
	if setErr != nil {
		t.Fatalf("turn on the namespace switch: %v", setErr)
	}

	err = s.CreateStateVersion(t.Context(), in, ns.Fence(), generousPendingCap)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("declare with only the namespace switch on: err = %v, want ErrOverrideRefused", err)
	}
}

// testUnlockedFinalizeNeedsSwitches checks that a switch turned off after an
// unlocked declare stops its finalize: the write never becomes current.
func testUnlockedFinalizeNeedsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := turnOnUnlockedSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"))

	in := newPendingVersion(t, w, "", stateInput("l1", "v1", 1))
	in.Holder = "ci"
	in.UnlockedWrite = true
	pending := createPendingVersion(t, s, ns, in)

	err := s.SetOverrides(
		t.Context(),
		ns.Name,
		namespace.Overrides{Forced: false, Unlocked: false},
		ns.Fence(),
	)
	if err != nil {
		t.Fatalf("turn off the namespace switch: %v", err)
	}

	err = finalizeRecorded(t, s, ns.Fence(), finalizedVersion(pending, now().Add(time.Hour), "v1"))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("finalize after the switch is off: err = %v, want ErrOverrideRefused", err)
	}

	if _, curErr := s.CurrentStateVersion(t.Context(), w.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a refused finalize: err = %v, want ErrNoState", curErr)
	}
}

// turnOnUnlockedSwitches turns on the unlocked-write switch of the namespace and
// of the unit, and returns the unit as stored.
func turnOnUnlockedSwitches(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace,
) workspace.Workspace {
	t.Helper()

	return turnOnSwitches(t, s, ns, w, namespace.Overrides{Forced: false, Unlocked: true})
}

// turnOnSwitches sets the switches of the namespace and of the unit to ov, and
// returns the unit as stored.
func turnOnSwitches(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace, ov namespace.Overrides,
) workspace.Workspace {
	t.Helper()

	if err := s.SetOverrides(t.Context(), ns.Name, ov, ns.Fence()); err != nil {
		t.Fatalf("set the namespace switches: %v", err)
	}

	on := w
	on.Overrides = ov
	on.Revision = w.Revision + 1
	on.UpdatedAt = now().Add(time.Minute)

	if err := s.SaveWorkspace(t.Context(), on, ns.Fence()); err != nil {
		t.Fatalf("set the unit switches: %v", err)
	}

	return on
}

// testReleaseUnfencedNeedsForcedSwitches checks that an unfenced release is refused
// unless the forced-write switches of the namespace and of the unit are on (ADR 44,
// decisions 4 and 6), and that the switch is read in the release itself.
func testReleaseUnfencedNeedsForcedSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	released := w
	released.Revision = w.Revision + 1
	released.UpdatedAt = now().Add(time.Hour)

	err := s.ReleaseUnfenced(t.Context(), released, ns.Fence(), releaseEntry(ns.Name))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("release with the switches off: err = %v, want ErrOverrideRefused", err)
	}

	on := turnOnSwitches(t, s, ns, w, namespace.Overrides{Forced: true, Unlocked: false})

	released = on
	released.Revision = on.Revision + 1
	released.UpdatedAt = now().Add(2 * time.Hour)

	err = s.ReleaseUnfenced(t.Context(), released, ns.Fence(), releaseEntry(ns.Name))
	if err != nil {
		t.Fatalf("release with the switches on: %v", err)
	}

	got, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, released)
}

// testForcedDeclareNeedsSwitches checks that the store refuses a forced declare
// into a unit that has state while the forced-write switches are off, and writes
// nothing (ADR 44, decisions 4 and 6).
func testForcedDeclareNeedsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	finalizeFirstState(t, s, ns, locked)

	in := newPendingVersion(t, locked, locked.Lock.ID, forcedInput("l1", "v2", 2))

	err := s.CreateStateVersion(t.Context(), in, ns.Fence(), generousPendingCap)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("declare a forced write into a unit with state, switches off: err = %v, want ErrOverrideRefused", err)
	}

	cur, curErr := s.CurrentStateVersion(t.Context(), w.ID)
	if curErr != nil {
		t.Fatalf("current: %v", curErr)
	}

	if cur.Serial != 1 {
		t.Fatalf("current serial after a refused declare = %d, want the first state", cur.Serial)
	}
}

// finalizeFirstState finalizes serial 1 of a unit, so that the unit has state.
func finalizeFirstState(t *testing.T, s WorkspaceStore, ns namespace.Namespace, locked workspace.Workspace) {
	t.Helper()

	first := createPendingVersion(t, s, ns, newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", 1)))
	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(first, now().Add(time.Minute), "v1")); err != nil {
		t.Fatalf("finalize the first state: %v", err)
	}
}

// testForcedFinalizeNeedsSwitches checks that a switch turned off after a forced
// declare into a unit with state stops its finalize: the forced version never
// becomes current.
func testForcedFinalizeNeedsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	on := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: false})
	locked := lockTestWorkspace(t, s, ns, on, "alice")
	finalizeFirstState(t, s, ns, locked)

	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l1", "v2", 2),
	))

	setErr := s.SetOverrides(
		t.Context(),
		ns.Name,
		namespace.Overrides{Forced: false, Unlocked: false},
		ns.Fence(),
	)
	if setErr != nil {
		t.Fatalf("turn off the namespace switch: %v", setErr)
	}

	err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(pending, now().Add(time.Hour), "v2"))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("finalize a forced write after the switch is off: err = %v, want ErrOverrideRefused", err)
	}
}

// testFenceAndUnlockKeepsSwitches checks that a fenced force-unlock does not turn
// the unit's switches off: they are still on after the release (ADR 44, decision 6).
func testFenceAndUnlockKeepsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	on := turnOnSwitches(t, s, ns, createTestWorkspace(t, s, ns, "network"),
		namespace.Overrides{Forced: true, Unlocked: true})
	locked := lockTestWorkspace(t, s, ns, on, "alice")

	released := releasedWorkspace(locked, now().Add(time.Hour))
	fence := newFenceVersion(t, locked, 1, now().Add(time.Hour))
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

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if got.Overrides != (namespace.Overrides{Forced: true, Unlocked: true}) {
		t.Fatalf("unit switches after a fenced force-unlock = %+v, want both on", got.Overrides)
	}
}

// testForcedIntoEmptyUnitNeedsNoOverride checks that a forced write into a unit
// with no current state is allowed with both switches off: it overwrites nothing
// (ADR 44, decision 4).
func testForcedIntoEmptyUnitNeedsNoOverride(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	pending := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l1", "v1", 1),
	))

	if err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(pending, now().Add(time.Hour), "v1")); err != nil {
		t.Fatalf("finalize a forced write into an empty unit: %v", err)
	}
}

// testForcedRaceNeedsSwitches checks the race where state appears between a forced
// declare into an empty unit and its finalize: the finalize needs the override,
// and is refused while the switches are off (ADR 44, decision 4).
func testForcedRaceNeedsSwitches(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")

	declared := createPendingVersion(t, s, ns, newPendingVersion(
		t, locked, locked.Lock.ID, forcedInput("l1", "forced", 1),
	))

	finalizeFirstState(t, s, ns, locked)

	err := finalizeRecorded(t, s, ns.Fence(), finalizedVersion(declared, now().Add(time.Hour), "forced"))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("finalize a forced write after state appeared: err = %v, want ErrOverrideRefused", err)
	}
}

// testNamespaceOverridesStaleFence checks that a namespace switch write with a
// fence from another failover version is refused, and that the switches do not
// change.
func testNamespaceOverridesStaleFence(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := s.SetOverrides(t.Context(), ns.Name, namespace.Overrides{Forced: true, Unlocked: true}, stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("set overrides with a stale fence: err = %v, want namespace.ErrFenced", err)
	}

	got, err := s.GetNamespace(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got.Overrides != (namespace.Overrides{Forced: false, Unlocked: false}) {
		t.Fatalf("namespace overrides = %+v after a refused write, want all off", got.Overrides)
	}
}

// testNamespaceOffUnitOnIsRefused checks that a unit switch cannot widen the
// namespace switch: with the namespace switch off and the unit switch on, a
// forced declare into a unit with state and an unlocked declare are refused
// (ADR 44, decision 6).
func testNamespaceOffUnitOnIsRefused(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")

	withState := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, withState, "alice")
	finalizeFirstState(t, s, ns, locked)

	on := setUnitSwitchesOnly(t, s, ns, locked, namespace.Overrides{Forced: true, Unlocked: true})

	forced := newPendingVersion(t, on, on.Lock.ID, forcedInput("l2", "v2", 2))

	err := s.CreateStateVersion(t.Context(), forced, ns.Fence(), generousPendingCap)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("forced declare with the namespace switch off: err = %v, want ErrOverrideRefused", err)
	}

	empty := setUnitSwitchesOnly(t, s, ns, createTestWorkspace(t, s, ns, "empty"),
		namespace.Overrides{Forced: true, Unlocked: true})

	unlocked := newPendingVersion(t, empty, "", stateInput("l3", "v3", 1))
	unlocked.Holder = "ci"
	unlocked.UnlockedWrite = true

	err = s.CreateStateVersion(t.Context(), unlocked, ns.Fence(), generousPendingCap)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("unlocked declare with the namespace switch off: err = %v, want ErrOverrideRefused", err)
	}
}

// setUnitSwitchesOnly sets the switches of the unit to ov, leaves the namespace
// switches as they are, and returns the unit as stored.
func setUnitSwitchesOnly(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace, ov namespace.Overrides,
) workspace.Workspace {
	t.Helper()

	on := w
	on.Overrides = ov
	on.Revision = w.Revision + 1
	on.UpdatedAt = now().Add(time.Minute)

	if err := s.SaveWorkspace(t.Context(), on, ns.Fence()); err != nil {
		t.Fatalf("set the unit switches: %v", err)
	}

	return on
}
