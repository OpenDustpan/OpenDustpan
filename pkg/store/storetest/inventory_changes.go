package storetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// historyOf returns the change log reader of s, and skips the test for a store
// that keeps no inventory change log.
func historyOf(t *testing.T, s any) inventory.HistoryReader { //nolint:ireturn // The optional port of the store.
	t.Helper()

	r, ok := storeAs[inventory.HistoryReader](s)
	if !ok {
		t.Skip("store keeps no inventory change log")
	}

	return r
}

// changesOf returns every row of the inventory change log of ns, in the order
// of the sequence.
func changesOf(t *testing.T, r inventory.HistoryReader, ns namespace.Name) []inventory.Change {
	t.Helper()

	rows, err := r.ListChanges(t.Context(), ns, now().Add(100*365*24*time.Hour), nil, 10000)
	if err != nil {
		t.Fatalf("list inventory changes of %s: %v", ns, err)
	}

	return rows
}

// kindsOf returns the kinds of rows, for a comparison.
func kindsOf(rows []inventory.Change) []inventory.ChangeKind {
	out := make([]inventory.ChangeKind, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].Kind)
	}

	return out
}

// assertKinds checks that the change log of ns holds exactly the kinds, in
// order, and that its sequences increase.
func assertKinds(
	t *testing.T, r inventory.HistoryReader, ns namespace.Name, want ...inventory.ChangeKind,
) []inventory.Change {
	t.Helper()

	rows := changesOf(t, r, ns)
	if !slices.Equal(kindsOf(rows), want) {
		t.Fatalf("change log kinds = %v, want %v", kindsOf(rows), want)
	}

	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("change log sequences %d then %d do not increase", rows[i-1].Seq, rows[i].Seq)
		}
	}

	return rows
}

// wantRow is what a change log row must hold: the tool, the unit, the name
// and the time.
type wantRow struct {
	at     time.Time
	tool   string
	unitID string
	name   string
}

// assertRow checks the fields of one change log row against want.
func assertRow(t *testing.T, row inventory.Change, want wantRow) {
	t.Helper()

	if row.Tool != want.tool || row.UnitID != want.unitID || row.Name != want.name ||
		row.DisplayName != want.name || !row.At.Equal(want.at) || row.Status != "" || row.ResourceCount != nil {
		t.Fatalf("change log row = %+v, want %+v", row, want)
	}
}

