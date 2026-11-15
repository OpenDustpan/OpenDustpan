package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// racingStore commits the change race just before each grant write,
// revoke and role write: a write on another replica that commits after the checks of
// the Service read, and before the write of the Service starts.
type racingStore struct {
	*sqlite.Store

	race func()
}

func (r *racingStore) CreateGrant(
	ctx context.Context, g access.Grant, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	r.race()

	return r.Store.CreateGrant(ctx, g, fence, e, check)
}

func (r *racingStore) UpdateRole(ctx context.Context, role access.Role, e audit.Entry, check access.GrantCheck) error {
	r.race()

	return r.Store.UpdateRole(ctx, role, e, check)
}

func (r *racingStore) RevokeGrant(
	ctx context.Context, id string, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	r.race()

	return r.Store.RevokeGrant(ctx, id, fence, e, check)
}

// racingService returns a Service and an Authorizer over the store of f,
// with race committed before each grant write and role write.
func racingService(t *testing.T, f *fixture, race func()) (*access.Service, *access.Authorizer) {
	t.Helper()

	return racingServiceWith(t, f, f.units, race)
}

// racingServiceWith is racingService with the collection port.
func racingServiceWith(
	t *testing.T, f *fixture, port access.UnitCollections, race func(),
) (*access.Service, *access.Authorizer) {
	t.Helper()

	st := &racingStore{Store: f.store, race: race}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: f.store, Collections: port, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: f.store, Namespaces: f.store, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: f.store, Roles: f.store, Callers: f.store, Principals: f.store, Collections: port, Units: f.store,
		},
		Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc, authz
}

// once returns a race that runs change on its first call only.
func once(change func()) func() {
	done := false

	return func() {
		if done {
			return
		}

		done = true

		change()
	}
}

// grantID is the ID that fixture.grant gives a grant.
func grantID(subject access.Subject, role string, obj identity.Object) string {
	return "grt-" + subject.ID + "-" + role + "-" + obj.String()
}

// revokeRace returns a race that revokes the grant of role on obj to
// subject.
func revokeRace(
	t *testing.T,
	f *fixture,
	subject access.Subject,
	role string,
	obj identity.Object,
) func() {
	t.Helper()

	return once(func() {
		fence, e := storetest.GrantWrite(t, f.store, obj, time.Unix(1, 0), "grant:held")
		if err := f.store.RevokeGrant(t.Context(), grantID(subject, role, obj), fence, e, nil); err != nil {
			t.Errorf("racing revoke: %v", err)
		}
	})
}

// assertNoGrants fails t when subject holds a grant on obj.
func assertNoGrants(t *testing.T, f *fixture, subject access.Subject, obj identity.Object) {
	t.Helper()

	got, err := f.store.GrantsOn(t.Context(), []access.Subject{subject}, []identity.Object{obj})
	if err != nil {
		t.Fatalf("grants on %q: %v", obj.String(), err)
	}

	if len(got) != 0 {
		t.Fatalf("grants of %s on %q = %+v, want none", subject.String(), obj.String(), got)
	}
}

// TestGrantRefusedAfterARacingRevoke checks that a grant reads the grants
// of its granter in its own write: a revoke of the granter's grant that
// commits after the checks of the Service, and before the write, refuses
// the grant.
func TestGrantRefusedAfterARacingRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	f.grant(t, prn("bob"), access.RoleAdmin, acme)

	svc, _ := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAdmin, acme))

	_, err := svc.Grant(t.Context(), testEntry(), bob, access.GrantInput{
		Object: acme, Role: access.RoleViewer, Subject: prn("carol"),
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant after a racing revoke of the granter's grant: err = %v, want ErrEscalation", err)
	}

	assertNoGrants(t, f, prn("carol"), acme)
}

