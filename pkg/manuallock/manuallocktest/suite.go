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
	"sync"
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

// actionUnitLock is the audit action of a lock and of its removal.
const actionUnitLock = "unit:lock"

// wantLockEntries is the number of audit entries of a lock and its removal.
const wantLockEntries = 2

// auditPageSize is the page size that a case reads from the audit log.
const auditPageSize = 50

// The tool codes of the inventory, and the holder most cases use.
const (
	toolOpenTofu = "opentofu"
	toolPulumi   = "pulumi"
	alice        = "alice"
	bob          = "bob"
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
		{name: "a create of a workspace under a namespace lock is refused", test: testCreateUnderNamespaceLock},
		{name: "a create under a namespace lock passes for a phase that started before it", test: testCreatePhasePass},
		{name: "a lock on a collection or on another unit does not refuse a create", test: testCreateIgnoresOtherLocks},
		{name: "lock a collection and read the lock", test: testLockCollection},
		{name: "a second lock on the same object is refused", test: testAlreadyLocked},
		{name: "a lock on a missing object is refused", test: testTargetNotFound},
		{name: "a stale fence refuses a lock and a removal", test: testFenced},
		{name: "a failed audit write leaves no lock and keeps a lock", test: testAuditRollsBack},
		{name: "the holder removes the lock and another principal cannot", test: testRemove},
		{name: "a forced removal ends the lock of another principal", test: testForcedRemove},
		{name: "a removal with an empty holder is refused and keeps the lock", test: testEmptyHolderRefused},
		{name: "a removal of a lock that does not exist is refused", test: testRemoveMissing},
		{name: "an unlock after a force-unlock and a new lock keeps the new lock", test: testStaleUnlockKeepsNewLock},
		{name: "two concurrent removals end the lock once", test: testConcurrentRemoval},
		{name: "a lock and its removal write an audit entry", test: testAuditEntries},
		{name: "a unit lock covers the unit only", test: testUnitLockCovers},
		{name: "a collection lock covers its units", test: testCollectionLockCovers},
		{name: "a unit that joins a locked collection is covered", test: testJoinCovered},
		{name: "every covering lock is listed, unit first", test: testCoveringLocksListsAll},
		{name: "a unit that leaves a locked collection is not covered", test: testLeaveNotCovered},
		{name: "a move out of a locked collection needs the right to end the lock", test: testLeaveNeedsEndsLock},
		{name: "a restore under a manual lock is refused and rolled back", test: testRestoreUnderLock},
		{name: "a soft delete under a manual lock is refused and rolled back", test: testDeleteUnderLock},
		{name: "a lock and a delete at the same time never delete a unit under the lock", test: testDeleteLockRace},
		{name: "the lock of the unit wins over the lock of its collection", test: testUnitLockWins},
		{name: "a lock does not cross a namespace", test: testNamespaceIsolation},
		{name: "a deleted collection takes its lock with it", test: testCollectionDeleteDropsLock},
		{name: "a Pulumi stack keeps its lock through a rename", test: testStackRename},
		{name: "the inventory shows the lock as a flag", test: testInventoryFlag},
		{name: "the locked state filter of a unit page keeps the locked units", test: testQueryUnitsLocked},
		{name: "a tool lock never changes a manual lock", test: testToolLockIndependent},
		{name: "lock a namespace and read the lock", test: testLockNamespace},
		{name: "a namespace lock covers each unit, also a unit made later", test: testNamespaceLockCovers},
		{name: "a namespace lock does not cover another namespace", test: testNamespaceLockIsolated},
		{name: "the nearest lock wins: unit, then collection, then namespace", test: testNearestLockWins},
		{
			name: "the holder removes a namespace lock, and a force removal ends another lock",
			test: testNamespaceRemoval,
		},
		{name: "a namespace lock writes audit entries that name the namespace", test: testNamespaceAudit},
		{name: "the inventory shows the namespace lock as a flag", test: testNamespaceInventoryFlag},
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

	cases := []struct {
		name   string
		target manuallock.Target
	}{
		{name: string(manuallock.KindUnit), target: manuallock.UnitTarget(f.web)},
		{name: string(manuallock.KindCollection), target: manuallock.CollectionTarget(f.payments.Ref)},
		{name: string(manuallock.KindNamespace), target: manuallock.NamespaceTarget(f.ns.Name)},
	}

	for i := range cases {
		tc := &cases[i]
		f.lock(t, tc.target, alice, "first")

		err := f.tryLock(t, tc.target, bob, "second")
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

	cases := []struct {
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
		{name: "namespace", target: manuallock.NamespaceTarget("absent")},
	}

	for i := range cases {
		tc := &cases[i]
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

	_, err := s.RemoveLock(t.Context(), target, alice, stale, lockAudit(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("RemoveLock with a stale fence: err = %v, want ErrFenced", err)
	}

	_, err = s.ForceRemoveLock(t.Context(), target, stale, lockAudit(stale))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("ForceRemoveLock with a stale fence: err = %v, want ErrFenced", err)
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

	_, err := s.RemoveLock(t.Context(), target, bob, f.ns.Fence(), lockAudit(f.ns.Fence()))
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

	f.lock(t, target, bob, "a new lock after the removal")
}

func testForcedRemove(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, target := range []manuallock.Target{
		manuallock.UnitTarget(f.web),
		manuallock.CollectionTarget(f.payments.Ref),
	} {
		f.lock(t, target, alice, testReason)

		removed, err := s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence()))
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

	target := manuallock.UnitTarget(f.web)

	if _, err := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence())); !errors.Is(
		err, manuallock.ErrNotLocked,
	) {
		t.Errorf("RemoveLock: err = %v, want ErrNotLocked", err)
	}

	if _, err := s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence())); !errors.Is(
		err, manuallock.ErrNotLocked,
	) {
		t.Errorf("ForceRemoveLock: err = %v, want ErrNotLocked", err)
	}
}

