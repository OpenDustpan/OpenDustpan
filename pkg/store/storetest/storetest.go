// Package storetest is the conformance suite for persistence adapters. Every
// built-in store runs it, and a custom store passed to dustpan.NewServer should
// too:
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		storetest.Run(t, func(t *testing.T) storetest.Store { return newMyStore(t) })
//	}
//
// RunUpdates covers the update-cycle ports (stack.UpdateStore,
// stack.CheckpointStore, stack.EventStore) the same way. RunWorkspace in
// workspaces.go covers the workspace ports (workspace.Store,
// workspace.StateStore) the same way.
package storetest

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// Store is every persistence port the suite exercises.
type Store interface {
	namespace.Store
	stack.Store
	inventory.Reader
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s Store)
		name string
	}{
		{name: "namespace round trip", test: testNamespaceRoundTrip},
		{name: "duplicate namespace is ErrAlreadyExists", test: testDuplicateNamespace},
		{name: "missing namespace is ErrNotFound", test: testMissingNamespace},
		{name: "list namespaces orders by name", test: testListNamespaces},
		{name: "ping answers on an open store and fails after close", test: testPing},
		{name: "stack round trip", test: testStackRoundTrip},
		{name: "duplicate stack is ErrAlreadyExists", test: testDuplicateStack},
		{name: "stale fence is ErrFenced", test: testStaleFence},
		{name: "stack in missing namespace is namespace.ErrNotFound", test: testStackMissingNamespace},
		{name: "missing stack is ErrNotFound", test: testMissingStack},
		{name: "list filters and orders", test: testList},
		{name: "stack ID is set and returned", test: testStackID},
		{name: "stack IDs are unique", test: testStackIDsUnique},
		{name: "delete then create gives a new ID", test: testStackIDAfterDelete},
		{name: "delete of a missing stack is ErrNotFound", test: testDeleteMissingStack},
		{name: "delete with a stale fence is ErrFenced", test: testDeleteStaleFence},
		{name: "delete removes the tags of the stack", test: testDeleteRemovesTags},
		{name: "update tags replaces all tags", test: testUpdateTags},
		{name: "update tags clears all tags", test: testUpdateTagsClear},
		{name: "update tags on missing stack is ErrNotFound", test: testUpdateTagsMissing},
		{name: "update tags with stale fence is ErrFenced", test: testUpdateTagsStaleFence},
		{name: "rename keeps the ID", test: testRenameKeepsID},
		{name: "rename to a taken name is ErrAlreadyExists", test: testRenameTaken},
		{name: "rename of a missing stack is ErrNotFound", test: testRenameMissing},
		{name: "rename with a stale fence is ErrFenced", test: testRenameStaleFence},
		{name: "rename keeps tags", test: testRenameKeepsTags},
		{name: "inventory lists the created stack", test: testInventoryAfterCreate},
		{name: "inventory follows a rename", test: testInventoryAfterRename},
		{name: "inventory drops a deleted stack", test: testInventoryAfterDelete},
		{name: "inventory follows a tag change", test: testInventoryAfterTagChange},
		{name: "inventory orders by tool then name", test: testInventoryOrders},
		{name: "failed stack write leaves inventory unchanged", test: testInventoryFailedWrite},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// now is a fixed, millisecond-precision instant: stores keep milliseconds.
func now() time.Time {
	return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)
}

func newNamespace(t *testing.T, s namespace.Store, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       now(),
		Name:            namespace.Name(name),
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

func newStack(ns namespace.Name, project, name string) stack.Stack {
	return stack.Stack{
		CreatedAt: now(),
		UpdatedAt: now(),
		Tags:      map[string]string{"team": "platform"},
		Ref:       stack.Ref{Namespace: ns, Project: stack.ProjectName(project), Stack: stack.Name(name)},
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}
}

func testNamespaceRoundTrip(t *testing.T, s Store) {
	t.Helper()

	want := newNamespace(t, s, "acme")

	got, err := s.GetNamespace(t.Context(), want.Name)
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	if got != want {
		t.Fatalf("get namespace = %+v, want %+v", got, want)
	}
}

func testDuplicateNamespace(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	if err := s.CreateNamespace(t.Context(), ns); !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create duplicate namespace: err = %v, want ErrAlreadyExists", err)
	}
}

func testMissingNamespace(t *testing.T, s Store) {
	t.Helper()

	if _, err := s.GetNamespace(t.Context(), "nope"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("get missing namespace: err = %v, want ErrNotFound", err)
	}
}