// TestGrantRefusedAfterARacingMembershipRemoval checks that the check in
// the write does not use the subjects that the request already read: a
// membership that ends after the decision of the route refuses the grant.
func TestGrantRefusedAfterARacingMembershipRemoval(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	ops := f.group(t, "ops", "bob")
	f.grant(t, ops, access.RoleAdmin, acme)

	svc, authz := racingService(t, f, once(func() {
		e := storetest.ServerChange(time.Unix(1, 0), "group:"+ops.ID)
		if err := f.store.RemoveMember(t.Context(), ops.ID, "prn-bob", e); err != nil {
			t.Errorf("racing membership removal: %v", err)
		}
	}))

	ctx := access.WithRequestCache(t.Context())
	if err := authz.Authorize(ctx, bob, identity.ActionNamespaceManageAccess, acme); err != nil {
		t.Fatalf("authorize the route: %v", err)
	}

	_, err := svc.Grant(ctx, testEntry(), bob, access.GrantInput{
		Object: acme, Role: access.RoleViewer, Subject: prn("carol"),
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant after a racing membership removal: err = %v, want ErrEscalation", err)
	}

	assertNoGrants(t, f, prn("carol"), acme)
}

// TestGrantRefusedAfterARacingRoleUpdate checks that a grant reads its role
// in its own write: a role update that commits after the checks of the
// Service and adds an action outside the data family refuses a grant of
// the role on a namespace.
func TestGrantRefusedAfterARacingRoleUpdate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	f.person(t, "carol")

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "deployer", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	svc, _ := racingService(t, f, once(func() {
		updated := role
		updated.Actions = []identity.Action{identity.ActionServerBackup, identity.ActionUnitRead}

		e := storetest.ServerChange(time.Unix(1, 0), "role:"+role.ID)
		if updateErr := f.store.UpdateRole(t.Context(), updated, e, nil); updateErr != nil {
			t.Errorf("racing role update: %v", updateErr)
		}
	}))

	_, err = svc.Grant(t.Context(), testEntry(), admin(), access.GrantInput{
		Object: acme, Role: role.ID, Subject: prn("carol"),
	})
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("grant on a namespace after a racing role update: err = %v, want ErrInvalid", err)
	}

	assertNoGrants(t, f, prn("carol"), acme)
}

// TestRoleUpdateRefusedAfterARacingGrant checks that a role update reads
// where the role is granted in its own write: a grant of the role on a
// namespace that commits after the checks of the Service refuses an
// action outside the data family.
func TestRoleUpdateRefusedAfterARacingGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	f.person(t, "carol")

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "deployer", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	svc, _ := racingService(t, f, once(func() {
		f.grant(t, prn("carol"), role.ID, acme)
	}))

	_, err = svc.UpdateRole(t.Context(), testEntry(), admin(), role.ID, access.RoleInput{
		Name:        "deployer",
		Description: "",
		Actions:     []identity.Action{identity.ActionServerBackup, identity.ActionUnitRead},
	})
	if !errors.Is(err, access.ErrInUse) {
		t.Fatalf("role update after a racing grant on a namespace: err = %v, want ErrInUse", err)
	}

	got, err := f.store.GetRole(t.Context(), role.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}

	if len(got.Actions) != 1 || got.Actions[0] != identity.ActionUnitRead {
		t.Fatalf("role actions after the refused update = %v, want only %s", got.Actions, identity.ActionUnitRead)
	}
}

// TestRoleUpdateRefusedAfterARacingRevoke checks that a role update reads
// the grants of its caller in its own write: a revoke of the caller's grant
// that commits after the checks of the Service refuses the update.
func TestRoleUpdateRefusedAfterARacingRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	server := identity.ServerObject()
	bob := f.person(t, "bob")
	f.grant(t, prn("bob"), access.RoleAccountAdministrator, server)

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "reader", Description: "", Actions: []identity.Action{identity.ActionServerReadPrincipals},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	svc, _ := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAccountAdministrator, server))

	_, err = svc.UpdateRole(t.Context(), testEntry(), bob, role.ID, access.RoleInput{
		Name: "reader", Description: "",
		Actions: []identity.Action{identity.ActionServerReadAccess, identity.ActionServerReadPrincipals},
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("role update after a racing revoke of the caller's grant: err = %v, want ErrEscalation", err)
	}
}

