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

func parseNamespace(t *testing.T, name string) namespace.Name {
	t.Helper()

	ns, err := namespace.ParseName(name)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	return ns
}

// TestDiscoveryGrantIDOfNoGrant proves that the authorizer names no grant
// and reports no error for a principal with no grant in the namespace.
func TestDiscoveryGrantIDOfNoGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")

	id, err := f.authz.DiscoveryGrantID(t.Context(), f.person(t, "nobody"), acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of no grant = %q, %v; want no ID, nil", id, err)
	}
}

// TestDiscoveryGrantIDOfACollectionGrant proves that the authorizer names
// the stable ID of the grant on a collection in the namespace.
func TestDiscoveryGrantIDOfACollectionGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	web := unitObject(t, "acme", "web/prod")
	f.units[web.String()] = "payments"

	alice := f.person(t, "alice")
	collection := identity.CollectionObject("acme", "payments")
	f.grant(t, prn("alice"), access.RoleWriter, collection)

	id, err := f.authz.DiscoveryGrantID(t.Context(), alice, acme)
	if want := grantID(prn("alice"), access.RoleWriter, collection); err != nil || id != want {
		t.Fatalf("DiscoveryGrantID of a collection grant = %q, %v; want %q, nil", id, err, want)
	}
}

// TestDiscoveryGrantIDOfAnotherNamespace proves that a grant in another
// namespace names no grant in this namespace.
func TestDiscoveryGrantIDOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	beta := parseNamespace(t, "beta")
	webBeta := unitObject(t, "beta", "web/prod")
	f.units[webBeta.String()] = "beta-payments"

	carol := f.person(t, "carol")
	betaCollection := identity.CollectionObject("beta", "beta-payments")
	f.grant(t, prn("carol"), access.RoleWriter, betaCollection)

	id, err := f.authz.DiscoveryGrantID(t.Context(), carol, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of another namespace = %q, %v; want no ID, nil", id, err)
	}

	id, err = f.authz.DiscoveryGrantID(t.Context(), carol, beta)
	if want := grantID(prn("carol"), access.RoleWriter, betaCollection); err != nil || id != want {
		t.Fatalf("DiscoveryGrantID of the beta grant = %q, %v; want %q, nil", id, err, want)
	}
}

// TestDiscoveryGrantIDOfAUnitGrant proves that the authorizer names the
// stable ID of the grant on a unit in the namespace.
func TestDiscoveryGrantIDOfAUnitGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	web := unitObject(t, "acme", "web/prod")
	f.units[web.String()] = "payments"

	frank := f.person(t, "frank")
	frankUnit := f.unit(t, web)
	f.grant(t, prn("frank"), access.RoleViewer, frankUnit)

	id, err := f.authz.DiscoveryGrantID(t.Context(), frank, acme)
	if want := grantID(prn("frank"), access.RoleViewer, frankUnit); err != nil || id != want {
		t.Fatalf("DiscoveryGrantID of a unit grant = %q, %v; want %q, nil", id, err, want)
	}
}

// TestDiscoveryGrantIDOfANamespaceGrant proves that a grant on the namespace
// itself names no discovery grant: the Authorizer decides it before discovery
// runs, and discovery opens from a collection or a unit only.
func TestDiscoveryGrantIDOfANamespaceGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")

	dave := f.person(t, "dave")
	f.grant(t, prn("dave"), access.RoleViewer, identity.NamespaceObject("acme"))

	id, err := f.authz.DiscoveryGrantID(t.Context(), dave, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of a namespace grant = %q, %v; want no ID, nil", id, err)
	}
}

