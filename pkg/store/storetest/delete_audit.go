package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// AuditBreaker is an optional capability of the stores of the update and
// workspace suites for the fail-closed delete cases: after BreakAuditAppends,
// every insert of an audit entry fails inside its transaction. Only SQL can
// make a valid append fail there, so the adapters implement it with a trigger.
type AuditBreaker interface {
	// BreakAuditAppends makes every later insert of an audit entry fail.
	BreakAuditAppends(ctx context.Context) error
}

// auditLister is the part of audit.Store that the delete cases read.
type auditLister interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

// testDeleteAppendsChangeEntry deletes the stack and expects the audit chain
// of the namespace to hold the entry of the delete, at the time of the delete
// and with the stable ID of the stack as its object ID.
func testDeleteAppendsChangeEntry(t *testing.T, f fixture) {
	t.Helper()

	lister, ok := f.s.(auditLister)
	if !ok {
		t.Skip("store does not list audit entries")
	}

	st := mustGetFixtureStack(t, f)

	want := deleteEntry(f.ref.Namespace)
	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: want, PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	listing, err := lister.List(t.Context(), f.ref.Namespace, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("audit entries = %+v, want the entry of the delete", listing.Entries)
	}

	got := listing.Entries[0]
	if got.Kind != audit.KindChange || got.Object != want.Object || got.Action != want.Action ||
		!got.At.Equal(want.At) || got.Sequence != 1 || got.ObjectID != st.ID.String() {
		t.Fatalf("audit entry = %+v, want %+v as sequence 1 with object ID %s", got, want, st.ID)
	}
}

// testDeleteRefusedEntryChangesNothing passes an entry that fails validation
// and an entry of another namespace to both deletes: each returns
// audit.ErrInvalidEntry, and the stack, its inventory entry and its name stay.
func testDeleteRefusedEntryChangesNothing(t *testing.T, f fixture) {
	t.Helper()

	before := mustGetFixtureStack(t, f)

	invalid := deleteEntry(f.ref.Namespace)
	invalid.Subject = ""

	elsewhere := deleteEntry("other")

	refused := []audit.Entry{invalid, elsewhere}
	for i := range refused {
		e := &refused[i]

		err := f.s.DeleteStack(
			t.Context(),
			f.ref,
			f.fence,
			stack.EmptyGuard(),
			inventory.Deletion{Entry: *e, PurgeAfter: purgeAt()},
		)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("delete with entry %+v: err = %v, want ErrInvalidEntry", e, err)
		}

		err = f.s.ForceDeleteStack(t.Context(), f.ref, f.fence, inventory.Deletion{Entry: *e, PurgeAfter: purgeAt()})
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("force delete with entry %+v: err = %v, want ErrInvalidEntry", e, err)
		}
	}

	assertStackUnchanged(t, f, before)
}

// testDeleteFailedAppendChangesNothing breaks the audit appends of the store,
// then deletes the stack with a valid entry: both deletes fail, and the stack,
// its inventory entry and its name stay, because the delete and its entry
// commit together or not at all (ADR 60, fail closed).
func testDeleteFailedAppendChangesNothing(t *testing.T, f fixture) {
	t.Helper()

	breaker, ok := f.s.(AuditBreaker)
	if !ok {
		t.Skip("store cannot make an audit append fail")
	}

	before := mustGetFixtureStack(t, f)

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	err := f.s.DeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.ref.Namespace), PurgeAfter: purgeAt()},
	)
	if err == nil {
		t.Fatal("delete with a failing audit append: err = nil, want an error")
	}

	err = f.s.ForceDeleteStack(
		t.Context(),
		f.ref,
		f.fence,
		inventory.Deletion{Entry: deleteEntry(f.ref.Namespace), PurgeAfter: purgeAt()},
	)
	if err == nil {
		t.Fatal("force delete with a failing audit append: err = nil, want an error")
	}

	assertStackUnchanged(t, f, before)
}

