// Package manuallocktest is the conformance suite for manuallock.Store. Every
// built-in store runs it, and a custom store should too:
//
//	func TestManualLockConformance(t *testing.T) {
//		t.Parallel()
//		manuallocktest.Run(t, func(t *testing.T) manuallocktest.Store { return newMyStore(t) })
//	}
//
// The suite creates units through the workspace and stack stores, because the
// inventory is a projection that the tool packages publish. A store must
// publish the unit in the same way it does for its own writes.
package manuallocktest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Store is every persistence port the suite exercises.
type Store interface {
	manuallock.Store
	collection.Store
	namespace.Store
	workspace.Store
	stack.Store
	inventory.Reader
	audit.Store
}

// testReason is a lock reason that a case does not care about.
const testReason = "reason"

// auditPageSize is the page size that a case reads from the audit log.
const auditPageSize = 50

// The tool codes of the inventory, and the holder most cases use.
const (
	toolOpenTofu = "opentofu"
	toolPulumi   = "pulumi"
	alice        = "alice"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s Store)
	}{
		{name: "lock a unit and read the lock", test: testLockUnit},
		{name: "lock a collection and read the lock", test: testLockCollection},
		{name: "a second lock on the same object is refused", test: testAlreadyLocked},
		{name: "a lock on a missing object is refused", test: testTargetNotFound},
		{name: "a stale fence refuses a lock and a removal", test: testFenced},
		{name: "a failed audit write leaves no lock and keeps a lock", test: testAuditRollsBack},
		{name: "the holder removes the lock and another principal cannot", test: testRemove},
		{name: "a forced removal ends the lock of another principal", test: testForcedRemove},
		{name: "a removal of a lock that does not exist is refused", test: testRemoveMissing},
		{name: "a lock and its removal write an audit entry", test: testAuditEntries},
		{name: "a unit lock covers the unit only", test: testUnitLockCovers},
		{name: "a collection lock covers its units", test: testCollectionLockCovers},
		{name: "a unit that joins a locked collection is covered", test: testJoinCovered},
		{name: "a unit that leaves a locked collection is not covered", test: testLeaveNotCovered},
		{name: "the lock of the unit wins over the lock of its collection", test: testUnitLockWins},
		{name: "a lock does not cross a namespace", test: testNamespaceIsolation},
		{name: "a deleted collection takes its lock with it", test: testCollectionDeleteDropsLock},
		{name: "a Pulumi stack keeps its lock through a rename", test: testStackRename},
		{name: "the inventory shows the lock as a flag", test: testInventoryFlag},
		{name: "a tool lock never changes a manual lock", test: testToolLockIndependent},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func testLockUnit(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)

	f.lock(t, target, alice, "freeze for the audit")

	got, err := s.GetLock(t.Context(), target)
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}

	want := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: "freeze for the audit"}
	if got != want {
		t.Errorf("lock = %+v, want %+v", got, want)
	}
}

func testLockCollection(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.CollectionTarget(f.payments.Ref)

	f.lock(t, target, alice, "change window closed")

	got, err := s.GetLock(t.Context(), target)
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}

	want := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: "change window closed"}
	if got != want {
		t.Errorf("lock = %+v, want %+v", got, want)
	}
}

func testAlreadyLocked(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, tc := range []struct {
		name   string
		target manuallock.Target
	}{
		{name: string(manuallock.KindUnit), target: manuallock.UnitTarget(f.web)},
		{name: string(manuallock.KindCollection), target: manuallock.CollectionTarget(f.payments.Ref)},
	} {
		f.lock(t, tc.target, alice, "first")

		err := f.tryLock(t, tc.target, "bob", "second")
		if !errors.Is(err, manuallock.ErrAlreadyLocked) {
			t.Errorf("%s: second lock: err = %v, want ErrAlreadyLocked", tc.name, err)
		}

		got, getErr := s.GetLock(t.Context(), tc.target)
		if getErr != nil || got.Holder != alice || got.Reason != "first" {
			t.Errorf("%s: lock after the refusal = %+v, %v; want the first lock", tc.name, got, getErr)
		}
	}
}

func testTargetNotFound(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, tc := range []struct {
		name   string
		target manuallock.Target
	}{
		{
			name:   "unit",
			target: manuallock.UnitTarget(collection.UnitRef{Namespace: f.ns.Name, Tool: toolOpenTofu, Name: "absent"}),
		},
		{
			name: "unit of another tool",
			target: manuallock.UnitTarget(
				collection.UnitRef{Namespace: f.ns.Name, Tool: toolPulumi, Name: f.web.Name},
			),
		},
		{
			name: "collection",
			target: manuallock.CollectionTarget(
				collection.Ref{Namespace: f.ns.Name, Name: mustName(t, "absent")},
			),
		},
	} {
		err := f.tryLock(t, tc.target, alice, testReason)
		if !errors.Is(err, manuallock.ErrTargetNotFound) {
			t.Errorf("%s: err = %v, want ErrTargetNotFound", tc.name, err)
		}
	}
}

