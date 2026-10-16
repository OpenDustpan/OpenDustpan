// Package collectiontest is the conformance suite for collection.Store. Every
// built-in store runs it, and a custom store should too:
//
//	func TestCollectionConformance(t *testing.T) {
//		t.Parallel()
//		collectiontest.Run(t, func(t *testing.T) collectiontest.Store { return newMyStore(t) })
//	}
//
// The suite creates units through workspace.Store, because the inventory is a
// projection that the tool packages publish. A store must publish the unit in
// the same way it does for its own writes.
package collectiontest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Store is every persistence port the suite exercises.
type Store interface {
	collection.Store
	namespace.Store
	workspace.Store
	stack.Store
	inventory.Reader
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s Store)
	}{
		{name: "create and get a collection", test: testCreateAndGet},
		{name: "names differ only in case are one collection", test: testCaseInsensitiveName},
		{name: "duplicate name is refused", test: testDuplicate},
		{name: "unknown namespace is refused", test: testCreateUnknownNamespace},
		{name: "stale fence refuses a create", test: testCreateFenced},
		{name: "get of a missing collection is not found", test: testGetMissing},
		{name: "list is ordered by name within one namespace", test: testListOrdered},
		{name: "a unit with no collection has none", test: testUnitWithoutCollection},
		{name: "a unit moves between collections and reports the old one", test: testMoveUnit},
		{name: "a unit of another namespace is not found", test: testUnitNotFound},
		{name: "a collection of another namespace is not found", test: testCollectionOtherNamespace},
		{name: "delete refuses a collection that holds a unit", test: testDeleteNotEmpty},
		{name: "delete of an empty collection removes it", test: testDeleteEmpty},
		{name: "delete of a missing collection is not found", test: testDeleteMissing},
		{name: "stale fence refuses a unit move", test: testMoveFenced},
		{name: "the inventory shows the collection through the unit", test: testInventoryDoesNotBreak},
		{name: "a collection has a stable ID", test: testCollectionID},
		{name: "a failed audit write leaves no collection", test: testAuditRollsBack},
		{name: "a move from a stale source is refused", test: testMoveStaleSource},
		{name: "a move to a collection that was re-created is refused", test: testMoveRecreatedTarget},
		{name: "a Pulumi stack keeps its collection through a rename", test: testStackRename},
		{name: "a soft-deleted unit does not keep its collection", test: testDeletedUnitMembership},
		{name: "CollectionOf names the collection, and false when there is none", test: testCollectionOf},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

func testCreateAndGet(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got, err := s.GetCollection(t.Context(), c.Ref)
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}

	if got.Ref != c.Ref {
		t.Errorf("ref = %v, want %v", got.Ref, c.Ref)
	}
}

func testCaseInsensitiveName(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "Payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	other := collection.Ref{Namespace: ns.Name, Name: mustName(t, "PAYMENTS")}

	got, err := s.GetCollection(t.Context(), other)
	if err != nil {
		t.Fatalf("GetCollection with other case: %v", err)
	}

	if got.Ref.Name.String() != "Payments" {
		t.Errorf("stored name = %q, want the name as first written", got.Ref.Name)
	}

	err = createColl(t, s, newCollection(t, ns.Name, "payments"), ns.Fence())
	if !errors.Is(err, collection.ErrAlreadyExists) {
		t.Errorf("duplicate with other case: err = %v, want ErrAlreadyExists", err)
	}
}

func testDuplicate(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("first CreateCollection: %v", err)
	}

	err := createColl(t, s, c, ns.Fence())
	if !errors.Is(err, collection.ErrAlreadyExists) {
		t.Errorf("second CreateCollection: err = %v, want ErrAlreadyExists", err)
	}
}

func testCreateUnknownNamespace(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, "nobody", "payments")

	err := createColl(t, s, c, namespace.Fence{Namespace: "nobody", FailoverVersion: ns.FailoverVersion})
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Errorf("err = %v, want namespace.ErrNotFound", err)
	}
}

func testCreateFenced(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	err := createColl(t, s, newCollection(t, ns.Name, "payments"), stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("err = %v, want namespace.ErrFenced", err)
	}
}