// testEmptyHolderRefused sends RemoveLock an empty holder, as if the service
// had not checked it. The store must refuse, keep the lock and write no audit
// entry: only ForceRemoveLock removes the lock of another principal.
func testEmptyHolderRefused(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, target := range []manuallock.Target{
		manuallock.UnitTarget(f.web),
		manuallock.CollectionTarget(f.payments.Ref),
	} {
		f.lock(t, target, alice, testReason)

		_, err := s.RemoveLock(t.Context(), target, "", f.ns.Fence(), lockAudit(f.ns.Fence()))
		if !errors.Is(err, manuallock.ErrNotHolder) {
			t.Errorf("%s: RemoveLock with an empty holder: err = %v, want ErrNotHolder", target, err)
		}

		got, getErr := s.GetLock(t.Context(), target)
		if getErr != nil || got.Holder != alice {
			t.Errorf("%s: after the refused removal: lock = %+v, err = %v, want the lock of alice", target, got, getErr)
		}
	}

	listing, err := s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	for i := range listing.Entries {
		if hasChange(listing.Entries[i], "lock-holder", alice) {
			t.Errorf("the refused removal wrote the audit entry %+v that ends a lock", listing.Entries[i])
		}
	}
}

func testStaleUnlockKeepsNewLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	for _, target := range []manuallock.Target{
		manuallock.UnitTarget(f.web),
		manuallock.CollectionTarget(f.payments.Ref),
	} {
		f.lock(t, target, alice, testReason)

		// Another principal force-unlocks the lock of alice and takes a new one.
		if _, err := s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
			t.Fatalf("%s: force-unlock: %v", target, err)
		}

		f.lock(t, target, bob, "the new lock of bob")

		// The unlock that alice sent before the force-unlock arrives now.
		_, err := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence()))
		if !errors.Is(err, manuallock.ErrNotHolder) {
			t.Fatalf("%s: stale unlock: err = %v, want ErrNotHolder", target, err)
		}

		got, err := s.GetLock(t.Context(), target)
		if err != nil || got.Holder != bob || got.Reason != "the new lock of bob" {
			t.Errorf("%s: after the stale unlock: lock = %+v, err = %v, want the lock of bob", target, got, err)
		}
	}
}

func testConcurrentRemoval(t *testing.T, s Store) {
	t.Helper()

	const callers = 6

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)

	f.lock(t, target, alice, "freeze for the race")

	var (
		wg   sync.WaitGroup
		errs [callers]error
	)

	start := make(chan struct{})

	for i := range callers {
		wg.Go(func() {
			<-start

			// Alice is the owner and removes the lock; the other callers force it.
			// Both kinds of caller race for the same lock.
			if i%2 == 0 {
				_, errs[i] = s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence()))

				return
			}

			_, errs[i] = s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence()))
		})
	}

	close(start)
	wg.Wait()

	removed := 0

	for i, err := range errs {
		switch {
		case err == nil:
			removed++
		case errors.Is(err, manuallock.ErrNotLocked):
		default:
			t.Errorf("caller %d: err = %v, want nil or ErrNotLocked", i, err)
		}
	}

	if removed != 1 {
		t.Errorf("%d removals succeeded, want exactly 1", removed)
	}

	listing, err := s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	ended := 0

	for i := range listing.Entries {
		if hasChange(listing.Entries[i], "lock-holder", alice) {
			ended++
		}
	}

	if ended != 1 {
		t.Errorf("%d audit entries end the lock, want exactly 1", ended)
	}
}

