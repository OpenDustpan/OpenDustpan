package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errNoPrincipal fails the read of a principal in the tests.
var errNoPrincipal = errors.New("principal read failed")

// plantedGrant returns the grant of role on obj to subject that the API
// refuses and that a restore or a row of another version holds.
func plantedGrant(id string, subject access.Subject, role string, obj identity.Object) access.Grant {
	return access.Grant{
		CreatedAt: time.Unix(0, 0), Condition: nil, Object: obj, ID: id, Role: role,
		CreatedBy: "admin", Subject: subject,
	}
}

// plantGrant stores g with no check, as a restore or a row of another
// version holds it. The store refuses a grant of a principal of a namespace
// on an object outside it, so the caller plants the grant before the
// principal gets its namespace.
func plantGrant(t *testing.T, f *fixture, g access.Grant) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, f.store, g.Object, g.CreatedAt, access.GrantRef(g))

	if err := f.store.CreateGrant(t.Context(), g, fence, e, nil); err != nil {
		t.Fatalf("plant grant %q: %v", g.ID, err)
	}
}

// namespaceEscalationResult holds the callers of the principals of the
// escalation test: the principal of acme and the principal of beta.
type namespaceEscalationResult struct {
	bot     identity.Principal
	betaBot identity.Principal
}

// namespaceEscalation plants the grants of a service principal of acme on
// the installation and on beta, and a grant of the service principal of beta
// on the installation, then makes both principals of their namespaces and
// grants the acme one viewer on acme. The grants are the rows that the store
// refuses once the principal is of a namespace.
func namespaceEscalation(t *testing.T, f *fixture) namespaceEscalationResult {
	t.Helper()

	tokenRole := f.tokenRole(t, "ns-token")

	subject := prn("ci-acme")
	betaSubject := access.Subject{Kind: access.SubjectPrincipal, ID: "prn-beta-bot"}

	plantGrant(t, f, plantedGrant(
		"grt-planted-server", subject, access.RoleSiteAdministrator, identity.ServerObject(),
	))
	plantGrant(t, f, plantedGrant(
		"grt-planted-beta", subject, access.RoleSiteAdministrator, identity.NamespaceObject("beta"),
	))
	plantGrant(t, f, plantedGrant(
		"grt-planted-token", subject, tokenRole.ID, identity.NamespaceObject("beta"),
	))
	plantGrant(t, f, plantedGrant(
		"grt-planted-beta-server", betaSubject, access.RoleSiteAdministrator, identity.ServerObject(),
	))

	f.namespaceBot(t, "prn-beta-bot", "ci-beta", "beta")
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")
	f.grant(t, subject, access.RoleViewer, identity.NamespaceObject("acme"))

	return namespaceEscalationResult{
		bot: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "ci-acme", DisplayName: "",
			Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindService,
			Credential: identity.CredentialAccessToken, JobID: "",
		},
		betaBot: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "ci-beta", DisplayName: "",
			Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindService,
			Credential: identity.CredentialAccessToken, JobID: "",
		},
	}
}

// TestNamespacePrincipalGrantEscalation proves that the Authorizer honors
// no grant of a principal of a namespace on an object outside that
// namespace: not a grant on the installation, and not a grant on another
// namespace, in a decision, in a scope and in the principals that the
// namespace token rule lists. A grant of the same principal inside its own
// namespace still works.
func TestNamespacePrincipalGrantEscalation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	escalation := namespaceEscalation(t, f)
	bot := escalation.bot
	ctx := t.Context()

	assertChecks(t, f.authz, bot, []check{
		{identity.ServerObject(), identity.ActionServerBackup, false},
		{identity.NamespaceObject("beta"), identity.ActionNamespaceRead, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, true},
	})
	assertChecks(t, f.authz, escalation.betaBot, []check{
		{identity.ServerObject(), identity.ActionServerBackup, false},
	})

	server, err := f.authz.ListObjects(ctx, bot, identity.ActionServerBackup)
	if err != nil {
		t.Fatalf("list objects of server:backup: %v", err)
	}

	if !server.IsEmpty() {
		t.Fatalf("scope of server:backup = %+v, want no object", server)
	}

	betaServer, err := f.authz.ListObjects(ctx, escalation.betaBot, identity.ActionServerBackup)
	if err != nil {
		t.Fatalf("list objects of the beta bot for server:backup: %v", err)
	}

	if !betaServer.IsEmpty() {
		t.Fatalf("scope of the beta bot for server:backup = %+v, want no object", betaServer)
	}

	read, err := f.authz.ListObjects(ctx, bot, identity.ActionNamespaceRead)
	if err != nil {
		t.Fatalf("list objects of namespace:read: %v", err)
	}

	if len(read.Namespaces) != 1 || read.Namespaces[0].String() != "acme" {
		t.Fatalf("scope of namespace:read = %+v, want acme alone", read.Namespaces)
	}

	tokens, err := f.authz.ListObjects(ctx, bot, identity.ActionPrincipalManageTokens)
	if err != nil {
		t.Fatalf("list objects of principal:manage-tokens: %v", err)
	}

	if !tokens.IsEmpty() {
		t.Fatalf("scope of principal:manage-tokens = %+v, want no object, so not the principal %q of beta",
			tokens, escalation.betaBot.Subject)
	}
}

