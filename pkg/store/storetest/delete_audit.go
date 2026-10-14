package storetest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// AuditBreaker is an optional UpdatesStore capability for the fail-closed
// delete case: after BreakAuditAppends, every insert of an audit entry fails
// inside its transaction. Only SQL can make a valid append fail there, so the
// adapters implement it with a trigger.
type AuditBreaker interface {
	UpdatesStore
	// BreakAuditAppends makes every later insert of an audit entry fail.
	BreakAuditAppends(ctx context.Context) error
}

// auditLister is the part of audit.Store that the delete cases read.
type auditLister interface {
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
}

// testDeleteAppendsChangeEntry deletes the stack and expects the audit chain
// of the namespace to hold the entry of the delete, at the time of the delete.
func testDeleteAppendsChangeEntry(t *testing.T, f fixture) {
	t.Helper()

	lister, ok := f.s.(auditLister)
	if !ok {
		t.Skip("store does not list audit entries")
	}

	want := deleteEntry(f.ref.Namespace)
	if err := f.s.DeleteStack(t.Context(), f.ref, f.fence, stack.EmptyGuard(), want); err != nil {
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
		!got.At.Equal(want.At) || got.Sequence != 1 {
		t.Fatalf("audit entry = %+v, want %+v as sequence 1", got, want)
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

		err := f.s.DeleteStack(t.Context(), f.ref, f.fence, stack.EmptyGuard(), *e)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("delete with entry %+v: err = %v, want ErrInvalidEntry", e, err)
		}

		err = f.s.ForceDeleteStack(t.Context(), f.ref, f.fence, *e)
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

	err := f.s.DeleteStack(t.Context(), f.ref, f.fence, stack.EmptyGuard(), deleteEntry(f.ref.Namespace))
	if err == nil {
		t.Fatal("delete with a failing audit append: err = nil, want an error")
	}

	if err := f.s.ForceDeleteStack(t.Context(), f.ref, f.fence, deleteEntry(f.ref.Namespace)); err == nil {
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

	if err := f.s.DeleteStack(ctx, f.ref, f.fence, stack.EmptyGuard(), deleteEntry(f.ref.Namespace)); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	tomb := stack.Ref{
		Namespace: f.ref.Namespace,
		Project:   f.ref.Project,
		Stack:     stack.Name("deleted:" + st.ID.String()),
	}

	if _, err := f.s.GetStack(ctx, tomb); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get stack by the stored name: err = %v, want ErrNotFound", err)
	}

	if err := f.s.RenameStack(ctx, tomb, "back", f.fence); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("rename by the stored name: err = %v, want ErrNotFound", err)
	}

	err := f.s.UpdateStackTags(ctx, tomb, map[string]string{"k": "v"}, deletedAt(), f.fence)
	if !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("update tags by the stored name: err = %v, want ErrNotFound", err)
	}

	if err := f.s.CreateUpdate(ctx, newUpdate(tomb, "u2", mutating(), "", 0), f.fence); !errors.Is(
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
		"update_journal", "update_journal_sequences", "secret_keys",
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
		deleteEntry(seed.acme.Name),
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