func testAuditEntries(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.UnitTarget(f.web)

	f.lock(t, target, alice, "freeze for the audit")

	if _, err := s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
		t.Fatalf("RemoveLock: %v", err)
	}

	listing, err := s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	var locks, unlocks []audit.Entry

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Action != actionUnitLock {
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

// testCoveringLocksListsAll proves CoveringLocks answers each lock that covers
// the unit, the unit first, then its collection, then its namespace, and that
// a unit which no lock covers gets ErrNotLocked and never an empty list.
func testCoveringLocksListsAll(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	if locks, err := s.CoveringLocks(t.Context(), f.web); !errors.Is(err, manuallock.ErrNotLocked) || len(locks) != 0 {
		t.Fatalf("no lock: %+v, %v; want ErrNotLocked and no lock", locks, err)
	}

	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.NamespaceTarget(f.ns.Name), alice, testReason)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason)
	f.lock(t, manuallock.UnitTarget(f.web), alice, testReason)

	locks, err := s.CoveringLocks(t.Context(), f.web)
	if err != nil {
		t.Fatalf("CoveringLocks: %v", err)
	}

	want := []manuallock.Target{
		manuallock.UnitTarget(f.web),
		manuallock.CollectionTarget(f.payments.Ref),
		manuallock.NamespaceTarget(f.ns.Name),
	}

	if len(locks) != len(want) {
		t.Fatalf("CoveringLocks = %d locks, want %d: %+v", len(locks), len(want), locks)
	}

	for i, target := range want {
		if locks[i].Target != target {
			t.Errorf("lock %d target = %s, want %s", i, locks[i].Target, target)
		}
	}

	first, err := s.CoveringLock(t.Context(), f.web)
	if err != nil || first.Target != want[0] {
		t.Errorf("CoveringLock = %+v, %v; want the lock of the unit", first, err)
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

// testLeaveNeedsEndsLock proves the store refuses, in the transaction of the
// move, a move out of a collection with a manual lock when the caller may not
// end the lock. The unit stays covered. The same move with the right to end the
// lock goes through, and a move out of a collection with no lock needs no right.
func testLeaveNeedsEndsLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, testReason)

	m := collection.Move{
		Unit:     f.web,
		From:     f.payments.Ref.Name,
		To:       f.platform.Ref,
		ToID:     f.platform.ID,
		EndsLock: false,
	}

	err := s.SetUnitCollection(t.Context(), m, f.ns.Fence(), auditFor(f.ns.Fence()))
	if !errors.Is(err, collection.ErrMoveOutOfLockedCollection) {
		t.Fatalf("move out without the right to end the lock: err = %v, want ErrMoveOutOfLockedCollection", err)
	}

	if got, covErr := s.CoveringLock(t.Context(), f.web); covErr != nil || got.Holder != alice {
		t.Errorf("after the refused move: covering lock = %+v, %v; want the lock of the collection", got, covErr)
	}

	// A move inside the same collection ends no coverage.
	same := collection.Move{
		Unit:     f.web,
		From:     f.payments.Ref.Name,
		To:       f.payments.Ref,
		ToID:     f.payments.ID,
		EndsLock: false,
	}
	if sameErr := s.SetUnitCollection(t.Context(), same, f.ns.Fence(), auditFor(f.ns.Fence())); sameErr != nil {
		t.Errorf("move into the same collection: %v", sameErr)
	}

	m.EndsLock = true
	if allowErr := s.SetUnitCollection(t.Context(), m, f.ns.Fence(), auditFor(f.ns.Fence())); allowErr != nil {
		t.Fatalf("move out with the right to end the lock: %v", allowErr)
	}

	// platform has no lock: leaving it needs no right.
	back := collection.Move{
		Unit:     f.web,
		From:     f.platform.Ref.Name,
		To:       f.payments.Ref,
		ToID:     f.payments.ID,
		EndsLock: false,
	}
	if backErr := s.SetUnitCollection(t.Context(), back, f.ns.Fence(), auditFor(f.ns.Fence())); backErr != nil {
		t.Errorf("move out of a collection with no lock: %v", backErr)
	}
}

