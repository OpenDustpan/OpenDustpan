// Package accesstest is the conformance suite for access.Store. Every
// built-in store runs it, and a custom store passed to dustpan.NewServer
// should too:
//
//	func TestAccessConformance(t *testing.T) {
//		t.Parallel()
//		accesstest.Run(t, func(t *testing.T) access.Store { return newMyStore(t) })
//	}
//
// A store that also implements namespace.Store and principal.Store runs the
// cases that cross those ports: a namespace created with its grant, and a
// deleted principal or group that loses its grants. A store that also
// implements access.ReviewStore runs the access-review cases.
package accesstest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) access.Store

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "role round trip", test: testRoleRoundTrip},
		{name: "duplicate role name is ErrAlreadyExists without regard to case", test: testDuplicateRoleName},
		{name: "duplicate role ID is ErrAlreadyExists", test: testDuplicateRoleID},
		{name: "missing role is ErrNotFound", test: testMissingRole},
		{name: "list roles orders by name without regard to case", test: testListRoles},
		{name: "update role replaces name, description and actions", test: testUpdateRole},
		{name: "rename to a taken role name is ErrAlreadyExists", test: testRenameRoleTaken},
		{name: "rename of a role to another case of its own name", test: testRenameRoleCase},
		{name: "delete of a role that a grant holds is ErrInUse", test: testDeleteRoleInUse},
		{name: "grant round trip on every object kind", test: testGrantRoundTrip},
		{name: "grant keeps its condition", test: testGrantCondition},
		{name: "duplicate grant tuple is ErrAlreadyExists", test: testDuplicateGrant},
		{name: "duplicate grant ID is ErrAlreadyExists", test: testDuplicateGrantID},
		{name: "grant on the caller object is ErrInvalid", test: testGrantOnCaller},
		{name: "missing grant is ErrNotFound", test: testMissingGrant},
		{name: "list grants on an object pages by subject and role", test: testListGrants},
		{name: "list grants names each holder and filters by holder and role", test: testListGrantHolders},
		{name: "grants on objects match subjects and objects", test: testGrantsOn},
		{name: "grants with roles match subjects and roles", test: testGrantsWithRoles},
		{name: "namespace and grant commit together", test: testNamespaceWithGrant},
		{name: "taken namespace stores no grant", test: testNamespaceWithGrantTaken},
		{name: "delete of a principal or group revokes its grants", test: testDeleteSubjectGrants},
		{name: "a revoked grant gives nothing and frees its tuple", test: testRevokedGrant},
		{name: "a deleted role frees its name and its grants give nothing", test: testDeletedRole},
		{name: "get roles reads several custom roles", test: testGetRoles},
		{name: "subjects of a caller are its principal and current groups", test: testSubjectsOf},
		{name: "a suspended membership gives no group", test: testSuspendedMembershipGivesNoRights},
		{name: "an entry that the audit log refuses changes nothing", test: testRefusedEntry},
		{name: "grants of a subject are its current grants on every object", test: testGrantsOf},
		{name: "grants of many subjects are the current grants of each, in one read", test: testGrantsOfSubjects},
		{name: "a role granted below the installation or on a principal is reported", test: testRoleGrantedBelow},
		{name: "a unit grant matches its stable ID, not its name", test: testUnitTarget},
		{name: "a grant inside a namespace checks its fence and audits there", test: testGrantFence},
	}

	for _, tc := range slices.Concat(cases, checkCases(), reviewCases(), unitCases(), moveCases(), reachCases()) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// at is a fixed, millisecond-precision instant: stores keep milliseconds.
func at(minute int) time.Time {
	return time.Date(2026, time.October, 13, 21, minute, 0, 0, time.UTC)
}

func newRole(id, name string, actions ...identity.Action) access.Role {
	return access.Role{
		CreatedAt:   at(0),
		UpdatedAt:   at(0),
		ID:          id,
		Name:        name,
		Description: "Description of " + name,
		Actions:     actions,
		BuiltIn:     false,
	}
}

func newNamespace(name namespace.Name) namespace.Namespace {
	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           at(0),
		Name:                name,
		PrimaryInstallation: "",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}
}

func principalSubject(id string) access.Subject {
	return access.Subject{Kind: access.SubjectPrincipal, ID: id}
}

// opsGroup is the group subject of the suite.
func opsGroup() access.Subject {
	return access.Subject{Kind: access.SubjectGroup, ID: "grp-ops"}
}

