package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// lifecycleEntry returns the audit entry of a restore or a purge in ns, with
// the given action. The store appends it in the transaction of the change.
func lifecycleEntry(ns namespace.Name, action string) audit.Entry {
	e := deleteEntry(ns)
	e.Action = action

	return e
}

// deleteFixtureStack soft-deletes the stack of the fixture and returns it as
// it was before the delete.
func deleteFixtureStack(t *testing.T, f fixture) stack.Stack {
	t.Helper()

	return deleteFixtureStackWith(t, f, stack.EmptyGuard())
}

// deleteFixtureStackWith deletes the fixture stack under guard and returns
// the stack as it was before the delete.
func deleteFixtureStackWith(t *testing.T, f fixture, guard stack.DeleteGuard) stack.Stack {
	t.Helper()

	st := mustGetFixtureStack(t, f)

	err := f.s.DeleteStack(t.Context(), f.ref, f.fence, guard, inventory.Deletion{
		Entry: deleteEntry(f.ref.Namespace), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	return st
}

// testRestoreStackUnderItsName deletes the stack and restores it with no new
// name: the stack is back under its name with its stable ID, its live
// inventory entry is back, and the deleted units are empty.
func testRestoreStackUnderItsName(t *testing.T, f fixture) {
	t.Helper()

	before := deleteFixtureStack(t, f)

	ref, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, lifecycleEntry(f.ref.Namespace, "unit:restore"))
	if err != nil {
		t.Fatalf("restore stack: %v", err)
	}

	if ref != f.ref {
		t.Fatalf("restored reference = %s, want %s", ref, f.ref)
	}

	assertStackUnchanged(t, f, before)
}

// testRestoreStackUnderANewName deletes the stack, takes its name with a new
// stack, and restores the deleted stack: under the old name it answers
// ErrAlreadyExists and changes nothing; under a new name in the same project
// it is back with its stable ID.
func testRestoreStackUnderANewName(t *testing.T, f fixture) {
	t.Helper()

	before := deleteFixtureStack(t, f)

	taker := newStack(f.ref.Namespace, f.ref.Project.String(), f.ref.Stack.String())
	if err := f.s.CreateStack(t.Context(), taker, f.fence); err != nil {
		t.Fatalf("create stack with the deleted name: %v", err)
	}

	entry := lifecycleEntry(f.ref.Namespace, "unit:restore")

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, entry); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("restore under a taken name: err = %v, want ErrAlreadyExists", err)
	}

	checkDeletedStackEntries(t, f.s, f.ref.Namespace, before.ID.String())

	ref, err := f.s.RestoreStack(t.Context(), before.ID, "restored", f.fence, entry)
	if err != nil {
		t.Fatalf("restore under a new name: %v", err)
	}

	want := stack.Ref{Namespace: f.ref.Namespace, Project: f.ref.Project, Stack: "restored"}
	if ref != want {
		t.Fatalf("restored reference = %s, want %s", ref, want)
	}

	got, err := f.s.GetStack(t.Context(), want)
	if err != nil || got.ID != before.ID {
		t.Fatalf("restored stack = %+v, %v, want ID %s", got, err, before.ID)
	}

	checkRestoreEntry(t, f.s, f.ref.Namespace, "pulumi:acme/"+f.ref.Project.String()+"/restored", before.ID.String())
}

// checkRestoreEntry checks that the newest audit entry of ns is the restore
// of the unit id, and that it names the unit under its name after the
// restore. A store with no audit list has no entry to check.
func checkRestoreEntry(t *testing.T, s any, ns namespace.Name, wantObject, id string) {
	t.Helper()

	lister, ok := storeAs[auditLister](s)
	if !ok {
		return
	}

	listing, err := lister.List(t.Context(), ns, audit.Page{Number: 1, Size: 1})
	if err != nil || len(listing.Entries) != 1 {
		t.Fatalf("list audit entries: %+v, %v", listing, err)
	}

	got := listing.Entries[0]
	if got.Action != "unit:restore" || got.Object != wantObject || got.ObjectID != id {
		t.Fatalf(
			"restore entry = %s %s %s, want unit:restore %s %s",
			got.Action,
			got.Object,
			got.ObjectID,
			wantObject,
			id,
		)
	}
}

// testRestoreOrPurgeUnknownStack restores and purges an unknown ID and a
// live stack: each answers ErrNotFound, and the live stack stays.
func testRestoreOrPurgeUnknownStack(t *testing.T, f fixture) {
	t.Helper()

	live := mustGetFixtureStack(t, f)
	entry := lifecycleEntry(f.ref.Namespace, "unit:purge")

	for _, id := range []stack.ID{"stk-unknown", live.ID} {
		if _, err := f.s.RestoreStack(t.Context(), id, "", f.fence, entry); !errors.Is(err, stack.ErrNotFound) {
			t.Fatalf("restore %s: err = %v, want ErrNotFound", id, err)
		}

		if err := f.s.PurgeStack(t.Context(), id, f.fence, time.Time{}, entry); !errors.Is(err, stack.ErrNotFound) {
			t.Fatalf("purge %s: err = %v, want ErrNotFound", id, err)
		}
	}

	assertStackUnchanged(t, f, live)
}

// testPurgeStack runs an update with a checkpoint and events, deletes the
// stack and purges it: the deleted units are empty, the name finds nothing,
// and a restore or a second purge answers ErrNotFound.
func testPurgeStack(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	if _, err := f.s.PutCheckpoint(t.Context(), f.checkpoint(t, u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	if err := f.s.AppendEvents(t.Context(), newEvents(u, 1, 0, 1), f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	before := deleteFixtureStackWith(t, f, countedGuard(u, `{"v":1}`))
	entry := lifecycleEntry(f.ref.Namespace, "unit:purge")

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, time.Time{}, entry); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	checkBlobPurge(t, f.s, "pulumi", before.ID.String(), f.ref.Namespace)

	deleted, err := f.s.ListDeletedUnits(t.Context(), f.ref.Namespace)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted units after the purge = %+v, %v, want none", deleted, err)
	}

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, entry); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("restore after the purge: err = %v, want ErrNotFound", err)
	}

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, time.Time{}, entry); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("second purge: err = %v, want ErrNotFound", err)
	}
}

