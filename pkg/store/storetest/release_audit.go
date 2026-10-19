package storetest

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// releaseAuditCases returns the cases of the audit entry of a release of a
// lock: it joins the transaction of the release, whatever kind of release it
// is.
func releaseAuditCases() []workspaceCase {
	return []workspaceCase{
		{name: "a fenced release appends its entry in its transaction", test: testFenceAndUnlockAppendsEntry},
		{name: "a release of a unit without state appends its entry", test: testSaveReleasedAppendsEntry},
		{name: "an unfenced release appends its entry in its transaction", test: testReleaseUnfencedAppendsEntry},
		{name: "a release with a refused entry changes nothing", test: testReleaseRefusedEntryChangesNothing},
		{name: "a release whose audit append fails changes nothing", test: testReleaseFailedAppendChangesNothing},
	}
}

// releaseReason is the reason in the entry of a release in the suite.
const releaseReason = "the run crashed; the lock is stale"

// releaseEntry returns the audit entry that the suite passes to the release
// of a lock in ns: a change by the principal "admin", which the store appends
// in the transaction of the release.
func releaseEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At:           now().Add(3 * time.Hour),
		Namespace:    ns,
		Subject:      "admin",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "unit:write-forced",
		Object:       "opentofu:" + ns.String() + "/network",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "storetest",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
		Reason:       releaseReason,
	}
}

// listReleaseEntries returns the audit entries of ns, or skips the test when
// the store lists none.
func listReleaseEntries(t *testing.T, s WorkspaceStore, ns namespace.Name) []audit.Entry {
	t.Helper()

	lister, ok := s.(auditLister)
	if !ok {
		t.Skip("store does not list audit entries")
	}

	listing, err := lister.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return listing.Entries
}

// checkReleaseEntry checks the one entry of a release in ns: the reason, the
// object ID and the version that the caller passed.
func checkReleaseEntry(t *testing.T, s WorkspaceStore, ns namespace.Name, w workspace.Workspace, version string) {
	t.Helper()

	entries := listReleaseEntries(t, s, ns)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %+v, want the one entry of the release", entries)
	}

	got := entries[0]
	if got.Kind != audit.KindChange || got.Reason != releaseReason || got.ObjectID != w.ID.String() ||
		got.Version != version || got.Sequence != 1 {
		t.Fatalf("audit entry = %+v, want a change with the reason, object ID %s and version %q", got, w.ID, version)
	}

	checker, ok := s.(audit.Store)
	if !ok {
		return
	}

	check, err := checker.Check(t.Context(), ns)
	if err != nil || !check.Valid {
		t.Fatalf("audit chain check = %+v, %v; want a valid chain", check, err)
	}
}

// testFenceAndUnlockAppendsEntry releases a lock with a fence version and
// expects the audit chain to hold the entry of the release, with its reason.
func testFenceAndUnlockAppendsEntry(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)

	entry := releaseEntry(ns.Name)
	entry.Version = strconv.FormatInt(fence.Serial, 10)

	err := s.FenceAndUnlock(t.Context(), released, fence, currentVersionID(t, s, locked.ID), ns.Fence(), entry)
	if err != nil {
		t.Fatalf("fence and unlock: %v", err)
	}

	checkReleaseEntry(t, s, ns.Name, locked, "2")
}

// testSaveReleasedAppendsEntry releases the lock of a unit with no state and
// expects the audit chain to hold the entry of the release.
func testSaveReleasedAppendsEntry(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked := lockTestWorkspace(t, s, ns, createTestWorkspace(t, s, ns, "network"), "alice")
	released := releasedWorkspace(locked, now().Add(2*time.Hour))

	if err := s.SaveReleased(t.Context(), released, ns.Fence(), releaseEntry(ns.Name)); err != nil {
		t.Fatalf("save released: %v", err)
	}

	assertFenceReleased(t, s, released)
	checkReleaseEntry(t, s, ns.Name, locked, "")
}

// testReleaseUnfencedAppendsEntry releases a lock unfenced with the switches
// on and expects the audit chain to hold the entry of the release. A release
// that the switches refuse appends none.
func testReleaseUnfencedAppendsEntry(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	locked := lockTestWorkspace(t, s, ns, w, "alice")
	refused := releasedWorkspace(locked, now().Add(time.Hour))

	err := s.ReleaseUnfenced(t.Context(), refused, ns.Fence(), releaseEntry(ns.Name))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("release with the switches off: err = %v, want ErrOverrideRefused", err)
	}

	if entries := listReleaseEntries(t, s, ns.Name); len(entries) != 0 {
		t.Fatalf("audit entries after a refused release = %+v, want none", entries)
	}

	on := turnOnSwitches(t, s, ns, locked, namespace.Overrides{Forced: true, Unlocked: false})
	released := releasedWorkspace(on, now().Add(2*time.Hour))

	err = s.ReleaseUnfenced(t.Context(), released, ns.Fence(), releaseEntry(ns.Name))
	if err != nil {
		t.Fatalf("release with the switches on: %v", err)
	}

	checkReleaseEntry(t, s, ns.Name, on, "")
}

// testReleaseRefusedEntryChangesNothing passes an entry of another namespace
// and an entry that fails validation to each release: each returns
// audit.ErrInvalidEntry, and the lock stays held.
func testReleaseRefusedEntryChangesNothing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	locked := lockTestWorkspace(t, s, ns, createTestWorkspace(t, s, ns, "network"), "alice")
	released := releasedWorkspace(locked, now().Add(2*time.Hour))

	invalid := releaseEntry(ns.Name)
	invalid.Reason = "first\nsecond"

	refused := []audit.Entry{releaseEntry("other"), invalid}
	for i := range refused {
		err := s.SaveReleased(t.Context(), released, ns.Fence(), refused[i])
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("save released with entry %+v: err = %v, want ErrInvalidEntry", refused[i], err)
		}
	}

	got, err := s.GetWorkspaceByID(t.Context(), locked.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	assertWorkspace(t, got, locked)
}

// testReleaseFailedAppendChangesNothing breaks the audit appends of the
// store, then releases a lock with a valid entry: the release fails, and the
// lock stays held (fail closed).
func testReleaseFailedAppendChangesNothing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	breaker, ok := s.(AuditBreaker)
	if !ok {
		t.Skip("store cannot make an audit append fail")
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	locked, _ := lockedWithState(t, s, ns)
	released := releasedWorkspace(locked, now().Add(2*time.Hour))
	fence := newFenceVersion(t, locked, 2, now().Add(2*time.Hour))
	stageFence(t, s, ns, fence)

	before := currentVersionID(t, s, locked.ID)

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	err := s.FenceAndUnlock(
		t.Context(), released, fence, currentVersionID(t, s, locked.ID), ns.Fence(), releaseEntry(ns.Name),
	)
	if err == nil {
		t.Fatal("fence and unlock with a failing audit append: err = nil, want an error")
	}

	got, getErr := s.GetWorkspaceByID(t.Context(), locked.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	assertWorkspace(t, got, locked)

	if after := currentVersionID(t, s, locked.ID); after != before {
		t.Fatalf("current state version after the failed release = %s, want %s", after, before)
	}
}