// entry returns a valid audit entry of the kind change in the scope
// @server, as a Service builds it.
func entry() audit.Entry {
	return audit.Entry{
		At:            at(9),
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-access",
		Object:        "grant:test",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "accesstest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// refused returns an entry that fails validation: it names no subject.
func refused() audit.Entry {
	e := entry()
	e.Subject = ""

	return e
}

// everyGrant asks for every grant on an object.
func everyGrant() access.GrantQuery {
	return access.GrantQuery{AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: access.MaxLimit + 1}
}

// grantsOf returns the grants of a list, without their holders.
func grantsOf(listed []access.ListedGrant) []access.Grant {
	out := make([]access.Grant, 0, len(listed))
	for i := range listed {
		out = append(out, listed[i].Grant)
	}

	return out
}

func newGrant(id string, subject access.Subject, role string, obj identity.Object) access.Grant {
	return access.Grant{
		CreatedAt: at(1),
		Condition: nil,
		Object:    obj,
		ID:        id,
		Role:      role,
		CreatedBy: "admin",
		Subject:   subject,
	}
}

func unit(t *testing.T, ns namespace.Name, name string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", ns, name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	// The store keeps the stable ID that the Service resolved. The suite
	// gives each unit one of its own.
	return obj.WithUnitID("stk-" + ns.String() + "-" + strings.ReplaceAll(name, "/", "-"))
}

func mustCreateRole(t *testing.T, s access.Store, r access.Role) {
	t.Helper()

	if err := s.CreateRole(t.Context(), r, entry()); err != nil {
		t.Fatalf("create role %q: %v", r.ID, err)
	}
}

func mustCreateGrant(t *testing.T, s access.Store, g access.Grant) {
	t.Helper()

	if err := createGrant(t, s, g, entry()); err != nil {
		t.Fatalf("create grant %q: %v", g.ID, err)
	}
}

func assertRole(t *testing.T, got, want access.Role) {
	t.Helper()

	if got.ID != want.ID || got.Name != want.Name || got.Description != want.Description ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		!slices.Equal(got.Actions, want.Actions) || got.BuiltIn {
		t.Fatalf("role = %+v, want %+v", got, want)
	}
}

func assertGrant(t *testing.T, got, want access.Grant) {
	t.Helper()

	sameCondition := (got.Condition == nil) == (want.Condition == nil) &&
		(got.Condition == nil || *got.Condition == *want.Condition)

	if got.ID != want.ID || got.Role != want.Role || got.CreatedBy != want.CreatedBy ||
		got.Subject != want.Subject || got.Object.String() != want.Object.String() ||
		!got.CreatedAt.Equal(want.CreatedAt) || !sameCondition {
		t.Fatalf("grant = %+v, want %+v", got, want)
	}
}

func grantIDs(grants []access.Grant) []string {
	out := make([]string, 0, len(grants))
	for i := range grants {
		out = append(out, grants[i].ID)
	}

	return out
}

func assertIDs(t *testing.T, label string, grants []access.Grant, want ...string) {
	t.Helper()

	if got := grantIDs(grants); !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func testRoleRoundTrip(t *testing.T, s access.Store) {
	t.Helper()

	want := newRole("rol-deployer", "Deployer", identity.ActionUnitRead, identity.ActionUnitWrite)
	mustCreateRole(t, s, want)

	got, err := s.GetRole(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}

	assertRole(t, got, want)

	list, err := s.ListRoles(t.Context())
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("list roles = %d roles, want 1", len(list))
	}

	assertRole(t, list[0], want)
}

func testDuplicateRoleName(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-one", "Deployer"))

	err := s.CreateRole(t.Context(), newRole("rol-two", "DEPLOYER"), entry())
	if !errors.Is(err, access.ErrAlreadyExists) {
		t.Fatalf("create role with taken name = %v, want ErrAlreadyExists", err)
	}
}

func testDuplicateRoleID(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-one", "Deployer"))

	err := s.CreateRole(t.Context(), newRole("rol-one", "Reader"), entry())
	if !errors.Is(err, access.ErrAlreadyExists) {
		t.Fatalf("create role with taken ID = %v, want ErrAlreadyExists", err)
	}
}

func testMissingRole(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()

	if _, err := s.GetRole(ctx, "rol-missing"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get missing role = %v, want ErrNotFound", err)
	}

	if err := s.UpdateRole(ctx, newRole("rol-missing", "Missing"), entry(), nil); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("update missing role = %v, want ErrNotFound", err)
	}

	if err := s.DeleteRole(ctx, "rol-missing", entry()); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("delete missing role = %v, want ErrNotFound", err)
	}
}

func testListRoles(t *testing.T, s access.Store) {
	t.Helper()

	roles := []access.Role{newRole("rol-c", "charlie"), newRole("rol-a", "Alpha"), newRole("rol-b", "bravo")}
	for i := range roles {
		mustCreateRole(t, s, roles[i])
	}

	list, err := s.ListRoles(t.Context())
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}

	names := make([]string, 0, len(list))
	for i := range list {
		names = append(names, list[i].Name)
	}

	if want := []string{"Alpha", "bravo", "charlie"}; !slices.Equal(names, want) {
		t.Fatalf("list roles = %v, want %v", names, want)
	}
}

