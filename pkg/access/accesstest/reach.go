package accesstest

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// reachStore is what the reach cases need: the grants and roles, the
// subjects of each caller, the principals, the units, and the namespaces
// whose fences the writes check. Both SQL stores are one.
type reachStore interface {
	access.Store
	access.Callers
	access.Units
	access.PrincipalNamespaces
	access.ServicePrincipals
	namespace.Store
	principal.Store
}

// reachCases are the cases of access.ReachOf and of
// access.ServicePrincipals. A store that does not keep principals skips
// them.
func reachCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "service principals list every service", test: testListServicePrincipals},
		{name: "reach follows direct, group and act-as grants", test: testReach},
		{name: "reach follows act-as edges past the first hop", test: testReachChain},
		{name: "a namespace token grant reaches the principals of the namespace", test: testReachNamespaceTokens},
		{name: "grants answer on the objects they hold", test: testReachHierarchy},
		{name: "a grant on a deleted unit gives nothing", test: testReachDeletedUnit},
		{name: "a grant on a collection with another stable ID gives nothing", test: testReachStaleCollection},
		{name: "a disabled principal is no act-as target", test: testReachDisabled},
		{name: "reach of each operator action", test: testReachOperators},
		{name: "reach covers holds each action on each object", test: testReachCovers},
		{name: "reach stops on a cycle", test: testReachCycle},
	}
}

// reachStores returns s as a reachStore, or skips t.
//
//nolint:ireturn // The suite tests the port: a store has no other shared type.
func reachStores(t *testing.T, s access.Store) reachStore {
	t.Helper()

	rs, ok := s.(reachStore)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	return rs
}

// reachConfig reads the reach dependencies from s.
func reachConfig(s reachStore) access.ReachConfig {
	return access.ReachConfig{
		Grants: s, Roles: s, Callers: s, Principals: s,
		Collections: testCollections(), Units: s,
	}
}

// reachCollections maps the wire form of a unit to its collection. Each
// named collection has the stable ID col-<name>.
type reachCollections map[string]string

// testCollections returns the collections of the reach cases: the Pulumi
// stack web/prod is in the collection payments of the namespace acme, and
// no other unit is in a collection.
func testCollections() reachCollections {
	return reachCollections{"pulumi:acme/web/prod": "payments"}
}

func (m reachCollections) CollectionOf(_ context.Context, unit identity.Object) (string, bool, error) {
	name, ok := m[unit.String()]

	return name, ok, nil
}

func (m reachCollections) CollectionID(_ context.Context, col identity.Object) (string, bool, error) {
	for _, name := range m {
		if name == col.CollectionName() {
			return "col-" + name, true, nil
		}
	}

	return "", false, nil
}

// mustCreatePerson stores a person with the ID id and the subject.
func mustCreatePerson(t *testing.T, s reachStore, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{}, BreakGlass: false,
	}
	if err := s.CreatePrincipal(t.Context(), p, entry()); err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}
}

// mustCreateService stores a service principal of the installation.
func mustCreateService(t *testing.T, s reachStore, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{}, BreakGlass: false,
	}
	if err := s.CreatePrincipal(t.Context(), p, entry()); err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}
}

// mustCreateBot stores a service principal of the namespace ns.
func mustCreateBot(t *testing.T, s reachStore, id, subject string, ns namespace.Name) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindService, Source: principal.SourceLocal, Namespace: ns, GroupCount: 0,
		DisabledAt: time.Time{}, BreakGlass: false,
	}

	fence := fenceOf(t, s, ns)
	if err := s.CreateNamespacePrincipal(t.Context(), p, fence, scoped(entry(), ns)); err != nil {
		t.Fatalf("create principal %q in %q: %v", subject, ns, err)
	}
}

// mustCreateMemberGroup stores the group id with the principal principalID
// as its only member.
func mustCreateMemberGroup(t *testing.T, s reachStore, id, name, principalID string) access.Subject {
	t.Helper()

	ctx := t.Context()

	g := principal.Group{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Name: name, Description: "",
		Source: principal.SourceLocal, MemberCount: 0,
	}
	if err := s.CreateGroup(ctx, g, entry()); err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}

	m := principal.Membership{
		AddedAt: at(1), SuspendedAt: time.Time{}, GroupID: id, PrincipalID: principalID,
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
		Pinned: false,
	}
	if err := s.AddMember(ctx, m, entry(), nil); err != nil {
		t.Fatalf("add member to %q: %v", name, err)
	}

	return access.Subject{Kind: access.SubjectGroup, ID: id}
}

