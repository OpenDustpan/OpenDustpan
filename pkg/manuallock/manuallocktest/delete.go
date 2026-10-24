package manuallocktest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// actionRaceDelete is the audit action of the delete in the race case. It keeps
// the entry apart from the entry of the lock.
const actionRaceDelete = "test:delete"

// raceRounds is the number of lock-and-delete races for each scope and tool.
const raceRounds = 20

// deletable is a unit that a case can delete, test and restore.
type deletable struct {
	unit    collection.UnitRef
	remove  func(ctx context.Context, e audit.Entry) error
	live    func(ctx context.Context) bool
	restore func(ctx context.Context) error
}

// deleteTool makes a unit of one tool, with the soft delete that the case
// runs on it and the error that the delete returns for a manual lock.
type deleteTool struct {
	name string
	// sentinel is the error of the tool that a refused delete matches too.
	sentinel error
	make     func(t *testing.T, f *fixture, name string) deletable
}

// deleteTools returns the soft deletes that the store offers: the workspace
// delete, the stack delete and the forced stack delete.
func deleteTools() []deleteTool {
	return []deleteTool{
		{name: "workspace", sentinel: workspace.ErrLocked, make: makeWorkspace},
		{name: "stack", sentinel: stack.ErrUpdateConflict, make: makePlainStack},
		{name: "forced stack", sentinel: stack.ErrUpdateConflict, make: makeForcedStack},
	}
}

func deletionOf(e audit.Entry) inventory.Deletion {
	return inventory.Deletion{Entry: e, PurgeAfter: now().Add(time.Hour)}
}

func makeWorkspace(t *testing.T, f *fixture, name string) deletable {
	t.Helper()

	unit := createWorkspace(t, f.s, f.ns, name)
	ref := workspace.Ref{Namespace: f.ns.Name, Name: mustWorkspaceName(t, name)}

	w, err := f.s.GetWorkspace(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetWorkspace %s: %v", name, err)
	}

	return deletable{
		unit: unit,
		remove: func(ctx context.Context, e audit.Entry) error {
			return f.s.DeleteWorkspace(ctx, ref, f.ns.Fence(), deletionOf(e))
		},
		live: func(ctx context.Context) bool {
			_, getErr := f.s.GetWorkspace(ctx, ref)

			return getErr == nil
		},
		restore: func(ctx context.Context) error {
			_, restoreErr := f.s.RestoreWorkspace(ctx, w.ID, "", f.ns.Fence(), auditFor(f.ns.Fence()))

			return restoreErr
		},
	}
}

func makePlainStack(t *testing.T, f *fixture, name string) deletable {
	t.Helper()

	return makeStack(t, f, name, func(ctx context.Context, st stack.Stack, e audit.Entry) error {
		return f.s.DeleteStack(ctx, st.Ref, f.ns.Fence(), stack.EmptyGuard(), deletionOf(e))
	})
}

func makeForcedStack(t *testing.T, f *fixture, name string) deletable {
	t.Helper()

	return makeStack(t, f, name, func(ctx context.Context, st stack.Stack, e audit.Entry) error {
		return f.s.ForceDeleteStack(ctx, st.Ref, f.ns.Fence(), deletionOf(e))
	})
}

// makeStack creates a stack of the Pulumi CLI in the namespace of f. The
// delete that remove runs is the one under test.
func makeStack(
	t *testing.T, f *fixture, name string, remove func(context.Context, stack.Stack, audit.Entry) error,
) deletable {
	t.Helper()

	st := newPulumiStack(f.ns.Name, "web", name)
	if err := f.s.CreateStack(t.Context(), st, f.ns.Fence()); err != nil {
		t.Fatalf("CreateStack %s: %v", name, err)
	}

	return deletable{
		unit: collection.UnitRef{Namespace: f.ns.Name, Tool: toolPulumi, Name: "web/" + name},
		remove: func(ctx context.Context, e audit.Entry) error {
			return remove(ctx, st, e)
		},
		live: func(ctx context.Context) bool {
			_, getErr := f.s.GetStack(ctx, st.Ref)

			return getErr == nil
		},
		restore: func(ctx context.Context) error {
			_, restoreErr := f.s.RestoreStack(ctx, st.ID, "", f.ns.Fence(), auditFor(f.ns.Fence()))

			return restoreErr
		},
	}
}

// The lock scopes of the cases.
const (
	scopeUnit       = "unit"
	scopeCollection = "collection"
	scopeNamespace  = "namespace"
)

// lockScope is the object a manual lock sits on, seen from one unit.
type lockScope struct {
	name string
	// covering puts the lock target that covers unit u.
	covering func(t *testing.T, f *fixture, u collection.UnitRef) manuallock.Target
	// other returns the target of a lock that covers a different unit only.
	// A nil other means the scope has no such target in the same namespace.
	other func(t *testing.T, f *fixture, seq int) manuallock.Target
}