// TestGrantRefusedAfterARacingRevokeOfManageAccess checks that a grant
// reads the manage-access right of its granter in its own write: a revoke
// of the grant that gave the granter manage-access, committed after the
// route authorized the request, refuses the grant, even when another grant
// still gives every action of the role.
func TestGrantRefusedAfterARacingRevokeOfManageAccess(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	f.grant(t, prn("bob"), access.RoleAdmin, acme)
	f.grant(t, prn("bob"), access.RoleViewer, acme)

	svc, authz := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAdmin, acme))

	ctx := access.WithRequestCache(t.Context())
	if err := authz.Authorize(ctx, bob, identity.ActionNamespaceManageAccess, acme); err != nil {
		t.Fatalf("authorize the route: %v", err)
	}

	_, err := svc.Grant(ctx, testEntry(), bob, access.GrantInput{
		Object: acme, Role: access.RoleViewer, Subject: prn("carol"),
	})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("grant after a racing revoke of the granter's manage-access: err = %v, want ErrDenied", err)
	}

	assertNoGrants(t, f, prn("carol"), acme)
}

// TestRoleUpdateRefusedAfterARacingRevokeOfManageRoles checks that a role
// update reads the server:manage-roles right of its caller in its own
// write.
func TestRoleUpdateRefusedAfterARacingRevokeOfManageRoles(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	server := identity.ServerObject()
	bob := f.person(t, "bob")
	f.grant(t, prn("bob"), access.RoleAccountAdministrator, server)

	reader, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "lister", Description: "", Actions: []identity.Action{identity.ActionServerReadPrincipals},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.grant(t, prn("bob"), reader.ID, server)

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "reader", Description: "", Actions: []identity.Action{identity.ActionServerReadPrincipals},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	svc, _ := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAccountAdministrator, server))

	_, err = svc.UpdateRole(t.Context(), testEntry(), bob, role.ID, access.RoleInput{
		Name: "reader", Description: "updated", Actions: []identity.Action{identity.ActionServerReadPrincipals},
	})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("role update after a racing revoke of the caller's manage-roles: err = %v, want ErrDenied", err)
	}
}

// TestGrantRefusedAfterARacingDisable checks that a grant reads the
// subjects of its granter in its own write: an inactivity disable of the
// granter that commits after the route authorized the request refuses the
// grant.
func TestGrantRefusedAfterARacingDisable(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.grant(t, prn("bob"), access.RoleAdmin, acme)

	svc, _ := racingService(t, f, once(func() {
		sw := accesstoken.Sweep{Inactivity: time.Millisecond, Tokens: false, Accounts: true}
		res, err := f.store.SweepInactive(t.Context(), sw, storetest.ServerChange)
		if err != nil || len(res.Principals) == 0 {
			t.Errorf("racing disable: %d principals disabled, err %v", len(res.Principals), err)
		}
	}))

	f.person(t, "carol")

	_, err := svc.Grant(t.Context(), testEntry(), bob, access.GrantInput{
		Object: acme, Role: access.RoleViewer, Subject: prn("carol"),
	})
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant after a racing disable of the granter: err = %v, want ErrEscalation", err)
	}

	assertNoGrants(t, f, prn("carol"), acme)
}

// TestRevokeRefusedAfterARacingRevoke checks that a revoke reads the grants
// of its caller in its own write: a revoke of the caller's grant that
// commits after the checks of the Service, and before the write, refuses
// the revoke.
func TestRevokeRefusedAfterARacingRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	f.grant(t, prn("bob"), access.RoleAdmin, acme)
	f.grant(t, prn("carol"), access.RoleViewer, acme)

	svc, _ := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAdmin, acme))

	target := grantID(prn("carol"), access.RoleViewer, acme)
	if _, err := svc.RevokeByID(t.Context(), testEntry(), bob, target); !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("revoke after a racing revoke of the caller's grant: err = %v, want ErrEscalation", err)
	}

	if _, err := f.store.GetGrant(t.Context(), target); err != nil {
		t.Fatalf("grant after the refused revoke: %v", err)
	}
}

