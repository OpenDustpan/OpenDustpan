package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// acmeBot stores ci-acme, a service principal of the namespace acme.
func (d *delegation) acmeBot(t *testing.T) principal.Principal {
	t.Helper()

	p, err := d.principals.CreateNamespacePrincipal(t.Context(),
		changeEntry(bootstrap(), identity.ActionNamespaceManagePrincipals), "acme",
		principal.NamespaceInput{Subject: "ci-acme", DisplayName: ""})
	if err != nil {
		t.Fatalf("create principal ci-acme in acme: %v", err)
	}

	return p
}

// namespaceNamed stores the namespace ns.
func (d *delegation) namespaceNamed(t *testing.T, ns namespace.Name) {
	t.Helper()

	err := d.st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:        namespace.NewID(),
		CreatedAt: time.Unix(0, 0), Name: ns, HomeRegion: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace %q: %v", ns, err)
	}
}

// testNamespacePrincipalGrants proves that a principal of a namespace can
// hold a grant on its namespace, but not on the installation, on another
// namespace or on a principal.
func testNamespacePrincipalGrants(t *testing.T, d *delegation) {
	t.Helper()

	d.namespaceNamed(t, "beta")
	bot := d.acmeBot(t)
	other := d.principalOf(t, "ci-deploy", principal.KindService)

	objects := []identity.Object{
		identity.ServerObject(), identity.NamespaceObject("beta"), identity.PrincipalObject(other.ID),
	}
	for i := range objects {
		obj := objects[i]

		role := access.RoleViewer
		if obj.IsPrincipal() {
			role = access.RoleTokenManager
		}

		if _, err := d.grant(t, bootstrap(), bot, role, obj); !errors.Is(err, access.ErrInvalid) {
			t.Errorf("grant to a principal of acme on %q: err = %v, want ErrInvalid", obj.String(), err)
		}
	}

	d.mustGrant(t, bootstrap(), bot, access.RoleWriter, identity.NamespaceObject("acme"))

	if _, err := d.grant(t, bootstrap(), other, access.RoleTokenManager, identity.PrincipalObject(bot.ID)); !errors.Is(
		err, access.ErrInvalid,
	) {
		t.Errorf("grant on a principal of a namespace: err = %v, want ErrInvalid", err)
	}
}

// testStoreChecksSubjectNamespace proves that the grant write reads the
// namespace of its subject in its own transaction: a write that skips the
// Service cannot put a principal of a namespace on another object.
func testStoreChecksSubjectNamespace(t *testing.T, d *delegation) {
	t.Helper()

	bot := d.acmeBot(t)
	at := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-race", Role: access.RoleViewer,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: bot.ID},
	}

	fence, e := GrantWrite(t, d.st, g.Object, at, access.GrantRef(g))
	if err := d.st.CreateGrant(t.Context(), g, fence, e, nil); !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("store grant to a principal of acme on the installation: err = %v, want ErrInvalid", err)
	}

	g.Object, g.ID = identity.NamespaceObject("acme"), "grt-fits"

	fence, e = GrantWrite(t, d.st, g.Object, at, access.GrantRef(g))
	if err := d.st.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("store grant to a principal of acme on acme: %v", err)
	}
}

