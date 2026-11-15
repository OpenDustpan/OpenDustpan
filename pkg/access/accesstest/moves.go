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
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// namedCollections gives every collection the stable ID col-<name>.
type namedCollections struct{}

func (namedCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (namedCollections) CollectionID(_ context.Context, col identity.Object) (string, bool, error) {
	return "col-" + col.CollectionName(), true, nil
}

// missingCollections finds no collection.
type missingCollections struct{}

func (missingCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
}

func (missingCollections) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return "", false, nil
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
		{name: "a move names a missing unit or collection with its own error", test: testMoveMissingTarget},
		{name: "a collection grant with an old stable ID gives nothing", test: testRenamedCollectionGrants},
		{name: "deleting a collection revokes its grants", test: testCollectionDeleteRevokes},
	}
}

// authorizerOver returns an authorizer and a service over s, with the
// collection port that port answers.
func authorizerOver(t *testing.T, s moveStore, port access.UnitCollections) (*access.Authorizer, *access.Service) {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: s, Callers: s, Collections: port, Units: s, Principals: s,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: s, Directory: s, Namespaces: s, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: s, Roles: s, Callers: s, Principals: s, Collections: port, Units: s,
		},
		Now: nil, NewID: nil, BootstrapAdmins: nil,
		Settings:    nil,
		LimitGroups: nil,
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

// testMoveMissingTarget runs the case of a move check for a unit or a target
// collection that does not exist. The error wraps ErrNotFound and the sentinel
// that names the missing object, so a caller can tell the two apart.
func testMoveMissingTarget(t *testing.T, s access.Store) {
	t.Helper()

	ms, ok := s.(moveStore)
	if !ok {
		t.Skip("the store keeps no units, roles and principals")
	}

	seedStack(t, s)

	caller := seedPrincipal(t, ms, "prn-mallory", "mallory")
	unit := unitRef(t, "pulumi", "web/prod")
	gone := unitRef(t, "pulumi", "web/gone")
	sandbox := identity.CollectionObject("acme", "sandbox")

	cases := []struct {
		name       string
		port       access.UnitCollections
		unit       identity.Object
		target     identity.Object
		wantMissed error
		notMissed  error
	}{
		{
			name: "missing target collection", port: missingCollections{}, unit: unit, target: sandbox,
			wantMissed: access.ErrCollectionMissing, notMissed: access.ErrUnitMissing,
		},
		{
			name: "missing unit", port: namedCollections{}, unit: gone, target: sandbox,
			wantMissed: access.ErrUnitMissing, notMissed: access.ErrCollectionMissing,
		},
	}

	for i := range cases {
		tc := &cases[i]
		_, svc := authorizerOver(t, ms, tc.port)

		_, err := svc.CheckMove(t.Context(), caller, tc.unit, tc.target)
		if !errors.Is(err, access.ErrNotFound) || !errors.Is(err, tc.wantMissed) || errors.Is(err, tc.notMissed) {
			t.Errorf("%s: CheckMove = %v, want ErrNotFound wrapping only %v", tc.name, err, tc.wantMissed)
		}
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