func testFenced(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	stale := namespace.Fence{Namespace: f.ns.Name, FailoverVersion: f.ns.FailoverVersion + 1}

	l := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: testReason}
	if err := s.PutLock(t.Context(), l, stale, auditFor(stale)); !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("PutLock with a stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetLock(t.Context(), target); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("after the refused lock: err = %v, want ErrNotLocked", err)
	}

	f.lock(t, target, alice, testReason)

	_, err := s.RemoveLock(t.Context(), target, "", stale, lockAudit(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("RemoveLock with a stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetLock(t.Context(), target); err != nil {
		t.Errorf("after the refused removal: err = %v, want the lock to stay", err)
	}
}

func testAuditRollsBack(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)
	bad := auditFor(f.ns.Fence())
	bad.Action = ""

	l := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: testReason}
	if err := s.PutLock(t.Context(), l, f.ns.Fence(), bad); err == nil {
		t.Fatal("PutLock with an invalid audit entry succeeded")
	}

	if _, err := s.GetLock(t.Context(), target); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("after the failed lock: err = %v, want ErrNotLocked", err)
	}

	f.lock(t, target, alice, testReason)

	if _, err := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), bad); err == nil {
		t.Fatal("RemoveLock with an invalid audit entry succeeded")
	}

	if _, err := s.GetLock(t.Context(), target); err != nil {
		t.Errorf("after the failed removal: err = %v, want the lock to stay", err)
	}
}

func testRemove(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)

	f.lock(t, target, alice, testReason)

	_, err := s.RemoveLock(t.Context(), target, "bob", f.ns.Fence(), lockAudit(f.ns.Fence()))
	if !errors.Is(err, manuallock.ErrNotHolder) {
		t.Fatalf("removal by another principal: err = %v, want ErrNotHolder", err)
	}

	if _, getErr := s.GetLock(t.Context(), target); getErr != nil {
		t.Fatalf("lock after the refused removal: %v", getErr)
	}

	removed, err := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence()))
	if err != nil {
		t.Fatalf("removal by the holder: %v", err)
	}

	if removed.Holder != alice || removed.Reason != testReason {
		t.Errorf("removed lock = %+v, want the lock of alice", removed)
	}

	if _, err := s.GetLock(t.Context(), target); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("after the removal: err = %v, want ErrNotLocked", err)
	}

	f.lock(t, target, "bob", "a new lock after the removal")
}

func testForcedRemove(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, target := range []manuallock.Target{
		manuallock.UnitTarget(f.web),
		manuallock.CollectionTarget(f.payments.Ref),
	} {
		f.lock(t, target, alice, testReason)

		removed, err := s.RemoveLock(t.Context(), target, "", f.ns.Fence(), lockAudit(f.ns.Fence()))
		if err != nil {
			t.Fatalf("%s: forced removal: %v", target, err)
		}

		if removed.Holder != alice {
			t.Errorf("%s: removed holder = %q, want alice", target, removed.Holder)
		}

		if _, err := s.GetLock(t.Context(), target); !errors.Is(err, manuallock.ErrNotLocked) {
			t.Errorf("%s: after the removal: err = %v, want ErrNotLocked", target, err)
		}
	}
}

func testRemoveMissing(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, holder := range []string{"", alice} {
		_, err := s.RemoveLock(t.Context(), manuallock.UnitTarget(f.web), holder, f.ns.Fence(), lockAudit(f.ns.Fence()))
		if !errors.Is(err, manuallock.ErrNotLocked) {
			t.Errorf("holder %q: err = %v, want ErrNotLocked", holder, err)
		}
	}
}

func testAuditEntries(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)

	f.lock(t, target, alice, "freeze for the audit")

	if _, err := s.RemoveLock(t.Context(), target, "", f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
		t.Fatalf("RemoveLock: %v", err)
	}

	listing, err := s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	var locks, unlocks []audit.Entry

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Action != "unit:lock" {
			continue
		}

		if e.Reason != "" {
			locks = append(locks, e)
		} else {
			unlocks = append(unlocks, e)
		}
	}

	if len(locks) != 1 || locks[0].Reason != "freeze for the audit" {
		t.Errorf("lock entries = %+v, want one entry with the reason", locks)
	}

	if len(unlocks) != 1 || !hasChange(unlocks[0], "lock-holder", alice) ||
		!hasChange(unlocks[0], "lock-reason", "freeze for the audit") {
		t.Errorf("removal entries = %+v, want one entry that names the holder and the reason it ended", unlocks)
	}
}