// testRestoreRacesPurge runs a restore and a purge of the same deleted stack
// at the same time (ADR 47: the sweep purges while a person restores). Each
// runs in its own fenced transaction, so exactly one wins and the other
// answers ErrNotFound and changes nothing.
func testRestoreRacesPurge(t *testing.T, f fixture) {
	t.Helper()

	before := deleteFixtureStack(t, f)
	entry := lifecycleEntry(f.ref.Namespace, "unit:restore")

	var (
		wg                   sync.WaitGroup
		restoreErr, purgeErr error
	)

	wg.Go(func() {
		_, restoreErr = f.s.RestoreStack(t.Context(), before.ID, "", f.fence, entry)
	})

	wg.Go(func() {
		purgeErr = f.s.PurgeStack(t.Context(), before.ID, f.fence, time.Time{}, entry)
	})

	wg.Wait()

	switch {
	case restoreErr == nil && errors.Is(purgeErr, stack.ErrNotFound):
		assertStackUnchanged(t, f, before)
	case purgeErr == nil && errors.Is(restoreErr, stack.ErrNotFound):
		if _, err := f.s.GetStack(t.Context(), f.ref); !errors.Is(err, stack.ErrNotFound) {
			t.Fatalf("stack after a winning purge: err = %v, want ErrNotFound", err)
		}
	default:
		t.Fatalf("restore err = %v, purge err = %v, want exactly one to win and the other ErrNotFound",
			restoreErr, purgeErr)
	}
}

// testRestoreFailedAppendChangesNothing breaks the audit appends, then
// restores and purges a deleted stack: both fail, and the stack stays
// deleted with its deleted inventory entry (ADR 60, fail closed).
func testRestoreFailedAppendChangesNothing(t *testing.T, f fixture) {
	t.Helper()

	breaker, ok := f.s.(AuditBreaker)
	if !ok {
		t.Skip("store cannot make an audit append fail")
	}

	before := deleteFixtureStack(t, f)

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	entry := lifecycleEntry(f.ref.Namespace, "unit:restore")

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, entry); err == nil {
		t.Fatal("restore with a failing audit append: err = nil, want an error")
	}

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, time.Time{}, entry); err == nil {
		t.Fatal("purge with a failing audit append: err = nil, want an error")
	}

	if _, err := f.s.GetStack(t.Context(), f.ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("stack after the failed restore: err = %v, want ErrNotFound", err)
	}

	checkDeletedStackEntries(t, f.s, f.ref.Namespace, before.ID.String())
}

// deleteTestWorkspace creates workspace "network" in ns and deletes it.
func deleteTestWorkspace(t *testing.T, s WorkspaceStore, ns namespace.Namespace) workspace.Workspace {
	t.Helper()

	w := createTestWorkspace(t, s, ns, "network")

	err := s.DeleteWorkspace(t.Context(), w.Ref, ns.Fence(), inventory.Deletion{
		Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	return w
}

// testRestoreWorkspace deletes a workspace, takes its name with a new one,
// and restores it: under the old name it answers ErrAlreadyExists, under a
// new name it is back with its ID, and the deleted units are empty.
func testRestoreWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	gone := deleteTestWorkspace(t, s, ns)
	createTestWorkspace(t, s, ns, "network")

	entry := lifecycleEntry(ns.Name, "unit:restore")

	if _, err := s.RestoreWorkspace(t.Context(), gone.ID, "", ns.Fence(), entry); !errors.Is(
		err, workspace.ErrAlreadyExists,
	) {
		t.Fatalf("restore under a taken name: err = %v, want ErrAlreadyExists", err)
	}

	ref, err := s.RestoreWorkspace(t.Context(), gone.ID, "restored", ns.Fence(), entry)
	if err != nil {
		t.Fatalf("restore under a new name: %v", err)
	}

	got, err := s.GetWorkspace(t.Context(), ref)
	if err != nil || got.ID != gone.ID || ref.Name != "restored" {
		t.Fatalf("restored workspace = %+v, %v, want ID %s under restored", got, err, gone.ID)
	}

	deleted, err := s.ListDeletedUnits(t.Context(), ns.Name)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted units after the restore = %+v, %v, want none", deleted, err)
	}

	checkRestoreEntry(t, s, ns.Name, "opentofu:acme/restored", gone.ID.String())
}

// testPurgeWorkspace deletes a workspace and purges it: the deleted units
// are empty, a restore answers ErrNotFound, and a purge of a live or unknown
// workspace answers ErrNotFound and changes nothing.
func testPurgeWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	gone := deleteTestWorkspace(t, s, ns)
	live := createTestWorkspace(t, s, ns, "network")
	entry := lifecycleEntry(ns.Name, "unit:purge")

	for _, id := range []workspace.ID{"ws-unknown", live.ID} {
		purgeErr := s.PurgeWorkspace(
			t.Context(),
			id,
			ns.Fence(),
			time.Time{},
			entry,
		)
		if !errors.Is(purgeErr, workspace.ErrNotFound) {
			t.Fatalf("purge %s: err = %v, want ErrNotFound", id, purgeErr)
		}
	}

	if err := s.PurgeWorkspace(t.Context(), gone.ID, ns.Fence(), time.Time{}, entry); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	checkBlobPurge(t, s, "opentofu", gone.ID.String(), ns.Name)

	deleted, err := s.ListDeletedUnits(t.Context(), ns.Name)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted units after the purge = %+v, %v, want none", deleted, err)
	}

	if _, err := s.RestoreWorkspace(t.Context(), gone.ID, "", ns.Fence(), entry); !errors.Is(
		err, workspace.ErrNotFound,
	) {
		t.Fatalf("restore after the purge: err = %v, want ErrNotFound", err)
	}

	if got, err := s.GetWorkspace(t.Context(), live.Ref); err != nil || got.ID != live.ID {
		t.Fatalf("live workspace after the purges = %+v, %v, want %s", got, err, live.ID)
	}
}

