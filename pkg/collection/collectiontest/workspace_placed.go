package collectiontest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newOpenTofuWorkspace returns a new workspace called name in ns.
func newOpenTofuWorkspace(t *testing.T, ns namespace.Name, name string) workspace.Workspace {
	t.Helper()

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now(),
		UpdatedAt:   now(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns, Name: mustWorkspaceName(t, name)},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

// workspacePlacement returns the placement of a new workspace into c under fence.
func workspacePlacement(c collection.Collection, fence namespace.Fence) workspace.Placement {
	return workspace.Placement{Collection: c.Ref, CollectionID: c.ID, Entry: auditFor(fence)}
}

// testCreateWorkspacePlaced checks that a placed create makes the workspace
// and its membership together, and shows the workspace in the inventory with
// its collection.
func testCreateWorkspacePlaced(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	w := newOpenTofuWorkspace(t, ns.Name, "network")
	if err := s.CreateWorkspacePlaced(t.Context(), w, workspacePlacement(c, ns.Fence()), ns.Fence()); err != nil {
		t.Fatalf("CreateWorkspacePlaced: %v", err)
	}

	unit := collection.UnitRef{Namespace: ns.Name, Tool: "opentofu", Name: "network"}

	got, err := s.UnitCollection(t.Context(), unit)
	if err != nil || got != c.Ref {
		t.Errorf("collection of the placed workspace = %v, %v; want %v", got, err, c.Ref)
	}

	if _, getErr := s.GetWorkspace(t.Context(), w.Ref); getErr != nil {
		t.Errorf("GetWorkspace of the placed workspace: %v", getErr)
	}
}

// testCreateWorkspacePlacedRefused checks that each refusal of a placed create
// leaves no workspace, no inventory entry and no membership: the create and
// the placement are one transaction. The first four cases send a hostile
// placement straight to the store.
func testCreateWorkspacePlacedRefused(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	reborn := c
	reborn.ID = ident.NewID("col-")

	var none workspace.Placement

	noID := workspacePlacement(c, ns.Fence())
	noID.CollectionID = ""

	noName := workspacePlacement(c, ns.Fence())
	noName.Collection.Name = ""

	otherNS := workspacePlacement(c, ns.Fence())
	otherNS.Collection.Namespace = otherNamespace

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}
	missing := newCollection(t, ns.Name, "absent")

	refusals := []struct {
		name  string
		p     workspace.Placement
		fence namespace.Fence
		want  error
	}{
		{"no placement", none, ns.Fence(), collection.ErrNoCollection},
		{"no collection ID", noID, ns.Fence(), collection.ErrNoCollection},
		{"no collection name", noName, ns.Fence(), collection.ErrNoCollection},
		{"a collection of another namespace", otherNS, ns.Fence(), collection.ErrNoCollection},
		{
			"a collection that does not exist",
			workspacePlacement(missing, ns.Fence()),
			ns.Fence(),
			collection.ErrNotFound,
		},
		{
			"a collection with another ID",
			workspacePlacement(reborn, ns.Fence()),
			ns.Fence(),
			collection.ErrCollectionChanged,
		},
		{"a stale fence", workspacePlacement(c, stale), stale, namespace.ErrFenced},
	}

	for i := range refusals {
		w := newOpenTofuWorkspace(t, ns.Name, "refused-"+string(rune('a'+i)))

		err := s.CreateWorkspacePlaced(t.Context(), w, refusals[i].p, refusals[i].fence)
		if !errors.Is(err, refusals[i].want) {
			t.Errorf("%s: err = %v, want %v", refusals[i].name, err, refusals[i].want)
		}

		if _, getErr := s.GetWorkspace(t.Context(), w.Ref); !errors.Is(getErr, workspace.ErrNotFound) {
			t.Errorf("%s: GetWorkspace = %v, want ErrNotFound", refusals[i].name, getErr)
		}
	}

	requireNoUnits(t, s, ns.Name, c)
}

// requireNoUnits checks that ns holds no unit and c holds no member.
func requireNoUnits(t *testing.T, s Store, ns namespace.Name, c collection.Collection) {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), ns)
	if err != nil || len(entries) != 0 {
		t.Errorf("inventory after the refusals = %+v, %v; want no unit", entries, err)
	}

	ids, err := s.ListCollectionUnitIDs(t.Context(), c.Ref, c.ID)
	if err != nil || len(ids) != 0 {
		t.Errorf("members after the refusals = %v, %v; want none", ids, err)
	}
}