func testGetMissing(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	_, err := s.GetCollection(t.Context(), collection.Ref{Namespace: ns.Name, Name: mustName(t, "payments")})
	if !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func testListOrdered(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "globex")

	for _, n := range []string{"web", "api", "data"} {
		if err := createColl(t, s, newCollection(t, ns.Name, n), ns.Fence()); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}

	if err := createColl(t, s, newCollection(t, other.Name, "hidden"), other.Fence()); err != nil {
		t.Fatalf("create in other namespace: %v", err)
	}

	got, err := s.ListCollections(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}

	want := []string{"api", "data", "web"}
	if len(got) != len(want) {
		t.Fatalf("got %d collections, want %d", len(got), len(want))
	}

	for i, c := range got {
		if c.Ref.Name.String() != want[i] {
			t.Errorf("collection %d = %q, want %q", i, c.Ref.Name, want[i])
		}
	}
}

func testUnitWithoutCollection(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)

	_, err := s.UnitCollection(t.Context(), unit)
	if !errors.Is(err, collection.ErrNoCollection) {
		t.Errorf("err = %v, want ErrNoCollection", err)
	}
}

func testMoveUnit(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	first := newCollection(t, ns.Name, "payments")
	second := newCollection(t, ns.Name, "platform")

	for _, c := range []collection.Collection{first, second} {
		if err := createColl(t, s, c, ns.Fence()); err != nil {
			t.Fatalf("create %v: %v", c.Ref, err)
		}
	}

	prev, err := setUnit(t, s, unit, first.Ref, ns.Fence())
	if err != nil {
		t.Fatalf("first SetUnitCollection: %v", err)
	}

	if prev != "" {
		t.Errorf("previous collection = %q, want none", prev)
	}

	prev, err = setUnit(t, s, unit, second.Ref, ns.Fence())
	if err != nil {
		t.Fatalf("move SetUnitCollection: %v", err)
	}

	if prev != first.Ref.Name {
		t.Errorf("previous collection = %q, want %q", prev, first.Ref.Name)
	}

	got, err := s.UnitCollection(t.Context(), unit)
	if err != nil {
		t.Fatalf("UnitCollection: %v", err)
	}

	if got != second.Ref {
		t.Errorf("collection = %v, want %v", got, second.Ref)
	}
}

func testUnitNotFound(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	missing := collection.UnitRef{Namespace: ns.Name, Tool: "opentofu", Name: "absent"}

	_, err := setUnit(t, s, missing, c.Ref, ns.Fence())
	if !errors.Is(err, collection.ErrUnitNotFound) {
		t.Errorf("SetUnitCollection: err = %v, want ErrUnitNotFound", err)
	}

	_, err = s.UnitCollection(t.Context(), missing)
	if !errors.Is(err, collection.ErrUnitNotFound) {
		t.Errorf("UnitCollection: err = %v, want ErrUnitNotFound", err)
	}
}

func testCollectionOtherNamespace(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	other := newNamespace(t, s, "globex")
	unit := createUnit(t, s, ns)

	foreign := newCollection(t, other.Name, "payments")
	if err := createColl(t, s, foreign, other.Fence()); err != nil {
		t.Fatalf("CreateCollection in other namespace: %v", err)
	}

	_, err := setUnit(t, s, unit, foreign.Ref, ns.Fence())
	if !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func testDeleteNotEmpty(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := setUnit(t, s, unit, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("SetUnitCollection: %v", err)
	}

	err := deleteColl(t, s, c.Ref, ns.Fence())
	if !errors.Is(err, collection.ErrNotEmpty) {
		t.Errorf("err = %v, want ErrNotEmpty", err)
	}
}

func testDeleteEmpty(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if err := deleteColl(t, s, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	_, err := s.GetCollection(t.Context(), c.Ref)
	if !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
}

func testDeleteMissing(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	ref := collection.Ref{Namespace: ns.Name, Name: mustName(t, "payments")}

	err := deleteColl(t, s, ref, ns.Fence())
	if !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func testMoveFenced(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	stale := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion + 1}

	_, err := setUnit(t, s, unit, c.Ref, stale)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("err = %v, want namespace.ErrFenced", err)
	}

	if _, err := s.UnitCollection(t.Context(), unit); !errors.Is(err, collection.ErrNoCollection) {
		t.Errorf("after fenced move: err = %v, want ErrNoCollection", err)
	}
}

// testInventoryDoesNotBreak checks that a unit still lists in the inventory
// after it moves: the projection keeps the unit and its name.
func testInventoryDoesNotBreak(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := setUnit(t, s, unit, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("SetUnitCollection: %v", err)
	}

	entries, err := s.ListUnits(t.Context(), ns.Name)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}

	if len(entries) != 1 || entries[0].Name != unit.Name {
		t.Fatalf("inventory = %+v, want the one unit %q", entries, unit.Name)
	}

	if entries[0].Collection != c.Ref.Name.String() {
		t.Fatalf("inventory collection = %q, want %q", entries[0].Collection, c.Ref.Name)
	}
}