func testListNamespaces(t *testing.T, s Store) {
	t.Helper()

	empty, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	if len(empty) != 0 {
		t.Fatalf("list namespaces = %v, want empty", empty)
	}

	for _, name := range []string{"zeta", "acme", "beta"} {
		newNamespace(t, s, name)
	}

	got, err := s.ListNamespaces(t.Context())
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}

	want := []namespace.Name{"acme", "beta", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("list namespaces = %v, want %v", names(got), want)
	}

	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("list namespaces = %v, want %v", names(got), want)
		}
	}
}

func names(namespaces []namespace.Namespace) []namespace.Name {
	out := make([]namespace.Name, 0, len(namespaces))
	for i := range namespaces {
		out = append(out, namespaces[i].Name)
	}

	return out
}

// testPing checks Ping answers nil on an open store and maps any failure to
// namespace.ErrUnavailable without driver text, here forced by Close.
func testPing(t *testing.T, s Store) {
	t.Helper()

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping open store: %v", err)
	}

	closeStore(t, s)

	err := s.Ping(t.Context())
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ping closed store: err = %v, want ErrUnavailable", err)
	}

	// The adapters drop the driver error, so none of the texts the drivers
	// report may reach the caller. The adapter names stay: each adapter wraps
	// the sentinel as "ping <adapter> database: database unavailable".
	for _, text := range []string{"sql: database is closed", "closed pool", "SQLITE", "sqlite3", "pgx"} {
		if strings.Contains(err.Error(), text) {
			t.Fatalf("ping closed store error %q holds driver text %q", err, text)
		}
	}
}

// closeStore closes s. The built-in adapters close with different signatures,
// so it accepts both.
func closeStore(t *testing.T, s Store) {
	t.Helper()

	var err error

	switch c := any(s).(type) {
	case interface{ Close() error }:
		err = c.Close()
	case interface{ Close() }:
		c.Close()
	default:
		t.Fatalf("close store of type %T: no Close method", s)
	}

	if err != nil {
		t.Fatalf("close store: %v", err)
	}
}

func testStackRoundTrip(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	want := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), want, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	got, err := s.GetStack(t.Context(), want.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	assertStack(t, got, want)
}

func testDuplicateStack(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	if err := s.CreateStack(t.Context(), st, ns.Fence()); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("create duplicate stack: err = %v, want ErrAlreadyExists", err)
	}
}

func testStaleFence(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create stack with stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetStack(t.Context(), st.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("fenced stack was written: err = %v, want ErrNotFound", err)
	}
}

func testStackMissingNamespace(t *testing.T, s Store) {
	t.Helper()

	st := newStack("ghost", "web", "prod")
	fence := namespace.Fence{Namespace: "ghost", FailoverVersion: 1}

	if err := s.CreateStack(t.Context(), st, fence); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("create stack in missing namespace: err = %v, want namespace.ErrNotFound", err)
	}
}

func testMissingStack(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ref := stack.Ref{Namespace: ns.Name, Project: "web", Stack: "prod"}

	if _, err := s.GetStack(t.Context(), ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get missing stack: err = %v, want ErrNotFound", err)
	}
}

func testList(t *testing.T, s Store) {
	t.Helper()

	acme := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "other")

	stacks := []stack.Stack{
		newStack(acme.Name, "web", "prod"),
		newStack(acme.Name, "api", "prod"),
		newStack(acme.Name, "web", "dev"),
		newStack(other.Name, "web", "prod"),
	}
	for i := range stacks {
		fence := namespace.Fence{Namespace: stacks[i].Ref.Namespace, FailoverVersion: 1}
		if err := s.CreateStack(t.Context(), stacks[i], fence); err != nil {
			t.Fatalf("create stack %s: %v", stacks[i].Ref, err)
		}
	}

	all, err := s.ListStacks(t.Context(), stack.Filter{Namespace: acme.Name, Project: ""})
	if err != nil {
		t.Fatalf("list namespace: %v", err)
	}

	assertRefs(t, all, "acme/api/prod", "acme/web/dev", "acme/web/prod")

	web, err := s.ListStacks(t.Context(), stack.Filter{Namespace: acme.Name, Project: "web"})
	if err != nil {
		t.Fatalf("list project: %v", err)
	}

	assertRefs(t, web, "acme/web/dev", "acme/web/prod")
}

func assertStack(t *testing.T, got, want stack.Stack) {
	t.Helper()

	if got.Ref != want.Ref || got.ID != want.ID || got.Version != want.Version ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		!maps.Equal(got.Tags, want.Tags) {
		t.Fatalf("stack = %+v, want %+v", got, want)
	}
}