func testUpdateRole(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-one", "Deployer", identity.ActionUnitRead))

	want := newRole("rol-one", "Releaser", identity.ActionUnitRead, identity.ActionUnitWrite)
	want.Description = "Runs releases"
	want.UpdatedAt = at(5)

	if err := s.UpdateRole(t.Context(), want, entry(), nil); err != nil {
		t.Fatalf("update role: %v", err)
	}

	got, err := s.GetRole(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}

	assertRole(t, got, want)
}

func testRenameRoleTaken(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-one", "Deployer"))
	mustCreateRole(t, s, newRole("rol-two", "Reader"))

	err := s.UpdateRole(t.Context(), newRole("rol-two", "deployer"), entry(), nil)
	if !errors.Is(err, access.ErrAlreadyExists) {
		t.Fatalf("rename to taken name = %v, want ErrAlreadyExists", err)
	}
}

func testRenameRoleCase(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-one", "Deployer"))

	if err := s.UpdateRole(t.Context(), newRole("rol-one", "DEPLOYER"), entry(), nil); err != nil {
		t.Fatalf("rename to another case of own name: %v", err)
	}
}

func testDeleteRoleInUse(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreateRole(t, s, newRole("rol-one", "Deployer", identity.ActionUnitRead))
	mustCreateGrant(t, s, newGrant("grt-one", principalSubject("prn-a"), "rol-one", identity.NamespaceObject("acme")))

	if err := s.DeleteRole(ctx, "rol-one", entry()); !errors.Is(err, access.ErrInUse) {
		t.Fatalf("delete role in use = %v, want ErrInUse", err)
	}

	if err := revokeGrant(t, s, "grt-one", "acme", entry()); err != nil {
		t.Fatalf("delete grant: %v", err)
	}

	if err := s.DeleteRole(ctx, "rol-one", entry()); err != nil {
		t.Fatalf("delete role after its grant: %v", err)
	}

	if _, err := s.GetRole(ctx, "rol-one"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get deleted role = %v, want ErrNotFound", err)
	}
}

func testGrantRoundTrip(t *testing.T, s access.Store) {
	t.Helper()

	objects := []identity.Object{
		identity.ServerObject(),
		identity.NamespaceObject("acme"),
		identity.CollectionObject("acme", "payments").WithCollectionID("col-payments"),
		unit(t, "acme", "web/prod"),
	}

	for i := range objects {
		obj := objects[i]
		want := newGrant("grt-"+string(rune('a'+i)), opsGroup(), access.RoleViewer, obj)
		mustCreateGrant(t, s, want)

		got, err := s.GetGrant(t.Context(), want.ID)
		if err != nil {
			t.Fatalf("get grant on %q: %v", obj.String(), err)
		}

		assertGrant(t, got, want)

		if !slices.Equal([]bool{got.Object.IsServer(), got.Object.IsCollection(), got.Object.IsUnit()},
			[]bool{obj.IsServer(), obj.IsCollection(), obj.IsUnit()}) {
			t.Fatalf("grant object %q lost its kind", obj.String())
		}
	}
}

func testGrantCondition(t *testing.T, s access.Store) {
	t.Helper()

	condition := "reserved"
	want := newGrant("grt-one", principalSubject("prn-a"), access.RoleViewer, identity.NamespaceObject("acme"))
	want.Condition = &condition
	mustCreateGrant(t, s, want)

	got, err := s.GetGrant(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}

	assertGrant(t, got, want)
}

func testDuplicateGrant(t *testing.T, s access.Store) {
	t.Helper()

	obj := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, newGrant("grt-one", principalSubject("prn-a"), access.RoleViewer, obj))

	err := createGrant(t, s, newGrant("grt-two", principalSubject("prn-a"), access.RoleViewer, obj), entry())
	if !errors.Is(err, access.ErrAlreadyExists) {
		t.Fatalf("create duplicate grant = %v, want ErrAlreadyExists", err)
	}

	mustCreateGrant(t, s, newGrant("grt-three", principalSubject("prn-a"), access.RoleWriter, obj))
}

func testDuplicateGrantID(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateGrant(t, s, newGrant("grt-one", principalSubject("prn-a"), access.RoleViewer, identity.ServerObject()))

	err := createGrant(
		t,
		s,
		newGrant("grt-one", principalSubject("prn-b"), access.RoleViewer, identity.ServerObject()),
		entry(),
	)
	if !errors.Is(err, access.ErrAlreadyExists) {
		t.Fatalf("create grant with taken ID = %v, want ErrAlreadyExists", err)
	}
}

