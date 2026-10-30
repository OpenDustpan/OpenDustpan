package accesstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// namedCollections gives every collection the stable ID col-<name>.
type namedCollections struct{}

func (namedCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (namedCollections) CollectionID(_ context.Context, col identity.Object) (string, bool, error) {
	return "col-" + col.CollectionName(), true, nil
}

// fixedCollections gives every collection one stable ID, as a port that holds
// a single generation of the collection answers.
type fixedCollections struct {
	id string
}

func (fixedCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (c fixedCollections) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return c.id, true, nil
}

// moveStore is the store that the move cases need: the grants, the roles and
// the principals, the units and the namespaces.
type moveStore interface {
	access.Store
	access.Callers
	access.Units
	namespace.Store
	principal.Store
}

// moveCases are the cases of the move check and of the collection grants that
// a deleted collection leaves behind.
func moveCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "a move keeps the escalation check of the target's grants", test: testMoveEscalation},
		{name: "a collection grant with an old stable ID gives nothing", test: testRenamedCollectionGrants},
		{name: "deleting a collection revokes its grants", test: testCollectionDeleteRevokes},
		{name: "review of a collection reads the grants on its units", test: testReviewCollectionUnits},
	}
}

// authorizerOver returns an authorizer and a service over s, with the
// collection port that port answers.
func authorizerOver(t *testing.T, s moveStore, port access.UnitCollections) (*access.Authorizer, *access.Service) {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: s, Callers: s, Collections: port, Units: s, Principals: nil,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: s, Directory: s, Namespaces: s, Authorizer: authz, Now: nil, NewID: nil, BootstrapAdmins: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	return authz, svc
}

// seedStack stores the namespace acme and the Pulumi stack web/prod, whose
// stable ID is stk-web, so that the unit resolves through the inventory.
func seedStack(t *testing.T, s access.Store) {
	t.Helper()

	st, ok := s.(interface {
		namespace.Store
		CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error
	})
	if !ok {
		t.Skip("the store keeps no stacks")
	}

	ns := newNamespace("acme")
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("stack ref: %v", err)
	}

	stk := stack.Stack{
		NamespaceID: "",
		CreatedAt:   at(0), UpdatedAt: at(0), Tags: map[string]string{}, Ref: ref, ID: "stk-web", Version: 0,
	}
	if err := st.CreateStack(t.Context(), stk, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}
}

// seedPrincipal stores the person with the subject name.
func seedPrincipal(t *testing.T, s moveStore, id, subject string) identity.Principal {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		DisabledAt: time.Time{},
	}
	if err := s.CreatePrincipal(t.Context(), p, entry()); err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}

	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                subject,
		DisplayName:            "",
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}

// testMoveEscalation runs the case of a move that gives the unit the grants of
// the target collection. A caller who may move a unit but holds less than the
// admin role on it is refused, although a direct grant of the admin role on the
// unit is refused too. A target with a role the caller holds is allowed.
func testMoveEscalation(t *testing.T, s access.Store) {
	t.Helper()

	ms, ok := s.(moveStore)
	if !ok {
		t.Skip("the store keeps no units, roles and principals")
	}

	seedStack(t, s)

	mallory := seedPrincipal(t, ms, "prn-mallory", "mallory")
	seedPrincipal(t, ms, "prn-carol", "carol")

	mustCreateRole(t, s, newRole("rol-move", "move",
		identity.ActionUnitRead, identity.ActionUnitManageAccess, identity.ActionUnitMove))
	mustCreateRole(t, s, newRole("rol-reader", "reader", identity.ActionUnitRead))

	unit := unitRef(t, "pulumi", "web/prod").WithUnitID("stk-web")
	sandbox := identity.CollectionObject("acme", "sandbox").WithCollectionID("col-sandbox")
	lab := identity.CollectionObject("acme", "lab").WithCollectionID("col-lab")

	mustCreateGrant(t, s, newGrant("grt-mallory-unit", principalSubject("prn-mallory"), "rol-move", unit))
	mustCreateGrant(t, s, newGrant("grt-carol-sandbox", principalSubject("prn-carol"), access.RoleAdmin, sandbox))
	mustCreateGrant(t, s, newGrant("grt-carol-lab", principalSubject("prn-carol"), "rol-reader", lab))

	_, svc := authorizerOver(t, ms, namedCollections{})

	_, err := svc.Grant(t.Context(), entry(), mallory, access.GrantInput{
		Object: unit, Role: access.RoleAdmin, Subject: principalSubject("prn-carol"),
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("direct grant of admin on the unit = %v, want ErrEscalation", err)
	}

	_, err = svc.CheckMove(t.Context(), mallory, unit, sandbox)
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("move into sandbox = %v, want ErrDenied wrapping ErrEscalation", err)
	}

	if _, err := svc.CheckMove(t.Context(), mallory, unit, lab); err != nil {
		t.Fatalf("move into lab = %v, want nil: the target gives only what the caller holds", err)
	}
}

