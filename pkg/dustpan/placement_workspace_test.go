package dustpan_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// placedUnitStore is the part of a store that a test placement reads and
// writes: the namespace for its fence and the collection that holds the unit.
type placedUnitStore interface {
	namespace.Store
	collection.Store
}

// fenceOf returns the fence of the namespace ns in st. The namespace must
// exist.
func fenceOf(tb testing.TB, st placedUnitStore, ns namespace.Name) namespace.Fence {
	tb.Helper()

	got, err := st.GetNamespace(tb.Context(), ns)
	if err != nil {
		tb.Fatalf("get namespace %s: %v", ns, err)
	}

	return got.Fence()
}

// placeWorkspace returns the placement of a new workspace in the namespace ns,
// in a collection that it makes in store when it does not exist yet.
func placeWorkspace(tb testing.TB, store any, ns namespace.Name) workspace.Placement {
	tb.Helper()

	st, ok := store.(placedUnitStore)
	if !ok {
		tb.Fatalf("store %T keeps no collections", store)
	}

	return placementtest.Workspace(tb, st, fenceOf(tb, st, ns))
}

// placeStack is placeWorkspace for a new stack.
func placeStack(tb testing.TB, store any, ns namespace.Name) stack.Placement {
	tb.Helper()

	st, ok := store.(placedUnitStore)
	if !ok {
		tb.Fatalf("store %T keeps no collections", store)
	}

	return placementtest.Stack(tb, st, fenceOf(tb, st, ns))
}

// seedWebWorkspace makes the workspace acme/web in a collection through the
// service, so that a request of a tool finds the workspace and does not need to
// create it.
func seedWebWorkspace(t *testing.T, f fixture) {
	t.Helper()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ref := workspace.Ref{Namespace: "acme", Name: name}

	if _, err := f.srv.Workspaces().Create(t.Context(), ref, placeWorkspace(t, f.store, "acme")); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}
