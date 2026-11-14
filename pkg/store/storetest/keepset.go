package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// A sweep lists the blobs of an owner (a stack, a workspace) and deletes each
// one that is old and that no row names. The keep set is the set of keys that
// the rows of the owner name. Three rules keep the sweep from deleting a blob
// that a row names:
//
//   - the sweep reads the keep set by the same key that it lists by, and that
//     key is an immutable ID, so a rename between the two reads changes
//     nothing;
//   - an owner that does not exist is an error, never an empty set, because an
//     empty set would let the sweep delete every blob of the owner;
//   - every table that names a blob is in the keep set.
//
// RunKeepSetRules runs the first two rules against every port that reads a
// keep set. The third rule needs the tables of the port, so each port suite
// holds its own cases (for example the cases of the referenced keys).

// keepSetOwner is one owner of blobs in a keep set rig, with the operations
// that act on it. An operation that the kind of owner does not have is nil.
type keepSetOwner struct {
	// rename gives the owner a new name. Its ID stays.
	rename func(t *testing.T)
	// softDelete deletes the owner and keeps its rows for a restore.
	softDelete func(t *testing.T)
	// purge removes a soft-deleted owner and its rows for good.
	purge func(t *testing.T)
	// id is the immutable ID that the keep set is read by.
	id string
	// keys are the blob keys that the rows of the owner name.
	keys []blob.Key
}

// keepSetRig is a fresh store with the operations that the rules need.
type keepSetRig struct {
	// referenced reads the keep set of the owner with the given ID.
	referenced func(ctx context.Context, id string) ([]blob.Key, error)
	// add creates an owner whose rows name the given number of blob keys.
	add func(t *testing.T, keys int) keepSetOwner
	// missing is the error for an owner that does not exist.
	missing error
	// softDeleted is the error for a soft-deleted owner, or nil when a
	// soft-deleted owner still returns its keys.
	softDeleted error
	// renames tells that the owners of the port have a rename.
	renames bool
}

// keepSetPort is one store method that reads a keep set.
type keepSetPort struct {
	// open returns a rig over a fresh store.
	open func(t *testing.T) keepSetRig
	// name is the name of the method on the store.
	name string
}

// keepSetPorts returns one row for each store method that reads a keep set. A
// new method of that kind needs a row here: the reflection test of each store
// adapter fails until it has one. The factories are nil when the caller needs
// only the names.
func keepSetPorts(newUpdates UpdatesFactory, newWorkspaces WorkspaceFactory) []keepSetPort {
	return []keepSetPort{
		{name: "ReferencedBlobKeys", open: stackKeepSetRig(newUpdates)},
		{name: "ReferencedContentKeys", open: workspaceKeepSetRig(newWorkspaces)},
	}
}

// KeepSetPortNames returns the name of each store method that the keep set
// rules cover, ordered. A store adapter compares it with the methods of its
// store type whose names fit Referenced*Keys.
func KeepSetPortNames() []string {
	ports := keepSetPorts(nil, nil)
	names := make([]string, 0, len(ports))

	for _, port := range ports {
		names = append(names, port.name)
	}

	slices.Sort(names)

	return names
}

// RunKeepSetRules runs the keep set rules against every port that reads a
// keep set, on stores produced by the factories.
func RunKeepSetRules(t *testing.T, newUpdates UpdatesFactory, newWorkspaces WorkspaceFactory) {
	t.Helper()

	for _, port := range keepSetPorts(newUpdates, newWorkspaces) {
		t.Run(port.name, func(t *testing.T) {
			t.Parallel()

			for _, rule := range keepSetRules() {
				t.Run(rule.name, func(t *testing.T) {
					t.Parallel()

					rig := port.open(t)
					if rule.needsRename && !rig.renames {
						return
					}

					rule.run(t, rig)
				})
			}
		})
	}
}

// keepSetRule is one rule of the keep set.
type keepSetRule struct {
	run  func(t *testing.T, rig keepSetRig)
	name string
	// needsRename marks a rule that renames an owner. A port whose owners
	// have no rename does not run it.
	needsRename bool
}

func keepSetRules() []keepSetRule {
	return []keepSetRule{
		{name: "the set holds the keys of the owner and no other", run: ruleKeepSetOwnKeys, needsRename: false},
		{name: "an owner without rows gives an empty set and no error", run: ruleKeepSetNoRows, needsRename: false},
		{
			name:        "an unknown or empty ID is the missing-owner error and no keys",
			run:         ruleKeepSetMissing,
			needsRename: false,
		},
		{name: "a rename keeps the set read by the ID", run: ruleKeepSetRename, needsRename: true},
		{name: "a soft delete gives the documented answer", run: ruleKeepSetSoftDelete, needsRename: false},
		{name: "a purge makes the owner missing", run: ruleKeepSetPurge, needsRename: false},
	}
}

