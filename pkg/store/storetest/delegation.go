package storetest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/limitgroups"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// DelegationStore is what the delegation suite wires: the principals, the
// roles and grants, the access tokens, the namespaces, the audit log and the
// gates. Both SQL stores meet it.
type DelegationStore interface {
	principal.Store
	access.Store
	access.Callers
	access.Units
	accesstoken.Store
	namespace.Store
	audit.Store
	gate.Store
}

// DelegationFactory returns a fresh, empty store for one subtest.
type DelegationFactory func(t *testing.T) DelegationStore

// RunDelegation runs the suite of the tokens of service principals over the
// services that the server wires: a holder of principal:manage-tokens on a
// service principal makes and rotates its tokens, and only a holder of
// server:manage-access gives that action. A personal token for another
// person keeps the escalation check.
func RunDelegation(t *testing.T, newStore DelegationFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, d *delegation)
		name string
	}{
		{name: "a token of a manage-tokens holder follows later grants", test: testTokenFollowsLaterGrants},
		{name: "a service token needs principal:manage-tokens", test: testServiceTokenNeedsTheGrant},
		{name: "principal:manage-tokens gives no right on a personal token", test: testNoRightOnPersonalTokens},
		{name: "a grant of manage-tokens needs server:manage-access", test: testManageTokensGrantNeedsAccess},
		{name: "a membership that gives manage-tokens needs server:manage-access", test: testMembershipNeedsAccess},
		{
			name: "a member add that can lift an access limit needs a site administrator",
			test: testMemberAddLiftingLimitNeedsSiteAdministrator,
		},
		{name: "a grant on a principal takes a service principal and principal actions", test: testPrincipalGrantRules},
		{name: "an account administrator cannot act as a site administrator", test: testNoPersonalEscalation},
		{
			name: "a grant of the token role on a principal needs the reach of the granter",
			test: testGrantOnAPrincipalNeedsTheReach,
		},
		{
			name: "a membership in a group that holds the token role needs the reach of the caller",
			test: testMembershipNeedsTheReach,
		},
		{name: "the audit entry of a grant names the token managers", test: testGrantNamesTokenManagers},
		{
			name: "the audit entry of a group grant names the token managers of its service principals",
			test: testGroupGrantNamesTokenManagers,
		},
		{
			name: "the audit entry of a large group grant names every service principal in one change",
			test: testGroupGrantLargeNamesAll,
		},
		{
			name: "the audit entry of a group grant pages past the member limit",
			test: testGroupGrantPagesMembers,
		},
		{
			name: "the audit entry of a group grant names a group that holds token managers",
			test: testGroupGrantNamesGroupHolder,
		},
		{name: "the audit entry of a membership add names the token managers", test: testMembershipNamesTokenManagers},
		{name: "the audit entry of a membership add names what the member acts as", test: testMembershipNamesActsAs},
		{name: "the export of a principal lists its token managers", test: testPrincipalExport},
		{name: "a delete of a principal ends the grants on it", test: testDeleteEndsPrincipalGrants},
		{name: "a delete of a principal audits its grants in their namespaces", test: testDeletePrincipalGrantsAudit},
		{name: "a delete of a group audits its grants in their namespaces", test: testDeleteGroupGrantsAudit},
		{name: "a principal of a namespace holds grants inside it only", test: testNamespacePrincipalGrants},
		{name: "the store checks the namespace of a subject in its write", test: testStoreChecksSubjectNamespace},
		{name: "a principal of a namespace gets no token and no group yet", test: testNamespacePrincipalLimits},
		{
			name: "the export of a principal of a namespace lists the namespace admins",
			test: testNamespacePrincipalTokenManagers,
		},
		{name: "a delete of a principal of a namespace ends its grants", test: testNamespacePrincipalDelete},
		{name: "the store refuses a change that ends the last site administrator", test: testLastSiteAdministrator},
		{name: "a change works while a second site administrator remains", test: testLastSiteAdministratorSecond},
		{name: "a site administrator through a group counts", test: testLastSiteAdministratorGroup},
		{name: "an installation with no site administrator refuses nothing", test: testNoSiteAdministratorNoRefusal},
		{name: "only a person counts as a site administrator", test: testLastSiteAdministratorCountsPersons},
		{name: "two removals of two site administrators at once leave one", test: testLastSiteAdministratorParallel},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newDelegation(t, newStore(t)))
		})
	}
}

// delegation is one store with the services of the server over it.
type delegation struct {
	st         DelegationStore
	authz      *access.Authorizer
	access     *access.Service
	tokens     *accesstoken.Service
	principals *principal.Service
}

// newDelegation wires the services over st as the server does, with the
// namespace acme.
func newDelegation(t *testing.T, st DelegationStore) *delegation {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: st, Collections: nil, Units: st, Principals: st,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	svc, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz,
		Reach: access.ReachConfig{Grants: st, Roles: st, Callers: st, Principals: st, Collections: nil, Units: st},
		Now:   nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, LimitGroups: limitgroups.New(st, st),
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil, Store: st, Guard: svc, GroupReach: svc, Namespaces: st, Tokens: svc,
			Now: nil, NewID: nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: authz,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: nil, Units: st,
		},
		Logger: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	err = st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return &delegation{st: st, authz: authz, access: svc, tokens: tokens, principals: principals}
}