// TestDiscoveryGrantIDAfterRevoke proves that a revoked grant names nothing on
// the next request.
func TestDiscoveryGrantIDAfterRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	web := unitObject(t, "acme", "web/prod")
	f.units[web.String()] = "payments"

	dave := f.person(t, "dave")
	collection := identity.CollectionObject("acme", "payments")
	f.grant(t, prn("dave"), access.RoleViewer, collection)

	grant := grantID(prn("dave"), access.RoleViewer, collection)

	id, err := f.authz.DiscoveryGrantID(t.Context(), dave, acme)
	if err != nil || id != grant {
		t.Fatalf("DiscoveryGrantID of a collection grant = %q, %v; want %q, nil", id, err, grant)
	}

	fence, entry := storetest.GrantWrite(t, f.store, collection, time.Unix(1, 0), "grant:held")
	if revokeErr := f.store.RevokeGrant(t.Context(), grant, fence, entry, nil); revokeErr != nil {
		t.Fatalf("revoke grant: %v", revokeErr)
	}

	id, err = f.authz.DiscoveryGrantID(t.Context(), dave, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID after the revoke = %q, %v; want no ID, nil", id, err)
	}
}

var errReadFailed = errors.New("read failed")

type failingRoles struct{ access.Store }

func (failingRoles) ListRoles(context.Context) ([]access.Role, error) {
	return nil, errReadFailed
}

type discoveryFailingGrants struct{ access.Store }

func (discoveryFailingGrants) GrantsWithRoles(
	context.Context, []access.Subject, []string,
) ([]access.Grant, error) {
	return nil, errReadFailed
}

type failingCallers struct{}

func (failingCallers) SubjectsOf(context.Context, string) ([]access.Subject, error) {
	return nil, errReadFailed
}

type failingUnits struct{ access.Units }

func (failingUnits) UnitObjects(context.Context, []string) (map[string]identity.Object, error) {
	return nil, errReadFailed
}

type failingCollections struct{ access.UnitCollections }

func (failingCollections) CollectionID(context.Context, identity.Object) (string, bool, error) {
	return "", false, errReadFailed
}

type discoveryFailingPrincipals struct{ access.PrincipalNamespaces }

func (discoveryFailingPrincipals) GetPrincipal(context.Context, string) (principal.Principal, error) {
	return principal.Principal{}, errReadFailed
}

// TestDiscoveryGrantIDFailsClosedOnAReadError proves that each failed read
// returns the error and no ID, so the caller never gets a grant ID from a
// partial read.
func TestDiscoveryGrantIDFailsClosedOnAReadError(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	web := unitObject(t, "acme", "web/prod")
	f.units[web.String()] = "payments"

	alice := f.person(t, "alice")
	f.grant(t, prn("alice"), access.RoleViewer, identity.CollectionObject("acme", "payments"))
	f.grant(t, prn("alice"), access.RoleViewer, web)

	base := access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: f.units, Units: f.store, Principals: f.store,
	}

	tests := []struct {
		name   string
		mutate func(*access.AuthorizerConfig)
	}{
		{"subjects", func(c *access.AuthorizerConfig) { c.Callers = failingCallers{} }},
		{"roles", func(c *access.AuthorizerConfig) { c.Store = failingRoles{Store: f.store} }},
		{"grants", func(c *access.AuthorizerConfig) { c.Store = discoveryFailingGrants{Store: f.store} }},
		{"confine", func(c *access.AuthorizerConfig) {
			c.Principals = discoveryFailingPrincipals{PrincipalNamespaces: f.store}
		}},
		{"units", func(c *access.AuthorizerConfig) { c.Units = failingUnits{Units: f.store} }},
		{"collections", func(c *access.AuthorizerConfig) {
			c.Collections = failingCollections{UnitCollections: f.units}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			tt.mutate(&cfg)

			authz, err := access.NewAuthorizer(cfg)
			if err != nil {
				t.Fatalf("new authorizer: %v", err)
			}

			id, err := authz.DiscoveryGrantID(t.Context(), alice, acme)
			if !errors.Is(err, errReadFailed) || id != "" {
				t.Fatalf("failed %s read: DiscoveryGrantID = %q, %v; want no ID, the read error", tt.name, id, err)
			}
		})
	}
}

type noSubjects struct{}