func testUnitLockCovers(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.UnitTarget(f.web), alice, testReason)

	for _, tc := range []struct {
		name string
		unit collection.UnitRef
		want bool
	}{
		{name: "the locked unit", unit: f.web, want: true},
		{name: "another unit", unit: f.api, want: false},
		{name: "a missing unit", unit: collection.UnitRef{Namespace: f.ns.Name, Tool: toolOpenTofu, Name: "x"}, want: false},
	} {
		got, err := s.CoveringLock(t.Context(), tc.unit)
		if tc.want {
			if err != nil || got.Holder != alice || got.Target != manuallock.UnitTarget(f.web) {
				t.Errorf("%s: covering lock = %+v, %v; want the lock of the unit", tc.name, got, err)
			}

			continue
		}

		if !errors.Is(err, manuallock.ErrNotLocked) {
			t.Errorf("%s: err = %v, want ErrNotLocked", tc.name, err)
		}
	}
}

func testCollectionLockCovers(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason)

	got, err := s.CoveringLock(t.Context(), f.web)
	if err != nil || got.Holder != alice || got.Target != manuallock.CollectionTarget(f.payments.Ref) {
		t.Errorf("member: covering lock = %+v, %v; want the lock of the collection", got, err)
	}

	if _, err := s.CoveringLock(t.Context(), f.api); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("unit in no collection: err = %v, want ErrNotLocked", err)
	}

	if _, err := s.GetLock(t.Context(), manuallock.UnitTarget(f.web)); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("GetLock of the member: err = %v, want ErrNotLocked: GetLock does not read the collection", err)
	}
}

func testJoinCovered(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason)

	if _, err := s.CoveringLock(t.Context(), f.api); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Fatalf("before the join: err = %v, want ErrNotLocked", err)
	}

	f.put(t, f.api, f.payments)

	if got, err := s.CoveringLock(t.Context(), f.api); err != nil || got.Holder != alice {
		t.Errorf("after the join: covering lock = %+v, %v; want the lock of the collection", got, err)
	}
}

func testLeaveNotCovered(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason)

	f.put(t, f.web, f.platform)

	if _, err := s.CoveringLock(t.Context(), f.web); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("after the move to another collection: err = %v, want ErrNotLocked", err)
	}
}

func testUnitLockWins(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, "collection reason")
	f.lock(t, manuallock.UnitTarget(f.web), "bob", "unit reason")

	got, err := s.CoveringLock(t.Context(), f.web)
	if err != nil || got.Holder != "bob" || got.Reason != "unit reason" {
		t.Errorf("covering lock = %+v, %v; want the lock of the unit", got, err)
	}

	unit := manuallock.UnitTarget(f.web)
	if _, rmErr := s.RemoveLock(t.Context(), unit, "", f.ns.Fence(), lockAudit(f.ns.Fence())); rmErr != nil {
		t.Fatalf("remove the unit lock: %v", rmErr)
	}

	got, err = s.CoveringLock(t.Context(), f.web)
	if err != nil || got.Holder != alice {
		t.Errorf("after the removal: covering lock = %+v, %v; want the lock of the collection", got, err)
	}
}

func testNamespaceIsolation(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	other := newNamespace(t, s, "globex")
	twin := createWorkspace(t, s, other, "web")

	f.lock(t, manuallock.UnitTarget(f.web), alice, testReason)

	if _, err := s.CoveringLock(t.Context(), twin); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("unit of the same name in another namespace: err = %v, want ErrNotLocked", err)
	}

	l := manuallock.Lock{Since: now(), Target: manuallock.UnitTarget(f.web), Holder: alice, Reason: testReason}
	if err := s.PutLock(t.Context(), l, other.Fence(), auditFor(other.Fence())); err == nil {
		t.Error("PutLock with the fence of another namespace succeeded")
	}
}

func testCollectionDeleteDropsLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.CollectionTarget(f.payments.Ref)

	f.lock(t, target, alice, testReason)

	if err := s.DeleteCollection(t.Context(), f.payments.Ref, f.ns.Fence(), auditFor(f.ns.Fence())); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	again := collection.Collection{CreatedAt: now(), ID: ident.NewID("col-"), Ref: f.payments.Ref}
	if err := s.CreateCollection(t.Context(), again, f.ns.Fence(), auditFor(f.ns.Fence())); err != nil {
		t.Fatalf("CreateCollection again: %v", err)
	}

	if _, err := s.GetLock(t.Context(), target); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("collection created again under the name: err = %v, want ErrNotLocked", err)
	}
}