// bootstrap is the bootstrap admin token: it holds every action.
func bootstrap() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                "admin",
		DisplayName:            "",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}
}

// signedIn returns the caller of p after a sign-in: no access token.
func signedIn(p principal.Principal) identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        p.Subject,
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}
}

// changeEntry returns the audit entry of a change by caller with action.
func changeEntry(caller identity.Principal, action identity.Action) audit.Entry {
	e := ServerChange(time.Time{}, "")
	e.Subject, e.Action = caller.Subject, string(action)

	return e
}

// principalOf stores a principal of kind with the subject.
func (d *delegation) principalOf(t *testing.T, subject string, kind principal.Kind) principal.Principal {
	t.Helper()

	p, err := d.principals.CreatePrincipal(t.Context(), changeEntry(bootstrap(), identity.ActionServerManagePrincipals),
		principal.Input{Subject: subject, DisplayName: "", Kind: kind})
	if err != nil {
		t.Fatalf("create principal %q: %v", subject, err)
	}

	return p
}

// grant gives role on obj to the principal p, for granter.
func (d *delegation) grant(
	t *testing.T, granter identity.Principal, p principal.Principal, role string, obj identity.Object,
) (access.Grant, error) {
	t.Helper()

	return d.access.Grant(t.Context(), changeEntry(granter, identity.ActionServerManageAccess), granter,
		access.GrantInput{Object: obj, Role: role, Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID}})
}

// mustGrant is grant that fails the test on an error.
func (d *delegation) mustGrant(
	t *testing.T, granter identity.Principal, p principal.Principal, role string, obj identity.Object,
) access.Grant {
	t.Helper()

	g, err := d.grant(t, granter, p, role, obj)
	if err != nil {
		t.Fatalf("grant %s on %q to %s: %v", role, obj.String(), p.Subject, err)
	}

	return g
}

// serviceToken makes a service token for p, for caller.
func (d *delegation) serviceToken(
	t *testing.T, caller identity.Principal, p principal.Principal,
) (accesstoken.Token, accesstoken.Secret, error) {
	t.Helper()

	return d.tokens.Create(t.Context(), caller, changeEntry(caller, identity.ActionPrincipalManageTokens),
		accesstoken.ScopeService, accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: p.ID, Name: "deploy"})
}

// testTokenFollowsLaterGrants proves the intended behavior of a token that
// a holder of principal:manage-tokens makes: it acts as the service
// principal, with the grants that the principal gets after the token, and
// the holder never needs those grants itself.
func testTokenFollowsLaterGrants(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))

	tok, secret, err := d.serviceToken(t, signedIn(ops), deploy)
	if err != nil {
		t.Fatalf("token by a manage-tokens holder: %v", err)
	}

	acme := identity.NamespaceObject("acme")

	as, err := d.tokens.AuthenticateToken(ctx, secret.Reveal())
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	before := d.authz.Authorize(ctx, as, identity.ActionNamespaceAdmin, acme)
	if !errors.Is(before, identity.ErrDenied) {
		t.Fatalf("token before the grant: err = %v, want ErrDenied", before)
	}

	d.mustGrant(t, bootstrap(), deploy, access.RoleAdmin, acme)

	if authErr := d.authz.Authorize(ctx, as, identity.ActionNamespaceAdmin, acme); authErr != nil {
		t.Fatalf("token after a later grant: %v", authErr)
	}

	if d.authz.Authorize(ctx, signedIn(ops), identity.ActionNamespaceAdmin, acme) == nil {
		t.Fatal("the holder got the grant of the principal")
	}

	rot, _, err := d.tokens.Rotate(ctx, signedIn(ops), changeEntry(signedIn(ops), identity.ActionPrincipalManageTokens),
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService})
	if err != nil {
		t.Fatalf("rotation by a manage-tokens holder: %v", err)
	}

	err = d.tokens.Revoke(ctx, signedIn(ops), changeEntry(signedIn(ops), identity.ActionPrincipalManageTokens),
		accesstoken.ScopeService, rot.New.ID)
	if err != nil {
		t.Fatalf("revoke by a manage-tokens holder: %v", err)
	}
}

// testServiceTokenNeedsTheGrant proves the refusals: a principal without
// principal:manage-tokens, and a holder on another principal.
func testServiceTokenNeedsTheGrant(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	plain := d.principalOf(t, "plain@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	other := d.principalOf(t, "ci-other", principal.KindService)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))

	if _, _, err := d.serviceToken(t, signedIn(plain), deploy); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("token without the grant: err = %v, want ErrDenied", err)
	}

	if _, _, err := d.serviceToken(t, signedIn(ops), other); !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("token for another principal: err = %v, want ErrDenied", err)
	}

	tok, _, err := d.serviceToken(t, bootstrap(), other)
	if err != nil {
		t.Fatalf("token by the bootstrap admin: %v", err)
	}

	err = d.tokens.Revoke(ctx, signedIn(ops), changeEntry(signedIn(ops), identity.ActionPrincipalManageTokens),
		accesstoken.ScopePersonal, tok.ID)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("revoke of a token of another principal: err = %v, want ErrDenied", err)
	}
}

