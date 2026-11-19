package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// tagSecret is a tag value that no audit entry may hold.
const tagSecret = "s3cr3t-tag-value"

// listAudit returns the audit entries of ns, or skips the test when the store
// cannot list them.
func listAudit(t *testing.T, s any, ns namespace.Name) []audit.Entry {
	t.Helper()

	lister, ok := s.(auditLister)
	if !ok {
		t.Skip("store does not list audit entries")
	}

	listing, err := lister.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return listing.Entries
}

// testUpdateTagsAppendsChangeEntry replaces the tags and expects one change
// entry in the transaction of the update: it names each key that the update
// adds, removes or changes, holds no tag value, and has the stable stack ID.
func testUpdateTagsAppendsChangeEntry(t *testing.T, f fixture) {
	t.Helper()

	st := mustGetFixtureStack(t, f)
	ns := f.ref.Namespace

	first := map[string]string{"keep": "a", "drop": tagSecret, "edit": "old"}
	err := f.s.UpdateStackTags(
		t.Context(),
		f.ref,
		stack.TagUpdate{Tags: first, UpdatedAt: now(), Entry: renameEntry(ns, f.ref)},
		f.fence,
	)
	if err != nil {
		t.Fatalf("update tags: %v", err)
	}

	second := map[string]string{"keep": "a", "edit": tagSecret, "new": tagSecret}
	err = f.s.UpdateStackTags(
		t.Context(),
		f.ref,
		stack.TagUpdate{Tags: second, UpdatedAt: now(), Entry: renameEntry(ns, f.ref)},
		f.fence,
	)
	if err != nil {
		t.Fatalf("update tags again: %v", err)
	}

	entries := listAudit(t, f.s, ns)
	if len(entries) != 2 {
		t.Fatalf("audit entries = %+v, want one for each update", entries)
	}

	got := entries[0]
	if entries[1].Sequence > got.Sequence {
		got = entries[1]
	}
	if got.Kind != audit.KindChange || got.ObjectID != st.ID.String() {
		t.Fatalf("audit entry = %+v, want a change with object ID %s", got, st.ID)
	}

	keys := make([]string, 0, len(got.Changes))
	for _, c := range got.Changes {
		keys = append(keys, c.Key)

		if strings.Contains(c.New, tagSecret) || (c.Old != nil && strings.Contains(*c.Old, tagSecret)) {
			t.Fatalf("change %+v holds a tag value", c)
		}
	}

	if want := []string{"tags.drop", "tags.edit", "tags.new"}; !slices.Equal(keys, want) {
		t.Fatalf("changed keys = %v, want %v", keys, want)
	}
}

// testUpdateTagsWithoutChangeAppendsNothing replaces the tags with the same
// tags: the stack keeps its tags and the chain gets no entry.
func testUpdateTagsWithoutChangeAppendsNothing(t *testing.T, f fixture) {
	t.Helper()

	same := map[string]string{"team": "core"}
	ns := f.ref.Namespace

	for range 2 {
		err := f.s.UpdateStackTags(
			t.Context(),
			f.ref,
			stack.TagUpdate{Tags: same, UpdatedAt: now(), Entry: renameEntry(ns, f.ref)},
			f.fence,
		)
		if err != nil {
			t.Fatalf("update tags: %v", err)
		}
	}

	if entries := listAudit(t, f.s, ns); len(entries) != 1 {
		t.Fatalf("audit entries = %+v, want one: only the first update changes a key", entries)
	}
}

// testUpdateTagsRefusedEntryChangesNothing passes an entry that fails
// validation and an entry of another namespace: the update returns
// audit.ErrInvalidEntry, and the tags and the chain stay as they were.
func testUpdateTagsRefusedEntryChangesNothing(t *testing.T, f fixture) {
	t.Helper()

	ns := f.ref.Namespace
	before := mustGetFixtureStack(t, f)

	invalid := renameEntry(ns, f.ref)
	invalid.Subject = ""

	refused := []audit.Entry{invalid, renameEntry("other", f.ref)}
	for i := range refused {
		e := &refused[i]
		err := f.s.UpdateStackTags(
			t.Context(),
			f.ref,
			stack.TagUpdate{Tags: map[string]string{"k": "v"}, UpdatedAt: now(), Entry: *e},
			f.fence,
		)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Fatalf("update tags with entry %+v: err = %v, want ErrInvalidEntry", e, err)
		}
	}

	after := mustGetFixtureStack(t, f)
	if len(after.Tags) != len(before.Tags) {
		t.Fatalf("tags after the refused update = %v, want %v", after.Tags, before.Tags)
	}

	if entries := listAudit(t, f.s, ns); len(entries) != 0 {
		t.Fatalf("audit entries = %+v, want none", entries)
	}
}

// testUpdateTagsFailedAppendChangesNothing breaks the audit append: the
// update fails and the tags stay (fail closed).
func testUpdateTagsFailedAppendChangesNothing(t *testing.T, f fixture) {
	t.Helper()

	breaker, ok := f.s.(AuditBreaker)
	if !ok {
		t.Skip("store cannot break audit appends")
	}

	before := mustGetFixtureStack(t, f)

	if err := breaker.BreakAuditAppends(t.Context()); err != nil {
		t.Fatalf("break audit appends: %v", err)
	}

	err := f.s.UpdateStackTags(
		t.Context(),
		f.ref,
		stack.TagUpdate{
			Tags:      map[string]string{"k": "v"},
			UpdatedAt: now(),
			Entry:     renameEntry(f.ref.Namespace, f.ref),
		},
		f.fence,
	)
	if err == nil {
		t.Fatal("update tags with a broken audit append succeeded")
	}

	if after := mustGetFixtureStack(t, f); len(after.Tags) != len(before.Tags) {
		t.Fatalf("tags after the failed update = %v, want %v", after.Tags, before.Tags)
	}
}