// mustGetFixtureStack returns the stack of the fixture.
func mustGetFixtureStack(t *testing.T, f fixture) stack.Stack {
	t.Helper()

	st, err := f.s.GetStack(t.Context(), f.ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	return st
}

// assertStackUnchanged checks that the stack of the fixture is still live
// with the ID of before, that its live inventory entry stays, and that the
// namespace has no deleted unit.
func assertStackUnchanged(t *testing.T, f fixture, before stack.Stack) {
	t.Helper()

	after := mustGetFixtureStack(t, f)
	if after.ID != before.ID || after.Version != before.Version {
		t.Fatalf("stack after the failed delete = %+v, want %+v", after, before)
	}

	found := false

	entries := listInventory(t, f.s, f.ref.Namespace)
	for i := range entries {
		if entries[i].UnitID == before.ID.String() {
			found = true
		}
	}

	if !found {
		t.Fatalf("inventory has no entry of stack %s after the failed delete", before.ID)
	}

	deleted, err := f.s.ListDeletedUnits(t.Context(), f.ref.Namespace)
	if err != nil {
		t.Fatalf("list deleted units: %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted units after the failed delete = %+v, want none", deleted)
	}
}

// testTombstoneNameFindsNothing deletes the stack, then uses the stored
// name of the deleted row, "deleted:" and the stack ID, in every lookup by
// name: none finds the deleted stack (ADR 47). Route validation already
// refuses the ":" in a name; this case proves the store does not rely on it.
func testTombstoneNameFindsNothing(t *testing.T, f fixture) {
	t.Helper()

	ctx := t.Context()
	st := mustGetFixtureStack(t, f)
	waiting := f.create(t, f.ref, "u1", mutating())

	err := f.s.DeleteStack(
		ctx,
		f.ref,
		f.fence,
		stack.EmptyGuard(),
		inventory.Deletion{Entry: deleteEntry(f.ref.Namespace), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	tomb := stack.Ref{
		Namespace: f.ref.Namespace,
		Project:   f.ref.Project,
		Stack:     stack.Name("deleted:" + st.ID.String()),
	}

	if _, err = f.s.GetStack(ctx, tomb); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get stack by the stored name: err = %v, want ErrNotFound", err)
	}

	err = f.s.RenameStack(
		ctx, tomb, "back", f.fence, renameEntry(f.ref.Namespace, tomb),
	)
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("rename by the stored name: err = %v, want ErrNotFound", err)
	}

	err = f.s.UpdateStackTags(ctx, tomb, map[string]string{"k": "v"}, deletedAt(), f.fence)
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("update tags by the stored name: err = %v, want ErrNotFound", err)
	}

	tombUpdate := newUpdate(tomb, "u2", mutating(), "", 0)
	if err := f.s.CreateUpdate(ctx, tombUpdate, UnrecordedKey(), f.fence); !errors.Is(
		err, stack.ErrNotFound,
	) {
		t.Fatalf("create update by the stored name: err = %v, want ErrNotFound", err)
	}

	waiting.Ref = tomb
	if _, err := f.s.StartUpdate(ctx, started(t, waiting), f.fence); err == nil {
		t.Fatal("start update by the stored name: err = nil, want an error")
	}
}

// keptChildTables are the tables whose rows a soft delete keeps unchanged
// (ADR 47, decision 3). The delete changes only the stacks row, the inventory
// and the audit chain.
func keptChildTables() []string {
	return []string{
		"stack_tags", "updates", "checkpoints", "update_events",
		"update_journal", "update_journal_sequences", "update_journal_refusals", "secret_keys",
	}
}