func lockScopes() []lockScope {
	return []lockScope{
		{
			name: scopeUnit,
			covering: func(_ *testing.T, _ *fixture, u collection.UnitRef) manuallock.Target {
				return manuallock.UnitTarget(u)
			},
			other: func(t *testing.T, f *fixture, seq int) manuallock.Target {
				t.Helper()

				return manuallock.UnitTarget(createWorkspace(t, f.s, f.ns, fmt.Sprintf("bystander-%d", seq)))
			},
		},
		{
			name: scopeCollection,
			covering: func(t *testing.T, f *fixture, u collection.UnitRef) manuallock.Target {
				t.Helper()

				f.put(t, u, f.payments)

				return manuallock.CollectionTarget(f.payments.Ref)
			},
			other: func(_ *testing.T, f *fixture, _ int) manuallock.Target {
				return manuallock.CollectionTarget(f.platform.Ref)
			},
		},
		{
			name: scopeNamespace,
			covering: func(_ *testing.T, f *fixture, _ collection.UnitRef) manuallock.Target {
				return manuallock.NamespaceTarget(f.ns.Name)
			},
			other: nil,
		},
	}
}

// testDeleteUnderLock proves a soft delete is refused in the store, and rolled
// back, while a manual lock covers the unit on the unit, on its collection or
// on its namespace. The unit stays live. After the lock goes, the delete works.
// A lock on a different unit does not stop the delete.
func testDeleteUnderLock(t *testing.T, s Store) {
	t.Helper()

	f := newFixture(t, s)
	seq := 0

	for _, scope := range lockScopes() {
		for _, tool := range deleteTools() {
			t.Run(scope.name+" lock, "+tool.name, func(t *testing.T) {
				seq++
				deleteUnderLock(t, f, scope, tool, seq)
			})
			t.Run(scope.name+" lock elsewhere, "+tool.name, func(t *testing.T) {
				seq++
				deleteWithOtherLock(t, f, scope, tool, seq)
			})
		}
	}
}

// deleteUnderLock checks the delete of one new unit against a lock that covers it.
func deleteUnderLock(t *testing.T, f *fixture, scope lockScope, tool deleteTool, seq int) {
	t.Helper()

	unit := tool.make(t, f, fmt.Sprintf("victim-%d", seq))
	target := scope.covering(t, f, unit.unit)

	f.lock(t, target, alice, testReason)

	before := auditEntryCount(t, f)

	err := unit.remove(t.Context(), auditFor(f.ns.Fence()))
	if !errors.Is(err, manuallock.ErrLocked) || !errors.Is(err, tool.sentinel) {
		t.Fatalf("delete under the %s lock: err = %v, want ErrLocked and %v", scope.name, err, tool.sentinel)
	}

	if locked, ok := errors.AsType[*manuallock.LockedError](err); !ok || locked == nil || locked.Unit != unit.unit {
		t.Fatalf(
			"delete under the %s lock: err = %v, want a refusal that names the unit %v",
			scope.name,
			err,
			unit.unit,
		)
	}

	if after := auditEntryCount(t, f); after != before {
		t.Errorf("audit entries after the refused delete = %d, want %d", after, before)
	}

	if !unit.live(t.Context()) {
		t.Error("the unit is not live after the refused delete")
	}

	f.forceRemove(t, target)

	if delErr := unit.remove(t.Context(), auditFor(f.ns.Fence())); delErr != nil {
		t.Errorf("delete after the lock goes: %v", delErr)
	}

	if unit.live(t.Context()) {
		t.Error("the unit is live after the delete")
	}
}

// auditEntryCount returns the number of audit entries of the namespace.
func auditEntryCount(t *testing.T, f *fixture) int {
	t.Helper()

	listing, err := f.s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	return len(listing.Entries)
}

// deleteWithOtherLock checks that a lock that covers a different unit does not
// stop the delete. A namespace lock covers a different unit only in another
// namespace.
func deleteWithOtherLock(t *testing.T, f *fixture, scope lockScope, tool deleteTool, seq int) {
	t.Helper()

	unit := tool.make(t, f, fmt.Sprintf("other-%d", seq))

	if scope.other == nil {
		other := newNamespace(t, f.s, fmt.Sprintf("globex-%d", seq))
		l := manuallock.Lock{
			Since: now(), Target: manuallock.NamespaceTarget(other.Name), Holder: alice, Reason: testReason,
		}

		if err := f.s.PutLock(t.Context(), l, other.Fence(), lockAudit(other.Fence())); err != nil {
			t.Fatalf("PutLock %s: %v", l.Target, err)
		}
	} else {
		target := scope.other(t, f, seq)
		f.lock(t, target, alice, testReason)

		defer f.forceRemove(t, target)
	}

	if err := unit.remove(t.Context(), auditFor(f.ns.Fence())); err != nil {
		t.Errorf("delete with a %s lock on another unit: %v", scope.name, err)
	}
}