// testNoRightOnPersonalTokens proves that principal:manage-tokens on the
// installation, which holds every principal, gives no right on the
// personal token of a person: a revoke or a rotation in the service scope
// answers ErrWrongKind, and the token stays active.
func testNoRightOnPersonalTokens(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	root := d.principalOf(t, "root@example.com", principal.KindPerson)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.ServerObject())
	d.mustGrant(t, bootstrap(), root, access.RoleSiteAdministrator, identity.ServerObject())

	tok, secret, err := d.tokens.Create(ctx, signedIn(root), changeEntry(signedIn(root), identity.ActionTokenManageOwn),
		accesstoken.ScopeOwn, accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "", Name: "laptop"})
	if err != nil {
		t.Fatalf("personal token: %v", err)
	}

	entry := changeEntry(signedIn(ops), identity.ActionPrincipalManageTokens)

	revokeErr := d.tokens.Revoke(ctx, signedIn(ops), entry, accesstoken.ScopeService, tok.ID)
	if !errors.Is(revokeErr, accesstoken.ErrWrongKind) {
		t.Fatalf("revoke of a personal token by a manage-tokens holder: err = %v, want ErrWrongKind", revokeErr)
	}

	_, _, rotErr := d.tokens.Rotate(ctx, signedIn(ops), entry,
		accesstoken.RotateInput{OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService})
	if !errors.Is(rotErr, accesstoken.ErrWrongKind) {
		t.Fatalf("rotation of a personal token by a manage-tokens holder: err = %v, want ErrWrongKind", rotErr)
	}

	if _, err := d.tokens.AuthenticateToken(ctx, secret.Reveal()); err != nil {
		t.Fatalf("the personal token no longer signs in: %v", err)
	}
}

// testManageTokensGrantNeedsAccess proves that a grant of a role with
// principal:manage-tokens needs server:manage-access on the installation,
// also for a granter that holds principal:manage-tokens everywhere.
func testManageTokensGrantNeedsAccess(t *testing.T, d *delegation) {
	t.Helper()

	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	target := identity.PrincipalObject(deploy.ID)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.ServerObject())

	_, err := d.grant(t, signedIn(ops), alice, access.RoleTokenManager, target)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant of manage-tokens without server:manage-access: err = %v, want ErrEscalation", err)
	}

	_, err = d.grant(t, signedIn(ops), alice, access.RoleTokenManager, identity.ServerObject())
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant of manage-tokens on the installation: err = %v, want ErrEscalation", err)
	}

	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, signedIn(admin), alice, access.RoleTokenManager, target)
}