func testGrantOnCaller(t *testing.T, s access.Store) {
	t.Helper()

	err := createGrant(
		t,
		s,
		newGrant("grt-one", principalSubject("prn-a"), access.RoleViewer, identity.CallerObject()),
		entry(),
	)
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("grant on the caller object = %v, want ErrInvalid", err)
	}
}

func testMissingGrant(t *testing.T, s access.Store) {
	t.Helper()

	if _, err := s.GetGrant(t.Context(), "grt-missing"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get missing grant = %v, want ErrNotFound", err)
	}

	if err := revokeGrant(t, s, "grt-missing", "", entry()); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("delete missing grant = %v, want ErrNotFound", err)
	}
}

// seedListGrants writes four grants over two objects, two subjects and two
// roles.
func seedListGrants(t *testing.T, s access.Store) {
	t.Helper()

	acme := identity.NamespaceObject("acme")
	beta := identity.NamespaceObject("beta")

	grants := []access.Grant{
		newGrant("grt-4", principalSubject("prn-b"), access.RoleWriter, beta),
		newGrant("grt-3", principalSubject("prn-a"), access.RoleWriter, acme),
		newGrant("grt-2", opsGroup(), access.RoleViewer, acme),
		newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme),
	}
	for i := range grants {
		mustCreateGrant(t, s, grants[i])
	}
}

func testListGrants(t *testing.T, s access.Store) {
	t.Helper()

	seedListGrants(t, s)

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")

	all, total, err := s.ListGrantsOn(
		ctx,
		acme,
		access.GrantQuery{AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: 10},
	)
	if err != nil {
		t.Fatalf("list grants on acme: %v", err)
	}

	assertIDs(t, "grants on acme", grantsOf(all), "grt-2", "grt-1", "grt-3")

	if total != 3 {
		t.Fatalf("total on acme = %d, want 3", total)
	}

	page, total, err := s.ListGrantsOn(ctx, acme, access.GrantQuery{
		AfterSubject: "principal:prn-a", AfterRole: access.RoleViewer, Subject: "", Role: "", Limit: 10,
	})
	if err != nil {
		t.Fatalf("list grants after a key: %v", err)
	}

	assertIDs(t, "grants after prn-a viewer", grantsOf(page), "grt-3")

	if total != 3 {
		t.Fatalf("total after a key = %d, want 3 with no regard to the key", total)
	}

	first, _, err := s.ListGrantsOn(
		ctx,
		acme,
		access.GrantQuery{AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: 1},
	)
	if err != nil {
		t.Fatalf("list one grant: %v", err)
	}

	assertIDs(t, "first grant on acme", grantsOf(first), "grt-2")
}

func testGrantsOn(t *testing.T, s access.Store) {
	t.Helper()

	seedListGrants(t, s)

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")
	beta := identity.NamespaceObject("beta")

	got, err := s.GrantsOn(ctx, []access.Subject{principalSubject("prn-a"), opsGroup()},
		[]identity.Object{acme, identity.ServerObject()})
	if err != nil {
		t.Fatalf("grants on: %v", err)
	}

	assertIDs(t, "grants on acme", got, "grt-2", "grt-1", "grt-3")

	got, err = s.GrantsOn(ctx, []access.Subject{principalSubject("prn-a")}, []identity.Object{beta})
	if err != nil {
		t.Fatalf("grants on beta: %v", err)
	}

	assertIDs(t, "grants of prn-a on beta", got)

	got, err = s.GrantsOn(ctx, nil, []identity.Object{acme})
	if err != nil || len(got) != 0 {
		t.Fatalf("grants of no subject = %v, %v, want none", got, err)
	}

	got, err = s.GrantsOn(ctx, []access.Subject{principalSubject("prn-a")}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("grants on no object = %v, %v, want none", got, err)
	}
}

func testGrantsWithRoles(t *testing.T, s access.Store) {
	t.Helper()

	seedListGrants(t, s)

	ctx := t.Context()
	subjects := []access.Subject{principalSubject("prn-a"), principalSubject("prn-b")}

	got, err := s.GrantsWithRoles(ctx, subjects, []string{access.RoleWriter})
	if err != nil {
		t.Fatalf("grants with roles: %v", err)
	}

	assertIDs(t, "writer grants", got, "grt-3", "grt-4")

	got, err = s.GrantsWithRoles(ctx, subjects, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("grants with no role = %v, %v, want none", got, err)
	}
}