// testQueryDeletedUnits deletes stacks whose names differ in case, in
// punctuation and in the end of their retention, then runs filtered and paged
// queries. Each answer must equal inventory.PageDeleted over the full list,
// and walking every page by its cursor must give the full list once.
func testQueryDeletedUnits(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	names := [][2]string{{"web", "prod"}, {"web", "Prod"}, {"web", "a_b"}, {"web", "a-b"}, {"apps", "x"}, {"Web", "z"}}

	for i, n := range names {
		st := newStack(ns.Name, n[0], n[1])
		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		d := inventory.Deletion{
			Entry:      deleteEntry(ns.Name),
			PurgeAfter: deletedAt().Add(time.Duration(i+1) * 24 * time.Hour),
		}
		if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), d); err != nil {
			t.Fatalf("delete stack %s: %v", st.Ref, err)
		}
	}

	all, err := s.ListDeletedUnits(t.Context(), ns.Name)
	if err != nil || len(all) != len(names) {
		t.Fatalf("deleted units = %d, %v, want %d", len(all), err, len(names))
	}

	queries := make([]inventory.DeletedQuery, 0, 9)
	queries = append(queries,
		deletedQuery(ns.Name, "", "", time.Time{}, 0),
		deletedQuery(ns.Name, "pulumi", "", time.Time{}, 2),
		deletedQuery(ns.Name, "opentofu", "", time.Time{}, 2),
		deletedQuery(ns.Name, "", "web/", time.Time{}, 2),
		deletedQuery(ns.Name, "", "Web/", time.Time{}, 10),
		deletedQuery(ns.Name, "", "", deletedAt().Add(3*24*time.Hour), 1),
	)

	// Restricted queries: the page, the cursor and the total cover only the
	// listed unit IDs, an unknown ID matches nothing, and an empty list
	// matches nothing.
	for _, ids := range [][]string{
		{all[1].UnitID, all[3].UnitID, all[4].UnitID},
		{all[0].UnitID, "stk-unknown"},
		{},
	} {
		q := deletedQuery(ns.Name, "", "", time.Time{}, 1)
		q.Restricted = true
		q.UnitIDs = ids
		queries = append(queries, q)
	}

	for i := range queries {
		checkDeletedPages(t, s, all, queries[i])
	}
}

// checkDeletedPages walks every page of q by its cursor and checks each page
// against inventory.PageDeleted over all.
func checkDeletedPages(t *testing.T, s Store, all []inventory.DeletedEntry, q inventory.DeletedQuery) {
	t.Helper()

	for pages := 0; ; pages++ {
		if pages > len(all)+1 {
			t.Fatalf("query %+v: more pages than entries", q)
		}

		got, err := s.QueryDeletedUnits(t.Context(), q)
		if err != nil {
			t.Fatalf("query %+v: %v", q, err)
		}

		want := inventory.PageDeleted(all, q)
		if !sameDeletedPage(got, want) {
			t.Fatalf("query %+v = %+v, want %+v", q, got, want)
		}

		if got.Next == nil {
			return
		}

		q.After = got.Next
	}
}

// sameDeletedPage reports whether two pages hold the same entries in order,
// the same total and the same next cursor.
func sameDeletedPage(a, b inventory.DeletedPage) bool {
	if a.Total != b.Total || len(a.Entries) != len(b.Entries) || (a.Next == nil) != (b.Next == nil) {
		return false
	}

	if a.Next != nil && *a.Next != *b.Next {
		return false
	}

	for i := range a.Entries {
		x, y := a.Entries[i], b.Entries[i]
		if x.UnitID != y.UnitID || x.Name != y.Name || x.Tool != y.Tool ||
			!x.DeletedAt.Equal(y.DeletedAt) || !x.PurgeAfter.Equal(y.PurgeAfter) {
			return false
		}
	}

	return true
}

// deletedQuery returns a query of the deleted units of ns from the first
// page.
func deletedQuery(ns namespace.Name, tool, prefix string, purgeBefore time.Time, limit int) inventory.DeletedQuery {
	return inventory.DeletedQuery{
		PurgeBefore: purgeBefore, After: nil, Namespace: ns, Tool: tool, NamePrefix: prefix, UnitIDs: nil,
		Limit: limit, Restricted: false,
	}
}

// unitGrant returns a grant of the viewer role to a principal on obj, with
// the ID id.
func unitGrant(id string, obj identity.Object) access.Grant {
	return access.Grant{
		CreatedAt: now(),
		Condition: nil,
		Object:    obj,
		ID:        id,
		Role:      access.RoleViewer,
		CreatedBy: "admin",
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: "prn-unit-reader"},
	}
}

// createUnitGrants creates one grant on the namespace of the unit obj and
// one on the unit, each with the fence and audit entry of a grant write in
// s. It returns the error of the grant on the unit.
func createUnitGrants(t *testing.T, s namespace.Store, as access.Store, obj identity.Object) error {
	t.Helper()

	ns := unitGrant("grt-namespace", identity.NamespaceObject(obj.Namespace()))
	fence, e := GrantWrite(t, s, ns.Object, now(), "grant:"+ns.ID)

	if err := as.CreateGrant(t.Context(), ns, fence, e, nil); err != nil {
		t.Fatalf("create grant %q: %v", ns.ID, err)
	}

	unit := unitGrant("grt-unit", obj)
	fence, e = GrantWrite(t, s, unit.Object, now(), "grant:"+unit.ID)

	if err := as.CreateGrant(t.Context(), unit, fence, e, nil); err != nil {
		return fmt.Errorf("create grant on the unit: %w", err)
	}

	return nil
}