func testStackID(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	want := newStack(ns.Name, "web", "prod")

	if want.ID == "" || !strings.HasPrefix(want.ID.String(), "stk-") {
		t.Fatalf("new stack ID = %q, want stk- prefix", want.ID)
	}

	if err := s.CreateStack(t.Context(), want, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	got, err := s.GetStack(t.Context(), want.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if got.ID != want.ID {
		t.Fatalf("get stack ID = %q, want %q", got.ID, want.ID)
	}

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 1 || list[0].ID != want.ID {
		t.Fatalf("list stacks IDs = %v, want [%q]", list, want.ID)
	}
}

func testStackIDsUnique(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := newStack(ns.Name, "web", "prod")
	second := newStack(ns.Name, "web", "dev")

	if err := s.CreateStack(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("create first stack: %v", err)
	}

	if err := s.CreateStack(t.Context(), second, ns.Fence()); err != nil {
		t.Fatalf("create second stack: %v", err)
	}

	if first.ID == second.ID {
		t.Fatalf("stack IDs are not unique: both %q", first.ID)
	}

	wants := []stack.Stack{first, second}
	for i := range wants {
		got, err := s.GetStack(t.Context(), wants[i].Ref)
		if err != nil {
			t.Fatalf("get stack %s: %v", wants[i].Ref, err)
		}

		if got.ID != wants[i].ID {
			t.Fatalf("get stack %s ID = %q, want %q", wants[i].Ref, got.ID, wants[i].ID)
		}
	}
}

func testStackIDAfterDelete(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	if err := s.DeleteStack(t.Context(), first.Ref, ns.Fence()); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if _, err := s.GetStack(t.Context(), first.Ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get deleted stack: err = %v, want ErrNotFound", err)
	}

	second := newStack(ns.Name, "web", "prod")
	if err := s.CreateStack(t.Context(), second, ns.Fence()); err != nil {
		t.Fatalf("create stack again: %v", err)
	}

	if second.ID == first.ID {
		t.Fatalf("stack ID after delete then create = %q, want a new ID", second.ID)
	}

	got, err := s.GetStack(t.Context(), second.Ref)
	if err != nil {
		t.Fatalf("get recreated stack: %v", err)
	}

	if got.ID != second.ID {
		t.Fatalf("recreated stack ID = %q, want %q", got.ID, second.ID)
	}
}

func testDeleteMissingStack(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ghost := stack.Ref{Namespace: ns.Name, Project: "web", Stack: "ghost"}

	if err := s.DeleteStack(t.Context(), ghost, ns.Fence()); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("delete missing stack: err = %v, want ErrNotFound", err)
	}
}

func testDeleteStaleFence(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	if err := s.DeleteStack(t.Context(), st.Ref, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete stack with stale fence: err = %v, want ErrFenced", err)
	}

	if _, err := s.GetStack(t.Context(), st.Ref); err != nil {
		t.Fatalf("fenced delete removed the stack: %v", err)
	}
}

// testRenameKeepsID renames a stack and checks the ID stayed, the old name is
// gone and the new name lists the same stack.
func testRenameKeepsID(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	newRef := stack.Ref{Namespace: ns.Name, Project: st.Ref.Project, Stack: "renamed"}

	if err := s.RenameStack(t.Context(), st.Ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	got, err := s.GetStack(t.Context(), newRef)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if got.ID != st.ID {
		t.Fatalf("renamed stack ID = %q, want %q", got.ID, st.ID)
	}

	if _, getErr := s.GetStack(t.Context(), st.Ref); !errors.Is(getErr, stack.ErrNotFound) {
		t.Fatalf("get old stack: err = %v, want ErrNotFound", getErr)
	}

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 1 || list[0].Ref != newRef || list[0].ID != st.ID {
		t.Fatalf("list stacks = %+v, want one stack %s with ID %q", list, newRef, st.ID)
	}
}

// testRenameTaken renames onto a taken name and expects ErrAlreadyExists,
// with both stacks unchanged.
func testRenameTaken(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := newStack(ns.Name, "web", "prod")
	second := newStack(ns.Name, "web", "dev")

	wants := []stack.Stack{first, second}
	for i := range wants {
		if err := s.CreateStack(t.Context(), wants[i], ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", wants[i].Ref, err)
		}
	}

	if err := s.RenameStack(t.Context(), first.Ref, "dev", ns.Fence()); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrAlreadyExists", err)
	}

	for i := range wants {
		got, err := s.GetStack(t.Context(), wants[i].Ref)
		if err != nil {
			t.Fatalf("get stack %s: %v", wants[i].Ref, err)
		}

		assertStack(t, got, wants[i])
	}
}

// testRenameMissing renames a stack that does not exist and expects ErrNotFound.
func testRenameMissing(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ref := stack.Ref{Namespace: ns.Name, Project: "web", Stack: "prod"}

	if err := s.RenameStack(t.Context(), ref, "renamed", ns.Fence()); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("rename missing stack: err = %v, want ErrNotFound", err)
	}
}

