package accesstest

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// reachStore is what the reach cases need: the grants and roles, the
// subjects of each caller, the principals, and the namespaces whose fences
// the writes check. Both SQL stores are one.
type reachStore interface {
	access.Store
	access.Callers
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
	return access.ReachConfig{Grants: s, Roles: s, Callers: s, Principals: s, Services: s}
}

// mustCreatePerson stores a person with the ID id and the subject.
func mustCreatePerson(t *testing.T, s reachStore, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: at(0), UpdatedAt: at(0), ID: id, Subject: subject, DisplayName: "",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		DisabledAt: time.Time{},
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
		DisabledAt: time.Time{},
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
		DisabledAt: time.Time{},
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

	m := principal.Membership{AddedAt: at(1), GroupID: id, PrincipalID: principalID}
	if err := s.AddMember(ctx, m, entry()); err != nil {
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
			name: "an account administrator reaches an operator action through its act-as edge",
			id:   "prn-account", action: identity.ActionServerBackup, obj: server, want: true,
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

		if got := reachOf(t, rs, tc.id).Has(tc.action, tc.obj); got != tc.want {
			t.Errorf("%s: reach of %q holds %s on %q = %t, want %t",
				tc.name, tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
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
		if got := reaches[tc.role].Has(tc.action, server); got != tc.want {
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
			name: "an account administrator covers an operator through its act-as edge",
			a:    account, b: direct, want: true,
		},
		{
			name: "an operator does not cover an account administrator",
			a:    direct, b: account, want: false,
		},
	}

	for _, tc := range cases {
		if got := tc.a.Covers(tc.b); got != tc.want {
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

		if got := reachOf(t, rs, tc.id).Has(tc.action, tc.obj); got != tc.want {
			t.Errorf("reach of %q holds %s on %q = %t, want %t",
				tc.id, tc.action, tc.obj.String(), got, tc.want)
		}
	}

	if left, right := reachOf(t, rs, "prn-left"), reachOf(t, rs, "prn-right"); !left.Covers(right) ||
		!right.Covers(left) {
		t.Errorf("the two sides of a cycle cover each other = %t, %t; want true, true",
			left.Covers(right), right.Covers(left))
	}
}