// checkUnitGrant checks that the read of the grant on the unit answers
// want: nil while it is current, access.ErrNotFound after it ends. The grant
// on the namespace must stay current.
func checkUnitGrant(t *testing.T, as access.Store, want error) {
	t.Helper()

	if _, err := as.GetGrant(t.Context(), "grt-unit"); !errors.Is(err, want) {
		t.Fatalf("grant on the unit: err = %v, want %v", err, want)
	}

	if _, err := as.GetGrant(t.Context(), "grt-namespace"); err != nil {
		t.Fatalf("grant on the namespace: %v, want current", err)
	}
}

// testPurgeStackRevokesItsGrants grants a role on the stack, deletes and
// restores it, then deletes and purges it. The restore keeps the grant,
// because it undoes the delete. The purge ends the grant in its transaction,
// so no current grant names the stable ID of a unit that is gone. A grant on
// the namespace stays. A store with no access port has no grants to end.
func testPurgeStackRevokesItsGrants(t *testing.T, f fixture) {
	t.Helper()

	as, ok := storeAs[access.Store](f.s)
	if !ok {
		return
	}

	st := mustGetFixtureStack(t, f)

	obj, err := identity.UnitObject("pulumi", f.ref.Namespace, f.ref.Project.String()+"/"+f.ref.Stack.String())
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	obj = obj.WithUnitID(st.ID.String())

	if err := createUnitGrants(t, f.s, as, obj); err != nil {
		t.Fatal(err)
	}

	before := deleteFixtureStack(t, f)
	restore := lifecycleEntry(f.ref.Namespace, "unit:restore")

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, restore); err != nil {
		t.Fatalf("restore stack: %v", err)
	}

	checkUnitGrant(t, as, nil)

	deleteFixtureStack(t, f)

	purge := lifecycleEntry(f.ref.Namespace, "unit:purge")
	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, time.Time{}, purge); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	checkUnitGrant(t, as, access.ErrNotFound)
}

// deleteWorkspaceOf soft-deletes the workspace w of ns.
func deleteWorkspaceOf(t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace) {
	t.Helper()

	err := s.DeleteWorkspace(t.Context(), w.Ref, ns.Fence(), inventory.Deletion{
		Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
}

// testPurgeWorkspaceRevokesItsGrants is testPurgeStackRevokesItsGrants for
// a workspace.
func testPurgeWorkspaceRevokesItsGrants(t *testing.T, s WorkspaceStore) {
	t.Helper()

	as, ok := storeAs[access.Store](s)
	if !ok {
		return
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	obj, err := identity.UnitObject("opentofu", ns.Name, w.Ref.Name.String())
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	if err := createUnitGrants(t, s, as, obj.WithUnitID(w.ID.String())); err != nil {
		t.Fatal(err)
	}

	deleteWorkspaceOf(t, s, ns, w)

	restore := lifecycleEntry(ns.Name, "unit:restore")
	if _, err := s.RestoreWorkspace(t.Context(), w.ID, "", ns.Fence(), restore); err != nil {
		t.Fatalf("restore workspace: %v", err)
	}

	checkUnitGrant(t, as, nil)

	deleteWorkspaceOf(t, s, ns, w)

	purge := lifecycleEntry(ns.Name, "unit:purge")
	if err := s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), time.Time{}, purge); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	checkUnitGrant(t, as, access.ErrNotFound)
}

// namespaceReader is the part of the namespace store that the checks read.
type namespaceReader interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// blobPurgesOf returns the blob purge records of the unit unitID of tool.
func blobPurgesOf(t *testing.T, purges inventory.BlobPurges, tool, unitID string) []inventory.BlobPurge {
	t.Helper()

	listed, err := purges.ListBlobPurges(t.Context(), nil, 100)
	if err != nil {
		t.Fatalf("list blob purges: %v", err)
	}

	var out []inventory.BlobPurge

	for _, p := range listed {
		if p.Tool == tool && p.UnitID == unitID {
			out = append(out, p)
		}
	}

	return out
}

// checkBlobPurge checks that the store holds exactly one blob purge record
// for the unit unitID of tool in ns, at the time of the purge, finishes it,
// and checks that a second finish is not an error.
func checkBlobPurge(t *testing.T, s any, tool, unitID string, ns namespace.Name) {
	t.Helper()

	purges, ok := storeAs[inventory.BlobPurges](s)
	if !ok {
		return
	}

	found := blobPurgesOf(t, purges, tool, unitID)
	if len(found) != 1 || !found[0].PurgedAt.Equal(deletedAt()) {
		t.Fatalf("blob purges of %s %s = %+v, want one in %s at %v", tool, unitID, found, ns, deletedAt())
	}

	// The record carries the ID of the namespace row, so the sweep deletes the
	// blobs under the prefix of the namespace without a read by name.
	namespaces, ok := storeAs[namespaceReader](s)
	if !ok {
		t.Fatalf("store %T cannot read a namespace", s)
	}

	row, err := namespaces.GetNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}

	if found[0].NamespaceID != row.ID || row.ID == "" {
		t.Fatalf("blob purge namespace ID = %q, want %q", found[0].NamespaceID, row.ID)
	}

	for range 2 {
		if err := purges.FinishBlobPurge(t.Context(), tool, unitID); err != nil {
			t.Fatalf("finish blob purge: %v", err)
		}
	}

	if left := blobPurgesOf(t, purges, tool, unitID); len(left) != 0 {
		t.Fatalf("blob purges of %s %s after the finish = %+v, want none", tool, unitID, left)
	}
}