// testMembershipNeedsAccess proves that a membership is a grant by proxy:
// to add a member to a group that holds token-manager on a service
// principal needs server:manage-access too, also for a caller that manages
// the groups and holds principal:manage-tokens everywhere.
func testMembershipNeedsAccess(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	grouper := d.principalOf(t, "grouper@example.com", principal.KindPerson)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	role, err := d.access.CreateRole(ctx, changeEntry(bootstrap(), identity.ActionServerManageRoles), bootstrap(),
		access.RoleInput{Name: "grouper", Description: "", Actions: []identity.Action{
			identity.ActionServerManageGroups, identity.ActionPrincipalManageTokens,
		}})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	d.mustGrant(t, bootstrap(), grouper, role.ID, identity.ServerObject())

	group, err := d.principals.CreateGroup(ctx, changeEntry(bootstrap(), identity.ActionServerManageGroups),
		principal.GroupInput{Name: "deployers", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	_, err = d.access.Grant(ctx, changeEntry(bootstrap(), identity.ActionServerManageAccess), bootstrap(),
		access.GrantInput{
			Object: identity.PrincipalObject(deploy.ID), Role: access.RoleTokenManager,
			Subject: access.Subject{Kind: access.SubjectGroup, ID: group.ID},
		})
	if err != nil {
		t.Fatalf("grant to the group: %v", err)
	}

	_, err = d.principals.AddMember(ctx, changeEntry(signedIn(grouper), identity.ActionServerManageGroups),
		signedIn(grouper), group.ID, alice.ID)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("membership that gives manage-tokens: err = %v, want ErrEscalation", err)
	}
}

// testMemberAddLiftingLimitNeedsSiteAdministrator proves that an add of a
// member to a group that an access limit names needs a site administrator,
// also when the caller holds server:manage-groups and every grant of the
// group, and that a caller with the site-administrator role on the
// installation passes.
func testMemberAddLiftingLimitNeedsSiteAdministrator(t *testing.T, d *delegation) {
	t.Helper()

	role, err := d.access.CreateRole(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageRoles),
		bootstrap(), access.RoleInput{
			Name: "grouper", Description: "",
			Actions: []identity.Action{identity.ActionServerManageGroups},
		})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	cases := []struct {
		name   string
		group  string
		limit  string
		setup  func(t *testing.T, group principal.Group) identity.Principal
		want   error
		member int64
	}{
		{
			name: "a caller who is not a site administrator is refused", group: "readers-one", limit: "lim-one",
			setup: func(t *testing.T, _ principal.Group) identity.Principal {
				t.Helper()

				grouper := d.principalOf(t, "grouper@example.com", principal.KindPerson)
				d.mustGrant(t, bootstrap(), grouper, access.RoleViewer, identity.NamespaceObject("acme"))
				d.mustGrant(t, bootstrap(), grouper, role.ID, identity.ServerObject())

				return signedIn(grouper)
			},
			want:   access.ErrEscalation,
			member: 0,
		},
		{
			name: "a site administrator by a stored grant passes", group: "readers-two", limit: "lim-two",
			setup: func(t *testing.T, _ principal.Group) identity.Principal {
				t.Helper()

				site := d.principalOf(t, "site@example.com", principal.KindPerson)
				d.mustGrant(t, bootstrap(), site, access.RoleSiteAdministrator, identity.ServerObject())

				return signedIn(site)
			},
			want:   nil,
			member: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := d.groupOf(t, tc.group)
			d.grantToGroup(t, group, identity.NamespaceObject("acme"))
			d.putLimit(t, gate.AccessLimit{
				Scope: gate.NamespaceScope("acme"), ID: gate.ID(tc.limit), Version: 0,
				Policy: fmt.Sprintf(
					`forbid (principal, action, resource) unless { principal in OpenDustpan::Group::%q };`,
					tc.group,
				),
			})

			caller := tc.setup(t, group)
			added := d.principalOf(t, tc.group+"-member@example.com", principal.KindPerson)

			_, err := d.principals.AddMember(t.Context(), changeEntry(caller, identity.ActionServerManageGroups),
				caller, group.ID, added.ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("add to group %q: err = %v, want %v", tc.group, err, tc.want)
			}

			page, err := d.principals.ListMembers(t.Context(), group.ID,
				principal.PageRequest{Prefix: "", Cursor: "", Limit: 0})
			if err != nil {
				t.Fatalf("list members of group %q: %v", tc.group, err)
			}

			if page.Total != tc.member {
				t.Fatalf("members of group %q = %d, want %d", tc.group, page.Total, tc.member)
			}
		})
	}
}

// putLimit stores an access limit of the namespace acme straight in the
// store, as a restore writes it. The store does not parse the policy.
func (d *delegation) putLimit(t *testing.T, limit gate.AccessLimit) {
	t.Helper()

	e := ServerChange(time.Unix(0, 0), "limit:"+string(limit.ID))
	e.Namespace = "acme"

	_, err := d.st.AccessLimits().Put(t.Context(), limit, 0,
		namespace.Fence{Namespace: "acme", FailoverVersion: 1}, e)
	if err != nil {
		t.Fatalf("put access limit %s: %v", limit.ID, err)
	}
}

// testPrincipalGrantRules proves that a grant on a principal names a
// service principal and a role with principal actions only.
func testPrincipalGrantRules(t *testing.T, d *delegation) {
	t.Helper()

	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)
	bob := d.principalOf(t, "bob@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	cases := []struct {
		obj  identity.Object
		role string
	}{
		{identity.PrincipalObject(bob.ID), access.RoleTokenManager},
		{identity.PrincipalObject(deploy.ID), access.RoleViewer},
		{identity.PrincipalObject(deploy.ID), access.RoleAccountAdministrator},
		{identity.NamespaceObject("acme"), access.RoleTokenManager},
	}

	for i := range cases {
		tc := &cases[i]
		if _, err := d.grant(t, bootstrap(), alice, tc.role, tc.obj); !errors.Is(err, access.ErrInvalid) {
			t.Errorf("grant of %s on %q: err = %v, want ErrInvalid", tc.role, tc.obj.String(), err)
		}
	}

	_, err := d.grant(t, bootstrap(), alice, access.RoleTokenManager, identity.PrincipalObject("prn-none"))
	if !errors.Is(err, principal.ErrNotFound) {
		t.Errorf("grant on a principal that does not exist: err = %v, want principal.ErrNotFound", err)
	}
}