// testRestoreUnderLock proves the restore of a deleted workspace and of a
// deleted stack is refused, and rolled back, while a manual lock covers the
// place the unit comes back to: its collection or its namespace. The unit stays
// deleted. After the lock goes, the restore works.
func testRestoreUnderLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)

	t.Run("workspace in a locked collection", func(t *testing.T) {
		f.put(t, f.web, f.payments)
		restoreWorkspaceUnderLock(t, f, f.web, manuallock.CollectionTarget(f.payments.Ref))
	})
	t.Run("workspace in a locked namespace", func(t *testing.T) {
		restoreWorkspaceUnderLock(t, f, f.api, manuallock.NamespaceTarget(f.ns.Name))
	})
	t.Run("stack in a locked namespace", func(t *testing.T) {
		restoreStackUnderLock(t, f, manuallock.NamespaceTarget(f.ns.Name))
	})
}

// restoreWorkspaceUnderLock deletes the workspace unit, takes the lock target,
// and checks the restore against it.
func restoreWorkspaceUnderLock(t *testing.T, f *fixture, unit collection.UnitRef, target manuallock.Target) {
	t.Helper()

	ctx, fence := t.Context(), f.ns.Fence()

	ws, err := f.s.GetWorkspace(ctx, workspace.Ref{Namespace: f.ns.Name, Name: workspace.Name(unit.Name)})
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}

	deletion := inventory.Deletion{Entry: auditFor(fence), PurgeAfter: now().Add(time.Hour)}
	if delErr := f.s.DeleteWorkspace(ctx, ws.Ref, fence, deletion); delErr != nil {
		t.Fatalf("DeleteWorkspace: %v", delErr)
	}

	f.lock(t, target, alice, testReason)

	_, restoreErr := f.s.RestoreWorkspace(ctx, ws.ID, "", fence, auditFor(fence))
	if !errors.Is(restoreErr, manuallock.ErrLocked) {
		t.Fatalf("restore under the %s lock: err = %v, want ErrLocked", target.Kind, restoreErr)
	}

	if _, getErr := f.s.GetWorkspace(ctx, ws.Ref); !errors.Is(getErr, workspace.ErrNotFound) {
		t.Errorf("workspace after the refused restore: err = %v, want ErrNotFound", getErr)
	}

	f.forceRemove(t, target)

	if _, restoreErr := f.s.RestoreWorkspace(ctx, ws.ID, "", fence, auditFor(fence)); restoreErr != nil {
		t.Errorf("restore after the lock goes: %v", restoreErr)
	}
}

// restoreStackUnderLock deletes a stack, takes the lock target, and checks the
// restore against it.
func restoreStackUnderLock(t *testing.T, f *fixture, target manuallock.Target) {
	t.Helper()

	ctx, fence := t.Context(), f.ns.Fence()
	st := newPulumiStack(f.ns.Name, "web", "prod")

	if err := f.s.CreateStack(ctx, st, fence); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	deletion := inventory.Deletion{Entry: auditFor(fence), PurgeAfter: now().Add(time.Hour)}
	if err := f.s.ForceDeleteStack(ctx, st.Ref, fence, deletion); err != nil {
		t.Fatalf("ForceDeleteStack: %v", err)
	}

	f.lock(t, target, alice, testReason)

	if _, err := f.s.RestoreStack(ctx, st.ID, "", fence, auditFor(fence)); !errors.Is(err, manuallock.ErrLocked) {
		t.Fatalf("restore under the %s lock: err = %v, want ErrLocked", target.Kind, err)
	}

	if _, err := f.s.GetStack(ctx, st.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Errorf("stack after the refused restore: err = %v, want ErrNotFound", err)
	}

	f.forceRemove(t, target)

	if _, err := f.s.RestoreStack(ctx, st.ID, "", fence, auditFor(fence)); err != nil {
		t.Errorf("restore after the lock goes: %v", err)
	}
}