func testStackRename(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	st := newPulumiStack(f.ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, f.ns.Fence()); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	unit := collection.UnitRef{Namespace: f.ns.Name, Tool: toolPulumi, Name: "web/prod"}
	f.lock(t, manuallock.UnitTarget(unit), alice, testReason)

	if err := s.RenameStack(t.Context(), st.Ref, "prod-2", f.ns.Fence()); err != nil {
		t.Fatalf("RenameStack: %v", err)
	}

	renamed := collection.UnitRef{Namespace: f.ns.Name, Tool: toolPulumi, Name: "web/prod-2"}

	if got, err := s.CoveringLock(t.Context(), renamed); err != nil || got.Holder != alice {
		t.Errorf("renamed stack: covering lock = %+v, %v; want the lock to follow the unit", got, err)
	}

	if _, err := s.CoveringLock(t.Context(), unit); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("old name: err = %v, want ErrNotLocked", err)
	}
}

func testInventoryFlag(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)

	if got := flags(t, s, f.ns.Name); got[f.web.Name] != nil || got[f.api.Name] != nil {
		t.Fatalf("flags before any lock = %+v, want none", got)
	}

	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, "collection reason")

	got := flags(t, s, f.ns.Name)
	wantCollection := &inventory.ManualLock{
		Since:  now(),
		Kind:   string(manuallock.KindCollection),
		Object: f.payments.Ref.String(),
		Holder: alice,
		Reason: "collection reason",
	}

	if got[f.web.Name] == nil || *got[f.web.Name] != *wantCollection {
		t.Errorf("flag of the member = %+v, want %+v", got[f.web.Name], wantCollection)
	}

	if got[f.api.Name] != nil {
		t.Errorf("flag of a unit outside the collection = %+v, want none", got[f.api.Name])
	}

	f.lock(t, manuallock.UnitTarget(f.web), "bob", "unit reason")

	wantUnit := &inventory.ManualLock{
		Since: now(), Kind: string(manuallock.KindUnit), Object: f.web.String(), Holder: "bob", Reason: "unit reason",
	}

	if got := flags(t, s, f.ns.Name)[f.web.Name]; got == nil || *got != *wantUnit {
		t.Errorf("flag with both locks = %+v, want the lock of the unit %+v", got, wantUnit)
	}

	checkGetUnitFlags(t, s, f.ns.Name)
}

// flags returns the manual lock flag of each unit of ns, by unit name.
func flags(t *testing.T, s Store, ns namespace.Name) map[string]*inventory.ManualLock {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}

	out := map[string]*inventory.ManualLock{}
	for i := range entries {
		out[entries[i].Name] = entries[i].ManualLock
	}

	return out
}

// checkGetUnitFlags checks that GetUnit shows the same flag as ListUnits for
// each unit of ns.
func checkGetUnitFlags(t *testing.T, s Store, ns namespace.Name) {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}

	for i := range entries {
		one, getErr := s.GetUnit(t.Context(), entries[i].UnitID)
		if getErr != nil {
			t.Fatalf("GetUnit: %v", getErr)
		}

		if (one.ManualLock == nil) != (entries[i].ManualLock == nil) {
			t.Errorf("GetUnit(%s) flag = %+v, ListUnits flag = %+v; want equal",
				entries[i].Name, one.ManualLock, entries[i].ManualLock)
		}
	}
}

func testToolLockIndependent(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.lock(t, manuallock.UnitTarget(f.web), alice, testReason)

	w, err := s.GetWorkspace(t.Context(), workspace.Ref{Namespace: f.ns.Name, Name: mustWorkspaceName(t, f.web.Name)})
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}

	if w.Lock.Locked() {
		t.Fatalf("workspace lock = %+v: a manual lock is not a tool lock", w.Lock)
	}

	w.Revision++
	w.Lock = workspace.Lock{LockedAt: now(), ID: "lock-1", Holder: "ci", Reason: "plan"}

	if err := s.SaveWorkspace(t.Context(), w, f.ns.Fence()); err != nil {
		t.Fatalf("SaveWorkspace with a tool lock: %v", err)
	}

	w.Revision++
	w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	if err := s.SaveWorkspace(t.Context(), w, f.ns.Fence()); err != nil {
		t.Fatalf("SaveWorkspace with the tool lock released: %v", err)
	}

	if got, err := s.GetLock(t.Context(), manuallock.UnitTarget(f.web)); err != nil || got.Holder != alice {
		t.Errorf("manual lock after a tool lock and unlock = %+v, %v; want it to stay", got, err)
	}
}

func hasChange(e audit.Entry, key, old string) bool {
	for _, c := range e.Changes {
		if c.Key == key && c.Old != nil && *c.Old == old {
			return true
		}
	}

	return false
}