// testPortableDeleteKeepsChildren deletes a stack that has tags, updates, a
// checkpoint, events and data keys, and compares a portable export from
// before the delete with one from after: every child table file is byte for
// byte the same, the stacks table keeps its row count, the live inventory
// loses one entry, and the deleted units and the audit chain gain one each.
func testPortableDeleteKeepsChildren(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seed := seedBackupStore(t, s)

	err := s.UpdateStackTags(t.Context(), seed.prod.Ref, map[string]string{"team": "core"}, now(), seed.acme.Fence())
	if err != nil {
		t.Fatalf("tag stack: %v", err)
	}

	before, sumBefore, _ := exportPortable(t, s)

	for _, table := range []string{"stack_tags", "updates", "checkpoints", "update_events", "secret_keys"} {
		if sumBefore.RowCounts[table] == 0 {
			t.Fatalf("seed holds no %s rows, want rows for the delete to keep", table)
		}
	}

	err = s.ForceDeleteStack(
		t.Context(),
		seed.prod.Ref,
		seed.acme.Fence(),
		inventory.Deletion{Entry: deleteEntry(seed.acme.Name), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	after, sumAfter, _ := exportPortable(t, s)

	for _, table := range keptChildTables() {
		path := backup.PortablePath(table)
		if !bytes.Equal(before.files[path].Bytes(), after.files[path].Bytes()) {
			t.Fatalf("table %s changed in a soft delete, want every row kept", table)
		}
	}

	deltas := map[string]int64{"stacks": 0, "inventory": -1, "inventory_deleted": 1, "audit_entries": 1}
	for table, delta := range deltas {
		if got, want := sumAfter.RowCounts[table], sumBefore.RowCounts[table]+delta; got != want {
			t.Fatalf("rows of %s after the delete = %d, want %d", table, got, want)
		}
	}
}

// workspaceDeleteEntry returns the audit entry of a delete of the workspace
// "network" in ns, the workspace of the delete cases.
func workspaceDeleteEntry(ns namespace.Name) audit.Entry {
	e := deleteEntry(ns)
	e.Object = "opentofu:" + ns.String() + "/network"

	return e
}

// testDeleteWorkspaceAppendsChangeEntry deletes a workspace and expects the
// audit chain of the namespace to hold the entry of the delete, with the
// workspace ID as its object ID.
func testDeleteWorkspaceAppendsChangeEntry(t *testing.T, s WorkspaceStore) {
	t.Helper()

	lister, ok := s.(auditLister)
	if !ok {
		t.Skip("store does not list audit entries")
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	want := workspaceDeleteEntry(ns.Name)

	err := s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: want, PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	listing, err := lister.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("audit entries = %+v, want the entry of the delete", listing.Entries)
	}

	got := listing.Entries[0]
	if got.Kind != audit.KindChange || got.Object != want.Object || !got.At.Equal(want.At) || got.Sequence != 1 ||
		got.ObjectID != w.ID.String() {
		t.Fatalf("audit entry = %+v, want %+v as sequence 1 with object ID %s", got, want, w.ID)
	}
}

// testDeleteWorkspaceRefusedEntryChangesNothing passes an entry that fails
// validation and an entry of another namespace: each delete returns
// audit.ErrInvalidEntry, and the workspace stays.
func testDeleteWorkspaceRefusedEntryChangesNothing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	invalid := workspaceDeleteEntry(ns.Name)
	invalid.Subject = ""

	refused := []audit.Entry{invalid, workspaceDeleteEntry("other")}
	for i := range refused {
		err := s.DeleteWorkspace(
			t.Context(),
			w.Ref,
			ns.Fence(),
			inventory.Deletion{Entry: refused[i], PurgeAfter: purgeAt()},
		)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("delete with entry %+v: err = %v, want ErrInvalidEntry", refused[i], err)
		}
	}

	assertWorkspaceUnchanged(t, s, ns, w)
}

// testDeleteWorkspaceFailedAppendChangesNothing breaks the audit appends of
// the store, then deletes a workspace with a valid entry: the delete fails,
// and the workspace, its inventory entry and its name stay (ADR 60, fail
// closed).
func testDeleteWorkspaceFailedAppendChangesNothing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	breaker, ok := s.(AuditBreaker)
	if !ok {
		t.Skip("store cannot make an audit append fail")
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	err := s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if err == nil {
		t.Fatal("delete with a failing audit append: err = nil, want an error")
	}

	assertWorkspaceUnchanged(t, s, ns, w)
}

// testDeletedWorkspaceStoredNameFindsNothing deletes a workspace, then looks
// it up by its stored name, "deleted:" and the workspace ID: the lookup finds
// nothing (ADR 47).
func testDeletedWorkspaceStoredNameFindsNothing(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	err := s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	tomb := workspace.Ref{Namespace: ns.Name, Name: workspace.Name("deleted:" + w.ID.String())}
	if _, err := s.GetWorkspace(t.Context(), tomb); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get workspace by the stored name: err = %v, want ErrNotFound", err)
	}
}

// assertWorkspaceUnchanged checks that w is still live under its name with its
// ID, that its live inventory entry stays, and that ns has no deleted unit.
func assertWorkspaceUnchanged(t *testing.T, s WorkspaceStore, ns namespace.Namespace, w workspace.Workspace) {
	t.Helper()

	got, err := s.GetWorkspace(t.Context(), w.Ref)
	if err != nil {
		t.Fatalf("get workspace after the failed delete: %v", err)
	}

	if got.ID != w.ID {
		t.Fatalf("workspace after the failed delete = %s, want %s", got.ID, w.ID)
	}

	found := false

	entries := listWorkspaceInventory(t, s, ns.Name)
	for i := range entries {
		if entries[i].UnitID == w.ID.String() {
			found = true
		}
	}

	if !found {
		t.Fatalf("inventory has no entry of workspace %s after the failed delete", w.ID)
	}

	deleted, err := s.ListDeletedUnits(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("list deleted units: %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted units after the failed delete = %+v, want none", deleted)
	}
}