// reachOf returns the reach of the principal id.
func reachOf(t *testing.T, s reachStore, id string) access.Reach {
	t.Helper()

	r, err := access.ReachOf(t.Context(), reachConfig(s), id)
	if err != nil {
		t.Fatalf("reach of %q: %v", id, err)
	}

	return r
}

// reachHas reports whether the reach r holds action on obj, and fails t on
// a resolver error.
func reachHas(t *testing.T, r access.Reach, action identity.Action, obj identity.Object) bool {
	t.Helper()

	got, err := r.Has(t.Context(), action, obj)
	if err != nil {
		t.Fatalf("reach holds %s on %q: %v", action, obj.String(), err)
	}

	return got
}

// reachCovers reports whether the reach a covers the reach b, and fails t
// on a resolver error.
func reachCovers(t *testing.T, a, b access.Reach) bool {
	t.Helper()

	got, err := a.Covers(t.Context(), b)
	if err != nil {
		t.Fatalf("covers: %v", err)
	}

	return got
}

func testListServicePrincipals(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	ctx := t.Context()

	mustCreatePerson(t, rs, "prn-alice", "alice")
	mustCreateService(t, rs, "prn-deploy", "ci-deploy")
	mustCreateService(t, rs, "prn-other", "ci-other")
	mustCreateBot(t, rs, "prn-bot", "ci-acme", "acme")
	mustCreateService(t, rs, "prn-gone", "ci-gone")

	if err := rs.DeletePrincipal(ctx, "prn-gone", entry()); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	got, err := rs.ListServicePrincipals(ctx)
	if err != nil {
		t.Fatalf("list service principals: %v", err)
	}

	slices.Sort(got)

	if want := []string{"prn-bot", "prn-deploy", "prn-other"}; !slices.Equal(got, want) {
		t.Fatalf("service principals = %v, want %v", got, want)
	}

	cases := []struct {
		id   string
		want bool
	}{
		{id: "prn-deploy", want: true},
		{id: "prn-other", want: true},
		{id: "prn-bot", want: true},
		{id: "prn-alice", want: false},
		{id: "prn-gone", want: false},
		{id: "prn-missing", want: false},
	}

	for _, tc := range cases {
		if listed := slices.Contains(got, tc.id); listed != tc.want {
			t.Errorf("service principals list %q = %t, want %t", tc.id, listed, tc.want)
		}
	}
}