// testDeleteLockRace runs a lock and a delete of the same unit at the same time
// many times. The two must not both win with the lock first: a delete that
// commits after a lock that covers the unit would leave a deleted unit with a
// lock that no route can remove. Each round has its own namespace, so the audit
// log of the namespace gives the order in which the two writes committed.
func testDeleteLockRace(t *testing.T, s Store) {
	t.Helper()

	rounds := 0

	for _, scope := range lockScopes() {
		for _, tool := range deleteTools() {
			t.Run(scope.name+" lock, "+tool.name, func(t *testing.T) {
				for range raceRounds {
					rounds++
					raceOnce(t, s, scope, tool, rounds)
				}
			})
		}
	}
}

func raceOnce(t *testing.T, s Store, scope lockScope, tool deleteTool, round int) {
	t.Helper()

	ns := newNamespace(t, s, fmt.Sprintf("race-%d", round))
	f := &fixture{
		s: s, ns: ns,
		web:      collection.UnitRef{Namespace: "", Tool: "", Name: ""},
		api:      collection.UnitRef{Namespace: "", Tool: "", Name: ""},
		payments: newCollection(t, ns.Name, "payments"),
		platform: newCollection(t, ns.Name, "platform"),
	}

	if err := s.CreateCollection(t.Context(), f.payments, ns.Fence(), auditFor(ns.Fence())); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	unit := tool.make(t, f, "victim")
	target := scope.covering(t, f, unit.unit)

	var lockErr, delErr error

	start := make(chan struct{})

	var wg sync.WaitGroup

	wg.Go(func() {
		<-start

		lockErr = f.tryLock(t, target, alice, testReason)
	})

	wg.Go(func() {
		<-start

		e := auditFor(ns.Fence())
		e.Action = actionRaceDelete
		delErr = unit.remove(t.Context(), e)
	})

	close(start)
	wg.Wait()

	checkRace(t, f, raceOutcome{scope: scope.name, lockErr: lockErr, delErr: delErr, unit: unit, tool: tool})
}

// raceOutcome is the result of one race.
type raceOutcome struct {
	scope   string
	lockErr error
	delErr  error
	unit    deletable
	tool    deleteTool
}

// checkRace asserts the invariant of the race: the unit is never deleted under
// a lock that covers it.
func checkRace(t *testing.T, f *fixture, o raceOutcome) {
	t.Helper()

	switch {
	case o.delErr != nil:
		// The lock won. The delete is refused and the unit stays live under the lock.
		if !errors.Is(o.delErr, manuallock.ErrLocked) || !errors.Is(o.delErr, o.tool.sentinel) {
			t.Fatalf("delete: err = %v, want nil or ErrLocked", o.delErr)
		}

		if o.lockErr != nil || !o.unit.live(t.Context()) {
			t.Fatalf("delete refused, lock err = %v, live = %v; want the lock held and the unit live",
				o.lockErr, o.unit.live(t.Context()))
		}
	case o.lockErr != nil:
		// The delete won. A unit lock then finds no unit. A lock on a collection or a
		// namespace never fails for a deleted unit.
		if o.scope != scopeUnit || !errors.Is(o.lockErr, manuallock.ErrTargetNotFound) {
			t.Fatalf("lock: err = %v, delete succeeded", o.lockErr)
		}

		if restoreErr := o.unit.restore(t.Context()); restoreErr != nil {
			t.Fatalf("restore after the delete won: %v", restoreErr)
		}
	default:
		// Both succeeded: the lock must have committed after the delete.
		checkLockAfterDelete(t, f, o)
	}
}

// checkLockAfterDelete reads the audit log of the namespace of f and fails when
// the lock committed before the delete, that is, when the delete ran under a
// lock that covers the unit.
func checkLockAfterDelete(t *testing.T, f *fixture, o raceOutcome) {
	t.Helper()

	listing, err := f.s.List(t.Context(), f.ns.Name, audit.Page{Number: 1, Size: auditPageSize})
	if err != nil {
		t.Fatalf("List audit: %v", err)
	}

	var lockSeq, delSeq int64

	for i := range listing.Entries {
		switch e := listing.Entries[i]; e.Action {
		case actionUnitLock:
			lockSeq = e.Sequence
		case actionRaceDelete:
			delSeq = e.Sequence
		default:
		}
	}

	if lockSeq == 0 || delSeq == 0 {
		t.Fatalf("audit entries: lock sequence %d, delete sequence %d; want both entries", lockSeq, delSeq)
	}

	if lockSeq < delSeq {
		t.Fatalf("%s lock committed at %d, before the delete at %d: the unit was deleted under the lock",
			o.scope, lockSeq, delSeq)
	}
}
