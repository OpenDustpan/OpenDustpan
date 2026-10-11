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
// stack.CheckpointStore, stack.EventStore) the same way.
package storetest

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
)

// Store is every persistence port the suite exercises.
type Store interface {
	namespace.Store
	stack.Store
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
		{name: "stack round trip", test: testStackRoundTrip},
		{name: "duplicate stack is ErrAlreadyExists", test: testDuplicateStack},
		{name: "stale fence is ErrFenced", test: testStaleFence},
		{name: "stack in missing namespace is namespace.ErrNotFound", test: testStackMissingNamespace},
		{name: "missing stack is ErrNotFound", test: testMissingStack},
		{name: "list filters and orders", test: testList},
		{name: "stack ID is set and returned", test: testStackID},
		{name: "stack IDs are unique", test: testStackIDsUnique},
		{name: "delete then create gives a new ID", test: testStackIDAfterDelete},
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

func newNamespace(t *testing.T, s Store, name string) namespace.Namespace {
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

func testStackIDAfterDelete(t *testing.T, _ Store) {
	t.Helper()

	t.Skip("delete then create with the same name waits for issue #49")
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