// testRestoreRacesPurgeWorkspace is testRestoreRacesPurge for a workspace:
// exactly one wins, and the other answers ErrNotFound and changes nothing.
func testRestoreRacesPurgeWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	gone := deleteTestWorkspace(t, s, ns)
	entry := lifecycleEntry(ns.Name, "unit:restore")

	var (
		wg                   sync.WaitGroup
		restoreErr, purgeErr error
	)

	wg.Go(func() {
		_, restoreErr = s.RestoreWorkspace(t.Context(), gone.ID, "", ns.Fence(), entry)
	})

	wg.Go(func() {
		purgeErr = s.PurgeWorkspace(t.Context(), gone.ID, ns.Fence(), time.Time{}, entry)
	})

	wg.Wait()

	got, getErr := s.GetWorkspace(t.Context(), gone.Ref)

	switch {
	case restoreErr == nil && errors.Is(purgeErr, workspace.ErrNotFound):
		if getErr != nil || got.ID != gone.ID {
			t.Fatalf("workspace after a winning restore = %+v, %v, want %s", got, getErr, gone.ID)
		}
	case purgeErr == nil && errors.Is(restoreErr, workspace.ErrNotFound):
		if !errors.Is(getErr, workspace.ErrNotFound) {
			t.Fatalf("workspace after a winning purge: err = %v, want ErrNotFound", getErr)
		}
	default:
		t.Fatalf("restore err = %v, purge err = %v, want exactly one to win and the other ErrNotFound",
			restoreErr, purgeErr)
	}
}

// testRestoreOrPurgeStackInAnotherNamespace deletes a stack of acme, then
// restores and purges its ID with the fence of the namespace beta: each
// answers ErrNotFound and changes nothing, and the stack stays restorable
// in acme.
func testRestoreOrPurgeStackInAnotherNamespace(t *testing.T, f fixture) {
	t.Helper()

	before := deleteFixtureStack(t, f)
	beta := newNamespace(t, f.s, "beta")
	entry := lifecycleEntry(beta.Name, "unit:purge")

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", beta.Fence(), entry); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("restore with the fence of beta: err = %v, want ErrNotFound", err)
	}

	purgeErr := f.s.PurgeStack(
		t.Context(),
		before.ID,
		beta.Fence(),
		time.Time{},
		entry,
	)
	if !errors.Is(purgeErr, stack.ErrNotFound) {
		t.Fatalf("purge with the fence of beta: err = %v, want ErrNotFound", purgeErr)
	}

	checkDeletedStackEntries(t, f.s, f.ref.Namespace, before.ID.String())

	restore := lifecycleEntry(f.ref.Namespace, "unit:restore")
	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, restore); err != nil {
		t.Fatalf("restore in acme: %v", err)
	}
}

// testRestoreOrPurgeWorkspaceInAnotherNamespace is
// testRestoreOrPurgeStackInAnotherNamespace for a workspace.
func testRestoreOrPurgeWorkspaceInAnotherNamespace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	gone := deleteTestWorkspace(t, s, ns)
	beta := newWorkspaceNamespace(t, s, "beta")
	entry := lifecycleEntry(beta.Name, "unit:purge")

	if _, err := s.RestoreWorkspace(t.Context(), gone.ID, "", beta.Fence(), entry); !errors.Is(
		err, workspace.ErrNotFound,
	) {
		t.Fatalf("restore with the fence of beta: err = %v, want ErrNotFound", err)
	}

	purgeErr := s.PurgeWorkspace(
		t.Context(),
		gone.ID,
		beta.Fence(),
		time.Time{},
		entry,
	)
	if !errors.Is(purgeErr, workspace.ErrNotFound) {
		t.Fatalf("purge with the fence of beta: err = %v, want ErrNotFound", purgeErr)
	}

	restore := lifecycleEntry(ns.Name, "unit:restore")
	if _, err := s.RestoreWorkspace(t.Context(), gone.ID, "", ns.Fence(), restore); err != nil {
		t.Fatalf("restore in acme: %v", err)
	}
}

// testListExpiredDeleted deletes stacks in two namespaces whose retention
// ends before, at and after a cut-off time, some at the same time. Each page
// of ListExpiredDeleted must equal inventory.PageExpired over every deleted
// unit, and walking the pages by their cursor must give each expired unit
// once: across namespaces, in the order of the end of retention, then tool,
// then unit ID.
func testListExpiredDeleted(t *testing.T, s Store) {
	t.Helper()

	expired, ok := storeAs[inventory.ExpiredDeleted](s)
	if !ok {
		t.Fatal("store does not implement inventory.ExpiredDeleted, which the retention sweep needs")
	}

	cut := deletedAt().Add(10 * 24 * time.Hour)
	ends := []time.Duration{-48 * time.Hour, -time.Hour, -time.Hour, 0, time.Millisecond, 24 * time.Hour}
	all := make([]inventory.DeletedEntry, 0, 2*len(ends))

	for _, name := range []string{"acme", "beta"} {
		ns := newNamespace(t, s, name)

		for i, end := range ends {
			st := newStack(ns.Name, "web", fmt.Sprintf("s%d", i))
			if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
				t.Fatalf("create stack %s: %v", st.Ref, err)
			}

			d := inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: cut.Add(end)}
			if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), d); err != nil {
				t.Fatalf("delete stack %s: %v", st.Ref, err)
			}
		}

		listed, err := s.ListDeletedUnits(t.Context(), ns.Name)
		if err != nil {
			t.Fatalf("deleted units of %s: %v", ns.Name, err)
		}

		all = append(all, listed...)
	}

	for _, limit := range []int{2, 100} {
		checkExpiredPages(t, expired, all, cut, limit)
	}

	if got, err := expired.ListExpiredDeleted(t.Context(), deletedAt(), nil, 10); err != nil || len(got) != 0 {
		t.Fatalf("expired before any retention ends = %d, %v, want none", len(got), err)
	}
}