// testCollectionOf checks the service answer of a unit: its collection, false
// for a unit with none, and false for a unit the inventory does not hold.
func testCollectionOf(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	c := newCollection(t, ns.Name, "payments")

	svc, err := collection.NewService(collection.Config{Store: s, Namespaces: s, Now: nil})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	obj := unitObject(t, unit.Tool, unit.Namespace, unit.Name)
	assertCollectionOf(t, svc, obj, "")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := setUnit(t, s, unit, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("SetUnitCollection: %v", err)
	}

	assertCollectionOf(t, svc, obj, "payments")
	assertCollectionOf(t, svc, unitObject(t, unit.Tool, unit.Namespace, "absent"), "")
}

// unitObject returns the object of a unit, or fails the test.
func unitObject(t *testing.T, tool string, ns namespace.Name, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatalf("UnitObject: %v", err)
	}

	return obj
}

// assertCollectionOf checks one CollectionOf answer with no error. An empty
// want means the unit has no collection, so the answer must be not found.
func assertCollectionOf(t *testing.T, svc *collection.Service, obj identity.Object, want string) {
	t.Helper()

	name, found, err := svc.CollectionOf(t.Context(), obj)
	if err != nil || found != (want != "") || name != want {
		t.Fatalf("CollectionOf(%s) = %q, %v, %v; want %q", obj, name, found, err, want)
	}
}

func testCollectionID(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got, err := s.GetCollection(t.Context(), c.Ref)
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}

	if !strings.HasPrefix(got.ID, "col-") || got.ID != c.ID {
		t.Errorf("ID = %q, want %q with the prefix col-", got.ID, c.ID)
	}
}

// testAuditRollsBack checks that a write whose audit entry is invalid makes no
// change: the entry and the write are one transaction (ADR 60).
func testAuditRollsBack(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	c := newCollection(t, ns.Name, "payments")

	bad := auditFor(ns.Fence())
	bad.Action = ""

	if err := s.CreateCollection(t.Context(), c, ns.Fence(), bad); err == nil {
		t.Fatal("CreateCollection with an invalid audit entry succeeded")
	}

	if _, err := s.GetCollection(t.Context(), c.Ref); !errors.Is(err, collection.ErrNotFound) {
		t.Errorf("after the failed write: err = %v, want ErrNotFound", err)
	}
}

// testMoveRecreatedTarget checks that a move names the target by the stable ID
// that the caller checked. A target deleted and created again has another ID,
// so the move is refused and the unit keeps its collection.
func testMoveRecreatedTarget(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	target := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, target, ns.Fence()); err != nil {
		t.Fatalf("create target: %v", err)
	}

	checked := collectionID(t, s, target.Ref)

	if err := deleteColl(t, s, target.Ref, ns.Fence()); err != nil {
		t.Fatalf("delete target: %v", err)
	}

	recreated := newCollection(t, ns.Name, "payments")
	if err := createColl(t, s, recreated, ns.Fence()); err != nil {
		t.Fatalf("create target again: %v", err)
	}

	err := s.SetUnitCollection(
		t.Context(),
		collection.Move{Unit: unit, From: "", To: target.Ref, ToID: checked},
		ns.Fence(),
		auditFor(ns.Fence()),
	)
	if !errors.Is(err, collection.ErrCollectionChanged) {
		t.Fatalf("move to a re-created target: err = %v, want ErrCollectionChanged", err)
	}

	if _, err := s.UnitCollection(t.Context(), unit); !errors.Is(err, collection.ErrNoCollection) {
		t.Fatalf("unit after a refused move: err = %v, want ErrNoCollection", err)
	}
}