// testDeleteWorkspaceRefusesPendingState declares a pending state version,
// releases the lock and deletes the workspace: the delete returns
// ErrPendingState and changes nothing, so an upload never loses its
// workspace. testDeleteWorkspace covers the delete after a finalize.
func testDeleteWorkspaceRefusesPendingState(t *testing.T, s WorkspaceStore) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	locked := lockTestWorkspace(t, s, ns, w, "alice")
	createPendingVersion(t, s, ns, newPendingVersion(t, locked, locked.Lock.ID, stateInput("l1", "v1", 1)))

	unlockTestWorkspace(t, s, ns, w.Ref)

	err := s.DeleteWorkspace(
		t.Context(),
		w.Ref,
		ns.Fence(),
		inventory.Deletion{Entry: workspaceDeleteEntry(ns.Name), PurgeAfter: purgeAt()},
	)
	if !errors.Is(err, workspace.ErrPendingState) {
		t.Fatalf("delete with a pending state version: err = %v, want ErrPendingState", err)
	}

	assertWorkspaceUnchanged(t, s, ns, w)
}

// testPortablePurgeRemovesChildren deletes and purges a stack that has tags,
// updates, a checkpoint, events and data keys: no table of a portable export
// holds a row of the stack any more, and the other stacks keep theirs.
func testPortablePurgeRemovesChildren(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seed := seedBackupStore(t, s)
	id := seed.prod.ID.String()

	deletion := inventory.Deletion{Entry: deleteEntry(seed.acme.Name), PurgeAfter: purgeAt()}
	if err := s.ForceDeleteStack(t.Context(), seed.prod.Ref, seed.acme.Fence(), deletion); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	purge := lifecycleEntry(seed.acme.Name, "unit:purge")
	if err := s.PurgeStack(t.Context(), seed.prod.ID, seed.acme.Fence(), time.Time{}, purge); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	after, sum, _ := exportPortable(t, s)

	for _, table := range append(keptChildTables(), "stacks", "inventory_deleted", "inventory_labels") {
		path := backup.PortablePath(table)

		for _, row := range readPortableLines(t, path, after.files[path].Bytes()) {
			if row["stack_id"] == id || row["id"] == id || row["unit_id"] == id {
				t.Fatalf("table %s still holds a row of the purged stack %s: %v", table, id, row)
			}
		}
	}

	if sum.RowCounts["stacks"] == 0 {
		t.Fatal("export holds no stack, want the other stacks of the seed")
	}
}

// Wrapper is a test wrapper around a store, as an adapter test builds one
// to break or count a part of it. Inner returns the store it wraps, so a
// conformance case reaches the optional ports of that store.
type Wrapper interface {
	Inner() any
}

// storeAs returns s, or the store that s wraps, as a T. It reports false
// when neither is a T.
func storeAs[T any](s any) (T, bool) {
	if v, ok := s.(T); ok {
		return v, true
	}

	if w, ok := s.(Wrapper); ok {
		return storeAs[T](w.Inner())
	}

	var zero T

	return zero, false
}

// auditAppender is the part of audit.Store that the object ID cases write.
type auditAppender interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// unitEntry returns an audit entry of the kind tool-operation on object in
// ns, with no object ID, as a tool adapter writes it.
func unitEntry(ns namespace.Name, object string) audit.Entry {
	e := deleteEntry(ns)
	e.Kind = audit.KindToolOperation
	e.Action = "unit:read-secrets"
	e.Object = object

	return e
}

// checkAppendedObjectID appends e and checks that the store stored want as
// its object ID.
func checkAppendedObjectID(t *testing.T, s auditAppender, fence namespace.Fence, e audit.Entry, want string) {
	t.Helper()

	stored, err := s.Append(t.Context(), fence, e)
	if err != nil {
		t.Fatalf("append entry on %s: %v", e.Object, err)
	}

	if stored.ObjectID != want {
		t.Fatalf("object ID of the entry on %s = %q, want %q", e.Object, stored.ObjectID, want)
	}

	if stored.Hash != audit.ComputeHash(stored, stored.PrevHash) {
		t.Fatalf("hash of the entry on %s does not cover its object ID", e.Object)
	}
}

// testAppendFillsStackID appends entries with no object ID, as the tool
// adapters write them (ADR 47). An entry on the live stack gets its stable
// ID in the append transaction; an entry on an unknown stack or on the
// namespace gets none.
func testAppendFillsStackID(t *testing.T, f fixture) {
	t.Helper()

	appender, ok := storeAs[auditAppender](f.s)
	if !ok {
		return
	}

	st := mustGetFixtureStack(t, f)
	ns := f.ref.Namespace
	unit := "pulumi:" + ns.String() + "/" + f.ref.Project.String() + "/" + f.ref.Stack.String()

	checkAppendedObjectID(t, appender, f.fence, unitEntry(ns, unit), st.ID.String())
	checkAppendedObjectID(t, appender, f.fence, unitEntry(ns, "pulumi:"+ns.String()+"/web/unknown"), "")
	checkAppendedObjectID(t, appender, f.fence, unitEntry(ns, ns.String()), "")
}