func ruleKeepSetOwnKeys(t *testing.T, rig keepSetRig) {
	t.Helper()

	first := rig.add(t, 2)
	second := rig.add(t, 1)

	assertKeepSet(t, rig, first.id, first.keys)
	assertKeepSet(t, rig, second.id, second.keys)
}

func ruleKeepSetNoRows(t *testing.T, rig keepSetRig) {
	t.Helper()

	empty := rig.add(t, 0)

	assertKeepSet(t, rig, empty.id, nil)
}

func ruleKeepSetMissing(t *testing.T, rig keepSetRig) {
	t.Helper()

	// A live owner with keys exists, so an answer for another ID that leaks
	// its keys shows too.
	rig.add(t, 1)

	for _, id := range []string{"no-such-owner-id", ""} {
		keys, err := rig.referenced(t.Context(), id)
		if !errors.Is(err, rig.missing) || len(keys) != 0 {
			t.Fatalf("keep set of the ID %q = %v, %v; want no keys and %v", id, keys, err, rig.missing)
		}
	}
}

func ruleKeepSetRename(t *testing.T, rig keepSetRig) {
	t.Helper()

	owner := rig.add(t, 2)
	owner.rename(t)

	assertKeepSet(t, rig, owner.id, owner.keys)
}

func ruleKeepSetSoftDelete(t *testing.T, rig keepSetRig) {
	t.Helper()

	owner := rig.add(t, 2)
	owner.softDelete(t)

	if rig.softDeleted == nil {
		assertKeepSet(t, rig, owner.id, owner.keys)

		return
	}

	keys, err := rig.referenced(t.Context(), owner.id)
	if !errors.Is(err, rig.softDeleted) || len(keys) != 0 {
		t.Fatalf("keep set of a soft-deleted owner = %v, %v; want no keys and %v", keys, err, rig.softDeleted)
	}
}

func ruleKeepSetPurge(t *testing.T, rig keepSetRig) {
	t.Helper()

	owner := rig.add(t, 2)
	owner.softDelete(t)
	owner.purge(t)

	keys, err := rig.referenced(t.Context(), owner.id)
	if !errors.Is(err, rig.missing) || len(keys) != 0 {
		t.Fatalf("keep set of a purged owner = %v, %v; want no keys and %v", keys, err, rig.missing)
	}
}

// assertKeepSet checks that the keep set of the owner id holds want, order
// aside, and no error.
func assertKeepSet(t *testing.T, rig keepSetRig, id string, want []blob.Key) {
	t.Helper()

	got, err := rig.referenced(t.Context(), id)
	if err != nil {
		t.Fatalf("keep set of %s: %v", id, err)
	}

	if !sameKeys(got, want) {
		t.Fatalf("keep set of %s = %v, want %v", id, got, want)
	}
}

// sameKeys reports whether a and b hold the same keys, order aside.
func sameKeys(a, b []blob.Key) bool {
	left, right := slices.Clone(a), slices.Clone(b)

	slices.Sort(left)
	slices.Sort(right)

	return slices.Equal(left, right)
}

// stackKeepSetRig returns the opener of the rig of stack.CheckpointStore.
func stackKeepSetRig(newStore UpdatesFactory) func(t *testing.T) keepSetRig {
	return func(t *testing.T) keepSetRig {
		t.Helper()

		s := newStore(t)
		ns := newNamespace(t, s, "acme")
		f := fixture{s: s, ref: stack.Ref{Namespace: "", Project: "", Stack: ""}, fence: ns.Fence()}
		count := 0

		return keepSetRig{
			referenced: func(ctx context.Context, id string) ([]blob.Key, error) {
				return s.ReferencedBlobKeys(ctx, stack.ID(id))
			},
			add: func(t *testing.T, keys int) keepSetOwner {
				t.Helper()

				count++

				return addKeepSetStack(t, f, ns.Name, fmt.Sprintf("s%d", count), keys)
			},
			missing:     stack.ErrNotFound,
			softDeleted: stack.ErrNotFound,
			renames:     true,
		}
	}
}

