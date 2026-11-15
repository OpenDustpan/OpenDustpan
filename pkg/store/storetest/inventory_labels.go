package storetest

import (
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// labelsOf returns the labels of the unit with the stable ID id in the
// inventory of ns. It fails the test when the unit is not listed, or when its
// labels are nil: a store reads an empty map for a unit with no label.
func labelsOf(t *testing.T, s Store, ns namespace.Name, id stack.ID) map[string]string {
	t.Helper()

	entries := listInventory(t, s, ns)
	for i := range entries {
		if entries[i].UnitID != id.String() {
			continue
		}

		if entries[i].Labels == nil {
			t.Fatalf("labels of unit %s = nil, want a map", id)
		}

		return entries[i].Labels
	}

	t.Fatalf("inventory of %q does not list unit %s", ns, id)

	return nil
}

// unitListed reports whether the live inventory of ns lists the unit id.
func unitListed(t *testing.T, s Store, ns namespace.Name, id stack.ID) bool {
	t.Helper()

	entries := listInventory(t, s, ns)
	for i := range entries {
		if entries[i].UnitID == id.String() {
			return true
		}
	}

	return false
}

// checkLabels fails the test when the labels of the unit are not want.
func checkLabels(t *testing.T, s Store, ns namespace.Name, id stack.ID, want map[string]string) {
	t.Helper()

	if got := labelsOf(t, s, ns, id); !maps.Equal(got, want) {
		t.Fatalf("labels of unit %s = %v, want %v", id, got, want)
	}
}

// createTagged creates a stack in a new namespace. The stack has the tags of
// the map tags.
func createTagged(t *testing.T, s Store, tags map[string]string) (namespace.Namespace, stack.Stack) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")
	st.Tags = tags

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return ns, st
}

// testInventoryLabelsAfterCreate checks that the tags of a new stack are its
// labels at once, and that a stack with no tag has an empty map.
func testInventoryLabelsAfterCreate(t *testing.T, s Store) {
	t.Helper()

	tests := []struct {
		name string
		tags map[string]string
		want map[string]string
	}{
		{name: "two tags", tags: map[string]string{"app": "web", "team": "payments"}, want: map[string]string{
			"app": "web", "team": "payments",
		}},
		{name: "an empty value", tags: map[string]string{"env": ""}, want: map[string]string{"env": ""}},
		{name: "no tag", tags: nil, want: map[string]string{}},
	}

	ns := newNamespace(t, s, "acme")

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			st := newStack(ns.Name, "web", "stack"+strconv.Itoa(i))
			st.Tags = tc.tags

			if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
				t.Fatalf("create stack: %v", err)
			}

			checkLabels(t, s, ns.Name, st.ID, tc.want)
		})
	}
}

// testInventoryLabelsFollowTagUpdate checks that a tag update replaces the
// labels: a new tag shows, a changed value shows, a removed tag is gone, and
// an empty update clears all of them.
func testInventoryLabelsFollowTagUpdate(t *testing.T, s Store) {
	t.Helper()

	ns, st := createTagged(t, s, map[string]string{"app": "web", "team": "payments"})
	at := now()

	steps := []struct {
		name string
		tags map[string]string
	}{
		{name: "a new tag and a changed value", tags: map[string]string{"app": "api", "team": "payments", "tier": "1"}},
		{name: "a removed tag", tags: map[string]string{"app": "api"}},
		{name: "all tags cleared", tags: nil},
	}

	for _, step := range steps {
		at = at.Add(time.Minute)

		if err := s.UpdateStackTags(t.Context(), st.Ref, maps.Clone(step.tags), at, ns.Fence()); err != nil {
			t.Fatalf("%s: update tags: %v", step.name, err)
		}

		want := step.tags
		if want == nil {
			want = map[string]string{}
		}

		checkLabels(t, s, ns.Name, st.ID, want)
	}
}

// testInventoryLabelsFollowRename checks that a rename keeps the labels of
// the unit, and that a tag update after the rename still reaches them.
func testInventoryLabelsFollowRename(t *testing.T, s Store) {
	t.Helper()

	want := map[string]string{"app": "web", "team": "payments"}
	ns, st := createTagged(t, s, want)

	if err := s.RenameStack(t.Context(), st.Ref, "live", ns.Fence(), renameEntry(ns.Name, st.Ref)); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	checkLabels(t, s, ns.Name, st.ID, want)

	renamed := stack.Ref{Namespace: ns.Name, Project: st.Ref.Project, Stack: "live"}
	next := map[string]string{"app": "web"}

	if err := s.UpdateStackTags(t.Context(), renamed, next, now().Add(time.Minute), ns.Fence()); err != nil {
		t.Fatalf("update tags after the rename: %v", err)
	}

	checkLabels(t, s, ns.Name, st.ID, next)
}

// testInventoryLabelsDeleteRestore checks that a delete takes the unit and its
// labels out of the live projection, and that a restore under a new name brings
// the labels back with the unit.
func testInventoryLabelsDeleteRestore(t *testing.T, s Store) {
	t.Helper()

	want := map[string]string{"app": "web", "team": "payments"}
	ns, st := createTagged(t, s, want)

	err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), inventory.Deletion{
		Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if unitListed(t, s, ns.Name, st.ID) {
		t.Fatalf("unit %s is still in the live inventory after the delete", st.ID)
	}

	restore := lifecycleEntry(ns.Name, "unit:restore")
	if _, err := s.RestoreStack(t.Context(), st.ID, "back", ns.Fence(), restore); err != nil {
		t.Fatalf("restore stack: %v", err)
	}

	checkLabels(t, s, ns.Name, st.ID, want)
}

// testInventoryLabelsPurge checks that a purge removes the unit and its
// labels, and that a new stack under the same name starts with its own
// labels only.
func testInventoryLabelsPurge(t *testing.T, s Store) {
	t.Helper()

	ns, st := createTagged(t, s, map[string]string{"app": "web", "team": "payments"})

	err := s.DeleteStack(t.Context(), st.Ref, ns.Fence(), stack.EmptyGuard(), inventory.Deletion{
		Entry: deleteEntry(ns.Name), PurgeAfter: purgeAt(),
	})
	if err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	purge := lifecycleEntry(ns.Name, "unit:purge")
	if err := s.PurgeStack(t.Context(), st.ID, ns.Fence(), time.Time{}, purge); err != nil {
		t.Fatalf("purge stack: %v", err)
	}

	if unitListed(t, s, ns.Name, st.ID) {
		t.Fatalf("unit %s is still in the live inventory after the purge", st.ID)
	}

	again := newStack(ns.Name, "web", "prod")
	again.Tags = map[string]string{"app": "new"}

	if err := s.CreateStack(t.Context(), again, ns.Fence()); err != nil {
		t.Fatalf("create stack under the purged name: %v", err)
	}

	checkLabels(t, s, ns.Name, again.ID, map[string]string{"app": "new"})
}

// testInventoryLabelsOutsideGrammar checks that the store keeps and lists a
// tag whose key or value the label grammar of the API does not allow, because
// the Pulumi CLI sets the tags and the store must not lose them.
func testInventoryLabelsOutsideGrammar(t *testing.T, s Store) {
	t.Helper()

	want := map[string]string{
		"a key with spaces": "value",
		"long":              strings.Repeat("x", 300),
		"multi":             "line one\nline two",
	}

	ns, st := createTagged(t, s, want)
	checkLabels(t, s, ns.Name, st.ID, want)
}