// testRenameStaleFence renames with a stale fence and expects ErrFenced,
// with the stack unchanged.
func testRenameStaleFence(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.RenameStack(t.Context(), st.Ref, "renamed", stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("rename with stale fence: err = %v, want ErrFenced", err)
	}

	got, err := s.GetStack(t.Context(), st.Ref)
	if err != nil {
		t.Fatalf("get stack after fenced rename: %v", err)
	}

	assertStack(t, got, st)
}

// testUpdateTags checks that a tag update replaces every tag, keys the rows
// by the stack ID and stamps the given time.
func testUpdateTags(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	updated := now().Add(time.Hour)
	next := map[string]string{"team": "e2e", "owner": "sre"}

	if err := s.UpdateStackTags(t.Context(), st.Ref, next, updated, ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	got, err := s.GetStack(t.Context(), st.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if !maps.Equal(got.Tags, next) {
		t.Fatalf("tags = %v, want %v", got.Tags, next)
	}

	if got.ID != st.ID {
		t.Fatalf("stack ID = %q, want %q", got.ID, st.ID)
	}

	if !got.UpdatedAt.Equal(updated) {
		t.Fatalf("updated at = %v, want %v", got.UpdatedAt, updated)
	}

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: ns.Name, Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 1 || !maps.Equal(list[0].Tags, next) {
		t.Fatalf("listed tags = %v, want %v", list, next)
	}
}

// testUpdateTagsClear checks that an empty map removes every tag row.
func testUpdateTagsClear(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	if err := s.UpdateStackTags(t.Context(), st.Ref, map[string]string{}, now(), ns.Fence()); err != nil {
		t.Fatalf("clear tags: %v", err)
	}

	got, err := s.GetStack(t.Context(), st.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if len(got.Tags) != 0 {
		t.Fatalf("tags = %v, want none", got.Tags)
	}
}

func testUpdateTagsMissing(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	ref := stack.Ref{Namespace: ns.Name, Project: "web", Stack: "prod"}

	if err := s.UpdateStackTags(t.Context(), ref, map[string]string{"team": "e2e"}, now(), ns.Fence()); !errors.Is(
		err, stack.ErrNotFound,
	) {
		t.Fatalf("update tags of missing stack: err = %v, want ErrNotFound", err)
	}
}

func testUpdateTagsStaleFence(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	if err := s.UpdateStackTags(t.Context(), st.Ref, map[string]string{"team": "e2e"}, now(), stale); !errors.Is(
		err, namespace.ErrFenced,
	) {
		t.Fatalf("update tags with stale fence: err = %v, want ErrFenced", err)
	}

	got, err := s.GetStack(t.Context(), st.Ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if !maps.Equal(got.Tags, st.Tags) {
		t.Fatalf("tags = %v, want unchanged %v", got.Tags, st.Tags)
	}
}

// testRenameKeepsTags checks that tags follow the stack ID through a rename.
func testRenameKeepsTags(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	next := map[string]string{"team": "e2e"}

	if err := s.UpdateStackTags(t.Context(), st.Ref, next, now(), ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := s.RenameStack(t.Context(), st.Ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	newRef := stack.Ref{Namespace: ns.Name, Project: st.Ref.Project, Stack: "renamed"}

	got, err := s.GetStack(t.Context(), newRef)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if !maps.Equal(got.Tags, next) {
		t.Fatalf("renamed tags = %v, want %v", got.Tags, next)
	}

	if got.ID != st.ID {
		t.Fatalf("renamed stack ID = %q, want %q", got.ID, st.ID)
	}

	if _, getErr := s.GetStack(t.Context(), st.Ref); !errors.Is(getErr, stack.ErrNotFound) {
		t.Fatalf("get old stack: err = %v, want ErrNotFound", getErr)
	}
}

func assertRefs(t *testing.T, stacks []stack.Stack, want ...string) {
	t.Helper()

	got := make([]string, 0, len(stacks))
	for i := range stacks {
		got = append(got, stacks[i].Ref.String())
	}

	if len(got) != len(want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refs = %v, want %v", got, want)
		}
	}
}

// testDeleteRemovesTags tags a stack, deletes it and creates a stack of the
// same name. The new stack must hold only its own tags, not the old ones.
func testDeleteRemovesTags(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	first := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), first, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	tags := map[string]string{"team": "e2e", "old": "only"}
	if err := s.UpdateStackTags(t.Context(), first.Ref, tags, now(), ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := s.DeleteStack(t.Context(), first.Ref, ns.Fence()); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	second := newStack(ns.Name, "web", "prod")
	if err := s.CreateStack(t.Context(), second, ns.Fence()); err != nil {
		t.Fatalf("create stack again: %v", err)
	}

	got, err := s.GetStack(t.Context(), second.Ref)
	if err != nil {
		t.Fatalf("get recreated stack: %v", err)
	}

	if !maps.Equal(got.Tags, second.Tags) {
		t.Fatalf("recreated stack tags = %v, want %v", got.Tags, second.Tags)
	}
}

// listInventory returns the inventory entries of ns, failing the test on error.
func listInventory(t *testing.T, s Store, ns namespace.Name) []inventory.Entry {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("list inventory of %q: %v", ns, err)
	}

	return entries
}

// assertInventoryEntry checks one entry against its stack and change time.
func assertInventoryEntry(t *testing.T, got inventory.Entry, st stack.Stack, changed time.Time) {
	t.Helper()

	wantName := st.Ref.Project.String() + "/" + st.Ref.Stack.String()

	if got.Tool != "pulumi" || got.Namespace != st.Ref.Namespace || got.Name != wantName ||
		got.UnitID != st.ID.String() || got.DisplayName != wantName ||
		!got.LastChanged.Equal(changed) || got.Status != "" || got.ResourceCount != nil {
		t.Fatalf("inventory entry = %+v, want tool pulumi name %q unit %q changed %v", got, wantName, st.ID, changed)
	}
}

// testInventoryAfterCreate creates a stack and checks its entry exists.
func testInventoryAfterCreate(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	entries := listInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertInventoryEntry(t, entries[0], st, st.UpdatedAt)
}

// testInventoryAfterRename renames a stack and checks one entry stays, under
// the new name and with the same stack ID.
func testInventoryAfterRename(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	if err := s.RenameStack(t.Context(), st.Ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	entries := listInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	renamed := st
	renamed.Ref.Stack = "renamed"

	assertInventoryEntry(t, entries[0], renamed, st.UpdatedAt)
}

// testInventoryAfterDelete deletes a stack and checks no entry stays.
func testInventoryAfterDelete(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	if err := s.DeleteStack(t.Context(), st.Ref, ns.Fence()); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if entries := listInventory(t, s, ns.Name); len(entries) != 0 {
		t.Fatalf("inventory entries = %+v, want none", entries)
	}
}

// testInventoryAfterTagChange changes tags and checks the entry follows the
// new change time.
func testInventoryAfterTagChange(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	updated := now().Add(time.Hour)
	next := map[string]string{"team": "e2e"}

	if err := s.UpdateStackTags(t.Context(), st.Ref, next, updated, ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	entries := listInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertInventoryEntry(t, entries[0], st, updated)
}

// testInventoryOrders checks entries order by tool, then name.
func testInventoryOrders(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	for _, nn := range [][2]string{{"web", "prod"}, {"api", "prod"}, {"web", "dev"}} {
		st := newStack(ns.Name, nn[0], nn[1])
		if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
			t.Fatalf("create stack %s: %v", st.Ref, err)
		}
	}

	entries := listInventory(t, s, ns.Name)
	if len(entries) != 3 {
		t.Fatalf("inventory entries = %+v, want three", entries)
	}

	want := []string{"api/prod", "web/dev", "web/prod"}

	for i := range want {
		if entries[i].Tool != "pulumi" || entries[i].Name != want[i] {
			t.Fatalf("inventory entry %d = %s:%s, want pulumi:%s", i, entries[i].Tool, entries[i].Name, want[i])
		}
	}
}

// testInventoryFailedWrite checks a write that fails leaves the table
// unchanged: a duplicate and a stale fence add or remove nothing.
func testInventoryFailedWrite(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	duplicate := newStack(ns.Name, "web", "prod")
	if err := s.CreateStack(t.Context(), duplicate, ns.Fence()); !errors.Is(err, stack.ErrAlreadyExists) {
		t.Fatalf("create duplicate stack: err = %v, want ErrAlreadyExists", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	ghost := newStack(ns.Name, "web", "dev")

	if err := s.CreateStack(t.Context(), ghost, stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create stack with stale fence: err = %v, want ErrFenced", err)
	}

	if err := s.RenameStack(t.Context(), st.Ref, "renamed", stale); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("rename with stale fence: err = %v, want ErrFenced", err)
	}

	entries := listInventory(t, s, ns.Name)
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %+v, want one", entries)
	}

	assertInventoryEntry(t, entries[0], st, st.UpdatedAt)
}