// addKeepSetStack creates the stack called name, with one update and one
// checkpoint for each of keys.
func addKeepSetStack(t *testing.T, f fixture, ns namespace.Name, name string, keys int) keepSetOwner {
	t.Helper()

	st := newStack(ns, "web", name)
	if err := f.s.CreateStack(t.Context(), st, f.fence); err != nil {
		t.Fatalf("create stack %s: %v", st.Ref, err)
	}

	ref := st.Ref
	guard := stack.EmptyGuard()
	owned := make([]blob.Key, 0, keys)

	for i := range keys {
		u := f.run(t, ref, fmt.Sprintf("%s-u%d", name, i), mutating())
		key := f.blobKey(t, u, 0)
		deployment := fmt.Sprintf(`{"v":%d}`, i+1)

		if _, err := f.s.PutCheckpoint(t.Context(), newBlobCheckpoint(u, key, 0, deployment), f.fence); err != nil {
			t.Fatalf("put checkpoint of %s: %v", u.ID, err)
		}

		f.end(t, u, stack.StatusSucceeded)

		owned = append(owned, key)
		guard = countedGuard(u, deployment)
	}

	id := f.stackIDOf(t, ref)

	return keepSetOwner{
		id:   id.String(),
		keys: owned,
		rename: func(t *testing.T) {
			t.Helper()

			renamed := ref
			renamed.Stack = stack.Name(name + "-renamed")

			err := f.s.RenameStack(
				t.Context(), ref, renamed.Stack, f.fence, renameEntry(ref.Namespace, ref),
			)
			if err != nil {
				t.Fatalf("rename stack %s: %v", ref, err)
			}

			ref = renamed
		},
		softDelete: func(t *testing.T) {
			t.Helper()

			err := f.s.DeleteStack(t.Context(), ref, f.fence, guard, inventory.Deletion{
				Entry: deleteEntry(ns), PurgeAfter: purgeAt(),
			})
			if err != nil {
				t.Fatalf("delete stack %s: %v", ref, err)
			}
		},
		purge: func(t *testing.T) {
			t.Helper()

			err := f.s.PurgeStack(t.Context(), id, f.fence, time.Time{}, lifecycleEntry(ns, "unit:purge"))
			if err != nil {
				t.Fatalf("purge stack %s: %v", id, err)
			}
		},
	}
}

// workspaceKeepSetRig returns the opener of the rig of workspace.StateStore.
// A workspace has no rename, so the rig runs no rename rule.
func workspaceKeepSetRig(newStore WorkspaceFactory) func(t *testing.T) keepSetRig {
	return func(t *testing.T) keepSetRig {
		t.Helper()

		s := newStore(t)
		ns := newWorkspaceNamespace(t, s, "acme")
		count := 0

		return keepSetRig{
			referenced: func(ctx context.Context, id string) ([]blob.Key, error) {
				return s.ReferencedContentKeys(ctx, workspace.ID(id))
			},
			add: func(t *testing.T, keys int) keepSetOwner {
				t.Helper()

				count++

				return addKeepSetWorkspace(t, s, ns, fmt.Sprintf("w%d", count), keys)
			},
			missing: workspace.ErrNotFound,
			// The versions of a soft-deleted workspace stay for a restore,
			// so the keep set keeps its keys.
			softDeleted: nil,
			renames:     false,
		}
	}
}

// addKeepSetWorkspace creates the workspace called name, with one finalized
// state version for each of keys, and no lock.
func addKeepSetWorkspace(
	t *testing.T, s WorkspaceStore, ns namespace.Namespace, name string, keys int,
) keepSetOwner {
	t.Helper()

	w := createTestWorkspace(t, s, ns, name)
	owned := make([]blob.Key, 0, keys)

	if keys > 0 {
		locked := lockTestWorkspace(t, s, ns, w, "alice")

		for i := range keys {
			pending := createPendingVersion(t, s, ns, newPendingVersion(
				t, locked, locked.Lock.ID, stateInput("l1", fmt.Sprintf("v%d", i+1), int64(i+1)),
			))
			finalized := finalizedVersion(pending, now().Add(2*time.Hour), "v")

			if err := finalizeRecorded(t, s, ns.Fence(), finalized); err != nil {
				t.Fatalf("finalize state version %s: %v", pending.ID, err)
			}

			owned = append(owned, finalized.Content)
		}

		unlockTestWorkspace(t, s, ns, w.Ref)
	}

	return keepSetOwner{
		id:     w.ID.String(),
		keys:   owned,
		rename: nil,
		softDelete: func(t *testing.T) {
			t.Helper()

			err := s.DeleteWorkspace(t.Context(), w.Ref, ns.Fence(), inventory.Deletion{
				Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt(),
			})
			if err != nil {
				t.Fatalf("delete workspace %s: %v", w.Ref, err)
			}
		},
		purge: func(t *testing.T) {
			t.Helper()

			err := s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), time.Time{}, lifecycleEntry(ns.Name, "unit:purge"))
			if err != nil {
				t.Fatalf("purge workspace %s: %v", w.ID, err)
			}
		},
	}
}