// testNoPersonalEscalation proves that no administrator makes a personal
// token for another person: an account administrator cannot make one for a
// site administrator, who holds more. It cannot make a service token for a
// service principal that holds more than its own reach either, and it can
// make one for a service principal that holds less.
func testNoPersonalEscalation(t *testing.T, d *delegation) {
	t.Helper()

	account := d.principalOf(t, "account@example.com", principal.KindPerson)
	site := d.principalOf(t, "site@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	viewer := d.principalOf(t, "ci-viewer", principal.KindService)

	d.mustGrant(t, bootstrap(), account, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), site, access.RoleSiteAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), deploy, access.RoleSiteAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), viewer, access.RoleViewer, identity.NamespaceObject("acme"))

	_, _, err := d.tokens.Create(t.Context(), signedIn(account),
		changeEntry(signedIn(account), identity.ActionServerManageTokens), accesstoken.ScopePersonal,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: site.ID, Name: "laptop"})
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, accesstoken.ErrNotOwnToken) {
		t.Fatalf("personal token for a site administrator: err = %v, want ErrDenied and ErrNotOwnToken", err)
	}

	_, _, reachErr := d.serviceToken(t, signedIn(account), deploy)
	if !errors.Is(reachErr, identity.ErrDenied) || !errors.Is(reachErr, accesstoken.ErrReachExceeded) {
		t.Fatalf("service token for a site administrator principal: err = %v, want ErrDenied and ErrReachExceeded",
			reachErr)
	}

	if _, _, viewerErr := d.serviceToken(t, signedIn(account), viewer); viewerErr != nil {
		t.Fatalf("service token for a viewer service principal: %v", viewerErr)
	}
}

// testGrantOnAPrincipalNeedsTheReach proves the reach rule of a grant of
// the token role on a service principal: an account administrator holds no
// operator action, so it does not hold the reach of an operator principal,
// and the grant is refused.
func testGrantOnAPrincipalNeedsTheReach(t *testing.T, d *delegation) {
	t.Helper()

	account := d.principalOf(t, "account@example.com", principal.KindPerson)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), account, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), deploy, access.RoleInstallationOperator, identity.ServerObject())

	_, err := d.grant(t, signedIn(account), alice, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant of the token role on an operator principal: err = %v, want ErrEscalation", err)
	}
}

// testMembershipNeedsTheReach proves the reach rule of a membership add: a
// group that holds the token role on a service principal gives each member
// the right to act as it, so an account administrator does not hold the
// reach of an operator principal and the add is refused.
func testMembershipNeedsTheReach(t *testing.T, d *delegation) {
	t.Helper()

	account := d.principalOf(t, "account@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), account, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), deploy, access.RoleInstallationOperator, identity.ServerObject())

	group, err := d.principals.CreateGroup(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageGroups),
		principal.GroupInput{Name: "deployers", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	_, err = d.access.Grant(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageAccess), bootstrap(),
		access.GrantInput{
			Object: identity.PrincipalObject(deploy.ID), Role: access.RoleTokenManager,
			Subject: access.Subject{Kind: access.SubjectGroup, ID: group.ID},
		})
	if err != nil {
		t.Fatalf("grant to the group: %v", err)
	}

	_, err = d.principals.AddMember(t.Context(), changeEntry(signedIn(account), identity.ActionServerManageGroups),
		signedIn(account), group.ID, account.ID)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("membership in a group that holds the token role on an operator principal: err = %v, "+
			"want ErrEscalation", err)
	}
}

// testGrantNamesTokenManagers proves that the audit entry of a grant to a
// service principal names, sorted, each holder of principal:manage-tokens
// on it, and that a grant to a person names none.
func testGrantNamesTokenManagers(t *testing.T, d *delegation) {
	t.Helper()

	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	g := d.mustGrant(t, bootstrap(), deploy, access.RoleViewer, identity.NamespaceObject("acme"))

	holders := []string{"admin", "principal:" + admin.ID, "principal:" + ops.ID}
	slices.Sort(holders)

	want := strings.Join(holders, ", ")
	if got := d.tokenManagersOf(t, access.GrantRef(g)); got != want {
		t.Fatalf("token managers = %q, want %q", got, want)
	}

	p := d.mustGrant(t, bootstrap(), ops, access.RoleViewer, identity.NamespaceObject("acme"))
	if got := d.tokenManagersOf(t, access.GrantRef(p)); got != "" {
		t.Fatalf("a grant to a person names token managers %q", got)
	}
}

// tokenManagersOf returns the change "tokenManagers" of the audit entry of
// object in the namespace acme, or the empty string when it has none.
func (d *delegation) tokenManagersOf(t *testing.T, object string) string {
	t.Helper()

	return d.changeOf(t, "acme", object, "tokenManagers")
}

// changeOf returns the new value of the change key of the audit entry of
// object in ns, or the empty string when it has none.
func (d *delegation) changeOf(t *testing.T, ns namespace.Name, object, key string) string {
	t.Helper()

	entry := d.entryOf(t, ns, object)

	for _, c := range entry.Changes {
		if c.Key == key {
			return c.New
		}
	}

	return ""
}