// checkExpiredPages walks the expired units at now in pages of limit and
// checks each page against inventory.PageExpired over all.
func checkExpiredPages(
	t *testing.T,
	s inventory.ExpiredDeleted,
	all []inventory.DeletedEntry,
	now time.Time,
	limit int,
) {
	t.Helper()

	var (
		cursor *inventory.ExpiredCursor
		seen   int
	)

	want := len(inventory.PageExpired(all, now, nil, len(all)))

	for pages := 0; ; pages++ {
		if pages > len(all)+1 {
			t.Fatalf("limit %d: more pages than entries", limit)
		}

		got, err := s.ListExpiredDeleted(t.Context(), now, cursor, limit)
		if err != nil {
			t.Fatalf("expired page %d: %v", pages, err)
		}

		wantPage := inventory.PageExpired(all, now, cursor, limit)
		if !sameDeletedPage(
			inventory.DeletedPage{Entries: got, Total: 0, Next: nil},
			inventory.DeletedPage{Entries: wantPage, Total: 0, Next: nil},
		) {
			t.Fatalf("limit %d, page %d = %+v, want %+v", limit, pages, got, wantPage)
		}

		seen += len(got)

		if len(got) < limit {
			break
		}

		last := got[len(got)-1]
		cursor = &inventory.ExpiredCursor{PurgeAfter: last.PurgeAfter, Tool: last.Tool, UnitID: last.UnitID}
	}

	if seen != want || want != 8 {
		t.Fatalf("limit %d: walked %d expired units, want %d (8)", limit, seen, want)
	}
}

// renewedRetention is the end of the retention period of a unit that a
// restore and a new delete renewed: 30 days after purgeAt.
func renewedRetention() time.Time {
	return purgeAt().Add(30 * 24 * time.Hour)
}

// testSweepPurgeChecksTheRetention lists the deleted stack as the sweep
// does, then restores it and deletes it again, so its retention period ends
// later. A purge due by the time of the list answers ErrNotFound and keeps
// the stack. A purge due by the new end removes it.
func testSweepPurgeChecksTheRetention(t *testing.T, f fixture) {
	t.Helper()

	before := deleteFixtureStack(t, f)

	_, restoreErr := f.s.RestoreStack(
		t.Context(),
		before.ID,
		"",
		f.fence,
		lifecycleEntry(f.ref.Namespace, "unit:restore"),
	)
	if restoreErr != nil {
		t.Fatalf("restore stack: %v", restoreErr)
	}

	err := f.s.DeleteStack(t.Context(), f.ref, f.fence, stack.EmptyGuard(), inventory.Deletion{
		Entry: deleteEntry(f.ref.Namespace), PurgeAfter: renewedRetention(),
	})
	if err != nil {
		t.Fatalf("delete stack again: %v", err)
	}

	purge := lifecycleEntry(f.ref.Namespace, "unit:purge")

	err = f.s.PurgeStack(t.Context(), before.ID, f.fence, purgeAt(), purge)
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("purge due by the old end: err = %v, want ErrNotFound", err)
	}

	deleted, err := f.s.ListDeletedUnits(t.Context(), f.ref.Namespace)
	if err != nil || len(deleted) != 1 || deleted[0].UnitID != before.ID.String() {
		t.Fatalf("deleted units after the refused purge = %+v, %v, want the stack", deleted, err)
	}

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, renewedRetention(), purge); err != nil {
		t.Fatalf("purge due by the new end: %v", err)
	}
}

// testSweepPurgeChecksTheRetentionWorkspace is
// testSweepPurgeChecksTheRetention for a workspace.
func testSweepPurgeChecksTheRetentionWorkspace(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	deleteWorkspaceOf(t, s, ns, w)

	_, restoreErr := s.RestoreWorkspace(
		t.Context(),
		w.ID,
		"",
		ns.Fence(),
		lifecycleEntry(ns.Name, "unit:restore"),
	)
	if restoreErr != nil {
		t.Fatalf("restore workspace: %v", restoreErr)
	}

	err := s.DeleteWorkspace(t.Context(), w.Ref, ns.Fence(), inventory.Deletion{
		Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: renewedRetention(),
	})
	if err != nil {
		t.Fatalf("delete workspace again: %v", err)
	}

	purge := lifecycleEntry(ns.Name, "unit:purge")

	purgeErr := s.PurgeWorkspace(
		t.Context(),
		w.ID,
		ns.Fence(),
		purgeAt(),
		purge,
	)
	if !errors.Is(purgeErr, workspace.ErrNotFound) {
		t.Fatalf("purge due by the old end: err = %v, want ErrNotFound", purgeErr)
	}

	deleted, err := s.ListDeletedUnits(t.Context(), ns.Name)
	if err != nil || len(deleted) != 1 || deleted[0].UnitID != w.ID.String() {
		t.Fatalf("deleted units after the refused purge = %+v, %v, want the workspace", deleted, err)
	}

	if err := s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), renewedRetention(), purge); err != nil {
		t.Fatalf("purge due by the new end: %v", err)
	}
}

// testBlobPurgePages purges five stacks at the same time and reads their
// blob purge records in pages of two by the cursor. Each page must equal
// inventory.PageBlobPurges over every record, and the walk must give each
// record once.
func testBlobPurgePages(t *testing.T, s Store) {
	t.Helper()

	purges, ok := storeAs[inventory.BlobPurges](s)
	if !ok {
		t.Fatal("store does not implement inventory.BlobPurges, which the retention sweep needs")
	}

	ns := newNamespace(t, s, "acme")

	for i := range 5 {
		st := newStack(ns.Name, "web", fmt.Sprintf("s%d", i))
		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		d := inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()}
		if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), d); err != nil {
			t.Fatalf("delete stack %s: %v", st.Ref, err)
		}

		purgeErr := s.PurgeStack(
			t.Context(),
			st.ID,
			ns.Fence(),
			time.Time{},
			lifecycleEntry(ns.Name, "unit:purge"),
		)
		if purgeErr != nil {
			t.Fatalf("purge stack %s: %v", st.Ref, purgeErr)
		}
	}

	all, err := purges.ListBlobPurges(t.Context(), nil, 100)
	if err != nil || len(all) != 5 {
		t.Fatalf("blob purge records = %d, %v, want 5", len(all), err)
	}

	checkBlobPurgePages(t, purges, all)
}