func testMoveStaleSource(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	first := newCollection(t, ns.Name, "payments")
	second := newCollection(t, ns.Name, "platform")

	for _, c := range []collection.Collection{first, second} {
		if err := createColl(t, s, c, ns.Fence()); err != nil {
			t.Fatalf("create %v: %v", c.Ref, err)
		}
	}

	if _, err := setUnit(t, s, unit, first.Ref, ns.Fence()); err != nil {
		t.Fatalf("first move: %v", err)
	}

	// The caller read "no collection" before the first move, so its source is stale.
	err := s.SetUnitCollection(
		t.Context(),
		collection.Move{Unit: unit, From: "", To: second.Ref, ToID: collectionID(t, s, second.Ref)},
		ns.Fence(),
		auditFor(ns.Fence()),
	)
	if !errors.Is(err, collection.ErrCollectionChanged) {
		t.Fatalf("move from a stale source: err = %v, want ErrCollectionChanged", err)
	}

	got, err := s.UnitCollection(t.Context(), unit)
	if err != nil || got.Name != first.Ref.Name {
		t.Errorf("unit = %v, %v; want it to stay in %q", got, err, first.Ref.Name)
	}
}

// testStackRename checks a Pulumi stack: its collection follows the unit ID
// through a rename, and the old name no longer names a unit.
func testStackRename(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	st := newPulumiStack(ns.Name, "web", "prod")

	if err := s.CreateStack(t.Context(), st, ns.Fence()); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	unit := collection.UnitRef{Namespace: ns.Name, Tool: "pulumi", Name: "web/prod"}
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := setUnit(t, s, unit, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("SetUnit: %v", err)
	}

	if err := s.RenameStack(t.Context(), st.Ref, "prod-2", ns.Fence()); err != nil {
		t.Fatalf("RenameStack: %v", err)
	}

	renamed := collection.UnitRef{Namespace: ns.Name, Tool: "pulumi", Name: "web/prod-2"}

	got, err := s.UnitCollection(t.Context(), renamed)
	if err != nil || got.Name != c.Ref.Name {
		t.Errorf("renamed stack collection = %v, %v; want %q", got, err, c.Ref.Name)
	}

	_, err = s.UnitCollection(t.Context(), unit)
	if !errors.Is(err, collection.ErrUnitNotFound) {
		t.Errorf("old name: err = %v, want ErrUnitNotFound", err)
	}

	entries, err := s.ListUnits(t.Context(), ns.Name)
	if err != nil || len(entries) != 1 || entries[0].Collection != c.Ref.Name.String() {
		t.Errorf("inventory after rename = %+v, %v; want the collection shown", entries, err)
	}
}

// testDeletedUnitMembership checks that a soft-deleted unit does not keep its
// collection from being deleted, and that its membership goes with the
// collection (item 6 of the review).
func testDeletedUnitMembership(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	unit := createUnit(t, s, ns)
	c := newCollection(t, ns.Name, "payments")

	if err := createColl(t, s, c, ns.Fence()); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	if _, err := setUnit(t, s, unit, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("SetUnit: %v", err)
	}

	ref := workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, unitName)}
	d := auditFor(ns.Fence())
	deletion := inventory.Deletion{Entry: d, PurgeAfter: d.At.Add(time.Hour)}
	if err := s.DeleteWorkspace(t.Context(), ref, ns.Fence(), deletion); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	if err := deleteColl(t, s, c.Ref, ns.Fence()); err != nil {
		t.Fatalf("DeleteCollection with a deleted unit: %v", err)
	}
}