// testAppendFillsWorkspaceID is testAppendFillsStackID for a workspace.
func testAppendFillsWorkspaceID(t *testing.T, s WorkspaceStore) {
	t.Helper()

	appender, ok := storeAs[auditAppender](s)
	if !ok {
		return
	}

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")

	checkAppendedObjectID(
		t,
		appender,
		ns.Fence(),
		unitEntry(ns.Name, "opentofu:acme/"+w.Ref.Name.String()),
		w.ID.String(),
	)
	checkAppendedObjectID(t, appender, ns.Fence(), unitEntry(ns.Name, "opentofu:acme/unknown"), "")
}

// seedPurgedWorkspace makes the workspace "doomed" in acme with a finalized
// state version and a data key, unlocks it, deletes it and purges it. It
// returns the stable ID of the workspace.
func seedPurgedWorkspace(t *testing.T, s PortableStore, acme namespace.Namespace) string {
	t.Helper()

	ctx := t.Context()
	w := createTestWorkspace(t, s, acme, "doomed")
	locked := lockTestWorkspace(t, s, acme, w, "alice")
	declared := newPendingVersion(t, locked, locked.Lock.ID, stateInput("l9", "v9", 1))
	createPendingVersion(t, s, acme, declared)

	current := finalizedVersion(declared, now().Add(2*time.Hour), "v9")
	if _, err := s.RecordUploadAttempt(ctx, current.ID, current.Content, acme.Fence()); err != nil {
		t.Fatalf("record upload attempt: %v", err)
	}

	if err := s.FinalizeStateVersion(ctx, current, acme.Fence()); err != nil {
		t.Fatalf("finalize state version: %v", err)
	}

	unlockTestWorkspace(t, s, acme, w.Ref)
	seedBlameRows(t, s, blameSeed{ns: acme, w: w, first: current.ID.String(), second: "sv-doomed-opaque"})

	scope, err := secret.NewScope(acme.Name, w.ID.String())
	if err != nil {
		t.Fatalf("scope of the workspace: %v", err)
	}

	key := secret.WrappedKey{CreatedAt: now(), Scope: scope, WrapperID: "kek-1", Ciphertext: []byte("wrapped-doomed")}
	if _, err := s.CreateKey(ctx, key, acme.Fence()); err != nil {
		t.Fatalf("create key of the workspace: %v", err)
	}

	d := inventory.Deletion{Entry: workspaceDeleteEntry(acme.Name), PurgeAfter: purgeAt()}
	d.Entry.Object = "opentofu:" + acme.Name.String() + "/doomed"

	if err := s.DeleteWorkspace(ctx, w.Ref, acme.Fence(), d); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	purgeErr := s.PurgeWorkspace(
		ctx,
		w.ID,
		acme.Fence(),
		time.Time{},
		lifecycleEntry(acme.Name, "unit:purge"),
	)
	if purgeErr != nil {
		t.Fatalf("purge workspace: %v", purgeErr)
	}

	return w.ID.String()
}

// testPortablePurgeRemovesWorkspaceRows deletes and purges a workspace that
// has a finalized state version and a data key (ADR 23, decision 5): no
// table of a portable export holds a row of the workspace any more, and the
// other workspace keeps its state versions.
func testPortablePurgeRemovesWorkspaceRows(t *testing.T, newStore PortableFactory) {
	t.Helper()

	s := newStore(t)
	seed := seedBackupStore(t, s)
	id := seedPurgedWorkspace(t, s, seed.acme)

	after, sum, _ := exportPortable(t, s)

	for _, table := range []string{
		"workspaces", "state_versions", "secret_keys", "inventory", "inventory_deleted",
		"workspace_blame_cursors", "workspace_blame_items", "workspace_resource_changes",
	} {
		path := backup.PortablePath(table)

		for _, row := range readPortableLines(t, path, after.files[path].Bytes()) {
			if row["id"] == id || row["workspace_id"] == id || row["stack_id"] == id || row["unit_id"] == id {
				t.Fatalf("table %s still holds a row of the purged workspace %s: %v", table, id, row)
			}
		}
	}

	if sum.RowCounts["state_versions"] == 0 || sum.RowCounts["secret_keys"] == 0 {
		t.Fatal("export lost the state versions or the data keys of the other units")
	}

	if _, ok := s.(workspace.BlameStore); ok {
		checkBlameRowCounts(t, s, sum)
	}
}