func testNamespaceWithGrant(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	ns := newNamespace("acme")
	g := newGrant("grt-one", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("acme"))
	e := entry()
	e.Object = access.GrantRef(g)

	if err := s.CreateNamespaceWithGrant(ctx, ns, g, e); err != nil {
		t.Fatalf("create namespace with grant: %v", err)
	}

	got, err := s.GetGrant(ctx, g.ID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}

	assertGrant(t, got, g)

	if nss, ok := s.(namespace.Store); ok {
		if _, nsErr := nss.GetNamespace(ctx, "acme"); nsErr != nil {
			t.Fatalf("get namespace: %v", nsErr)
		}
	}

	checkCreatorGrantEntry(t, s, g, e)

	var noGrant access.Grant // A grant with no ID stores the namespace alone.
	if err = s.CreateNamespaceWithGrant(ctx, newNamespace("beta"), noGrant, entry()); err != nil {
		t.Fatalf("create namespace without grant: %v", err)
	}

	list, _, err := s.ListGrantsOn(ctx, identity.NamespaceObject("beta"), everyGrant())
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	assertIDs(t, "grants on beta", grantsOf(list))
}

// checkCreatorGrantEntry proves that the create wrote the grant of the
// creator as the first entry of the chain of the new namespace, with the
// same action and object as the entry of @server, and that the chain of the
// new namespace checks as valid. A store without the audit log skips it.
func checkCreatorGrantEntry(t *testing.T, s access.Store, g access.Grant, e audit.Entry) {
	t.Helper()

	log, ok := s.(audit.Store)
	if !ok {
		return
	}

	ctx := t.Context()
	ns := g.Object.Namespace()

	listing, err := log.List(ctx, ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit of %q: %v", ns, err)
	}

	if listing.Total != 1 {
		t.Fatalf("entries of %q = %d, want the grant of its creator", ns, listing.Total)
	}

	first := listing.Entries[len(listing.Entries)-1]
	if first.Sequence != 1 || first.Namespace != ns || first.Action != e.Action ||
		first.Object != e.Object || first.Kind != audit.KindChange {
		t.Fatalf("first entry of %q = %+v, want the admin grant %q", ns, first, e.Object)
	}

	check, err := log.Check(ctx, ns)
	if err != nil {
		t.Fatalf("check audit of %q: %v", ns, err)
	}

	if !check.Valid {
		t.Fatalf("check of %q = %+v, want a valid chain", ns, check)
	}
}

func testNamespaceWithGrantTaken(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	ns := newNamespace("acme")
	first := newGrant("grt-one", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("acme"))

	if err := s.CreateNamespaceWithGrant(ctx, ns, first, entry()); err != nil {
		t.Fatalf("create namespace with grant: %v", err)
	}

	second := newGrant(
		"grt-two",
		principalSubject("prn-b"),
		access.RoleNamespaceAdmin,
		identity.NamespaceObject("acme"),
	)

	err := s.CreateNamespaceWithGrant(ctx, ns, second, entry())
	if !errors.Is(err, namespace.ErrAlreadyExists) {
		t.Fatalf("create taken namespace = %v, want namespace.ErrAlreadyExists", err)
	}

	if _, err := s.GetGrant(ctx, second.ID); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant of the refused create = %v, want ErrNotFound", err)
	}
}

func testDeleteSubjectGrants(t *testing.T, s access.Store) {
	t.Helper()

	ps, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	ctx := t.Context()

	prn := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "prn-a", Subject: "alice", DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		DisabledAt: time.Time{},
	}
	if err := ps.CreatePrincipal(ctx, prn, entry()); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	grp := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "grp-ops", Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := ps.CreateGroup(ctx, grp, entry()); err != nil {
		t.Fatalf("create group: %v", err)
	}

	acme := identity.NamespaceObject("acme")
	mustCreateGrant(t, s, newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-2", opsGroup(), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-3", principalSubject("prn-b"), access.RoleViewer, acme))

	if err := ps.DeletePrincipal(ctx, "prn-a", entry()); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	if err := ps.DeleteGroup(ctx, "grp-ops", entry()); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	list, _, err := s.ListGrantsOn(ctx, acme, everyGrant())
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}

	assertIDs(t, "grants after the deletes", grantsOf(list), "grt-3")

	// A grant to a deleted subject is refused in the transaction of the
	// grant, so a delete that commits first wins.
	err = createGrant(t, s, newGrant("grt-4", opsGroup(), access.RoleViewer, acme), entry())
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant to a deleted group = %v, want ErrNotFound", err)
	}
}