// TestRevokeRefusedAfterARacingRoleUpdate checks that a revoke reads the
// role of its grant in its own write: a role update that commits after the
// checks of the Service and adds an action that the caller does not hold
// refuses the revoke.
func TestRevokeRefusedAfterARacingRoleUpdate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	f.grant(t, prn("bob"), access.RoleViewer, acme)

	role, err := f.svc.CreateRole(t.Context(), testEntry(), admin(), access.RoleInput{
		Name: "reader", Description: "", Actions: []identity.Action{identity.ActionUnitRead},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	f.grant(t, prn("carol"), role.ID, acme)

	svc, _ := racingService(t, f, once(func() {
		updated := role
		updated.Actions = []identity.Action{identity.ActionUnitRead, identity.ActionUnitWrite}

		e := storetest.ServerChange(time.Unix(1, 0), "role:"+role.ID)
		if updateErr := f.store.UpdateRole(t.Context(), updated, e, nil); updateErr != nil {
			t.Errorf("racing role update: %v", updateErr)
		}
	}))

	target := grantID(prn("carol"), role.ID, acme)
	if _, err := svc.RevokeByID(t.Context(), testEntry(), bob, target); !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("revoke after a racing role update: err = %v, want ErrEscalation", err)
	}

	if _, err := f.store.GetGrant(t.Context(), target); err != nil {
		t.Fatalf("grant after the refused revoke: %v", err)
	}
}

// TestRevokeRefusedAfterARacingRevokeOfManageAccess checks that a revoke
// reads the manage-access right of its caller in its own write: a revoke
// of the grant that gave the caller manage-access, committed after the
// route authorized the request, refuses the revoke, even when another
// grant still gives every action of the revoked grant.
func TestRevokeRefusedAfterARacingRevokeOfManageAccess(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := identity.NamespaceObject("acme")
	bob := f.person(t, "bob")
	f.person(t, "carol")
	f.grant(t, prn("bob"), access.RoleAdmin, acme)
	f.grant(t, prn("bob"), access.RoleViewer, acme)
	f.grant(t, prn("carol"), access.RoleViewer, acme)

	svc, _ := racingService(t, f, revokeRace(t, f, prn("bob"), access.RoleAdmin, acme))

	target := grantID(prn("carol"), access.RoleViewer, acme)
	if _, err := svc.RevokeByID(t.Context(), testEntry(), bob, target); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("revoke after a racing revoke of the caller's manage-access: err = %v, want ErrDenied", err)
	}

	if _, err := f.store.GetGrant(t.Context(), target); err != nil {
		t.Fatalf("grant after the refused revoke: %v", err)
	}
}

// collectionPort returns the collection port over the store of f: the
// collection service, as a server configures it.
func collectionPort(t *testing.T, f *fixture) *collection.Service {
	t.Helper()

	svc, err := collection.NewService(collection.Config{Store: f.store, Namespaces: f.store, Now: nil})
	if err != nil {
		t.Fatalf("new collection service: %v", err)
	}

	return svc
}

// webInCollection stores the unit acme web/prod and the collections blue (ID
// col-1) and green (ID col-2), and puts the unit in blue. It returns the unit.
func webInCollection(t *testing.T, f *fixture) identity.Object {
	t.Helper()

	unit := f.unit(t, unitObject(t, "acme", "web/prod"))
	fence, e := storetest.GrantWrite(t, f.store, unit, time.Unix(0, 0), "collection:test")

	for _, c := range []collection.Collection{
		{CreatedAt: time.Unix(0, 0), ID: "col-1", Ref: collection.Ref{Namespace: "acme", Name: "blue"}},
		{CreatedAt: time.Unix(0, 0), ID: "col-2", Ref: collection.Ref{Namespace: "acme", Name: "green"}},
	} {
		if err := f.store.CreateCollection(t.Context(), c, fence, e); err != nil {
			t.Fatalf("create collection %s: %v", c.Ref.Name, err)
		}
	}

	moveWeb(t, f, "", "blue", "col-1")

	return unit
}