// testBlobPurgeBacklog counts the blob purge records of an empty store, then
// after one purge and after three purges. Each count must equal the number of
// records that ListBlobPurges returns, and the oldest purge time must equal
// the earliest record.
func testBlobPurgeBacklog(t *testing.T, s Store) {
	t.Helper()

	counter, ok := storeAs[inventory.BlobPurgeCounter](s)
	if !ok {
		t.Fatal("store does not implement inventory.BlobPurgeCounter, which the blob purge gauges need")
	}

	purges, ok := storeAs[inventory.BlobPurges](s)
	if !ok {
		t.Fatal("store does not implement inventory.BlobPurges, which the retention sweep needs")
	}

	ns := newNamespace(t, s, "acme")

	checkBlobPurgeBacklog(t, counter, purges, 0)

	for i := range 3 {
		st := newStack(ns.Name, "web", fmt.Sprintf("b%d", i))
		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}

		d := inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()}
		if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), d); err != nil {
			t.Fatalf("delete stack %s: %v", st.Ref, err)
		}

		purgeErr := s.PurgeStack(
			t.Context(),
			st.ID,
			ns.Fence(),
			time.Time{},
			lifecycleEntry(ns.Name, "unit:purge"),
		)
		if purgeErr != nil {
			t.Fatalf("purge stack %s: %v", st.Ref, purgeErr)
		}

		checkBlobPurgeBacklog(t, counter, purges, int64(i+1))
	}
}

// checkBlobPurgeBacklog checks that the backlog holds want records, that the
// records ListBlobPurges returns are that many, and that the oldest purge time
// equals the earliest of them. It checks a zero time when no record exists.
func checkBlobPurgeBacklog(t *testing.T, counter inventory.BlobPurgeCounter, purges inventory.BlobPurges, want int64) {
	t.Helper()

	backlog, err := counter.CountBlobPurges(t.Context())
	if err != nil {
		t.Fatalf("count blob purges: %v", err)
	}

	if backlog.Pending != want {
		t.Fatalf("blob purge backlog pending = %d, want %d", backlog.Pending, want)
	}

	listed, err := purges.ListBlobPurges(t.Context(), nil, 100)
	if err != nil {
		t.Fatalf("list blob purges: %v", err)
	}

	if int64(len(listed)) != want {
		t.Fatalf("listed blob purges = %d, want %d", len(listed), want)
	}

	if want == 0 {
		if !backlog.OldestPurgedAt.IsZero() {
			t.Fatalf("oldest purge of an empty backlog = %v, want zero", backlog.OldestPurgedAt)
		}

		return
	}

	oldest := listed[0].PurgedAt
	for _, p := range listed {
		if p.PurgedAt.Before(oldest) {
			oldest = p.PurgedAt
		}
	}

	if !backlog.OldestPurgedAt.Equal(oldest) {
		t.Fatalf("oldest purge = %v, want %v", backlog.OldestPurgedAt, oldest)
	}
}

// namespacePurger is the test hook that deletes a namespace row and keeps the
// rows that name it, as a purge of a namespace does.
type namespacePurger interface {
	PurgeNamespace(ctx context.Context, ns namespace.Name) error
}

// testBlobPurgeNamespaceID proves that a blob purge record holds the ID of the
// namespace that held the unit, not its name. In each case a stack is purged
// in a namespace, and the case then changes the namespace row. The sweep reads
// the record by the ID only, so a new namespace of the same name must not get
// the record.
func testBlobPurgeNamespaceID(t *testing.T, s Store) {
	t.Helper()

	purges, ok := storeAs[inventory.BlobPurges](s)
	if !ok {
		t.Fatal("store does not implement inventory.BlobPurges, which the retention sweep needs")
	}

	purger, ok := storeAs[namespacePurger](s)
	if !ok {
		t.Fatalf("store %T has no PurgeNamespace test hook, which the namespace made again case needs", s)
	}

	cases := []struct {
		name string
		// recreate says whether the case deletes the namespace and creates
		// another one of the same name.
		recreate bool
	}{
		{name: "the record holds the ID of the live namespace", recreate: false},
		{name: "a namespace made again under the same name does not get the record", recreate: true},
	}

	for i, tc := range cases {
		name := fmt.Sprintf("purgeid%d", i)
		ns := purgeOneStack(t, s, tc.name, name)

		if tc.recreate {
			if err := purger.PurgeNamespace(t.Context(), ns.Name); err != nil {
				t.Fatalf("%s: purge namespace: %v", tc.name, err)
			}

			if again := newNamespace(t, s, name); again.ID == ns.ID {
				t.Fatalf("%s: the new namespace has the old ID %s", tc.name, again.ID)
			}
		}

		found := blobPurgesOf(t, purges, "pulumi", ns.unitID)
		if len(found) != 1 || found[0].NamespaceID != ns.ID {
			t.Fatalf("%s: blob purges = %+v, want one with namespace ID %s", tc.name, found, ns.ID)
		}
	}
}

// purgedIn is a namespace and the ID of the unit that a case purged in it.
type purgedIn struct {
	namespace.Namespace

	unitID string
}