// testRenamedCollectionGrants runs the case of a grant on a collection whose
// name has a new stable ID: the grant gives nothing, so a re-created collection
// does not show the grants of the old one.
func testRenamedCollectionGrants(t *testing.T, s access.Store) {
	t.Helper()

	ms, ok := s.(moveStore)
	if !ok {
		t.Skip("the store keeps no principals and roles")
	}

	seedPrincipal(t, ms, "prn-alice", "alice")
	mustCreateRole(t, s, newRole("rol-coll-read", "collection reader", identity.ActionCollectionRead))

	old := identity.CollectionObject("acme", "payments").WithCollectionID("col-old")
	mustCreateGrant(t, s, newGrant("grt-alice-old", principalSubject("prn-alice"), "rol-coll-read", old))

	alice := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "alice",
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{name: "the collection now holds another ID", id: "col-new", want: 0},
		{name: "the collection still holds the granted ID", id: "col-old", want: 1},
	} {
		authz, _ := authorizerOver(t, ms, fixedCollections{id: tc.id})

		scope, err := authz.ListObjects(t.Context(), alice, identity.ActionCollectionRead)
		if err != nil {
			t.Fatalf("%s: list objects: %v", tc.name, err)
		}

		if len(scope.Collections) != tc.want {
			t.Fatalf("%s: collections = %d, want %d", tc.name, len(scope.Collections), tc.want)
		}
	}
}

// testCollectionDeleteRevokes runs the case of a deleted collection: each grant
// on it is revoked, so a grant can neither be revoked by name nor reach a
// collection that is created under the same name later.
func testCollectionDeleteRevokes(t *testing.T, s access.Store) {
	t.Helper()

	cs, ok := s.(collection.Store)
	if !ok {
		t.Skip("the store keeps no collections")
	}

	ms, ok := s.(moveStore)
	if !ok {
		t.Skip("the store keeps no principals, roles and namespaces")
	}

	ns := newNamespace("acme")
	if err := ms.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	seedPrincipal(t, ms, "prn-dave", "dave")

	ref := collection.Ref{Namespace: "acme", Name: "payments"}
	old := collection.Collection{CreatedAt: at(0), ID: "col-old", Ref: ref}
	if err := cs.CreateCollection(t.Context(), old, ns.Fence(), scoped(entry(), "acme")); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	grant := newGrant("grt-dave-old", principalSubject("prn-dave"), access.RoleViewer,
		identity.CollectionObject("acme", "payments").WithCollectionID("col-old"))
	mustCreateGrant(t, s, grant)

	if err := cs.DeleteCollection(t.Context(), ref, ns.Fence(), scoped(entry(), "acme")); err != nil {
		t.Fatalf("delete collection: %v", err)
	}

	if _, err := s.GetGrant(t.Context(), grant.ID); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant of a deleted collection = %v, want ErrNotFound", err)
	}

	lister, ok := s.(interface {
		List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
	})
	if !ok {
		return
	}

	listing, err := lister.List(t.Context(), "acme", audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	for i := range listing.Entries {
		if listing.Entries[i].ObjectID == grant.ID {
			return
		}
	}

	t.Fatalf("no audit entry names the revoked grant %s", grant.ID)
}

