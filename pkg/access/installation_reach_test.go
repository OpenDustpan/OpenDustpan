package access_test

import (
	"slices"
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

// TestInstallationReachAccessReview proves that the access review of a
// namespace shows a grant on the installation as inherited only while the
// setting is `true`. While it is `false`, the review shows no row for it.
func TestInstallationReachAccessReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fixture  func(testing.TB) reachFixture
		wantRows int
	}{
		{name: "the setting is true", fixture: reachOn, wantRows: 1},
		{name: "the setting is false", fixture: reachOff, wantRows: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := tc.fixture(t).fixture
			f.person(t, "holder")
			f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

			page, err := f.svc.Review(t.Context(), identity.NamespaceObject("acme"), access.ReviewRequest{
				From: nil, To: nil, Subject: "principal:prn-holder", Cursor: "", Limit: 0, Expand: false,
			})
			if err != nil {
				t.Fatalf("review: %v", err)
			}

			inherited := 0

			for i := range page.Entries {
				if from := page.Entries[i].InheritedFrom; from != nil && from.IsServer() {
					inherited++
				}
			}

			if inherited != tc.wantRows {
				t.Fatalf(
					"rows inherited from @server = %d, want %d (entries %+v)",
					inherited,
					tc.wantRows,
					page.Entries,
				)
			}
		})
	}
}

// TestInstallationReachDiscoveryGrantID proves the grant ID discovery of a
// namespace: a grant on the installation never gives the ID of a grant, with
// the setting `true` or `false`, a grant on a collection of the namespace
// gives its ID in both cases, and a failed read of the setting gives an error
// and no ID.
func TestInstallationReachDiscoveryGrantID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		settings     reachReader
		onCollection bool
		wantID       bool
		wantErr      bool
	}{
		{
			name: "installation grant, setting true", settings: reachReader{value: new("true"), err: nil},
			onCollection: false, wantID: false, wantErr: false,
		},
		{
			name: "installation grant, setting false", settings: reachReader{value: new("false"), err: nil},
			onCollection: false, wantID: false, wantErr: false,
		},
		{
			name: "collection grant, setting false", settings: reachReader{value: new("false"), err: nil},
			onCollection: true, wantID: true, wantErr: false,
		},
		{
			name: "failed read", settings: reachReader{value: new("true"), err: errSettingsGone},
			onCollection: false, wantID: false, wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := collections{}
			f := newFixtureWithSettings(t, units, tc.settings)
			f.units = units
			holder := f.person(t, "holder")

			f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

			if tc.onCollection {
				payments := identity.CollectionObject("acme", "payments")
				f.collection(payments)
				f.grant(t, prn("holder"), access.RoleViewer, payments)
			}

			id, err := f.authz.DiscoveryGrantID(t.Context(), holder, "acme")
			if (err != nil) != tc.wantErr {
				t.Fatalf("discovery grant ID error = %v, want error %t", err, tc.wantErr)
			}

			if (id != "") != tc.wantID {
				t.Fatalf("discovery grant ID = %q, want an ID: %t", id, tc.wantID)
			}
		})
	}
}

// TestInstallationReachFailedReadDenies proves the fail-closed rule of the
// reach setting: a failed read and a value that is not a boolean deny the
// decision inside a namespace, with an error.
func TestInstallationReachFailedReadDenies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings reachReader
	}{
		{name: "a failed read", settings: reachReader{value: new("false"), err: errSettingsGone}},
		{name: "a value that is not a boolean", settings: reachReader{value: new("maybe"), err: nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureWithSettings(t, nil, tc.settings)
			holder := f.person(t, "holder")

			f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

			acme := identity.NamespaceObject("acme")

			err := f.authz.Authorize(t.Context(), holder, identity.ActionNamespaceRead, acme)
			if err == nil {
				t.Fatal("authorize on a namespace = nil, want a denial with an error")
			}
		})
	}
}

// TestInstallationReachNilPortKeepsTheDefault proves that an authorizer with no
// settings port reads the default `true`, so the grant on the installation
// reaches the namespace.
func TestInstallationReachNilPortKeepsTheDefault(t *testing.T) {
	t.Parallel()

	f := newFixtureWithSettings(t, nil, nil)
	holder := f.person(t, "holder")

	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	err := f.authz.Authorize(t.Context(), holder, identity.ActionNamespaceRead, identity.NamespaceObject("acme"))
	if err != nil {
		t.Fatalf("authorize on a namespace with no settings port = %v, want nil", err)
	}
}

// TestInstallationReachReadsTheNewValueOnTheNextRequest proves that a change of
// the setting takes effect on the next decision: the first decision reads
// `true` and allows, and the second reads `false` and denies.
func TestInstallationReachReadsTheNewValueOnTheNextRequest(t *testing.T) {
	t.Parallel()

	value := access.DefaultInstallationGrantsReachNamespaces
	f := newFixtureWithSettings(t, nil, reachReader{value: &value, err: nil})
	holder := f.person(t, "holder")
	acme := identity.NamespaceObject("acme")

	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	first := f.authz.Authorize(t.Context(), holder, identity.ActionNamespaceRead, acme)
	if first != nil {
		t.Fatalf("first decision with true = %v, want nil", first)
	}

	value = "false"

	second := f.authz.Authorize(t.Context(), holder, identity.ActionNamespaceRead, acme)
	if second == nil {
		t.Fatal("second decision with false = nil, want a denial")
	}
}

// TestInstallationReachActionScopes proves that each action of the families
// has one scope: the operator scope holds each operator action and the
// administration actions that act on the installation as a whole, and no other
// action. The list below is that table. An action that moves to the other
// side fails this test.
func TestInstallationReachActionScopes(t *testing.T) {
	t.Parallel()

	operatorAdministration := []identity.Action{
		identity.ActionServerReadPrincipals, identity.ActionServerManagePrincipals,
		identity.ActionServerManageAccess, identity.ActionServerReadAccess,
		identity.ActionServerCreateNamespace, identity.ActionServerDisableAuditChain,
		identity.ActionServerReadTokens, identity.ActionServerManageTokens,
		identity.Action("server:manage-service-tokens"),
		identity.ActionServerRestoreAccess,
	}

	families := []access.Family{
		access.FamilyOperator, access.FamilyAdministration, access.FamilyData, access.FamilyDelegation,
	}

	for _, family := range families {
		for _, action := range access.Actions(family) {
			want := family == access.FamilyOperator || slices.Contains(operatorAdministration, action)

			if got := access.OperatorScope(action); got != want {
				t.Errorf("OperatorScope(%s) = %t, want %t (family %s)", action, got, want, family)
			}
		}
	}

	for _, action := range operatorAdministration {
		if family, known := access.FamilyOf(action); !known || family != access.FamilyAdministration {
			t.Errorf("%s is listed as operator scope, but its family is %q, %t", action, family, known)
		}
	}
}
