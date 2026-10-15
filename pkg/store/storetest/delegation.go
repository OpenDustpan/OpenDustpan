package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/tokengrants"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// DelegationStore is what the delegation suite wires: the principals, the
// roles and grants, the access tokens, the namespaces and the audit log.
// Both SQL stores meet it.
type DelegationStore interface {
	principal.Store
	access.Store
	access.Callers
	access.Units
	accesstoken.Store
	namespace.Store
	audit.Store
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
		{name: "a grant on a principal takes a service principal and principal actions", test: testPrincipalGrantRules},
		{name: "an account administrator cannot act as a site administrator", test: testNoPersonalEscalation},
		{name: "the audit entry of a grant names the token managers", test: testGrantNamesTokenManagers},
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
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"},
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	principals, err := principal.NewService(
		principal.Config{Store: st, Guard: svc, Namespaces: st, Now: nil, NewID: nil},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, Freshness: nil, Grants: tokengrants.Check{Grants: svc, Principals: st},
		Authorizer: authz, Logger: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	err = st.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt: time.Unix(0, 0), Name: "acme", HomeRegion: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return &delegation{st: st, authz: authz, access: svc, tokens: tokens, principals: principals}
}

// bootstrap is the bootstrap admin token: it holds every action.
func bootstrap() identity.Principal {
	return identity.Principal{Subject: "admin", DisplayName: "", Admin: true, TokenID: "", TokenExpiresAt: time.Time{}}
}

// signedIn returns the caller of p after a sign-in: no access token.
func signedIn(p principal.Principal) identity.Principal {
	return identity.Principal{
		Subject:        p.Subject,
		DisplayName:    "",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
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

	tok, secret, err := d.tokens.Create(ctx, bootstrap(), changeEntry(bootstrap(), identity.ActionServerManageTokens),
		accesstoken.ScopePersonal, accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: root.ID, Name: "laptop"})
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

// testNoPersonalEscalation proves that the escalation check stays for a
// personal token: an account administrator cannot make a token for a site
// administrator, who holds more. It can still make a token for any
// service principal.
func testNoPersonalEscalation(t *testing.T, d *delegation) {
	t.Helper()

	account := d.principalOf(t, "account@example.com", principal.KindPerson)
	site := d.principalOf(t, "site@example.com", principal.KindPerson)
	deploy := d.principalOf(t, "ci-deploy", principal.KindService)

	d.mustGrant(t, bootstrap(), account, access.RoleAccountAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), site, access.RoleSiteAdministrator, identity.ServerObject())
	d.mustGrant(t, bootstrap(), deploy, access.RoleSiteAdministrator, identity.ServerObject())

	_, _, err := d.tokens.Create(t.Context(), signedIn(account),
		changeEntry(signedIn(account), identity.ActionServerManageTokens), accesstoken.ScopePersonal,
		accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: site.ID, Name: "laptop"})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("personal token for a site administrator: err = %v, want ErrDenied", err)
	}

	if _, _, err := d.serviceToken(t, signedIn(account), deploy); err != nil {
		t.Fatalf("service token by an account administrator: %v", err)
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
	if got := d.tokenManagersOf(t, "acme", access.GrantRef(g)); got != want {
		t.Fatalf("token managers = %q, want %q", got, want)
	}

	p := d.mustGrant(t, bootstrap(), ops, access.RoleViewer, identity.NamespaceObject("acme"))
	if got := d.tokenManagersOf(t, "acme", access.GrantRef(p)); got != "" {
		t.Fatalf("a grant to a person names token managers %q", got)
	}
}

// tokenManagersOf returns the change "tokenManagers" of the audit entry of
// object in ns, or the empty string when it has none.
func (d *delegation) tokenManagersOf(t *testing.T, ns namespace.Name, object string) string {
	t.Helper()

	listing, err := d.st.List(t.Context(), ns, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	i := slices.IndexFunc(listing.Entries, func(e audit.Entry) bool { return e.Object == object })
	if i < 0 {
		t.Fatalf("no audit entry of %q", object)
	}

	for _, c := range listing.Entries[i].Changes {
		if c.Key == "tokenManagers" {
			return c.New
		}
	}

	return ""
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