// testStackChangeLog follows one stack through a create, a tag change, a
// rename, a delete, a restore under a new name, and a purge: each write
// appends one row with the fields of the entry after the write.
func testStackChangeLog(t *testing.T, f fixture) {
	t.Helper()

	ns := f.ref.Namespace
	id := f.stackID(t, f.ref)
	oldName := f.ref.Project.String() + "/" + f.ref.Stack.String()

	assertKinds(t, historyOf(t, f.s), ns, inventory.ChangeCreated)

	tags := map[string]string{"k": "v"}
	if err := f.s.UpdateStackTags(t.Context(), f.ref, tags, now().Add(time.Minute), f.fence); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := f.s.RenameStack(t.Context(), f.ref, "live", f.fence, renameEntry(ns, f.ref)); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	renamed := stack.Ref{Namespace: ns, Project: f.ref.Project, Stack: "live"}
	newName := renamed.Project.String() + "/live"

	deletion := inventory.Deletion{Entry: deleteEntry(ns), PurgeAfter: purgeAt()}
	if err := f.s.DeleteStack(t.Context(), renamed, f.fence, stack.EmptyGuard(), deletion); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	restoreEntry := lifecycleEntry(ns, "unit:restore")
	if _, err := f.s.RestoreStack(t.Context(), stack.ID(id), "again", f.fence, restoreEntry); err != nil {
		t.Fatalf("restore stack: %v", err)
	}

	againName := f.ref.Project.String() + "/again"
	again := stack.Ref{Namespace: ns, Project: f.ref.Project, Stack: "again"}

	if err := f.s.DeleteStack(t.Context(), again, f.fence, stack.EmptyGuard(), deletion); err != nil {
		t.Fatalf("delete restored stack: %v", err)
	}

	purge := lifecycleEntry(ns, "unit:purge")
	if err := f.s.PurgeStack(t.Context(), stack.ID(id), f.fence, time.Time{}, purge); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	rows := assertKinds(t, historyOf(t, f.s), ns,
		inventory.ChangeCreated, inventory.ChangeChanged, inventory.ChangeRenamed, inventory.ChangeDeleted,
		inventory.ChangeRestored, inventory.ChangeDeleted, inventory.ChangePurged)

	assertRow(t, rows[0], wantRow{tool: "pulumi", unitID: id, name: oldName, at: now()})
	assertRow(t, rows[1], wantRow{tool: "pulumi", unitID: id, name: oldName, at: now().Add(time.Minute)})
	assertRow(t, rows[2], wantRow{tool: "pulumi", unitID: id, name: newName, at: rows[2].At})
	assertRow(t, rows[3], wantRow{tool: "pulumi", unitID: id, name: newName, at: deletion.Entry.At})
	assertRow(t, rows[4], wantRow{tool: "pulumi", unitID: id, name: againName, at: restoreEntry.At})
	assertRow(t, rows[5], wantRow{tool: "pulumi", unitID: id, name: againName, at: deletion.Entry.At})
	assertRow(t, rows[6], wantRow{tool: "pulumi", unitID: id, name: againName, at: purge.At})

	for i := range rows {
		if rows[i].Namespace != ns {
			t.Fatalf("row %d namespace = %q, want %q", i, rows[i].Namespace, ns)
		}
	}

	names, err := historyOf(t, f.s).UnitNames(t.Context(), ns, "pulumi", id, now())
	if err != nil || names != (inventory.Names{AtTime: oldName, Current: ""}) {
		t.Fatalf("names of the purged stack = %+v, %v, want %q then none", names, err, oldName)
	}
}

// testStackChangeLogUpdates checks that the start of an update and a preview
// append no row, and that the end of a mutating update appends one changed
// row at its end time.
func testStackChangeLogUpdates(t *testing.T, f fixture) {
	t.Helper()

	ns := f.ref.Namespace

	preview := f.run(t, f.ref, "u-preview", preview())
	f.end(t, preview, stack.StatusSucceeded)

	assertKinds(t, historyOf(t, f.s), ns, inventory.ChangeCreated)

	u := f.run(t, f.ref, "u1", mutating())

	assertKinds(t, historyOf(t, f.s), ns, inventory.ChangeCreated)

	done := f.end(t, u, stack.StatusSucceeded)

	rows := assertKinds(t, historyOf(t, f.s), ns, inventory.ChangeCreated, inventory.ChangeChanged)
	if !rows[1].At.Equal(done.EndedAt) {
		t.Fatalf("changed row at %v, want the end of the update %v", rows[1].At, done.EndedAt)
	}
}

// testStackChangeLogFence checks that writes with a stale fence append no
// row, and that a failed create (a taken name) appends none.
func testStackChangeLogFence(t *testing.T, f fixture) {
	t.Helper()

	ns := f.ref.Namespace
	stale := namespace.Fence{Namespace: ns, FailoverVersion: f.fence.FailoverVersion - 1}

	other := newStack(ns, "web", "dev")
	if err := f.s.CreateStack(t.Context(), other, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence: err = %v, want ErrFenced", err)
	}

	err := f.s.RenameStack(
		t.Context(), f.ref, "live", stale, renameEntry(ns, f.ref),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("rename with a stale fence: err = %v, want ErrFenced", err)
	}

	dup := newStack(ns, f.ref.Project.String(), f.ref.Stack.String())
	if err := f.s.CreateStack(t.Context(), dup, f.fence); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("create with a taken name: err = %v, want ErrAlreadyExists", err)
	}

	assertKinds(t, historyOf(t, f.s), ns, inventory.ChangeCreated)
}