func testRevokedGrant(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")
	g := newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme)
	mustCreateGrant(t, s, g)

	if err := revokeGrant(t, s, "grt-1", "acme", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	err := revokeGrant(t, s, "grt-1", "acme", entry())
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("revoke a revoked grant = %v, want ErrNotFound", err)
	}

	_, err = s.GetGrant(ctx, "grt-1")
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get a revoked grant = %v, want ErrNotFound", err)
	}

	on, err := s.GrantsOn(ctx, []access.Subject{principalSubject("prn-a")}, []identity.Object{acme})
	if err != nil || len(on) != 0 {
		t.Fatalf("grants on acme after the revoke = %v, %v; want none", on, err)
	}

	with, err := s.GrantsWithRoles(ctx, []access.Subject{principalSubject("prn-a")}, []string{access.RoleViewer})
	if err != nil || len(with) != 0 {
		t.Fatalf("grants with viewer after the revoke = %v, %v; want none", with, err)
	}

	list, total, err := s.ListGrantsOn(ctx, acme, everyGrant())
	if err != nil || len(list) != 0 || total != 0 {
		t.Fatalf("list after the revoke = %v, %d, %v; want none", list, total, err)
	}

	mustCreateGrant(t, s, newGrant("grt-2", principalSubject("prn-a"), access.RoleViewer, acme))
}

func testDeletedRole(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreateRole(t, s, newRole("rol-one", "Deployer", identity.ActionUnitRead))

	if err := s.DeleteRole(ctx, "rol-one", entry()); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	if _, err := s.GetRole(ctx, "rol-one"); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get a deleted role = %v, want ErrNotFound", err)
	}

	roles, err := s.GetRoles(ctx, []string{"rol-one"})
	if err != nil || len(roles) != 0 {
		t.Fatalf("get roles of a deleted role = %v, %v; want none", roles, err)
	}

	list, err := s.ListRoles(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("list roles after the delete = %v, %v; want none", list, err)
	}

	if err := s.DeleteRole(ctx, "rol-one", entry()); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("delete a deleted role = %v, want ErrNotFound", err)
	}

	mustCreateRole(t, s, newRole("rol-two", "DEPLOYER", identity.ActionUnitRead))
}

func testGetRoles(t *testing.T, s access.Store) {
	t.Helper()

	mustCreateRole(t, s, newRole("rol-a", "alpha", identity.ActionUnitRead))
	mustCreateRole(t, s, newRole("rol-b", "bravo", identity.ActionUnitWrite))
	mustCreateRole(t, s, newRole("rol-c", "charlie", identity.ActionUnitRead))

	got, err := s.GetRoles(t.Context(), []string{"rol-a", "rol-c", "rol-missing"})
	if err != nil {
		t.Fatalf("get roles: %v", err)
	}

	ids := make([]string, 0, len(got))
	for i := range got {
		ids = append(ids, got[i].ID)
	}

	slices.Sort(ids)

	if !slices.Equal(ids, []string{"rol-a", "rol-c"}) {
		t.Fatalf("get roles = %v, want rol-a and rol-c", ids)
	}

	none, err := s.GetRoles(t.Context(), nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("get no roles = %v, %v; want none", none, err)
	}
}

func testSubjectsOf(t *testing.T, s access.Store) {
	t.Helper()

	callers, isCallers := s.(access.Callers)
	ps, isPrincipals := s.(principal.Store)

	if !isCallers || !isPrincipals {
		t.Skip("the store answers no subjects of a caller")
	}

	ctx := t.Context()

	seedCaller(t, ps)

	if err := ps.RemoveMember(ctx, "grp-2", "prn-a", entry()); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	want := []string{"group:grp-1#member", "group:grp-3#member", "principal:prn-a"}
	if got := subjectNames(t, callers, "alice"); !slices.Equal(got, want) {
		t.Fatalf("subjects of alice = %v, want %v", got, want)
	}

	if got := subjectNames(t, callers, "nobody"); len(got) != 0 {
		t.Fatalf("subjects of nobody = %v, want none", got)
	}

	if err := ps.DeletePrincipal(ctx, "prn-a", entry()); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	if got := subjectNames(t, callers, "alice"); len(got) != 0 {
		t.Fatalf("subjects of a deleted principal = %v, want none", got)
	}
}