func (noSubjects) SubjectsOf(context.Context, string) ([]access.Subject, error) {
	return nil, nil
}

// TestDiscoveryGrantIDOfNoSubjects proves that a principal with no subject
// names no grant and reads no grant: the role and grant stores fail, so a
// read after the subject lookup returns an error.
func TestDiscoveryGrantIDOfNoSubjects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")
	alice := f.person(t, "alice")

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store:       failingRoles{Store: discoveryFailingGrants{Store: f.store}},
		Callers:     noSubjects{},
		Collections: f.units,
		Units:       f.store,
		Principals:  f.store,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	id, err := authz.DiscoveryGrantID(t.Context(), alice, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of no subjects = %q, %v; want no ID, nil", id, err)
	}
}

// TestDiscoveryGrantIDOfAnInstallationGrant proves that a grant on the
// installation alone names nothing in the namespace.
func TestDiscoveryGrantIDOfAnInstallationGrant(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")

	erin := f.person(t, "erin")
	f.grant(t, prn("erin"), access.RoleViewer, identity.ServerObject())

	id, err := f.authz.DiscoveryGrantID(t.Context(), erin, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of an installation grant = %q, %v; want no ID, nil", id, err)
	}
}

// TestDiscoveryGrantIDOfAnAdministrator proves that an administrator gets no
// grant: the Authorizer allows the caller before any grant runs.
func TestDiscoveryGrantIDOfAnAdministrator(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acme := parseNamespace(t, "acme")

	admin := identity.Principal{
		Subject:                "principal:prn-root",
		DisplayName:            "Root",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Kind:                   identity.KindPerson,
		Credential:             identity.CredentialAccessToken,
		JobID:                  "",
	}

	id, err := f.authz.DiscoveryGrantID(t.Context(), admin, acme)
	if err != nil || id != "" {
		t.Fatalf("DiscoveryGrantID of an administrator = %q, %v; want no ID, nil", id, err)
	}
}

// namingAuthorizer is an authorizer that names the grant id and the error err
// to DiscoveryGrantID. It denies every other question.
type namingAuthorizer struct {
	id  string
	err error
}

func (namingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object,
) error {
	return identity.ErrDenied
}

func (namingAuthorizer) ListObjects(
	context.Context, identity.Principal, identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

func (a namingAuthorizer) DiscoveryGrantID(
	context.Context, identity.Principal, namespace.Name,
) (string, error) {
	return a.id, a.err
}

// TestDiscoveryGrantIDRefusesAnIDThatIsNotAGrantID proves that the wrapper
// that every adapter calls refuses an ID that is not a grant ID, so the ID
// never reaches a response header. The authorizer here is not the one of the
// package: the check must hold without help from it.
func TestDiscoveryGrantIDRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	acme := parseNamespace(t, "acme")
	alice := newFixture(t).person(t, "alice")

	cases := []struct {
		name string
		id   string
	}{
		{"a role name", "admins"},
		{"a header line break", "grt-a\r\nX-A: 1"},
		{"a line feed", "grt-a\nb"},
		{"a space", "grt-a b"},
		{"65 bytes", "grt-" + strings.Repeat("a", 61)},
		{"the prefix of another kind", "tok-abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, found, err := access.DiscoveryGrantID(t.Context(), namingAuthorizer{id: tc.id, err: nil}, alice, acme)
			if !found || id != "" || !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("DiscoveryGrantID(%q) = %q, %v, %v; want no ID, true, ErrInvalid", tc.id, id, found, err)
			}
		})
	}

	for _, ok := range []string{"", "grt-a", "grt-" + strings.Repeat("a", 60)} {
		id, found, err := access.DiscoveryGrantID(t.Context(), namingAuthorizer{id: ok, err: nil}, alice, acme)
		if !found || id != ok || err != nil {
			t.Fatalf("DiscoveryGrantID(%q) = %q, %v, %v; want the same ID, true, nil", ok, id, found, err)
		}
	}
}
