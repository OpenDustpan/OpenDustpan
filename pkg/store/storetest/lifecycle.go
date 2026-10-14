package storetest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
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

	st := mustGetFixtureStack(t, f)

	err := f.s.DeleteStack(t.Context(), f.ref, f.fence, stack.EmptyGuard(), inventory.Deletion{
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

		if err := f.s.PurgeStack(t.Context(), id, f.fence, entry); !errors.Is(err, stack.ErrNotFound) {
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
	if err := f.s.PutCheckpoint(t.Context(), newCheckpoint(u, 0, `{"v":1}`), f.fence); err != nil {
		t.Fatalf("put checkpoint: %v", err)
	}

	if err := f.s.AppendEvents(t.Context(), newEvents(u, 1, 0, 1), f.fence); err != nil {
		t.Fatalf("append events: %v", err)
	}

	f.end(t, u, stack.StatusSucceeded)

	before := deleteFixtureStack(t, f)
	entry := lifecycleEntry(f.ref.Namespace, "unit:purge")

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, entry); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	deleted, err := f.s.ListDeletedUnits(t.Context(), f.ref.Namespace)
	if err != nil || len(deleted) != 0 {
		t.Fatalf("deleted units after the purge = %+v, %v, want none", deleted, err)
	}

	if _, err := f.s.RestoreStack(t.Context(), before.ID, "", f.fence, entry); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("restore after the purge: err = %v, want ErrNotFound", err)
	}

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, entry); !errors.Is(err, stack.ErrNotFound) {
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
		purgeErr = f.s.PurgeStack(t.Context(), before.ID, f.fence, entry)
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

	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, entry); err == nil {
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
		if err := s.PurgeWorkspace(t.Context(), id, ns.Fence(), entry); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("purge %s: err = %v, want ErrNotFound", id, err)
		}
	}

	if err := s.PurgeWorkspace(t.Context(), gone.ID, ns.Fence(), entry); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

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

	if err := as.CreateGrant(t.Context(), ns, fence, e); err != nil {
		t.Fatalf("create grant %q: %v", ns.ID, err)
	}

	unit := unitGrant("grt-unit", obj)
	fence, e = GrantWrite(t, s, unit.Object, now(), "grant:"+unit.ID)

	if err := as.CreateGrant(t.Context(), unit, fence, e); err != nil {
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
	if err := f.s.PurgeStack(t.Context(), before.ID, f.fence, purge); err != nil {
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
	if err := s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), purge); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	checkUnitGrant(t, as, access.ErrNotFound)
}
