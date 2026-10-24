package storetest

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// LiveMarkerStore is every port the live marker suite reads through, and the
// one hook that writes a marker as a restored backup or another tool could.
type LiveMarkerStore interface {
	RevocationStore
	WorkspaceStore
	access.RoleStore

	// SetMarker sets the column of the row id in table with no check, as a
	// write to the database itself would. A nil value stores NULL. The suite
	// uses it to store a value that the ports never write, for example -1.
	SetMarker(ctx context.Context, table, column, id string, value *int64) error
}

// LiveMarkerFactory returns a fresh, empty store for one subtest.
type LiveMarkerFactory func(t *testing.T) LiveMarkerStore

// liveMarkerValue is one stored value of a deleted or ended marker.
type liveMarkerValue struct {
	value *int64
	name  string
	live  bool
}

// liveMarkerValues returns the values that the suite stores in a marker. A
// row reads as live only when its marker is NULL: -1, 0 and a past time are
// values that a restored backup may hold, and none of them may read as live.
func liveMarkerValues() []liveMarkerValue {
	minusOne, zero, past := int64(-1), int64(0), int64(1_700_000_000_000)

	return []liveMarkerValue{
		{name: "NULL", value: nil, live: true},
		{name: "-1", value: &minusOne, live: false},
		{name: "0", value: &zero, live: false},
		{name: "a past time", value: &past, live: false},
	}
}

// RunLiveMarkers executes the suite of the live row checks. A lookup reads a
// row as live only when the deleted or end marker of the row is NULL. No
// stored value, also not -1 or 0, makes a deleted row read as live. A new
// adapter must run it.
func RunLiveMarkers(t *testing.T, newStore LiveMarkerFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s LiveMarkerStore, v liveMarkerValue)
		name string
	}{
		{name: "an access token of a principal", test: testMarkerPrincipalToken},
		{name: "a session of a principal", test: testMarkerPrincipalSession},
		{name: "a grant by its ID", test: testMarkerGrantRead},
		{name: "a revoke of a grant by its ID", test: testMarkerGrantRevoke},
		{name: "a custom role by its ID", test: testMarkerRole},
		{name: "a workspace by its ID", test: testMarkerWorkspace},
	}

	for _, tc := range cases {
		for _, v := range liveMarkerValues() {
			t.Run(tc.name+" with marker "+v.name, func(t *testing.T) {
				t.Parallel()
				tc.test(t, newStore(t), v)
			})
		}
	}
}

// markerRef names the marker of one row.
type markerRef struct {
	table  string
	column string
	id     string
}

// setMarker stores value in the marker of the row id and fails the test on an
// error.
func setMarker(t *testing.T, s LiveMarkerStore, ref markerRef, v liveMarkerValue) {
	t.Helper()

	if err := s.SetMarker(t.Context(), ref.table, ref.column, ref.id, v.value); err != nil {
		t.Fatalf("set %s.%s of %q to %s: %v", ref.table, ref.column, ref.id, v.name, err)
	}
}

// wantLive fails the test unless found matches the value: only NULL reads as
// live.
func wantLive(t *testing.T, what string, v liveMarkerValue, err, notFound error) {
	t.Helper()

	switch {
	case v.live && err != nil:
		t.Fatalf("%s with a NULL marker: %v, want the live row", what, err)
	case !v.live && err == nil:
		t.Fatalf("%s with the marker %s: found, want %v", what, v.name, notFound)
	case !v.live && !errors.Is(err, notFound):
		t.Fatalf("%s with the marker %s: err = %v, want %v", what, v.name, err, notFound)
	default:
	}
}

func testMarkerPrincipalToken(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustToken(t, s, revToken(revPersonalToken, revPerson, accesstoken.KindPersonal), 3)
	setMarker(t, s, markerRef{table: "principals", column: "deleted_at_ms", id: revPerson}, v)

	_, err := s.GetTokenByDigest(t.Context(), revDigest(3))
	wantLive(t, "token by digest", v, err, accesstoken.ErrNotFound)
}

func testMarkerPrincipalSession(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustSession(t, s, revSessionOne, revPerson, 1)
	setMarker(t, s, markerRef{table: "principals", column: "deleted_at_ms", id: revPerson}, v)

	_, err := s.GetSessionByDigest(t.Context(), revDigest(1))
	wantLive(t, "session by digest", v, err, session.ErrNotFound)
}

func testMarkerGrantRead(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustGrant(t, s, revGrantServer, identity.ServerObject(), revNoFence())
	setMarker(t, s, markerRef{table: "grants", column: "revoked_at_ms", id: revGrantServer}, v)

	_, err := s.GetGrant(t.Context(), revGrantServer)
	wantLive(t, "grant by ID", v, err, access.ErrNotFound)
}

func testMarkerGrantRevoke(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	revMustPrincipal(t, s, revPerson, principal.KindPerson)
	revMustGrant(t, s, revGrantServer, identity.ServerObject(), revNoFence())
	setMarker(t, s, markerRef{table: "grants", column: "revoked_at_ms", id: revGrantServer}, v)

	err := s.RevokeGrant(t.Context(), revGrantServer, revNoFence(), revEntry("grant:"+revGrantServer, revAt(5)), nil)
	wantLive(t, "revoke of a grant by ID", v, err, access.ErrNotFound)
}

func testMarkerRole(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	role := access.Role{
		CreatedAt: revAt(0), UpdatedAt: revAt(0), ID: "rol-marker", Name: "Marker", Description: "",
		Actions: []identity.Action{identity.ActionUnitRead}, BuiltIn: false,
	}

	if err := s.CreateRole(t.Context(), role, revEntry("role:rol-marker", revAt(0))); err != nil {
		t.Fatalf("create role: %v", err)
	}

	setMarker(t, s, markerRef{table: "roles", column: "deleted_at_ms", id: role.ID}, v)

	got, err := s.GetRoles(t.Context(), []string{role.ID})
	if err != nil {
		t.Fatalf("get roles: %v", err)
	}

	if live := len(got) == 1; live != v.live {
		t.Fatalf("roles by IDs with the marker %s: found %d, want live = %v", v.name, len(got), v.live)
	}
}

func testMarkerWorkspace(t *testing.T, s LiveMarkerStore, v liveMarkerValue) {
	t.Helper()

	ns := newWorkspaceNamespace(t, s, "acme")
	w := createTestWorkspace(t, s, ns, "network")
	setMarker(t, s, markerRef{table: "workspaces", column: "deleted_at_ms", id: string(w.ID)}, v)

	_, err := s.GetWorkspaceByID(t.Context(), w.ID)
	wantLive(t, "workspace by ID", v, err, workspace.ErrNotFound)
}