// entryOf returns the audit entry of object in ns.
func (d *delegation) entryOf(t *testing.T, ns namespace.Name, object string) audit.Entry {
	t.Helper()

	for number := 1; ; number++ {
		listing, err := d.st.List(t.Context(), ns, audit.Page{Number: number, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}

		for i := range listing.Entries {
			if listing.Entries[i].Object == object {
				return listing.Entries[i]
			}
		}

		if int64(number*audit.MaxPageSize) >= listing.Total {
			t.Fatalf("no audit entry of %q", object)
		}
	}
}

// countChanges returns the count of the changes with the key in the
// audit entry of object in ns.
func (d *delegation) countChanges(t *testing.T, ns namespace.Name, object, key string) int {
	t.Helper()

	entry := d.entryOf(t, ns, object)
	n := 0

	for _, c := range entry.Changes {
		if c.Key == key {
			n++
		}
	}

	return n
}

// groupOf stores a group with the name and returns it.
func (d *delegation) groupOf(t *testing.T, name string) principal.Group {
	t.Helper()

	g, err := d.principals.CreateGroup(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageGroups),
		principal.GroupInput{Name: name, Description: ""})
	if err != nil {
		t.Fatalf("create group %q: %v", name, err)
	}

	return g
}

// addMemberOf puts the principal p in the group g for the bootstrap admin.
func (d *delegation) addMemberOf(t *testing.T, g principal.Group, p principal.Principal) {
	t.Helper()

	_, err := d.principals.AddMember(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageGroups),
		bootstrap(), g.ID, p.ID)
	if err != nil {
		t.Fatalf("add %q to group %q: %v", p.Subject, g.Name, err)
	}
}

// grantToGroup gives the viewer role on obj to the group g for the bootstrap admin.
func (d *delegation) grantToGroup(
	t *testing.T, g principal.Group, obj identity.Object,
) access.Grant {
	t.Helper()

	granted, err := d.access.Grant(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageAccess),
		bootstrap(), access.GrantInput{
			Object: obj, Role: access.RoleViewer, Subject: access.Subject{Kind: access.SubjectGroup, ID: g.ID},
		})
	if err != nil {
		t.Fatalf("grant %s on %q to group %q: %v", access.RoleViewer, obj.String(), g.Name, err)
	}

	return granted
}

// testGroupGrantNamesTokenManagers proves that the audit entry of a grant
// to a group names, in one change "tokenManagers" with one line per service
// principal member, the holders of principal:manage-tokens on it, and names
// nothing for a person member.
func testGroupGrantNamesTokenManagers(t *testing.T, d *delegation) {
	t.Helper()

	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	holders := []string{"admin", "principal:" + admin.ID, "principal:" + ops.ID}
	slices.Sort(holders)

	want := deploy.ID + ": " + strings.Join(holders, ", ")

	mixed := d.groupOf(t, "deployers")
	d.addMemberOf(t, mixed, deploy)
	d.addMemberOf(t, mixed, alice)

	g := d.grantToGroup(t, mixed, identity.NamespaceObject("acme"))

	if got := d.tokenManagersOf(t, access.GrantRef(g)); got != want {
		t.Fatalf("token managers = %q, want %q", got, want)
	}

	if got := d.changeOf(t, "acme", access.GrantRef(g), "tokenManagers."+deploy.ID); got != "" {
		t.Fatalf("a group grant names a per-principal change %q", got)
	}

	if n := d.countChanges(t, "acme", access.GrantRef(g), "tokenManagers"); n != 1 {
		t.Fatalf("tokenManagers changes = %d, want 1", n)
	}

	persons := d.groupOf(t, "readers")
	d.addMemberOf(t, persons, alice)

	h := d.grantToGroup(t, persons, identity.NamespaceObject("acme"))

	if got := d.tokenManagersOf(t, access.GrantRef(h)); got != "" {
		t.Fatalf("a group of persons names token managers %q", got)
	}
}

// testGroupGrantLargeNamesAll proves that a grant to a group with 300
// service-principal members succeeds with one "tokenManagers" change, and
// that the change names every member in lines sorted by principal ID.
func testGroupGrantLargeNamesAll(t *testing.T, d *delegation) {
	t.Helper()

	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	holders := []string{"admin", "principal:" + admin.ID}
	slices.Sort(holders)

	holderText := strings.Join(holders, ", ")
	group := d.groupOf(t, "deployers")

	ids := make([]string, 0, 300)

	for i := range 300 {
		p := d.principalOf(t, fmt.Sprintf("svc-%03d@example.com", i), principal.KindService)
		d.addMemberOf(t, group, p)
		ids = append(ids, p.ID)
	}

	slices.Sort(ids)

	g := d.grantToGroup(t, group, identity.NamespaceObject("acme"))

	if n := d.countChanges(t, "acme", access.GrantRef(g), "tokenManagers"); n != 1 {
		t.Fatalf("tokenManagers changes = %d, want 1", n)
	}

	got := d.tokenManagersOf(t, access.GrantRef(g))
	lines := strings.Split(got, "\n")

	if len(lines) != len(ids) {
		t.Fatalf("tokenManagers lines = %d, want %d", len(lines), len(ids))
	}

	for i, id := range ids {
		want := id + ": " + holderText
		if lines[i] != want {
			t.Fatalf("tokenManagers line %d = %q, want %q", i, lines[i], want)
		}
	}
}

