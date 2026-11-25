package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// reachFixture is a fixture whose authorizer reads one value of the reach
// setting.
type reachFixture struct {
	fixture *fixture
	value   string
}

// newReachFixture returns a fixture whose authorizer reads the reach setting
// through settings. The value is the text of the setting.
func newReachFixture(tb testing.TB, value string) reachFixture {
	tb.Helper()

	return reachFixture{
		fixture: newFixtureWithSettings(tb, nil, reachReader{value: &value, err: nil}),
		value:   value,
	}
}

// reachOn returns a fixture whose authorizer reads the reach setting as
// `true`.
func reachOn(tb testing.TB) reachFixture {
	tb.Helper()

	return newReachFixture(tb, access.DefaultInstallationGrantsReachNamespaces)
}

// reachOff returns a fixture whose authorizer reads the reach setting as
// `false`.
func reachOff(tb testing.TB) reachFixture {
	tb.Helper()

	return newReachFixture(tb, "false")
}

// TestInstallationGrantReachesNamespaceWhenTheSettingIsTrue proves that a
// grant on the installation reaches a namespace while the setting is `true`:
// the holder reads a unit of the namespace.
func TestInstallationGrantReachesNamespaceWhenTheSettingIsTrue(t *testing.T) {
	t.Parallel()

	f := reachOn(t).fixture
	holder := f.person(t, "holder")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	assertChecks(t, f.authz, holder, []check{
		{web, identity.ActionUnitRead, true},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, true},
		{identity.ServerObject(), identity.ActionServerReadAudit, false},
	})
}

// TestInstallationGrantDoesNotReachNamespaceWhenTheSettingIsFalse proves that
// a grant on the installation gives no action inside a namespace while the
// setting is `false`, and that its operator actions stay: the holder reads
// nothing of the namespace, and still holds the operator action of its role.
func TestInstallationGrantDoesNotReachNamespaceWhenTheSettingIsFalse(t *testing.T) {
	t.Parallel()

	f := reachOff(t).fixture
	holder := f.person(t, "holder")
	web := unitObject(t, "acme", "web/prod")

	f.grant(t, prn("holder"), access.RoleInstallationOperator, identity.ServerObject())

	assertChecks(t, f.authz, holder, []check{
		{web, identity.ActionUnitRead, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
		{identity.ServerObject(), identity.ActionServerBackup, true},
	})
}

// TestInstallationGrantBelowTheSettingIsDeniedOnEveryNamespace proves the
// refusal of a caller that holds only a grant on the installation while the
// setting is `false`: each action inside each namespace is denied, and the
// server changes nothing.
func TestInstallationGrantBelowTheSettingIsDeniedOnEveryNamespace(t *testing.T) {
	t.Parallel()

	f := reachOff(t).fixture
	holder := f.person(t, "holder")
	web := unitObject(t, "acme", "web/prod")
	other := unitObject(t, "beta", "web/prod")
	payments := identity.CollectionObject("acme", "payments")

	f.grant(t, prn("holder"), access.RoleSiteAdministrator, identity.ServerObject())

	assertChecks(t, f.authz, holder, []check{
		{web, identity.ActionUnitRead, false},
		{other, identity.ActionUnitRead, false},
		{payments, identity.ActionCollectionRead, false},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, false},
		{identity.NamespaceObject("beta"), identity.ActionNamespaceRead, false},
		{identity.ServerObject(), identity.ActionServerReadAudit, true},
	})
}

// TestGrantOnANamespaceReachesItWhenTheSettingIsFalse proves that the setting
// confines a grant on the installation only: a grant on the namespace still
// gives its actions inside that namespace.
func TestGrantOnANamespaceReachesItWhenTheSettingIsFalse(t *testing.T) {
	t.Parallel()

	f := reachOff(t).fixture
	holder := f.person(t, "holder")
	web := unitObject(t, "acme", "web/prod")
	other := unitObject(t, "beta", "web/prod")

	f.grant(t, prn("holder"), access.RoleViewer, identity.NamespaceObject("acme"))

	assertChecks(t, f.authz, holder, []check{
		{web, identity.ActionUnitRead, true},
		{identity.NamespaceObject("acme"), identity.ActionNamespaceRead, true},
		{other, identity.ActionUnitRead, false},
	})
}

// TestListObjectsOmitsNamespacesWhenTheSettingIsFalse proves that the scope of
// a caller with a grant on the installation alone holds no namespace while the
// setting is `false`.
func TestListObjectsOmitsNamespacesWhenTheSettingIsFalse(t *testing.T) {
	t.Parallel()

	f := reachOff(t).fixture
	holder := f.person(t, "holder")

	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	scope, err := f.authz.ListObjects(t.Context(), holder, identity.ActionUnitRead)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}

	if scope.CoversEveryObject() {
		t.Fatalf("scope = %+v, want no namespace of the installation", scope)
	}

	if len(scope.Namespaces) != 0 || len(scope.Collections) != 0 || len(scope.Units) != 0 {
		t.Fatalf("scope = %+v, want no namespace, collection or unit", scope)
	}
}

// TestExplainNamesNoGrantWhenTheSettingIsFalse proves that the answer of an
// explain holds no grant that gives an action inside a namespace while the
// setting is `false`.
func TestExplainNamesNoGrantWhenTheSettingIsFalse(t *testing.T) {
	t.Parallel()

	f := reachOff(t).fixture
	holder := f.person(t, "holder")

	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	got, err := f.authz.Explain(t.Context(), holder, identity.NamespaceObject("acme"))
	if err != nil {
		t.Fatalf("explain: %v", err)
	}

	if len(got.Actions) != 0 || len(got.Grants) != 0 {
		t.Fatalf("explain = %+v, want no action and no grant", got)
	}
}