// testNamespacePrincipalLimits proves the tokens of a principal of a
// namespace: the bootstrap admin token and a holder of the namespace admin
// role make them, a principal without the right cannot, and the principal
// still joins no group.
func testNamespacePrincipalLimits(t *testing.T, d *delegation) {
	t.Helper()

	bot := d.acmeBot(t)
	admin := d.principalOf(t, "alice@example.com", principal.KindPerson)
	outsider := d.principalOf(t, "bob@example.com", principal.KindPerson)

	d.mustGrant(t, bootstrap(), admin, access.RoleAdmin, identity.NamespaceObject("acme"))

	_, _, bootErr := d.serviceToken(t, bootstrap(), bot)
	if bootErr != nil {
		t.Fatalf("token by the bootstrap admin: %v", bootErr)
	}

	tok, secret, adminErr := d.serviceToken(t, signedIn(admin), bot)
	if adminErr != nil {
		t.Fatalf("token by a namespace admin: %v", adminErr)
	}

	_, _, outsiderErr := d.serviceToken(t, signedIn(outsider), bot)
	if !errors.Is(outsiderErr, identity.ErrDenied) {
		t.Fatalf("token without the right: err = %v, want ErrDenied", outsiderErr)
	}

	_, authErr := d.tokens.AuthenticateToken(t.Context(), secret.Reveal())
	if authErr != nil {
		t.Fatalf("authenticate as the namespace principal: %v", authErr)
	}

	revokeErr := d.tokens.Revoke(t.Context(), signedIn(admin),
		changeEntry(signedIn(admin), identity.ActionPrincipalManageTokens),
		accesstoken.ScopeService, tok.ID)
	if revokeErr != nil {
		t.Fatalf("revoke by a namespace admin: %v", revokeErr)
	}

	group, groupErr := d.principals.CreateGroup(
		t.Context(), changeEntry(bootstrap(), identity.ActionServerManageGroups),
		principal.GroupInput{Name: "ops", Description: ""},
	)
	if groupErr != nil {
		t.Fatalf("create group: %v", groupErr)
	}

	_, memberErr := d.principals.AddMember(t.Context(), changeEntry(bootstrap(), identity.ActionServerManageGroups),
		bootstrap(), group.ID, bot.ID)
	if !errors.Is(memberErr, principal.ErrInvalid) {
		t.Fatalf("membership of a principal of a namespace: err = %v, want ErrInvalid", memberErr)
	}
}

// testNamespacePrincipalTokenManagers proves that the export of a principal
// of a namespace lists the holders of the token action on its namespace,
// and that the audit entry of a grant to it names them in the change
// "tokenManagers".
func testNamespacePrincipalTokenManagers(t *testing.T, d *delegation) {
	t.Helper()

	bot := d.acmeBot(t)
	alice := d.principalOf(t, "alice@example.com", principal.KindPerson)

	d.mustGrant(t, bootstrap(), alice, access.RoleAdmin, identity.NamespaceObject("acme"))

	got, err := d.access.TokenManagers(t.Context(), bot.ID)
	if err != nil {
		t.Fatalf("token managers: %v", err)
	}

	want := []string{"admin", "principal:" + alice.ID}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("token managers = %q, want %q", got, want)
	}

	g := d.mustGrant(t, bootstrap(), bot, access.RoleViewer, identity.NamespaceObject("acme"))

	if managers := d.changeOf(t, "acme", access.GrantRef(g), "tokenManagers"); !strings.Contains(
		managers, "principal:"+alice.ID,
	) {
		t.Fatalf("token managers of the grant = %q, want alice", managers)
	}
}

// testNamespacePrincipalDelete proves that the delete of a principal of a
// namespace ends its grants and writes its entry in the audit log of the
// namespace.
func testNamespacePrincipalDelete(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	bot := d.acmeBot(t)
	d.mustGrant(t, bootstrap(), bot, access.RoleWriter, identity.NamespaceObject("acme"))

	err := d.principals.DeleteNamespacePrincipal(
		ctx,
		changeEntry(bootstrap(), identity.ActionNamespaceManagePrincipals),
		bootstrap(),
		"beta",
		bot.ID,
	)
	if !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("delete through another namespace: err = %v, want ErrNotFound", err)
	}

	err = d.principals.DeleteNamespacePrincipal(ctx, changeEntry(bootstrap(), identity.ActionNamespaceManagePrincipals),
		bootstrap(), "acme", bot.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	grants, err := d.st.GrantsOf(ctx, access.Subject{Kind: access.SubjectPrincipal, ID: bot.ID})
	if err != nil || len(grants) != 0 {
		t.Fatalf("grants of a deleted principal = %v, %v; want none", grants, err)
	}

	listing, err := d.st.List(ctx, "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit of acme: %v", err)
	}

	entries := 0

	for i := range listing.Entries {
		if listing.Entries[i].Object == principal.Ref(bot.ID) {
			entries++
		}
	}

	if entries != 2 {
		t.Fatalf("entries of the principal in acme = %d, want the create and the delete", entries)
	}
}