// seedCaller writes the principal alice (prn-a), a member of the groups
// grp-1, grp-2 and grp-3.
func seedCaller(t *testing.T, ps principal.Store) {
	t.Helper()

	ctx := t.Context()

	alice := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: "prn-a", Subject: "alice", DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		DisabledAt: time.Time{},
	}
	if err := ps.CreatePrincipal(ctx, alice, entry()); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	for _, id := range []string{"grp-1", "grp-2", "grp-3"} {
		g := principal.Group{
			CreatedAt: at(0), UpdatedAt: at(0), ID: id, Name: id, Description: "",
			Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
		if err := ps.CreateGroup(ctx, g, entry()); err != nil {
			t.Fatalf("create group: %v", err)
		}

		m := principal.Membership{
			AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: id, PrincipalID: "prn-a",
			Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
			Pinned: false,
		}
		if err := ps.AddMember(ctx, m, entry(), nil); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
}

// subjectNames returns the text forms of the subjects of subject, sorted.
func subjectNames(t *testing.T, callers access.Callers, subject string) []string {
	t.Helper()

	got, err := callers.SubjectsOf(t.Context(), subject)
	if err != nil {
		t.Fatalf("subjects of %s: %v", subject, err)
	}

	names := make([]string, 0, len(got))
	for _, sub := range got {
		names = append(names, sub.String())
	}

	slices.Sort(names)

	return names
}

func testRefusedEntry(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")

	err := createGrant(t, s, newGrant("grt-x", principalSubject("prn-a"), access.RoleViewer, acme), refused())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("grant with a refused entry = %v, want audit.ErrInvalidEntry", err)
	}

	_, err = s.GetGrant(ctx, "grt-x")
	if !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("grant of a refused create = %v, want ErrNotFound", err)
	}

	err = s.CreateRole(ctx, newRole("rol-x", "refused", identity.ActionUnitRead), refused())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("role with a refused entry = %v, want audit.ErrInvalidEntry", err)
	}

	mustCreateGrant(t, s, newGrant("grt-1", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateRole(t, s, newRole("rol-1", "kept", identity.ActionUnitRead))

	err = revokeGrant(t, s, "grt-1", "acme", refused())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("revoke with a refused entry = %v, want audit.ErrInvalidEntry", err)
	}

	err = s.DeleteRole(ctx, "rol-1", refused())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("delete role with a refused entry = %v, want audit.ErrInvalidEntry", err)
	}

	_, err = s.GetGrant(ctx, "grt-1")
	if err != nil {
		t.Fatalf("grant after a refused revoke: %v", err)
	}

	_, err = s.GetRole(ctx, "rol-1")
	if err != nil {
		t.Fatalf("role after a refused delete: %v", err)
	}

	err = s.CreateNamespaceWithGrant(ctx, newNamespace("gamma"), newGrant(
		"grt-ns", principalSubject("prn-a"), access.RoleNamespaceAdmin, identity.NamespaceObject("gamma"),
	), refused())
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("namespace with a refused entry = %v, want audit.ErrInvalidEntry", err)
	}

	if nss, ok := s.(namespace.Store); ok {
		if _, nsErr := nss.GetNamespace(ctx, "gamma"); !errors.Is(nsErr, namespace.ErrNotFound) {
			t.Fatalf("namespace of a refused create = %v, want namespace.ErrNotFound", nsErr)
		}
	}
}

