package workspace_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// expectedLock returns the lock that a caller expects to find: the lock with
// id held by holder. Both can be empty.
func expectedLock(id, holder string) workspace.Lock {
	return workspace.Lock{LockedAt: time.Time{}, ID: id, Holder: holder, Reason: ""}
}

// changeValue returns the old value of the change with key, or "" and false.
func changeValue(e audit.Entry, key string) (string, bool) {
	for _, c := range e.Changes {
		if c.Key == key && c.Old != nil {
			return *c.Old, true
		}
	}

	return "", false
}

// checkEntryFields checks the fields of the audit entry of a force-unlock
// that the service sets: the kind, the subject, the unit and the action.
func checkEntryFields(t *testing.T, e audit.Entry, f fixture) {
	t.Helper()

	if e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Subject != "admin" {
		t.Fatalf("entry = %+v, want a successful change by admin", e)
	}

	if e.Namespace != f.ws.Ref.Namespace || e.ObjectID != f.ws.ID.String() || e.Action != "unit:write-forced" {
		t.Fatalf("entry = %+v, want the unit %s and the action unit:write-forced", e, f.ws.ID)
	}

	if err := e.Validate(); err != nil {
		t.Fatalf("entry fails validation: %v", err)
	}
}

// checkReleaseEntry checks the audit entry of a force-unlock: its fields, the
// version, the reason, and the lock that the release ended.
func checkReleaseEntry(t *testing.T, e audit.Entry, f fixture, version string) {
	t.Helper()

	checkEntryFields(t, e, f)

	if e.Version != version || e.Reason != forceEntry().Reason {
		t.Fatalf("entry version %q reason %q, want %q and %q", e.Version, e.Reason, version, forceEntry().Reason)
	}

	if holder, ok := changeValue(e, "lock-holder"); !ok || holder != "alice" {
		t.Fatalf("entry changes = %+v, want the released holder alice", e.Changes)
	}

	if at, ok := changeValue(e, "locked-at"); !ok || at == "" {
		t.Fatalf("entry changes = %+v, want the lock time", e.Changes)
	}
}

// forceUnlockEntryCase is one kind of force-unlock that writes an entry.
type forceUnlockEntryCase struct {
	name     string
	state    string
	version  string
	allow    namespace.Overrides
	unfenced bool
	records  int
}

// TestForceUnlockWritesOneChangeEntry proves that each kind of force-unlock
// appends one audit entry of the kind change with the released holder, the
// lock time, the new version and the reason: a release of a unit without
// state, a release with a fence, and an unfenced release of a client-encrypted
// state, which also keeps its override entry.
func TestForceUnlockWritesOneChangeEntry(t *testing.T) {
	t.Parallel()

	cases := []forceUnlockEntryCase{
		{"no state", "", "", noOverrides(), false, 0},
		{"fence", smallDoc, "2", noOverrides(), false, 0},
		{"client encrypted", envelope, "", forcedOnly(), true, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkForceUnlockEntry(t, tc)
		})
	}
}

// checkForceUnlockEntry force-unlocks a locked workspace in the state of tc and
// checks the one entry that the release writes.
func checkForceUnlockEntry(t *testing.T, tc forceUnlockEntryCase) {
	t.Helper()

	f := newFixture(t)
	lockAndPush(t, f, tc.state)

	ctx := t.Context()

	if _, err := f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, tc.allow); err != nil {
		t.Fatalf("set namespace switches: %v", err)
	}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, tc.allow); err != nil {
		t.Fatalf("set unit switches: %v", err)
	}

	records := 0
	record := func() error {
		records++

		return nil
	}

	released, unfenced, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", tc.allow, record, forceEntry())
	if err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if released.Lock.Locked() || unfenced != tc.unfenced || records != tc.records {
		t.Fatalf("locked = %t, unfenced = %t, records = %d; want released, %t and %d",
			released.Lock.Locked(), unfenced, records, tc.unfenced, tc.records)
	}

	entries := f.store.releaseEntries()
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly one", entries)
	}

	checkReleaseEntry(t, entries[0], f, tc.version)
}

// lockAndPush locks the workspace of f as alice under the lock ID "lock-1",
// and pushes state when it is not empty.
func lockAndPush(t *testing.T, f fixture, state string) {
	t.Helper()

	if _, err := f.svc.LockWithID(t.Context(), f.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if state == "" {
		return
	}

	if err := f.push(t, newState("lin-old", 1, state), state); err != nil {
		t.Fatalf("push state: %v", err)
	}
}

// forceUnlockRefusal is one force-unlock that the service refuses.
type forceUnlockRefusal struct {
	name   string
	state  string
	lock   bool
	expect workspace.Lock
	reason string
	want   error
}

// TestForceUnlockRefusesWithoutAnEntry proves that a refused force-unlock
// writes no entry and keeps the lock: a reason that audit.ValidateReason
// refuses, a lock that differs from the expected lock, an unlocked unit and
// a client-encrypted state without the override.
func TestForceUnlockRefusesWithoutAnEntry(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", audit.MaxReasonLen+1)

	cases := []forceUnlockRefusal{
		{"reason too long", "", true, expectedLock("", ""), long, audit.ErrInvalidReason},
		{"line break in the reason", "", true, expectedLock("", ""), "first\nsecond", audit.ErrInvalidReason},
		{"other lock ID", "", true, expectedLock("lock-x", ""), "", workspace.ErrLockMismatch},
		{"other lock holder", "", true, expectedLock("lock-1", "bob"), "", workspace.ErrLockMismatch},
		{"unit not locked", "", false, expectedLock("", ""), "", workspace.ErrNotLocked},
		{
			"client encrypted without the override", envelope, true,
			expectedLock("", ""), "", workspace.ErrClientEncrypted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			ctx := t.Context()

			if tc.lock {
				lockAndPush(t, f, tc.state)
			}

			entry := forceEntry()
			entry.Reason = tc.reason

			_, _, err := f.svc.ForceUnlockLock(ctx, f.ws.ID, tc.expect, "admin", noOverrides(), nil, entry)
			if !errors.Is(err, tc.want) {
				t.Fatalf("force unlock: err = %v, want %v", err, tc.want)
			}

			if entries := f.store.releaseEntries(); len(entries) != 0 {
				t.Fatalf("entries = %+v, want none", entries)
			}

			held, getErr := f.svc.GetByID(ctx, f.ws.ID)
			if getErr != nil || held.Lock.Locked() != tc.lock {
				t.Fatalf("lock after the refusal: locked = %t, err = %v; want locked = %t",
					held.Lock.Locked(), getErr, tc.lock)
			}
		})
	}
}