// testReach follows the grants of one principal through its groups and its
// act-as edges: an account administrator holds principal:manage-tokens on
// the installation, so its reach includes the operator grant of a service
// principal, and a member reaches through a group grant of
// principal:manage-tokens.
func testReach(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	server := identity.ServerObject()
	acme := identity.NamespaceObject("acme")

	mustCreatePerson(t, rs, "prn-account", "account")
	mustCreateService(t, rs, "prn-deploy", "ci-deploy")
	mustCreatePerson(t, rs, "prn-member", "member")
	mustCreatePerson(t, rs, "prn-outsider", "outsider")
	mustCreateService(t, rs, "prn-direct", "ci-direct")
	mustCreatePerson(t, rs, "prn-planner", "planner")
	mustCreateBot(t, rs, "prn-bot", "ci-acme", "acme")

	ops := mustCreateMemberGroup(t, rs, "grp-ops", "ops", "prn-member")

	mustCreateGrant(t, rs, newGrant(
		"grt-account", principalSubject("prn-account"), access.RoleAccountAdministrator, server,
	))
	mustCreateGrant(t, rs, newGrant(
		"grt-operator", principalSubject("prn-deploy"), access.RoleInstallationOperator, server,
	))
	mustCreateGrant(t, rs, newGrant("grt-group", ops, access.RoleTokenManager, identity.PrincipalObject("prn-direct")))
	mustCreateGrant(t, rs, newGrant("grt-direct", principalSubject("prn-direct"), access.RoleViewer, acme))
	mustCreateGrant(t, rs, newGrant("grt-bot", principalSubject("prn-planner"), access.RoleAdmin, acme))

	cases := []struct {
		action identity.Action
		obj    identity.Object
		id     string
		name   string
		want   bool
	}{
		{
			name: "an account administrator does not reach an operator action through its act-as edge",
			id:   "prn-account", action: identity.ActionServerBackup, obj: server, want: false,
		},
		{
			name: "an account administrator keeps its administration actions",
			id:   "prn-account", action: identity.ActionServerManageAccess, obj: server, want: true,
		},
		{
			name: "a member reaches through a group grant of principal:manage-tokens",
			id:   "prn-member", action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "a member holds principal:manage-tokens on the service principal",
			id:   "prn-member", action: identity.ActionPrincipalManageTokens,
			obj: identity.PrincipalObject("prn-direct"), want: true,
		},
		{
			name: "an outsider misses the grant of the group",
			id:   "prn-outsider", action: identity.ActionNamespaceRead, obj: acme, want: false,
		},
		{
			name: "a namespace grant does not reach another namespace",
			id:   "prn-direct", action: identity.ActionNamespaceRead,
			obj: identity.NamespaceObject("beta"), want: false,
		},
		{
			name: "a namespace admin does not reach an operator action",
			id:   "prn-planner", action: identity.ActionServerBackup, obj: server, want: false,
		},
		{
			name: "an unknown principal has an empty reach",
			id:   "prn-missing", action: identity.ActionUnitRead, obj: acme, want: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachHas(t, reachOf(t, rs, tc.id), tc.action, tc.obj); got != tc.want {
			t.Errorf("%s: reach of %q holds %s on %q = %t, want %t",
				tc.name, tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}
}

// testReachChain follows act-as edges past the first hop: alice manages
// the tokens of the middle service principal, which manages the tokens of
// the last one, which holds a grant on the namespace.
func testReachChain(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	acme := identity.NamespaceObject("acme")

	mustCreatePerson(t, rs, "prn-alice", "alice")
	mustCreatePerson(t, rs, "prn-outsider", "outsider")
	mustCreateService(t, rs, "prn-middle", "ci-middle")
	mustCreateService(t, rs, "prn-last", "ci-last")

	mustCreateGrant(t, rs, newGrant(
		"grt-first", principalSubject("prn-alice"), access.RoleTokenManager,
		identity.PrincipalObject("prn-middle"),
	))
	mustCreateGrant(t, rs, newGrant(
		"grt-second", principalSubject("prn-middle"), access.RoleTokenManager,
		identity.PrincipalObject("prn-last"),
	))
	mustCreateGrant(t, rs, newGrant("grt-viewer", principalSubject("prn-last"), access.RoleViewer, acme))

	cases := []struct {
		id     string
		action identity.Action
		obj    identity.Object
		name   string
		want   bool
	}{
		{
			name: "a principal reaches past the first hop", id: "prn-alice",
			action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "the middle of the chain reaches the end", id: "prn-middle",
			action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "the end reaches its own grant", id: "prn-last",
			action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "an outsider misses the chain", id: "prn-outsider",
			action: identity.ActionNamespaceRead, obj: acme, want: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachHas(t, reachOf(t, rs, tc.id), tc.action, tc.obj); got != tc.want {
			t.Errorf("%s: reach of %q holds %s on %q = %t, want %t",
				tc.name, tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}
}

// testReachNamespaceTokens follows a namespace act-as edge: a grant of a
// role with namespace:manage-principal-tokens on a namespace reaches the
// service principals of that namespace and their actions.
func testReachNamespaceTokens(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	acme := identity.NamespaceObject("acme")
	bot := identity.PrincipalObject("prn-bot")

	mustCreateRole(t, s, newRole(
		"rol-ns-tokens", "namespace token manager", identity.ActionNamespaceManagePrincipalTokens,
	))
	mustCreatePerson(t, rs, "prn-manager", "manager")
	mustCreatePerson(t, rs, "prn-outsider", "outsider")
	mustCreateBot(t, rs, "prn-bot", "ci-acme", "acme")

	mustCreateGrant(t, rs, newGrant("grt-manager", principalSubject("prn-manager"), "rol-ns-tokens", acme))
	mustCreateGrant(t, rs, newGrant("grt-bot", principalSubject("prn-bot"), access.RoleViewer, acme))

	cases := []struct {
		id     string
		action identity.Action
		obj    identity.Object
		name   string
		want   bool
	}{
		{
			name: "a token grant on a namespace reaches the principals of the namespace",
			id:   "prn-manager", action: identity.ActionPrincipalManageTokens, obj: bot, want: true,
		},
		{
			name: "a token grant on a namespace reaches through the principals",
			id:   "prn-manager", action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "a token grant on a namespace gives only what the principals hold",
			id:   "prn-manager", action: identity.ActionUnitWrite, obj: acme, want: false,
		},
		{
			name: "an outsider misses the principals of the namespace",
			id:   "prn-outsider", action: identity.ActionPrincipalManageTokens, obj: bot, want: false,
		},
		{
			name: "an outsider misses the grants of the principals",
			id:   "prn-outsider", action: identity.ActionNamespaceRead, obj: acme, want: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachHas(t, reachOf(t, rs, tc.id), tc.action, tc.obj); got != tc.want {
			t.Errorf("%s: reach of %q holds %s on %q = %t, want %t",
				tc.name, tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}
}

// testReachHierarchy checks the chain of a grant: a grant on the
// installation answers on a namespace, a collection and a unit inside it,
// a grant on a namespace answers on a collection and a unit inside it, and
// a grant on a collection answers on a unit inside it and on nothing else.
func testReachHierarchy(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	acme := identity.NamespaceObject("acme")
	beta := identity.NamespaceObject("beta")
	payments := identity.CollectionObject("acme", "payments").WithCollectionID("col-payments")

	mustCreatePerson(t, rs, "prn-site", "site")
	mustCreatePerson(t, rs, "prn-ns", "ns")
	mustCreatePerson(t, rs, "prn-coll", "coll")
	mustCreatePerson(t, rs, "prn-outsider", "outsider")

	mustCreateGrant(
		t,
		rs,
		newGrant("grt-site", principalSubject("prn-site"), access.RoleViewer, identity.ServerObject()),
	)
	mustCreateGrant(t, rs, newGrant("grt-ns", principalSubject("prn-ns"), access.RoleViewer, acme))
	mustCreateGrant(t, rs, newGrant("grt-coll", principalSubject("prn-coll"), access.RoleViewer, payments))

	web, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	api, err := identity.UnitObject("pulumi", "acme", "api/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	other, err := identity.UnitObject("pulumi", "beta", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	cases := []struct {
		id     string
		action identity.Action
		obj    identity.Object
		name   string
		want   bool
	}{
		{
			name: "a grant on the installation answers on a namespace",
			id:   "prn-site", action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			name: "a grant on the installation answers on a unit",
			id:   "prn-site", action: identity.ActionUnitRead, obj: web, want: true,
		},
		{
			name: "a grant on the installation answers on a collection",
			id:   "prn-site", action: identity.ActionCollectionRead, obj: payments, want: true,
		},
		{
			name: "a grant on a namespace answers on a unit inside it",
			id:   "prn-ns", action: identity.ActionUnitRead, obj: web, want: true,
		},
		{
			name: "a grant on a namespace answers on a collection inside it",
			id:   "prn-ns", action: identity.ActionCollectionRead, obj: payments, want: true,
		},
		{
			name: "a grant on a namespace misses a unit outside it",
			id:   "prn-ns", action: identity.ActionUnitRead, obj: other, want: false,
		},
		{
			name: "a grant on a collection answers on a unit inside it",
			id:   "prn-coll", action: identity.ActionUnitRead, obj: web, want: true,
		},
		{
			name: "a grant on a collection answers on itself",
			id:   "prn-coll", action: identity.ActionCollectionRead, obj: payments, want: true,
		},
		{
			name: "a grant on a collection misses a unit outside it",
			id:   "prn-coll", action: identity.ActionUnitRead, obj: api, want: false,
		},
		{
			name: "a grant on a collection misses another namespace",
			id:   "prn-coll", action: identity.ActionNamespaceRead, obj: beta, want: false,
		},
		{
			name: "an outsider misses the collection",
			id:   "prn-outsider", action: identity.ActionUnitRead, obj: web, want: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachHas(t, reachOf(t, rs, tc.id), tc.action, tc.obj); got != tc.want {
			t.Errorf("%s: reach of %q holds %s on %q = %t, want %t",
				tc.name, tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}
}

// testReachDeletedUnit checks that a grant on a deleted unit gives nothing:
// the grant row stays, but the unit has no stable ID, so the reach leaves
// the grant out. A grant on a unit that exists still answers.
func testReachDeletedUnit(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	ctx := t.Context()

	ws, ok := any(s).(interface {
		access.Units
		namespace.Store
		CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
		DeleteWorkspace(ctx context.Context, ref workspace.Ref, fence namespace.Fence, d inventory.Deletion) error
	})
	if !ok {
		t.Skip("the store keeps no workspaces")
	}

	mustCreatePerson(t, rs, "prn-reader", "reader")

	live := workspaceObject(t, "live", "ws-live")
	gone := workspaceObject(t, "gone", "ws-gone")

	mustCreateWorkspace(t, s, ws, "live", "ws-live")
	mustCreateWorkspace(t, s, ws, "gone", "ws-gone")
	mustCreateGrant(t, rs, newGrant("grt-live", principalSubject("prn-reader"), access.RoleViewer, live))
	mustCreateGrant(t, rs, newGrant("grt-gone", principalSubject("prn-reader"), access.RoleViewer, gone))

	name, err := workspace.ParseName("gone")
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	deleted := entry()
	deleted.Namespace, deleted.Action, deleted.Object = "acme", "unit:manage", "opentofu:acme/gone"
	deletion := inventory.Deletion{Entry: deleted, PurgeAfter: deleted.At.Add(time.Hour)}

	deleteErr := ws.DeleteWorkspace(
		ctx,
		workspace.Ref{Namespace: "acme", Name: name},
		fenceOf(t, s, "acme"),
		deletion,
	)
	if deleteErr != nil {
		t.Fatalf("delete workspace: %v", deleteErr)
	}

	if _, err := s.GetGrant(ctx, "grt-gone"); err != nil {
		t.Fatalf("grant of a deleted unit: %v, want the row to stay", err)
	}

	if got := reachHas(t, reachOf(t, rs, "prn-reader"), identity.ActionUnitRead, live); !got {
		t.Errorf("reach holds %s on the live unit = %t, want true", identity.ActionUnitRead, got)
	}

	if got := reachHas(t, reachOf(t, rs, "prn-reader"), identity.ActionUnitRead, gone); got {
		t.Errorf("reach holds %s on the deleted unit = %t, want false", identity.ActionUnitRead, got)
	}
}

// workspaceObject returns the OpenTofu unit called name in the namespace
// acme with the stable ID id.
func workspaceObject(t *testing.T, name, id string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj.WithUnitID(id)
}

// mustCreateWorkspace stores the OpenTofu workspace called name with the
// stable ID id in the namespace acme.
func mustCreateWorkspace(t *testing.T, s access.Store, ws interface {
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
}, name, id string,
) {
	t.Helper()

	parsed, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   at(0), UpdatedAt: at(0),
		Lock: workspace.Lock{LockedAt: at(0), ID: "", Holder: "", Reason: ""},
		ID:   workspace.ID(id), Ref: workspace.Ref{Namespace: "acme", Name: parsed},
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, Revision: 1,
	}

	if err := ws.CreateWorkspace(t.Context(), w, fenceOf(t, s, "acme")); err != nil {
		t.Fatalf("create workspace %q: %v", name, err)
	}
}

// testReachStaleCollection checks that a grant on a collection with another
// stable ID gives nothing: the collection was deleted and created again
// under its name, so it is a new collection. The grant row stays.
func testReachStaleCollection(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	ctx := t.Context()

	mustCreatePerson(t, rs, "prn-reader", "reader")

	old := identity.CollectionObject("acme", "payments").WithCollectionID("col-old")
	current := identity.CollectionObject("acme", "payments").WithCollectionID("col-payments")

	mustCreateGrant(t, rs, newGrant("grt-old", principalSubject("prn-reader"), access.RoleViewer, old))

	if _, err := s.GetGrant(ctx, "grt-old"); err != nil {
		t.Fatalf("grant on the old collection: %v, want the row to stay", err)
	}

	if got := reachHas(t, reachOf(t, rs, "prn-reader"), identity.ActionCollectionRead, current); got {
		t.Errorf("reach holds %s on the new collection = %t, want false", identity.ActionCollectionRead, got)
	}
}

// disabledAs answers the record rec for the principal id and s otherwise,
// so a test can disable a principal that the store cannot disable.
type disabledAs struct {
	access.PrincipalNamespaces

	id  string
	rec principal.Principal
}

func (f disabledAs) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	if id == f.id {
		return f.rec, nil
	}

	return f.PrincipalNamespaces.GetPrincipal(ctx, id)
}

// testReachDisabled checks that a disabled principal is no act-as target: a
// holder of principal:manage-tokens on it gets none of its grants. The
// disabled principal itself keeps its own grants.
func testReachDisabled(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	ctx := t.Context()
	acme := identity.NamespaceObject("acme")

	mustCreatePerson(t, rs, "prn-holder", "holder")
	mustCreateService(t, rs, "prn-deploy", "ci-deploy")

	mustCreateGrant(t, rs, newGrant(
		"grt-manage", principalSubject("prn-holder"), access.RoleTokenManager,
		identity.PrincipalObject("prn-deploy"),
	))
	mustCreateGrant(t, rs, newGrant("grt-viewer", principalSubject("prn-deploy"), access.RoleViewer, acme))

	rec, err := rs.GetPrincipal(ctx, "prn-deploy")
	if err != nil {
		t.Fatalf("get principal: %v", err)
	}

	rec.DisabledAt = at(3)

	cfg := reachConfig(rs)
	cfg.Principals = disabledAs{PrincipalNamespaces: rs, id: "prn-deploy", rec: rec}

	holder, err := access.ReachOf(ctx, cfg, "prn-holder")
	if err != nil {
		t.Fatalf("reach of the holder: %v", err)
	}

	if got := reachHas(t, holder, identity.ActionNamespaceRead, acme); got {
		t.Errorf("reach of the holder holds %s on %q = %t, want false",
			identity.ActionNamespaceRead, acme.String(), got)
	}

	enabled, err := access.ReachOf(ctx, reachConfig(rs), "prn-holder")
	if err != nil {
		t.Fatalf("reach of the holder with the principal enabled: %v", err)
	}

	if got := reachHas(t, enabled, identity.ActionNamespaceRead, acme); !got {
		t.Errorf("reach of the holder with the principal enabled holds %s on %q = %t, want true",
			identity.ActionNamespaceRead, acme.String(), got)
	}

	target, err := access.ReachOf(ctx, cfg, "prn-deploy")
	if err != nil {
		t.Fatalf("reach of the disabled principal: %v", err)
	}

	if got := reachHas(t, target, identity.ActionNamespaceRead, acme); !got {
		t.Errorf("reach of the disabled principal holds %s on %q = %t, want true",
			identity.ActionNamespaceRead, acme.String(), got)
	}
}

// testReachOperators checks the reach of each role that holds an operator
// action: the installation operator and the site administrator hold every
// operator action on the installation, and the account administrator holds
// none without an act-as edge.
func testReachOperators(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	server := identity.ServerObject()

	mustCreatePerson(t, rs, "prn-op", "operator")
	mustCreatePerson(t, rs, "prn-site", "site")
	mustCreatePerson(t, rs, "prn-account", "account")

	mustCreateGrant(t, rs, newGrant("grt-op", principalSubject("prn-op"), access.RoleInstallationOperator, server))
	mustCreateGrant(t, rs, newGrant("grt-site", principalSubject("prn-site"), access.RoleSiteAdministrator, server))
	mustCreateGrant(t, rs, newGrant(
		"grt-account", principalSubject("prn-account"), access.RoleAccountAdministrator, server,
	))

	reaches := map[string]access.Reach{
		access.RoleInstallationOperator: reachOf(t, rs, "prn-op"),
		access.RoleSiteAdministrator:    reachOf(t, rs, "prn-site"),
		access.RoleAccountAdministrator: reachOf(t, rs, "prn-account"),
	}

	operatorActions := access.Actions(access.FamilyOperator)

	cases := make([]struct {
		role   string
		action identity.Action
		want   bool
	}, 0, 3*len(operatorActions))

	for _, action := range operatorActions {
		cases = append(cases,
			struct {
				role   string
				action identity.Action
				want   bool
			}{role: access.RoleInstallationOperator, action: action, want: true},
			struct {
				role   string
				action identity.Action
				want   bool
			}{role: access.RoleSiteAdministrator, action: action, want: true},
			struct {
				role   string
				action identity.Action
				want   bool
			}{role: access.RoleAccountAdministrator, action: action, want: false},
		)
	}

	if len(cases) == 0 {
		t.Fatal("no operator action is classified")
	}

	for _, tc := range cases {
		if got := reachHas(t, reaches[tc.role], tc.action, server); got != tc.want {
			t.Errorf("reach of %q holds %s = %t, want %t", tc.role, tc.action, got, tc.want)
		}
	}
}

// testReachCovers checks the covers comparison: a reach covers another
// when it holds each action of the other on each object of the other.
func testReachCovers(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	server := identity.ServerObject()
	acme := identity.NamespaceObject("acme")

	mustCreatePerson(t, rs, "prn-admin", "admin-user")
	mustCreatePerson(t, rs, "prn-viewer", "viewer")
	mustCreatePerson(t, rs, "prn-other", "other")
	mustCreatePerson(t, rs, "prn-empty", "empty")
	mustCreateService(t, rs, "prn-deploy", "ci-deploy")
	mustCreatePerson(t, rs, "prn-account", "account")

	mustCreateGrant(t, rs, newGrant("grt-admin", principalSubject("prn-admin"), access.RoleAdmin, acme))
	mustCreateGrant(t, rs, newGrant("grt-viewer", principalSubject("prn-viewer"), access.RoleViewer, acme))
	mustCreateGrant(t, rs, newGrant("grt-other", principalSubject("prn-other"), access.RoleViewer, acme))
	mustCreateGrant(t, rs, newGrant(
		"grt-site", principalSubject("prn-deploy"), access.RoleInstallationOperator, server,
	))
	mustCreateGrant(t, rs, newGrant(
		"grt-account", principalSubject("prn-account"), access.RoleAccountAdministrator, server,
	))

	admin := reachOf(t, rs, "prn-admin")
	viewer := reachOf(t, rs, "prn-viewer")
	other := reachOf(t, rs, "prn-other")
	empty := reachOf(t, rs, "prn-empty")
	account := reachOf(t, rs, "prn-account")
	direct := reachOf(t, rs, "prn-deploy")

	cases := []struct {
		a    access.Reach
		b    access.Reach
		name string
		want bool
	}{
		{name: "an admin covers a viewer on the same namespace", a: admin, b: viewer, want: true},
		{name: "a viewer does not cover an admin", a: viewer, b: admin, want: false},
		{name: "an equal reach covers itself", a: viewer, b: other, want: true},
		{name: "every reach covers an empty reach", a: viewer, b: empty, want: true},
		{name: "an empty reach covers nothing else", a: empty, b: viewer, want: false},
		{
			name: "an account administrator does not cover an operator",
			a:    account, b: direct, want: false,
		},
		{
			name: "an operator does not cover an account administrator",
			a:    direct, b: account, want: false,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachCovers(t, tc.a, tc.b); got != tc.want {
			t.Errorf("%s: covers = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// testReachCycle proves that mutual act-as edges stop: two service
// principals that manage the tokens of each other share one reach.
func testReachCycle(t *testing.T, s access.Store) {
	t.Helper()

	rs := reachStores(t, s)
	acme := identity.NamespaceObject("acme")

	mustCreateService(t, rs, "prn-left", "ci-left")
	mustCreateService(t, rs, "prn-right", "ci-right")

	mustCreateGrant(
		t,
		rs,
		newGrant(
			"grt-left",
			principalSubject("prn-left"),
			access.RoleTokenManager,
			identity.PrincipalObject("prn-right"),
		),
	)
	mustCreateGrant(
		t,
		rs,
		newGrant(
			"grt-right",
			principalSubject("prn-right"),
			access.RoleTokenManager,
			identity.PrincipalObject("prn-left"),
		),
	)
	mustCreateGrant(t, rs, newGrant("grt-viewer", principalSubject("prn-right"), access.RoleViewer, acme))

	cases := []struct {
		id     string
		action identity.Action
		obj    identity.Object
		want   bool
	}{
		{
			id: "prn-left", action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
		{
			id: "prn-right", action: identity.ActionNamespaceRead, obj: acme, want: true,
		},
	}

	for i := range cases {
		tc := &cases[i]

		if got := reachHas(t, reachOf(t, rs, tc.id), tc.action, tc.obj); got != tc.want {
			t.Errorf("reach of %q holds %s on %q = %t, want %t",
				tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}

	left, right := reachOf(t, rs, "prn-left"), reachOf(t, rs, "prn-right")
	if !reachCovers(t, left, right) || !reachCovers(t, right, left) {
		t.Errorf("the two sides of a cycle cover each other = %t, %t; want true, true",
			reachCovers(t, left, right), reachCovers(t, right, left))
	}
}