// moveWeb moves the unit acme web/prod from the collection from to the
// collection to, whose stable ID is toID.
func moveWeb(t *testing.T, f *fixture, from, to collection.Name, toID string) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, f.store, identity.NamespaceObject("acme"), time.Unix(1, 0), "collection:test")
	m := collection.Move{
		Unit: collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"},
		From: from, To: collection.Ref{Namespace: "acme", Name: to}, ToID: toID, EndsLock: false,
	}

	if err := f.store.SetUnitCollection(t.Context(), m, fence, e); err != nil {
		t.Errorf("move the unit to %s: %v", to, err)
	}
}

// grantOnBlue stores a grant of role on the collection blue to subject.
func grantOnBlue(t *testing.T, f *fixture, subject access.Subject, role string) {
	t.Helper()

	obj := identity.CollectionObject("acme", "blue").WithCollectionID("col-1")
	fence, e := storetest.GrantWrite(t, f.store, obj, time.Unix(0, 0), "grant:test")

	err := f.store.CreateGrant(t.Context(), access.Grant{
		CreatedAt: time.Unix(0, 0), Condition: nil, Object: obj, ID: "grt-blue-" + subject.ID,
		Role: role, CreatedBy: "admin", Subject: subject,
	}, fence, e, nil)
	if err != nil {
		t.Fatalf("grant on blue: %v", err)
	}
}

// TestGrantRefusedAfterARacingMove checks that a grant on a unit reads the
// collection of the unit in its own write: a move of the unit out of the
// collection of the granter's grant, committed after the checks of the
// Service, refuses the grant.
func TestGrantRefusedAfterARacingMove(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bob := f.person(t, "bob")
	f.person(t, "carol")
	unit := webInCollection(t, f)
	grantOnBlue(t, f, prn("bob"), access.RoleAdmin)

	svc, _ := racingServiceWith(t, f, collectionPort(t, f), once(func() { moveWeb(t, f, "blue", "green", "col-2") }))

	_, err := svc.Grant(t.Context(), testEntry(), bob, access.GrantInput{
		Object: unit, Role: access.RoleViewer, Subject: prn("carol"),
	})
	if !errors.Is(err, access.ErrUnitMoved) {
		t.Fatalf("grant on a unit after a racing move: err = %v, want ErrUnitMoved", err)
	}

	assertNoGrants(t, f, prn("carol"), unit)
}

// TestRevokeRefusedAfterARacingMove checks that a revoke of a grant on a
// unit reads the collection of the unit in its own write.
func TestRevokeRefusedAfterARacingMove(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bob := f.person(t, "bob")
	f.person(t, "carol")
	unit := webInCollection(t, f)
	grantOnBlue(t, f, prn("bob"), access.RoleAdmin)
	f.grant(t, prn("carol"), access.RoleViewer, unit)

	svc, _ := racingServiceWith(t, f, collectionPort(t, f), once(func() { moveWeb(t, f, "blue", "green", "col-2") }))

	target := grantID(prn("carol"), access.RoleViewer, unit)
	if _, err := svc.RevokeByID(t.Context(), testEntry(), bob, target); !errors.Is(err, access.ErrUnitMoved) {
		t.Fatalf("revoke on a unit after a racing move: err = %v, want ErrUnitMoved", err)
	}

	if _, err := f.store.GetGrant(t.Context(), target); err != nil {
		t.Fatalf("grant after the refused revoke: %v", err)
	}
}