// purgeOneStack makes the namespace name, then creates, deletes and purges one
// stack in it.
func purgeOneStack(t *testing.T, s Store, label, name string) purgedIn {
	t.Helper()

	ns := newNamespace(t, s, name)

	st := newStack(ns.Name, "web", "s")
	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("%s: create stack: %v", label, err)
	}

	d := inventory.Deletion{Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt()}
	if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), d); err != nil {
		t.Fatalf("%s: delete stack: %v", label, err)
	}

	purgeErr := s.PurgeStack(t.Context(), st.ID, ns.Fence(), time.Time{}, lifecycleEntry(ns.Name, "unit:purge"))
	if purgeErr != nil {
		t.Fatalf("%s: purge stack: %v", label, purgeErr)
	}

	return purgedIn{Namespace: ns, unitID: st.ID.String()}
}

// checkBlobPurgePages walks the blob purge records in pages of two by the
// cursor and checks each page against inventory.PageBlobPurges over all.
func checkBlobPurgePages(t *testing.T, purges inventory.BlobPurges, all []inventory.BlobPurge) {
	t.Helper()

	var (
		cursor *inventory.BlobPurgeCursor
		seen   int
	)

	for pages := 0; pages <= len(all); pages++ {
		got, err := purges.ListBlobPurges(t.Context(), cursor, 2)
		if err != nil {
			t.Fatalf("blob purge page %d: %v", pages, err)
		}

		want := inventory.PageBlobPurges(all, cursor, 2)
		if !sameBlobPurges(got, want) {
			t.Fatalf("blob purge page %d = %+v, want %+v", pages, got, want)
		}

		seen += len(got)

		if len(got) < 2 {
			break
		}

		last := got[len(got)-1]
		cursor = &inventory.BlobPurgeCursor{PurgedAt: last.PurgedAt, Tool: last.Tool, UnitID: last.UnitID}
	}

	if seen != len(all) {
		t.Fatalf("walked %d blob purge records, want %d", seen, len(all))
	}
}

// sameBlobPurges reports whether two lists hold the same records in order.
func sameBlobPurges(a, b []inventory.BlobPurge) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Tool != b[i].Tool || a[i].UnitID != b[i].UnitID || a[i].NamespaceID != b[i].NamespaceID ||
			!a[i].PurgedAt.Equal(b[i].PurgedAt) {
			return false
		}
	}

	return true
}

// setUnitContext sets the owners on the unit and on a resource of it, so the
// store holds entries and owner rows for both.
func setUnitContext(t *testing.T, cs objectcontext.Store, unit graph.Unit, fence namespace.Fence) []objectcontext.Ref {
	t.Helper()

	refs := []objectcontext.Ref{
		objectcontext.UnitRef(unit), objectcontext.ResourceRef(unit, "aws_s3_bucket.logs"),
	}

	for _, ref := range refs {
		e := lifecycleEntry(unit.Namespace, "unit:set-context")
		patch := objectcontext.Patch{
			Summary: nil, Runbook: nil, OnCall: nil, Owners: &[]string{"group:net"}, Links: nil,
		}

		if _, err := cs.SetAPI(t.Context(), ref, patch, "user:ada", fence, e); err != nil {
			t.Fatalf("set context of %s: %v", ref.Key(), err)
		}
	}

	return refs
}

// checkContextGone checks that cs holds no entry for refs and no entry that
// names the owner group:net.
func checkContextGone(t *testing.T, cs objectcontext.Store, ns namespace.Name, refs []objectcontext.Ref) {
	t.Helper()

	got, err := cs.Entries(t.Context(), refs)
	if err != nil || len(got) != 0 {
		t.Fatalf("entries after the purge = %+v, %v, want none", got, err)
	}

	byOwner, _, err := cs.ByOwner(t.Context(), ns, "group:net", "", 10)
	if err != nil || len(byOwner) != 0 {
		t.Fatalf("entries of the owner after the purge = %+v, %v, want none", byOwner, err)
	}
}

// testPurgeStackDropsItsContext sets the context of a stack and of a resource
// of it, deletes the stack and checks that a soft delete keeps the context.
// The purge then removes the entries and their owner rows. A store with no
// context port has none to remove.
func testPurgeStackDropsItsContext(t *testing.T, f fixture) {
	t.Helper()

	cs, ok := storeAs[objectcontext.Store](f.s)
	if !ok {
		return
	}

	st := mustGetFixtureStack(t, f)
	unit := graph.Unit{Namespace: f.ref.Namespace, Tool: "pulumi", ID: st.ID.String()}
	refs := setUnitContext(t, cs, unit, f.fence)

	deleteFixtureStack(t, f)

	if got, err := cs.Entries(t.Context(), refs); err != nil || len(got) != len(refs) {
		t.Fatalf("entries after the soft delete = %+v, %v, want %d", got, err, len(refs))
	}

	purge := lifecycleEntry(f.ref.Namespace, "unit:purge")
	if err := f.s.PurgeStack(t.Context(), st.ID, f.fence, time.Time{}, purge); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	checkContextGone(t, cs, f.ref.Namespace, refs)
}

// testPurgeWorkspaceDropsItsContext is testPurgeStackDropsItsContext for a
// workspace.
func testPurgeWorkspaceDropsItsContext(t *testing.T, s WorkspaceStore) {
	t.Helper()

	cs, ok := storeAs[objectcontext.Store](s)
	if !ok {
		return
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	unit := graph.Unit{Namespace: ns.Name, Tool: "opentofu", ID: w.ID.String()}
	refs := setUnitContext(t, cs, unit, ns.Fence())

	deleteWorkspaceOf(t, s, ns, w)

	if got, err := cs.Entries(t.Context(), refs); err != nil || len(got) != len(refs) {
		t.Fatalf("entries after the soft delete = %+v, %v, want %d", got, err, len(refs))
	}

	purge := lifecycleEntry(ns.Name, "unit:purge")
	if err := s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), time.Time{}, purge); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	checkContextGone(t, cs, ns.Name, refs)
}