func testGrantsOf(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")

	mustCreateGrant(t, s, newGrant("grt-1", opsGroup(), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-2", opsGroup(), access.RoleWriter, unit(t, "acme", "web/prod")))
	mustCreateGrant(t, s, newGrant("grt-3", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-4", opsGroup(), access.RoleAdmin, identity.NamespaceObject("beta")))

	if err := revokeGrant(t, s, "grt-4", "beta", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	got, err := s.GrantsOf(ctx, opsGroup())
	if err != nil {
		t.Fatalf("grants of: %v", err)
	}

	ids := grantIDs(got)
	slices.Sort(ids)

	if !slices.Equal(ids, []string{"grt-1", "grt-2"}) {
		t.Fatalf("grants of the group = %v, want grt-1 grt-2", ids)
	}

	for i := range got {
		if got[i].ID == "grt-2" && got[i].Object.UnitID() == "" {
			t.Fatalf("unit grant %+v lost its stable ID", got[i])
		}
	}
}

// testGrantsOfSubjects reads the grants of two groups and a principal with
// one call. A revoked grant and a grant of a subject that the call does not
// name are not in the result, and each grant keeps its subject.
func testGrantsOfSubjects(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	acme := identity.NamespaceObject("acme")
	devs := access.Subject{Kind: access.SubjectGroup, ID: "grp-devs"}
	other := access.Subject{Kind: access.SubjectGroup, ID: "grp-other"}

	mustCreateGrant(t, s, newGrant("grt-1", opsGroup(), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-2", opsGroup(), access.RoleWriter, unit(t, "acme", "web/prod")))
	mustCreateGrant(t, s, newGrant("grt-3", devs, access.RoleViewer, identity.ServerObject()))
	mustCreateGrant(t, s, newGrant("grt-4", devs, access.RoleAdmin, identity.NamespaceObject("beta")))
	mustCreateGrant(t, s, newGrant("grt-5", principalSubject("prn-a"), access.RoleViewer, acme))
	mustCreateGrant(t, s, newGrant("grt-6", other, access.RoleViewer, acme))

	if err := revokeGrant(t, s, "grt-4", "beta", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	got, err := s.GrantsOfSubjects(ctx, []access.Subject{opsGroup(), devs, principalSubject("prn-a")})
	if err != nil {
		t.Fatalf("grants of subjects: %v", err)
	}

	want := map[string]access.Subject{
		"grt-1": opsGroup(), "grt-2": opsGroup(), "grt-3": devs, "grt-5": principalSubject("prn-a"),
	}

	if len(got) != len(want) {
		t.Fatalf("grants of the subjects = %v, want grt-1 grt-2 grt-3 grt-5", grantIDs(got))
	}

	for i := range got {
		subject, ok := want[got[i].ID]
		if !ok || got[i].Subject != subject {
			t.Fatalf("grant %s of %s: want only grt-1 grt-2 grt-3 grt-5, each of its own subject",
				got[i].ID, got[i].Subject.String())
		}

		if got[i].ID == "grt-2" && got[i].Object.UnitID() == "" {
			t.Fatalf("unit grant %+v lost its stable ID", got[i])
		}
	}

	none, err := s.GrantsOfSubjects(ctx, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("grants of no subject = %v, %v; want none", grantIDs(none), err)
	}

	checkGrantsOfSubjectsBound(t, s)
}

// checkGrantsOfSubjectsBound reads the grants of one page of subjects, the
// bound, and checks that one subject more is refused with ErrInvalid. The
// caller stores grt-1 and grt-2 for the group opsGroup.
func checkGrantsOfSubjectsBound(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()

	page := make([]access.Subject, 0, principal.MaxLimit+1)
	for i := range principal.MaxLimit {
		page = append(page, access.Subject{Kind: access.SubjectGroup, ID: fmt.Sprintf("grp-page-%04d", i)})
	}

	page[0] = opsGroup()

	got, err := s.GrantsOfSubjects(ctx, page)
	if err != nil || len(got) != 2 {
		t.Fatalf("grants of %d subjects = %v, %v; want grt-1 grt-2", len(page), grantIDs(got), err)
	}

	page = append(page, access.Subject{Kind: access.SubjectGroup, ID: "grp-page-over"})

	over, err := s.GrantsOfSubjects(ctx, page)
	if !errors.Is(err, access.ErrInvalid) || over != nil {
		t.Fatalf("grants of %d subjects = %v, %v; want access.ErrInvalid", len(page), grantIDs(over), err)
	}
}

func testRoleGrantedBelow(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreateGrant(t, s, newGrant("grt-1", opsGroup(), "rol-a", identity.ServerObject()))
	mustCreateGrant(t, s, newGrant("grt-2", opsGroup(), "rol-b", identity.NamespaceObject("acme")))
	mustCreateGrant(t, s, newGrant("grt-3", opsGroup(), "rol-c", identity.NamespaceObject("acme")))
	mustCreateGrant(t, s, newGrant("grt-4", opsGroup(), "rol-d", identity.PrincipalObject("prn-deploy")))
	mustCreateGrant(t, s, newGrant("grt-5", opsGroup(), "rol-e", identity.PrincipalObject("prn-deploy")))

	if err := revokeGrant(t, s, "grt-3", "acme", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := revokeGrant(t, s, "grt-5", "", entry()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	below := map[string]bool{"rol-a": false, "rol-b": true, "rol-c": false, "rol-d": false, "rol-none": false}
	for role, want := range below {
		got, err := s.RoleGrantedBelowInstallation(ctx, role)
		if err != nil || got != want {
			t.Errorf("role %s granted below the installation = %t, %v; want %t", role, got, err, want)
		}
	}

	onPrincipal := map[string]bool{"rol-a": false, "rol-b": false, "rol-d": true, "rol-e": false, "rol-none": false}
	for role, want := range onPrincipal {
		got, err := s.RoleGrantedOnPrincipal(ctx, role)
		if err != nil || got != want {
			t.Errorf("role %s granted on a principal = %t, %v; want %t", role, got, err, want)
		}
	}
}

func testUnitTarget(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	web := unit(t, "acme", "web/prod")

	mustCreateGrant(t, s, newGrant("grt-1", opsGroup(), access.RoleViewer, web))

	// The same name with another stable ID is another unit.
	other := web.WithUnitID("stk-other")

	got, err := s.GrantsOn(ctx, []access.Subject{opsGroup()}, []identity.Object{other})
	if err != nil || len(got) != 0 {
		t.Fatalf("grants on a new unit with the old name = %v, %v; want none", grantIDs(got), err)
	}

	got, err = s.GrantsOn(ctx, []access.Subject{opsGroup()}, []identity.Object{web})
	if err != nil || len(got) != 1 {
		t.Fatalf("grants on the unit = %v, %v; want grt-1", grantIDs(got), err)
	}

	noID := web.WithUnitID("")

	err = createGrant(t, s, newGrant("grt-2", opsGroup(), access.RoleWriter, noID), entry())
	if !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("grant on a unit with no stable ID = %v, want ErrInvalid", err)
	}
}