// testGroupGrantPagesMembers proves that a grant to a group with more
// members than principal.MaxLimit still names every service principal: the
// member listing pages past the limit.
func testGroupGrantPagesMembers(t *testing.T, d *delegation) {
	t.Helper()

	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	holders := []string{"admin", "principal:" + admin.ID}
	slices.Sort(holders)

	holderText := strings.Join(holders, ", ")
	group := d.groupOf(t, "deployers")

	const members = principal.MaxLimit + 1

	ids := make([]string, 0, members)

	for i := range members {
		p := d.principalOf(t, fmt.Sprintf("svc-%05d@example.com", i), principal.KindService)
		d.addMemberOf(t, group, p)
		ids = append(ids, p.ID)
	}

	slices.Sort(ids)

	g := d.grantToGroup(t, group, identity.NamespaceObject("acme"))

	if n := d.countChanges(t, "acme", access.GrantRef(g), "tokenManagers"); n != 1 {
		t.Fatalf("tokenManagers changes = %d, want 1", n)
	}

	got := d.tokenManagersOf(t, access.GrantRef(g))
	lines := strings.Split(got, "\n")

	if len(lines) != len(ids) {
		t.Fatalf("tokenManagers lines = %d, want %d", len(lines), len(ids))
	}

	for i, id := range ids {
		want := id + ": " + holderText
		if lines[i] != want {
			t.Fatalf("tokenManagers line %d = %q, want %q", i, lines[i], want)
		}
	}
}

// testGroupGrantNamesGroupHolder proves that when a group holds
// principal:manage-tokens on a service principal, the "tokenManagers" line
// of a later grant to a group with that principal names the holder as
// "group:<id>#member".
func testGroupGrantNamesGroupHolder(t *testing.T, d *delegation) {
	t.Helper()

	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	managers := d.groupOf(t, "managers")
	d.mustGrant(t, bootstrap(), admin, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))

	_, err := d.access.Grant(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageAccess),
		bootstrap(), access.GrantInput{
			Object: identity.PrincipalObject(deploy.ID), Role: access.RoleTokenManager,
			Subject: access.Subject{Kind: access.SubjectGroup, ID: managers.ID},
		})
	if err != nil {
		t.Fatalf("grant token-manager to group: %v", err)
	}

	group := d.groupOf(t, "deployers")
	d.addMemberOf(t, group, deploy)

	g := d.grantToGroup(t, group, identity.NamespaceObject("acme"))
	got := d.tokenManagersOf(t, access.GrantRef(g))

	wantHolder := "group:" + managers.ID + "#member"
	found := false

	for line := range strings.SplitSeq(got, "\n") {
		id, holders, ok := strings.Cut(line, ": ")
		if !ok || id != deploy.ID {
			continue
		}

		found = true

		if !slices.Contains(strings.Split(holders, ", "), wantHolder) {
			t.Fatalf("token managers of %q = %q, want holder %q", deploy.ID, holders, wantHolder)
		}
	}

	if !found {
		t.Fatalf("no tokenManagers line for %q in %q", deploy.ID, got)
	}
}

// testMembershipNamesTokenManagers proves that the audit entry of a
// membership add of a service principal names the holders of
// principal:manage-tokens on it, and that the entry of a person names
// none.
func testMembershipNamesTokenManagers(t *testing.T, d *delegation) {
	t.Helper()

	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())

	holders := []string{"admin", "principal:" + admin.ID, "principal:" + ops.ID}
	slices.Sort(holders)

	want := strings.Join(holders, ", ")

	group := d.groupOf(t, "deployers")
	d.addMemberOf(t, group, deploy)
	d.addMemberOf(t, group, alice)

	if got := d.changeOf(t, audit.ServerScope, principal.MemberRef(group.ID, deploy.ID), "tokenManagers"); got != want {
		t.Fatalf("token managers of %q = %q, want %q", deploy.ID, got, want)
	}

	if got := d.changeOf(t, audit.ServerScope, principal.MemberRef(group.ID, alice.ID), "tokenManagers"); got != "" {
		t.Fatalf("a person member names token managers %q", got)
	}
}

// testPrincipalExport proves that the export of a service principal lists
// the holders of principal:manage-tokens on it: a grant on it, and a grant
// on the installation with a role that holds the action. A grant on the
// installation with another role is not in it.
func testPrincipalExport(t *testing.T, d *delegation) {
	t.Helper()

	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	reader := d.principalOf(t, "reader@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	d.mustGrant(t, bootstrap(), admin, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), reader, access.RoleViewer, identity.ServerObject())

	got, err := d.access.TokenManagers(t.Context(), deploy.ID)
	if err != nil {
		t.Fatalf("token managers: %v", err)
	}

	want := []string{"admin", "principal:" + admin.ID, "principal:" + ops.ID}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("token managers = %q, want %q", got, want)
	}

	person := d.principalOf(t, "alice@example.com", principal.KindPerson)
	if _, err := d.access.TokenManagers(t.Context(), person.ID); !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("export of a person: err = %v, want ErrInvalid", err)
	}
}