// TestNewAuthorizerRequiresPrincipals proves that NewAuthorizer refuses a
// config with no principals port, so that no grant of a principal of a
// namespace passes unseen.
func TestNewAuthorizerRequiresPrincipals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, tc := range []struct {
		name string
		cfg  access.AuthorizerConfig
	}{
		{
			name: "a store",
			cfg: access.AuthorizerConfig{
				Store: nil, Callers: f.store, Collections: nil, Units: nil, Principals: f.store,
			},
		},
		{
			name: "callers",
			cfg: access.AuthorizerConfig{
				Store: f.store, Callers: nil, Collections: nil, Units: nil, Principals: f.store,
			},
		},
		{
			name: "principals",
			cfg: access.AuthorizerConfig{
				Store: f.store, Callers: f.store, Collections: nil, Units: nil, Principals: nil,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			authz, err := access.NewAuthorizer(tc.cfg)
			if !errors.Is(err, access.ErrMissingDependency) {
				t.Fatalf("new authorizer = %v, want ErrMissingDependency", err)
			}

			if authz != nil {
				t.Fatalf("new authorizer = %+v, want no authorizer", authz)
			}
		})
	}
}

// namespacePrincipalCaller returns the caller of the service principal ci-acme
// of the namespace acme.
func namespacePrincipalCaller() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "ci-acme", DisplayName: "",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindService,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// groupCallers adds one group to the subjects of one caller, as a membership
// row from another source does: the store and the principal service refuse
// the membership on write.
type groupCallers struct {
	access.Callers

	subject string
	group   string
}

func (c groupCallers) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	subjects, err := c.Callers.SubjectsOf(ctx, subject)
	if err != nil {
		return nil, err
	}

	if subject != c.subject {
		return subjects, nil
	}

	return append(subjects, access.Subject{Kind: access.SubjectGroup, ID: c.group}), nil
}

// TestNamespacePrincipalGroupGrants proves that the Authorizer gives no
// action of a group to a principal of a namespace, though the subjects of the
// caller name a group: the grants of the group on the installation and on the
// namespace of the principal give nothing.
func TestNamespacePrincipalGroupGrants(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bot := namespacePrincipalCaller()

	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	team := f.group(t, "team")
	f.grant(t, team, access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, team, access.RoleViewer, identity.NamespaceObject("acme"))

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: groupCallers{Callers: f.store, subject: "ci-acme", group: team.ID},
		Collections: f.units, Units: f.store, Principals: f.store,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	assertChecks(t, authz, bot, []check{
		{identity.ServerObject(), identity.ActionServerBackup, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
	})

	for _, action := range []identity.Action{identity.ActionServerBackup, identity.ActionNamespaceRead} {
		scope, listErr := authz.ListObjects(t.Context(), bot, action)
		if listErr != nil {
			t.Fatalf("list objects of %s: %v", action, listErr)
		}

		if !scope.IsEmpty() {
			t.Fatalf("scope of %s = %+v, want no object", action, scope)
		}
	}
}

// TestNamespacePrincipalGroupReadFailsClosed proves that the Authorizer gives
// no subject when the read of the principal fails, also when a group has a
// grant on the namespace of the principal.
func TestNamespacePrincipalGroupReadFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bot := namespacePrincipalCaller()

	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	team := f.group(t, "team")
	f.grant(t, team, access.RoleViewer, identity.NamespaceObject("acme"))

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: groupCallers{Callers: f.store, subject: "ci-acme", group: team.ID},
		Collections: f.units, Units: f.store, Principals: failingPrincipals{},
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	authErr := authz.Authorize(t.Context(), bot, identity.ActionNamespaceRead, identity.NamespaceObject("acme"))
	if !errors.Is(authErr, errNoPrincipal) {
		t.Fatalf("authorize with an unreadable principal = %v, want the read error", authErr)
	}
}

// TestNamespacePrincipalGrantDenialText proves that the refusal of a grant
// outside the namespace of its subject names neither the grant nor the
// namespace of the grant: the caller cannot read either, so the denial
// keeps the generic text.
func TestNamespacePrincipalGrantDenialText(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bot := namespaceEscalation(t, f).bot

	err := f.authz.Authorize(t.Context(), bot, identity.ActionServerBackup, identity.ServerObject())
	if err == nil || !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("authorize = %v, want ErrDenied", err)
	}

	want := `ci-acme may not server:backup on "@server"`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("denial = %q, want the generic text %q", err, want)
	}

	for _, leak := range []string{"grt-planted", "beta"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("denial = %q names %q, which the caller cannot read", err, leak)
		}
	}
}

// failingPrincipals fails every read of a principal, as a store that is
// down does.
type failingPrincipals struct{}

func (failingPrincipals) GetPrincipal(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, errNoPrincipal
}

func (failingPrincipals) ListNamespacePrincipals(
	context.Context, namespace.Name, principal.Query,
) ([]principal.Principal, int64, error) {
	return nil, 0, errNoPrincipal
}

// TestNamespacePrincipalGrantReadFailsClosed proves that the Authorizer
// honors no grant when the namespace of the subject cannot be read: the
// decision fails, also for a grant inside the namespace.
func TestNamespacePrincipalGrantReadFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bot := namespaceEscalation(t, f).bot

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: f.units, Units: f.store,
		Principals: failingPrincipals{},
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	authErr := authz.Authorize(t.Context(), bot, identity.ActionNamespaceRead, identity.NamespaceObject("acme"))
	if authErr == nil {
		t.Fatal("authorize with an unreadable namespace of the subject = nil, want a refusal")
	}

	if _, err := authz.ListObjects(t.Context(), bot, identity.ActionNamespaceRead); err == nil {
		t.Fatal("list objects with an unreadable namespace of the subject = nil, want a refusal")
	}
}