// testWorkspaceChangeLog follows one workspace through a create, a lock, a
// state version, an unlock, a delete, a restore and a purge: the lock and the
// unlock append no row.
func testWorkspaceChangeLog(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	id := w.ID.String()

	assertKinds(t, historyOf(t, s), ns.Name, inventory.ChangeCreated)

	locked := lockTestWorkspace(t, s, ns, w, "alice")

	assertKinds(t, historyOf(t, s), ns.Name, inventory.ChangeCreated)

	finalizeFirstState(t, s, ns, locked)

	rows := assertKinds(t, historyOf(t, s), ns.Name, inventory.ChangeCreated, inventory.ChangeChanged)
	if !rows[1].At.Equal(now().Add(time.Minute)) {
		t.Fatalf("changed row at %v, want the finalize time", rows[1].At)
	}

	current, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if current.Lock.Locked() {
		unlockTestWorkspace(t, s, ns, w.Ref)
	}

	assertKinds(t, historyOf(t, s), ns.Name, inventory.ChangeCreated, inventory.ChangeChanged)

	deletion := inventory.Deletion{Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt()}
	if err = s.DeleteWorkspace(t.Context(), w.Ref, ns.Fence(), deletion); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	restore := lifecycleEntry(ns.Name, "unit:restore")
	if _, err = s.RestoreWorkspace(t.Context(), w.ID, "network-2", ns.Fence(), restore); err != nil {
		t.Fatalf("restore workspace: %v", err)
	}

	restored := workspace.Ref{Namespace: ns.Name, Name: "network-2"}
	if err = s.DeleteWorkspace(t.Context(), restored, ns.Fence(), deletion); err != nil {
		t.Fatalf("delete restored workspace: %v", err)
	}

	purge := lifecycleEntry(ns.Name, "unit:purge")
	if err = s.PurgeWorkspace(t.Context(), w.ID, ns.Fence(), time.Time{}, purge); err != nil {
		t.Fatalf("purge workspace: %v", err)
	}

	rows = assertKinds(t, historyOf(t, s), ns.Name,
		inventory.ChangeCreated, inventory.ChangeChanged, inventory.ChangeDeleted, inventory.ChangeRestored,
		inventory.ChangeDeleted, inventory.ChangePurged)

	assertRow(t, rows[0], wantRow{tool: "opentofu", unitID: id, name: "network", at: w.UpdatedAt})
	assertRow(t, rows[2], wantRow{tool: "opentofu", unitID: id, name: "network", at: deletion.Entry.At})
	assertRow(t, rows[3], wantRow{tool: "opentofu", unitID: id, name: "network-2", at: restore.At})
	assertRow(t, rows[4], wantRow{tool: "opentofu", unitID: id, name: "network-2", at: deletion.Entry.At})
	assertRow(t, rows[5], wantRow{tool: "opentofu", unitID: id, name: "network-2", at: purge.At})

	names, err := historyOf(t, s).UnitNames(t.Context(), ns.Name, "opentofu", id, rows[1].At)
	if err != nil || names != (inventory.Names{AtTime: "network", Current: ""}) {
		t.Fatalf("names of the purged workspace = %+v, %v, want %q then none", names, err, "network")
	}
}

// testWorkspaceChangeLogFence checks that writes with a stale fence, and a
// create of a taken name, append no row.
func testWorkspaceChangeLogFence(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion - 1}

	other := newTestWorkspace(ns, mustWorkspaceName(t, "other"))
	if err := s.CreateWorkspace(t.Context(), other, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence: err = %v, want ErrFenced", err)
	}

	deletion := inventory.Deletion{Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt()}
	if err := s.DeleteWorkspace(t.Context(), w.Ref, stale, deletion); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete with a stale fence: err = %v, want ErrFenced", err)
	}

	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); !errors.Is(err, workspace.ErrAlreadyExists) {
		t.Fatalf("create with a taken name: err = %v, want ErrAlreadyExists", err)
	}

	assertKinds(t, historyOf(t, s), ns.Name, inventory.ChangeCreated)
}