// testDeleteEndsPrincipalGrants proves that the delete of a service
// principal ends the grants on it, so a new principal never inherits them.
func testDeleteEndsPrincipalGrants(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	ops := d.principalOf(t, "ops@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	target := identity.PrincipalObject(deploy.ID)

	d.mustGrant(t, bootstrap(), ops, access.RoleTokenManager, target)

	deleteEntry := changeEntry(bootstrap(), identity.ActionServerManagePrincipals)
	if err := d.principals.DeletePrincipal(ctx, deleteEntry, bootstrap(), deploy.ID); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	grants, err := d.st.GrantsOn(ctx, []access.Subject{{Kind: access.SubjectPrincipal, ID: ops.ID}},
		[]identity.Object{target})
	if err != nil || len(grants) != 0 {
		t.Fatalf("grants on a deleted principal = %v, %v; want none", grants, err)
	}

	if err := d.authz.Authorize(ctx, signedIn(ops), identity.ActionPrincipalManageTokens, target); err == nil {
		t.Fatal("a grant on a deleted principal still gives principal:manage-tokens")
	}
}

// grantRoleToGroup gives role on obj to the group g for the bootstrap admin.
func (d *delegation) grantRoleToGroup(t *testing.T, g principal.Group, role string, obj identity.Object) access.Grant {
	t.Helper()

	granted, err := d.access.Grant(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageAccess),
		bootstrap(), access.GrantInput{
			Object: obj, Role: role, Subject: access.Subject{Kind: access.SubjectGroup, ID: g.ID},
		})
	if err != nil {
		t.Fatalf("grant %s on %q to group %q: %v", role, obj.String(), g.Name, err)
	}

	return granted
}

// testMembershipNamesActsAs proves that the audit entry of a membership add
// names, in one change "actsAs" with one line each and sorted, what the
// grants of the group let the new member act as, as the access review
// names it: "principal:<id>" for a grant on a service principal, the
// namespace for a grant on it with namespace:manage-principal-tokens, and
// "@server" for a grant on the installation with principal:manage-tokens. A
// revoked grant, a grant of another role, and a grant of the admin role on
// the installation name nothing, and a group with
// no such grant adds no change.
func testMembershipNamesActsAs(t *testing.T, d *delegation) {
	t.Helper()

	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)
	bob := d.principalOf(t, "bob@example.com", principal.KindPerson)
	carol := d.principalOf(t, "carol@example.com", principal.KindPerson)
	dave := d.principalOf(t, "dave@example.com", principal.KindPerson)
	erin := d.principalOf(t, "erin@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)
	build := d.principalOf(t, "ci-build", principal.KindService)
	release := d.principalOf(t, "ci-release", principal.KindService)

	deployers := d.groupOf(t, "deployers")
	d.grantRoleToGroup(t, deployers, access.RoleTokenManager, identity.PrincipalObject(deploy.ID))
	d.grantRoleToGroup(t, deployers, access.RoleTokenManager, identity.PrincipalObject(build.ID))
	d.grantToGroup(t, deployers, identity.NamespaceObject("acme"))

	gone := d.grantRoleToGroup(t, deployers, access.RoleTokenManager, identity.PrincipalObject(release.ID))
	revokeEntry := changeEntry(bootstrap(), identity.ActionServerManageAccess)

	if _, err := d.access.RevokeByID(t.Context(), revokeEntry, bootstrap(), gone.ID); err != nil {
		t.Fatalf("revoke grant on %s: %v", release.Subject, err)
	}

	admins := d.groupOf(t, "admins")
	d.grantRoleToGroup(t, admins, access.RoleAccountAdministrator, identity.ServerObject())

	nsAdmins := d.groupOf(t, "ns-admins")
	d.grantRoleToGroup(t, nsAdmins, access.RoleAdmin, identity.NamespaceObject("acme"))

	serverAdmins := d.groupOf(t, "server-admins")
	d.grantRoleToGroup(t, serverAdmins, access.RoleAdmin, identity.ServerObject())

	readers := d.groupOf(t, "readers")
	d.grantToGroup(t, readers, identity.NamespaceObject("acme"))

	d.addMemberOf(t, deployers, alice)
	d.addMemberOf(t, admins, bob)
	d.addMemberOf(t, readers, carol)
	d.addMemberOf(t, nsAdmins, dave)
	d.addMemberOf(t, serverAdmins, erin)

	lines := []string{
		identity.PrincipalObject(deploy.ID).String(), identity.PrincipalObject(build.ID).String(),
	}
	slices.Sort(lines)

	cases := []struct {
		group principal.Group
		p     principal.Principal
		want  string
	}{
		{group: deployers, p: alice, want: strings.Join(lines, "\n")},
		{group: admins, p: bob, want: "@server"},
		{group: nsAdmins, p: dave, want: "acme"},
		{group: serverAdmins, p: erin, want: ""},
		{group: readers, p: carol, want: ""},
	}

	for i := range cases {
		tc := &cases[i]

		got := d.changeOf(t, audit.ServerScope, principal.MemberRef(tc.group.ID, tc.p.ID), "actsAs")
		if got != tc.want {
			t.Fatalf("actsAs of %s joining %s = %q, want %q", tc.p.Subject, tc.group.Name, got, tc.want)
		}
	}
}