// testReviewCollectionUnits runs the export of a collection: it holds the
// grants on the collection, on the units that are in it now (a deleted unit
// included), and on its containers with a role in ContainerRoles. It holds no
// grant on a unit of another collection or on no collection.
func testReviewCollectionUnits(t *testing.T, s access.Store) {
	t.Helper()

	cs, ok := s.(collection.Store)
	if !ok {
		t.Skip("the store keeps no collections")
	}

	ws, ok := s.(workspace.Store)
	if !ok {
		t.Skip("the store keeps no workspaces")
	}

	ms, ok := s.(moveStore)
	if !ok {
		t.Skip("the store keeps no principals, roles and namespaces")
	}

	rs := reviewStore(t, s)
	ns := newNamespace("acme")

	if err := ms.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	payments := collection.Collection{
		CreatedAt: at(0), ID: "col-payments", Ref: collection.Ref{Namespace: "acme", Name: "payments"},
	}
	platform := collection.Collection{
		CreatedAt: at(0), ID: "col-platform", Ref: collection.Ref{Namespace: "acme", Name: "platform"},
	}

	for _, c := range []collection.Collection{payments, platform} {
		if err := cs.CreateCollection(t.Context(), c, ns.Fence(), scoped(entry(), "acme")); err != nil {
			t.Fatalf("create collection %v: %v", c.Ref, err)
		}
	}

	units := map[string]workspace.ID{}

	for _, name := range []string{"web", "api", "data", "loose"} {
		units[name] = reviewWorkspace(t, ws, ns, name)
	}

	for name, to := range map[string]collection.Collection{"web": payments, "api": payments, "data": platform} {
		move := collection.Move{
			Unit: collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: name},
			From: "", To: to.Ref, ToID: to.ID, EndsLock: false,
		}
		if err := cs.SetUnitCollection(t.Context(), move, ns.Fence(), scoped(entry(), "acme")); err != nil {
			t.Fatalf("move %s: %v", name, err)
		}
	}

	// The membership stays with the deleted unit, so its grant is in the export.
	gone := scoped(entry(), "acme")
	gone.Action, gone.Object = "unit:manage", "opentofu:acme/api"
	ref := workspace.Ref{Namespace: "acme", Name: "api"}

	deletion := inventory.Deletion{Entry: gone, PurgeAfter: gone.At.Add(time.Hour)}
	if err := ws.DeleteWorkspace(t.Context(), ref, ns.Fence(), deletion); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	unitObj := func(name string) identity.Object {
		return mustWorkspaceObject(t, name).WithUnitID(string(units[name]))
	}

	for i, g := range []*struct {
		id   string
		role string
		obj  identity.Object
	}{
		{"grt-col", access.RoleAuditor, identity.CollectionObject("acme", "payments").WithCollectionID(payments.ID)},
		{"grt-web", access.RoleWriter, unitObj("web")},
		{"grt-api", access.RoleViewer, unitObj("api")},
		{"grt-data", access.RoleViewer, unitObj("data")},
		{"grt-loose", access.RoleViewer, unitObj("loose")},
		{"grt-other", access.RoleAuditor, identity.CollectionObject("acme", "platform").WithCollectionID(platform.ID)},
		{"grt-ns", access.RoleWriter, identity.NamespaceObject("acme")},
		{"grt-ns-viewer", access.RoleViewer, identity.NamespaceObject("acme")},
	} {
		mustCreateGrant(t, s, grantAt(i+1, g.id, principalSubject("prn-a"), g.role, g.obj))
	}

	scope := access.ReviewScope{
		CollectionID: payments.ID, Namespace: "",
		Object:         identity.CollectionObject("acme", "payments").WithCollectionID(payments.ID),
		Containers:     []string{"acme", "@server"},
		ContainerRoles: []string{access.RoleWriter},
		TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}

	review(t, rs, "collection payments", everyRow(scope), "grt-col", "grt-web", "grt-api", "grt-ns")

	// With no collection ID, the export holds no grant on a unit.
	scope.CollectionID = ""

	review(t, rs, "collection payments with no members", everyRow(scope), "grt-col", "grt-ns")
}

// reviewWorkspace makes the workspace name in ns and returns its stable ID.
func reviewWorkspace(t *testing.T, ws workspace.Store, ns namespace.Namespace, name string) workspace.ID {
	t.Helper()

	wsName, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name %q: %v", name, err)
	}

	w := workspace.Workspace{
		NamespaceID: "", CreatedAt: at(0), UpdatedAt: at(0),
		Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		ID:        workspace.ID("ws-" + name),
		Ref:       workspace.Ref{Namespace: ns.Name, Name: wsName},
		Revision:  1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := ws.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %q: %v", name, err)
	}

	return w.ID
}

// mustWorkspaceObject returns the object of the workspace name in acme.
func mustWorkspaceObject(t *testing.T, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatalf("unit object %q: %v", name, err)
	}

	return obj
}
