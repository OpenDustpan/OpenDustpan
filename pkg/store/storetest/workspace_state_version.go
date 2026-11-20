package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// testGetWorkspaceStateVersion proves that GetWorkspaceStateVersion returns a
// version only for the workspace and the namespace that own it, and that every
// other pair reads as ErrStateVersionNotFound.
func testGetWorkspaceStateVersion(t *testing.T, s WorkspaceStateAtStore) {
	t.Helper()

	own, ownWS := newStateAtWorkspace(t, s)
	done := own.finalizeAt(t, s, 1, workspaceStateAtBase().Add(time.Minute))

	// A second workspace in the same namespace, and one in another namespace.
	sibling := createTestWorkspace(t, s, own.ns, "storage")
	otherNS := newWorkspaceNamespace(t, s, "beta")
	foreign := createTestWorkspace(t, s, otherNS, "network")

	got, err := s.GetWorkspaceStateVersion(t.Context(), own.ns.Name, ownWS.ID, done.ID)
	if err != nil {
		t.Fatalf("get own state version: %v", err)
	}

	want, err := s.GetStateVersion(t.Context(), done.ID)
	if err != nil {
		t.Fatalf("get state version: %v", err)
	}

	if got.ID != want.ID || got.Workspace != want.Workspace || got.Serial != want.Serial ||
		got.Status != want.Status || !got.FinalizedAt.Equal(want.FinalizedAt) {
		t.Fatalf("own state version = %+v, want %+v", got, want)
	}

	for _, tc := range []struct {
		name string
		ns   namespace.Name
		ws   workspace.ID
		id   workspace.StateVersionID
	}{
		{name: "workspace of the same namespace", ns: own.ns.Name, ws: sibling.ID, id: done.ID},
		{name: "workspace of another namespace", ns: otherNS.Name, ws: foreign.ID, id: done.ID},
		{name: "right workspace, wrong namespace", ns: otherNS.Name, ws: ownWS.ID, id: done.ID},
		{name: "unknown workspace", ns: own.ns.Name, ws: "ws-unknown", id: done.ID},
		{name: "unknown version", ns: own.ns.Name, ws: ownWS.ID, id: "sv-unknown"},
	} {
		sv, err := s.GetWorkspaceStateVersion(t.Context(), tc.ns, tc.ws, tc.id)
		if !errors.Is(err, workspace.ErrStateVersionNotFound) {
			t.Fatalf("%s: err = %v, want ErrStateVersionNotFound", tc.name, err)
		}

		if sv.ID != "" {
			t.Fatalf("%s: version %q with an error, want the zero value", tc.name, sv.ID)
		}
	}
}
