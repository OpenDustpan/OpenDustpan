package accesstest

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// UnitStore is a store that answers the stable ID of a unit and keeps the
// units of the Pulumi CLI and OpenTofu. Both SQL stores are one.
type UnitStore interface {
	access.Units
	namespace.Store
	CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	DeleteWorkspace(ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion) error
}

// unitCases are the cases of access.Units. A store that does not keep
// units skips them.
func unitCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "unit ID reads a stack and a workspace by name", test: testUnitID},
		{name: "unit objects read the current name of each ID", test: testUnitObjects},
		{name: "a deleted unit has no stable ID, so its grants give nothing", test: testDeletedUnit},
	}
}

// unitStore returns s as a UnitStore with one stack and one workspace in
// the namespace acme, or skips t.
//
//nolint:ireturn // The suite tests the port: a store has no other shared type.
func unitStore(t *testing.T, s access.Store) UnitStore {
	t.Helper()

	us, ok := s.(UnitStore)
	if !ok {
		t.Skip("the store keeps no units")
	}

	ctx := t.Context()

	ns := newNamespace("acme")
	if err := us.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   at(0), UpdatedAt: at(0), Tags: map[string]string{}, Ref: ref, ID: "stk-web", Version: 0,
	}
	if createErr := us.CreateStack(ctx, st, ns.Fence()); createErr != nil {
		t.Fatalf("create stack: %v", createErr)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	ws := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		Lock:        workspace.Lock{LockedAt: at(0), ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          "ws-network",
		Ref:         workspace.Ref{Namespace: "acme", Name: name},
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Revision: 1,
		Origin:   identity.StateOrigin{Job: "", Commit: ""},
	}
	if err := us.CreateWorkspace(ctx, ws, ns.Fence()); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	return us
}

func unitRef(t *testing.T, tool, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject(tool, "acme", name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj
}

func testUnitID(t *testing.T, s access.Store) {
	t.Helper()

	us := unitStore(t, s)

	cases := []struct {
		obj   identity.Object
		want  string
		found bool
	}{
		{unitRef(t, "pulumi", "web/prod"), "stk-web", true},
		{unitRef(t, "opentofu", "network"), "ws-network", true},
		{unitRef(t, "pulumi", "web/dev"), "", false},
		{unitRef(t, "pulumi", "web"), "", false},
		{unitRef(t, "opentofu", "web"), "", false},
		{unitRef(t, "other", "network"), "", false},
		{identity.NamespaceObject("acme"), "", false},
	}

	for i := range cases {
		tc := &cases[i]

		id, found, err := us.UnitID(t.Context(), tc.obj)
		if err != nil || id != tc.want || found != tc.found {
			t.Errorf("unit ID of %q = %q, %t, %v; want %q, %t", tc.obj.String(), id, found, err, tc.want, tc.found)
		}
	}
}

func testUnitObjects(t *testing.T, s access.Store) {
	t.Helper()

	us := unitStore(t, s)

	got, err := us.UnitObjects(t.Context(), []string{"stk-web", "ws-network", "stk-gone", "other"})
	if err != nil {
		t.Fatalf("unit objects: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("unit objects = %v, want the stack and the workspace", got)
	}

	for id, want := range map[string]string{"stk-web": "pulumi:acme/web/prod", "ws-network": "opentofu:acme/network"} {
		if obj := got[id]; obj.String() != want || obj.UnitID() != id {
			t.Errorf("unit object of %s = %q (ID %q), want %q", id, obj.String(), obj.UnitID(), want)
		}
	}
}

func testDeletedUnit(t *testing.T, s access.Store) {
	t.Helper()

	us := unitStore(t, s)
	ctx := t.Context()

	ns, err := us.GetNamespace(ctx, "acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	name, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	gone := entry()
	gone.Namespace, gone.Action, gone.Object = "acme", "unit:manage", "opentofu:acme/network"

	ref := workspace.Ref{Namespace: "acme", Name: name}
	deletion := inventory.Deletion{Entry: gone, PurgeAfter: gone.At.Add(time.Hour)}
	if deleteErr := us.DeleteWorkspace(ctx, ref, ns.Fence(), deletion); deleteErr != nil {
		t.Fatalf("delete workspace: %v", deleteErr)
	}

	assertLiveStack(t, us)
	assertDeletedWorkspaceGone(t, us)

	got, err := us.UnitObjects(ctx, []string{"ws-network", "stk-web"})
	if err != nil {
		t.Fatalf("unit objects: %v", err)
	}

	if _, ok := got["ws-network"]; ok || len(got) != 1 {
		t.Fatalf("unit objects = %v, want the stack only", got)
	}
}

// assertLiveStack checks that a live stack keeps its ID and its name.
func assertLiveStack(t *testing.T, us UnitStore) {
	t.Helper()

	live, found, err := us.UnitID(t.Context(), unitRef(t, "pulumi", "web/prod"))
	if err != nil || !found || live != "stk-web" {
		t.Fatalf("unit ID of the live stack = %q, %t, %v; want stk-web, true", live, found, err)
	}
}

// assertDeletedWorkspaceGone checks that a deleted workspace has no ID.
func assertDeletedWorkspaceGone(t *testing.T, us UnitStore) {
	t.Helper()

	id, found, err := us.UnitID(t.Context(), unitRef(t, "opentofu", "network"))
	if err != nil || found || id != "" {
		t.Fatalf("unit ID of a deleted workspace = %q, %t, %v; want none", id, found, err)
	}
}