func testUnitLockWins(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)
	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), alice, "collection reason")
	f.lock(t, manuallock.UnitTarget(f.web), bob, "unit reason")

	got, err := s.CoveringLock(t.Context(), f.web)
	if err != nil || got.Holder != bob || got.Reason != "unit reason" {
		t.Errorf("covering lock = %+v, %v; want the lock of the unit", got, err)
	}

	unit := manuallock.UnitTarget(f.web)
	if _, rmErr := s.ForceRemoveLock(t.Context(), unit, f.ns.Fence(), lockAudit(f.ns.Fence())); rmErr != nil {
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

	if err := s.RenameStack(t.Context(), st.Ref, "prod-2", f.ns.Fence(), auditFor(f.ns.Fence())); err != nil {
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

	f.lock(t, manuallock.UnitTarget(f.web), bob, "unit reason")

	wantUnit := &inventory.ManualLock{
		Since: now(), Kind: string(manuallock.KindUnit), Object: f.web.String(), Holder: bob, Reason: "unit reason",
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

func testLockNamespace(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.NamespaceTarget(f.ns.Name)

	f.lock(t, target, alice, "release freeze")

	got, err := s.GetLock(t.Context(), target)
	if err != nil {
		t.Fatalf("GetLock: %v", err)
	}

	want := manuallock.Lock{Since: now(), Target: target, Holder: alice, Reason: "release freeze"}
	if got != want {
		t.Errorf("lock = %+v, want %+v", got, want)
	}

	if _, err := s.GetLock(t.Context(), manuallock.UnitTarget(f.web)); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("GetLock of a unit: err = %v, want ErrNotLocked: GetLock does not read the namespace", err)
	}
}

func testNamespaceLockCovers(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.NamespaceTarget(f.ns.Name)

	f.lock(t, target, alice, testReason)

	// The create of a workspace meets the lock of its namespace. A job whose
	// phase started before the lock passes it, so this workspace is made after
	// the lock, and the lock still covers it.
	later := newWorkspaceValue(t, f.ns, "later")
	passed := workspace.WithLockPass(t.Context(), now().Add(-time.Hour))

	if err := s.CreateWorkspace(passed, later, f.ns.Fence()); err != nil {
		t.Fatalf("create workspace %q: %v", "later", err)
	}

	laterUnit := collection.UnitRef{Namespace: f.ns.Name, Tool: toolOpenTofu, Name: "later"}
	stk := newPulumiStack(f.ns.Name, "proj", "dev")

	if err := s.CreateStack(t.Context(), stk, f.ns.Fence()); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	pulumi := collection.UnitRef{Namespace: f.ns.Name, Tool: toolPulumi, Name: "proj/dev"}

	for _, unit := range []collection.UnitRef{f.web, f.api, laterUnit, pulumi} {
		got, err := s.CoveringLock(t.Context(), unit)
		if err != nil || got.Holder != alice || got.Target != target {
			t.Errorf("%s: covering lock = %+v, %v; want the lock of the namespace", unit, got, err)
		}
	}

	missing := collection.UnitRef{Namespace: f.ns.Name, Tool: toolOpenTofu, Name: "absent"}
	if _, err := s.CoveringLock(t.Context(), missing); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("missing unit: err = %v, want ErrNotLocked", err)
	}
}

func testNamespaceLockIsolated(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	other := newNamespace(t, s, "globex")
	twin := createWorkspace(t, s, other, "web")

	f.lock(t, manuallock.NamespaceTarget(f.ns.Name), alice, testReason)

	if _, err := s.CoveringLock(t.Context(), twin); !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("unit of another namespace: err = %v, want ErrNotLocked", err)
	}

	l := manuallock.Lock{Since: now(), Target: manuallock.NamespaceTarget(f.ns.Name), Holder: alice, Reason: testReason}
	if err := s.PutLock(t.Context(), l, other.Fence(), auditFor(other.Fence())); err == nil {
		t.Error("PutLock of a namespace with the fence of another namespace succeeded")
	}
}