// createColl makes the collection with the audit entry of the fence.
func createColl(t *testing.T, s Store, c collection.Collection, fence namespace.Fence) error {
	t.Helper()

	return s.CreateCollection(t.Context(), c, fence, auditFor(fence))
}

// deleteColl deletes the collection with the audit entry of the fence.
func deleteColl(t *testing.T, s Store, ref collection.Ref, fence namespace.Fence) error {
	t.Helper()

	return s.DeleteCollection(t.Context(), ref, fence, auditFor(fence))
}

// setUnit moves the unit into to. It reads the current collection first and
// passes it as the expected source, as the service does. It returns the
// collection the unit had, or the empty name.
func setUnit(
	t *testing.T, s Store, unit collection.UnitRef, to collection.Ref, fence namespace.Fence,
) (collection.Name, error) {
	t.Helper()

	var from collection.Name

	cur, err := s.UnitCollection(t.Context(), unit)

	switch {
	case errors.Is(err, collection.ErrNoCollection):
	case err != nil:
		return "", err
	default:
		from = cur.Name
	}

	return from, s.SetUnitCollection(
		t.Context(),
		collection.Move{Unit: unit, From: from, To: to, ToID: collectionID(t, s, to)},
		fence,
		auditFor(fence),
	)
}

// collectionID returns the stable ID of the collection ref, as the caller of a
// move reads it before the move.
func collectionID(t *testing.T, s Store, ref collection.Ref) string {
	t.Helper()

	c, err := s.GetCollection(t.Context(), ref)
	if err != nil {
		t.Fatalf("get collection %v: %v", ref, err)
	}

	return c.ID
}

// auditFor returns a valid audit entry for a write in the namespace of fence.
func auditFor(fence namespace.Fence) audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    fence.Namespace,
		Subject:      "tester",
		IDPSubject:   "",
		TokenID:      "",
		Action:       "collection:test",
		Object:       fence.Namespace.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		Changes:      nil,
		ObjectID:     "",
		FenceVersion: 0,
	}
}

// newPulumiStack returns a stack of the Pulumi CLI, as storetest makes one.
func newPulumiStack(ns namespace.Name, project, name string) stack.Stack {
	return stack.Stack{
		CreatedAt: now(),
		UpdatedAt: now(),
		Tags:      map[string]string{},
		Ref:       stack.Ref{Namespace: ns, Project: stack.ProjectName(project), Stack: stack.Name(name)},
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}
}

func newNamespace(t *testing.T, s Store, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		CreatedAt:       now(),
		Name:            namespace.Name(name),
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

func newCollection(t *testing.T, ns namespace.Name, name string) collection.Collection {
	t.Helper()

	return collection.Collection{
		CreatedAt: now(),
		ID:        ident.NewID("col-"),
		Ref:       collection.Ref{Namespace: ns, Name: mustName(t, name)},
	}
}

// unitName is the name of the workspace that createUnit makes.
const unitName = "web"

// testNowMs is the fixed clock of the suite, in Unix milliseconds.
const testNowMs = 1_700_000_000_000

// createUnit makes a workspace named unitName in ns and returns its inventory
// reference.
func createUnit(t *testing.T, s Store, ns namespace.Namespace) collection.UnitRef {
	t.Helper()

	name := unitName
	ref := workspace.Ref{Namespace: ns.Name, Name: mustWorkspaceName(t, name)}
	w := workspace.Workspace{
		CreatedAt: now(),
		UpdatedAt: now(),
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        workspace.ID(ident.NewID("ws-")),
		Ref:       ref,
		Revision:  1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %q: %v", name, err)
	}

	return collection.UnitRef{Namespace: ns.Name, Tool: "opentofu", Name: name}
}

func mustName(t *testing.T, s string) collection.Name {
	t.Helper()

	n, err := collection.ParseName(s)
	if err != nil {
		t.Fatalf("parse collection name %q: %v", s, err)
	}

	return n
}

func mustWorkspaceName(t *testing.T, s string) workspace.Name {
	t.Helper()

	n, err := workspace.ParseName(s)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", s, err)
	}

	return n
}

func now() time.Time {
	return time.UnixMilli(testNowMs).UTC()
}
