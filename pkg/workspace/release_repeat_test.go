package workspace_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// releaseAt() is the time of the release in the record of the repeat tests:
// the time of the clock of the fixture.
func releaseAt() time.Time {
	return time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
}

// freeWith returns a free workspace with the record r.
func freeWith(r workspace.Release) workspace.Workspace {
	var w workspace.Workspace

	w.LastRelease = r

	return w
}

// normalRecord returns the record of bob's normal release of lock-a.
func normalRecord() workspace.Release {
	return workspace.Release{
		At: releaseAt(), LockID: "lock-a", Holder: "bob", By: "bob", Kind: workspace.ReleaseNormal,
	}
}

// forcedRecord returns the record of alice's force-unlock of bob's lock-a.
func forcedRecord() workspace.Release {
	return workspace.Release{
		At: releaseAt(), LockID: "lock-a", Holder: "bob", By: "alice", Kind: workspace.ReleaseForced,
	}
}

// repeatOf returns a request of caller for the kind, naming lockID and holder.
func repeatOf(kind workspace.ReleaseKind, lockID, holder, caller string) workspace.ReleaseRepeat {
	return workspace.ReleaseRepeat{LockID: lockID, Holder: holder, Caller: caller, Kind: kind}
}

// TestRepeatsRelease proves the rule of a repeated release, one row for each
// case: only a request of the kind of the record, by the principal that the
// record names, with no lock ID or the lock ID of the record, less than 24
// hours after the release, on a free workspace, matches.
func TestRepeatsRelease(t *testing.T) {
	t.Parallel()

	normal, forced := workspace.ReleaseNormal, workspace.ReleaseForced
	locked := freeWith(normalRecord())
	locked.Lock = workspace.Lock{LockedAt: releaseAt(), ID: "lock-b", Holder: "carol", Reason: ""}

	invalid := normalRecord()
	invalid.Kind = "expired"

	cases := []struct {
		name string
		w    workspace.Workspace
		q    workspace.ReleaseRepeat
		now  time.Time
		want bool
	}{
		{
			"matching record of a normal release",
			freeWith(normalRecord()),
			repeatOf(normal, "lock-a", "", "bob"),
			releaseAt(),
			true,
		},
		{
			"a request with no lock ID by the holder",
			freeWith(normalRecord()),
			repeatOf(normal, "", "", "bob"),
			releaseAt(),
			true,
		},
		{"another lock ID", freeWith(normalRecord()), repeatOf(normal, "lock-b", "", "bob"), releaseAt(), false},
		{"another caller", freeWith(normalRecord()), repeatOf(normal, "lock-a", "", "carol"), releaseAt(), false},
		{"no caller", freeWith(normalRecord()), repeatOf(normal, "lock-a", "", ""), releaseAt(), false},
		{
			"a forced release for the original holder",
			freeWith(forcedRecord()),
			repeatOf(normal, "lock-a", "", "bob"),
			releaseAt(),
			false,
		},
		{
			"a repeated force-unlock by the same releaser",
			freeWith(forcedRecord()),
			repeatOf(forced, "lock-a", "", "alice"),
			releaseAt(),
			true,
		},
		{
			"a repeated force-unlock that names the holder",
			freeWith(forcedRecord()),
			repeatOf(forced, "lock-a", "bob", "alice"),
			releaseAt(),
			true,
		},
		{
			"a repeated force-unlock that names another holder",
			freeWith(forcedRecord()),
			repeatOf(forced, "lock-a", "carol", "alice"),
			releaseAt(),
			false,
		},
		{
			"a force-unlock by another caller",
			freeWith(forcedRecord()),
			repeatOf(forced, "lock-a", "", "bob"),
			releaseAt(),
			false,
		},
		{
			"a force-unlock after a normal release",
			freeWith(normalRecord()),
			repeatOf(forced, "lock-a", "", "bob"),
			releaseAt(),
			false,
		},
		{
			"a record less than 24 hours old", freeWith(normalRecord()), repeatOf(normal, "lock-a", "", "bob"),
			releaseAt().Add(workspace.ReleaseWindow - time.Millisecond), true,
		},
		{
			"a record 24 hours old", freeWith(normalRecord()), repeatOf(normal, "lock-a", "", "bob"),
			releaseAt().Add(workspace.ReleaseWindow), false,
		},
		{
			"a record older than 24 hours", freeWith(forcedRecord()), repeatOf(forced, "lock-a", "", "alice"),
			releaseAt().Add(48 * time.Hour), false,
		},
		{"a locked workspace", locked, repeatOf(normal, "lock-a", "", "bob"), releaseAt(), false},
		{"no record", freeWith(workspace.NoRelease()), repeatOf(normal, "", "", ""), releaseAt(), false},
		{
			"a record with a kind that is not valid",
			freeWith(invalid),
			repeatOf("expired", "lock-a", "", "bob"),
			releaseAt(),
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.w.RepeatsRelease(tc.now, tc.q); got != tc.want {
				t.Fatalf("RepeatsRelease(%+v) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

// TestRepeatsLock proves the rule of a repeated LOCK: the lock is held with
// the same lock ID and the same holder.
func TestRepeatsLock(t *testing.T) {
	t.Parallel()

	held := freeWith(workspace.NoRelease())
	held.Lock = workspace.Lock{LockedAt: releaseAt(), ID: "lock-a", Holder: "bob", Reason: "apply"}

	cases := []struct {
		name, holder, lockID string
		w                    workspace.Workspace
		want                 bool
	}{
		{name: "same lock ID and holder", holder: "bob", lockID: "lock-a", w: held, want: true},
		{name: "another lock ID", holder: "bob", lockID: "lock-b", w: held, want: false},
		{name: "another holder", holder: "alice", lockID: "lock-a", w: held, want: false},
		{name: "no lock ID", holder: "bob", lockID: "", w: held, want: false},
		{name: "a free workspace", holder: "bob", lockID: "lock-a", w: freeWith(normalRecord()), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.w.RepeatsLock(tc.holder, tc.lockID); got != tc.want {
				t.Fatalf("RepeatsLock(%q, %q) = %v, want %v", tc.holder, tc.lockID, got, tc.want)
			}
		})
	}
}

// repeatCall is one call of the service in a repeat case. It returns the
// error of the call.
type repeatCall func(t *testing.T, f fixture) error

func lockAs(holder, lockID string) repeatCall {
	return func(t *testing.T, f fixture) error {
		t.Helper()

		_, err := f.svc.LockWithID(t.Context(), f.ws.ID, holder, "apply", lockID)

		return err
	}
}

func unlockAs(holder, lockID string) repeatCall {
	return func(t *testing.T, f fixture) error {
		t.Helper()

		_, err := f.svc.UnlockLock(t.Context(), f.ws.ID, lockID, holder)

		return err
	}
}

func forceAs(by, lockID string) repeatCall {
	return func(t *testing.T, f fixture) error {
		t.Helper()

		expect := workspace.Lock{LockedAt: time.Time{}, ID: lockID, Holder: "", Reason: ""}
		_, _, err := f.svc.ForceUnlockLock(t.Context(), f.ws.ID, expect, by, noOverrides(), nil, forceEntry())

		return err
	}
}

func later(d time.Duration, call repeatCall) repeatCall {
	return func(t *testing.T, f fixture) error {
		t.Helper()

		*f.now = f.now.Add(d)

		return call(t, f)
	}
}

// TestServiceRepeatedReleases proves the rule in the service, one row for
// each case. The steps before the last one set the workspace up. The last
// call is the call under test: it returns want, and it changes nothing: the
// workspace and the release audit entries stay as they were.
func TestServiceRepeatedReleases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		steps []repeatCall
		want  error
	}{
		{
			name:  "a repeated LOCK with the same lock ID and holder",
			steps: []repeatCall{lockAs("bob", "lock-a"), lockAs("bob", "lock-a")},
			want:  nil,
		},
		{
			name:  "a LOCK with another lock ID by the holder",
			steps: []repeatCall{lockAs("bob", "lock-a"), lockAs("bob", "lock-b")},
			want:  workspace.ErrLocked,
		},
		{
			name:  "a LOCK with the same lock ID by another holder",
			steps: []repeatCall{lockAs("bob", "lock-a"), lockAs("alice", "lock-a")},
			want:  workspace.ErrLocked,
		},
		{
			name:  "a repeated unlock that matches the record",
			steps: []repeatCall{lockAs("bob", "lock-a"), unlockAs("bob", "lock-a"), unlockAs("bob", "lock-a")},
			want:  nil,
		},
		{
			name:  "a repeated unlock with no lock ID by the holder",
			steps: []repeatCall{lockAs("bob", "lock-a"), unlockAs("bob", ""), unlockAs("bob", "")},
			want:  nil,
		},
		{
			name:  "an unlock with another lock ID",
			steps: []repeatCall{lockAs("bob", "lock-a"), unlockAs("bob", "lock-a"), unlockAs("bob", "lock-b")},
			want:  workspace.ErrLockMismatch,
		},
		{
			name:  "an unlock by another caller",
			steps: []repeatCall{lockAs("bob", "lock-a"), unlockAs("bob", ""), unlockAs("alice", "")},
			want:  workspace.ErrNotLocked,
		},
		{
			name:  "the unlock of the original holder after a forced release",
			steps: []repeatCall{lockAs("bob", "lock-a"), forceAs("alice", "lock-a"), unlockAs("bob", "lock-a")},
			want:  workspace.ErrLockMismatch,
		},
		{
			name:  "a repeated force-unlock by the same releaser",
			steps: []repeatCall{lockAs("bob", "lock-a"), forceAs("alice", "lock-a"), forceAs("alice", "lock-a")},
			want:  nil,
		},
		{
			name:  "a force-unlock by another caller after a forced release",
			steps: []repeatCall{lockAs("bob", "lock-a"), forceAs("alice", "lock-a"), forceAs("carol", "lock-a")},
			want:  workspace.ErrNotLocked,
		},
		{
			name: "an unlock that matches a record older than 24 hours",
			steps: []repeatCall{
				lockAs("bob", "lock-a"), unlockAs("bob", "lock-a"),
				later(workspace.ReleaseWindow, unlockAs("bob", "lock-a")),
			},
			want: workspace.ErrLockMismatch,
		},
		{
			name: "a force-unlock that matches a record older than 24 hours",
			steps: []repeatCall{
				lockAs("bob", "lock-a"), forceAs("alice", "lock-a"),
				later(workspace.ReleaseWindow, forceAs("alice", "lock-a")),
			},
			want: workspace.ErrNotLocked,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runRepeatCase(t, tc.steps, tc.want)
		})
	}
}

// runRepeatCase runs the steps of a repeat case on a new fixture. The steps
// before the last one must succeed. The last one must return want, and must
// change neither the workspace nor the release audit entries.
func runRepeatCase(t *testing.T, steps []repeatCall, want error) {
	t.Helper()

	f := newFixture(t)
	last := len(steps) - 1

	for i, step := range steps[:last] {
		if err := step(t, f); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	before, entries := f.stored(t), f.store.releaseCount()

	if err := steps[last](t, f); !errors.Is(err, want) || (want == nil && err != nil) {
		t.Fatalf("call under test: err = %v, want %v", err, want)
	}

	after := f.stored(t)
	if after.Revision != before.Revision || after.Lock != before.Lock || after.LastRelease != before.LastRelease {
		t.Fatalf("call under test changed the workspace: %+v, want %+v", after, before)
	}

	if got := f.store.releaseCount(); got != entries {
		t.Fatalf("call under test wrote %d release audit entries, want none", got-entries)
	}
}

// TestReleaseWritesTheRecord proves that each release writes its record: an
// unlock writes a normal release by the holder, and a force-unlock a forced
// release by the principal that forced it.
func TestReleaseWritesTheRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		release repeatCall
		want    workspace.Release
	}{
		{
			name:    "an unlock",
			release: unlockAs("bob", "lock-a"),
			want: workspace.Release{
				At:     releaseAt(),
				LockID: "lock-a",
				Holder: "bob",
				By:     "bob",
				Kind:   workspace.ReleaseNormal,
			},
		},
		{
			name:    "a force-unlock",
			release: forceAs("alice", ""),
			want: workspace.Release{
				At:     releaseAt(),
				LockID: "lock-a",
				Holder: "bob",
				By:     "alice",
				Kind:   workspace.ReleaseForced,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			if err := lockAs("bob", "lock-a")(t, f); err != nil {
				t.Fatalf("lock: %v", err)
			}

			if err := tc.release(t, f); err != nil {
				t.Fatalf("release: %v", err)
			}

			if got := f.stored(t).LastRelease; got != tc.want {
				t.Fatalf("record = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// stored returns the workspace of the fixture as the store holds it.
func (f fixture) stored(t *testing.T) workspace.Workspace {
	t.Helper()

	w, err := f.store.GetWorkspaceByID(t.Context(), f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	return w
}

// releaseCount returns the count of the audit entries of the releases that
// the store holds.
func (m *memStore) releaseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.released)
}