func testNearestLockWins(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)

	namespaceLock := manuallock.NamespaceTarget(f.ns.Name)
	collectionLock := manuallock.CollectionTarget(f.payments.Ref)
	unitLock := manuallock.UnitTarget(f.web)

	f.lock(t, namespaceLock, alice, "namespace reason")
	f.lock(t, collectionLock, bob, "collection reason")
	f.lock(t, unitLock, alice, "unit reason")

	// Each step removes the nearest lock, and the next one covers the unit.
	// The first step removes nothing.
	removals := []*manuallock.Target{nil, &unitLock, &collectionLock}
	wants := []manuallock.Target{unitLock, collectionLock, namespaceLock}

	for i, remove := range removals {
		if remove != nil {
			if _, err := s.ForceRemoveLock(t.Context(), *remove, f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
				t.Fatalf("remove %s: %v", remove, err)
			}
		}

		got, err := s.CoveringLock(t.Context(), f.web)
		if err != nil || got.Target != wants[i] {
			t.Errorf("step %d: covering lock = %+v, %v; want the lock of %s", i, got, err, wants[i])
		}
	}

	// The unit that is in no collection meets the namespace lock first.
	if got, err := s.CoveringLock(t.Context(), f.api); err != nil || got.Target != namespaceLock {
		t.Errorf("unit in no collection: covering lock = %+v, %v; want the lock of the namespace", got, err)
	}
}

func testNamespaceRemoval(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.NamespaceTarget(f.ns.Name)

	f.lock(t, target, alice, "release freeze")

	_, err := s.RemoveLock(t.Context(), target, bob, f.ns.Fence(), lockAudit(f.ns.Fence()))
	if !errors.Is(err, manuallock.ErrNotHolder) {
		t.Fatalf("removal by another principal: err = %v, want ErrNotHolder", err)
	}

	if _, getErr := s.GetLock(t.Context(), target); getErr != nil {
		t.Fatalf("lock after a refused removal: %v", getErr)
	}

	if _, rmErr := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence())); rmErr != nil {
		t.Fatalf("removal by the holder: %v", rmErr)
	}

	if _, getErr := s.GetLock(t.Context(), target); !errors.Is(getErr, manuallock.ErrNotLocked) {
		t.Errorf("lock after the removal: err = %v, want ErrNotLocked", getErr)
	}

	f.lock(t, target, alice, "again")

	if _, forceErr := s.ForceRemoveLock(t.Context(), target, f.ns.Fence(), lockAudit(f.ns.Fence())); forceErr != nil {
		t.Fatalf("force removal: %v", forceErr)
	}

	if _, coverErr := s.CoveringLock(t.Context(), f.web); !errors.Is(coverErr, manuallock.ErrNotLocked) {
		t.Errorf("covering lock after the force removal: err = %v, want ErrNotLocked", coverErr)
	}

	_, err = s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence()))
	if !errors.Is(err, manuallock.ErrNotLocked) {
		t.Errorf("removal of a missing namespace lock: err = %v, want ErrNotLocked", err)
	}
}

func testNamespaceAudit(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	target := manuallock.NamespaceTarget(f.ns.Name)

	f.lock(t, target, alice, "release freeze")

	if _, err := s.RemoveLock(t.Context(), target, alice, f.ns.Fence(), lockAudit(f.ns.Fence())); err != nil {
		t.Fatalf("RemoveLock: %v", err)
	}

	listing, err := s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	var locks []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == actionUnitLock {
			locks = append(locks, listing.Entries[i])
		}
	}

	if len(locks) != wantLockEntries {
		t.Fatalf("lock audit entries = %d, want %d (lock and unlock)", len(locks), wantLockEntries)
	}

	for i := range locks {
		if locks[i].ObjectID != f.ns.ID.String() {
			t.Errorf("audit ObjectID = %q, want the ID of the namespace %q", locks[i].ObjectID, f.ns.ID)
		}
	}
}

func testNamespaceInventoryFlag(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	f.put(t, f.web, f.payments)

	f.lock(t, manuallock.NamespaceTarget(f.ns.Name), alice, "namespace reason")

	wantNamespace := &inventory.ManualLock{
		Since:  now(),
		Kind:   string(manuallock.KindNamespace),
		Object: f.ns.Name.String(),
		Holder: alice,
		Reason: "namespace reason",
	}

	got := flags(t, s, f.ns.Name)
	for _, name := range []string{f.web.Name, f.api.Name} {
		if got[name] == nil || *got[name] != *wantNamespace {
			t.Errorf("flag of %s = %+v, want %+v", name, got[name], wantNamespace)
		}
	}

	f.lock(t, manuallock.CollectionTarget(f.payments.Ref), bob, "collection reason")

	if got := flags(t, s, f.ns.Name)[f.web.Name]; got == nil || got.Kind != string(manuallock.KindCollection) {
		t.Errorf("flag of the member with both locks = %+v, want the lock of the collection", got)
	}

	checkGetUnitFlags(t, s, f.ns.Name)
}
