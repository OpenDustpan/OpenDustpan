package sqlite_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// snapshotStack makes the namespace acme and the stack acme/web/dev with the
// tag team=blue in a fresh store, and returns the store, the namespace and
// the reference of the stack.
func snapshotStack(t *testing.T) (*sqlite.Store, namespace.Namespace, stack.Ref) {
	t.Helper()

	s := open(t)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", HomeRegion: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "", CreatedAt: time.Now(), UpdatedAt: time.Now(), Tags: map[string]string{"team": "blue"},
		Ref: ref, ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}
	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	return s, ns, ref
}

// retagBetweenReads sets the hook of s that changes the tag team of ref to
// red one time, between the statement of a page and the statement of its tags
// or labels.
func retagBetweenReads(t *testing.T, s *sqlite.Store, ns namespace.Namespace, ref stack.Ref) {
	t.Helper()

	done := false

	sqlite.SetBetweenPageReads(s, func(ctx context.Context) {
		if done {
			return
		}

		done = true

		err := s.UpdateStackTags(ctx, ref, map[string]string{"team": "red"}, time.Now(), ns.Fence())
		if err != nil {
			t.Errorf("change the tags between the reads: %v", err)
		}
	})
}

// TestQueryStacksReadsOneSnapshot proves that the tags of a stack page belong
// to the state that the page statement read: a tag write between the two
// statements is not in the answer.
func TestQueryStacksReadsOneSnapshot(t *testing.T) {
	t.Parallel()

	s, ns, ref := snapshotStack(t)
	retagBetweenReads(t, s, ns, ref)

	got, err := s.QueryStacks(t.Context(), stack.PageQuery{
		After: nil, Project: "", Tag: nil, Namespaces: []namespace.Name{"acme"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("query stacks: %v", err)
	}

	if len(got) != 1 || got[0].Tags["team"] != "blue" {
		t.Fatalf("stacks = %+v, want acme/web/dev with the tag team=blue of the page snapshot", got)
	}
}

// TestQueryUnitsReadsOneSnapshot proves that the labels of a unit page belong
// to the state that the page statement read: a unit that the label filter
// team=blue selected does not come back with the label team=red.
func TestQueryUnitsReadsOneSnapshot(t *testing.T) {
	t.Parallel()

	s, ns, ref := snapshotStack(t)
	retagBetweenReads(t, s, ns, ref)

	got, err := s.QueryUnits(t.Context(), inventory.UnitQuery{
		Namespace: "acme", After: "", Labels: []inventory.UnitLabel{{Key: "team", Value: "blue"}}, Units: nil,
		States: nil, Limit: 10, RestrictUnits: false, RestrictStates: false,
	})
	if err != nil {
		t.Fatalf("query units: %v", err)
	}

	if len(got) != 1 || got[0].Labels["team"] != "blue" {
		t.Fatalf("units = %+v, want the one unit with the label team=blue of the page snapshot", got)
	}
}
