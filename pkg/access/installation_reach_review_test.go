package access_test

import (
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// reviewPrincipal returns a service principal with the ID id in ns.
func reviewPrincipal(at time.Time, id, subject, ns string) principal.Principal {
	return principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: subject, Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: namespace.Name(ns), GroupCount: 0, BreakGlass: false,
	}
}

// createNamespacePrincipal stores a service principal in the namespace acme
// that the fixture already holds.
func createNamespacePrincipal(t *testing.T, f *fixture, at time.Time) {
	t.Helper()

	pNs := reviewPrincipal(at, "prn-ns", "ns", "acme")
	fence, entry := storetest.GrantWrite(t, f.store, identity.NamespaceObject("acme"), at, principal.Ref(pNs.ID))
	prErr := f.store.CreateNamespacePrincipal(t.Context(), pNs, fence, entry)
	if prErr != nil {
		t.Fatalf("create principal: %v", prErr)
	}
}

// checkNoInstallationHolder fails when a holder of a grant on @server is
// named.
func checkNoInstallationHolder(t *testing.T, mgrs []string) {
	t.Helper()

	if slices.Contains(mgrs, "principal:prn-holder") {
		t.Fatalf("TokenManagers of namespace principal with false names holder of @server grant: %v", mgrs)
	}
}

// managersOfNamespacePrincipal builds the fixture with the reach value and
// returns the token managers of a service principal of a namespace.
func managersOfNamespacePrincipal(t *testing.T, reaches bool) []string {
	t.Helper()

	f := newFixtureWithSettings(t, nil, access.FixedSettings(reaches))

	at := time.Unix(0, 0)

	f.person(t, "holder")
	f.grant(t, prn("holder"), access.RoleTokenManager, identity.ServerObject())

	createNamespacePrincipal(t, f, at)

	mgrs, mgrErr := f.svc.TokenManagers(t.Context(), "prn-ns")
	if mgrErr != nil {
		t.Fatalf("TokenManagers: %v", mgrErr)
	}

	return mgrs
}

// TestPrincipalTokenManagersInstallationReach checks the token managers of a
// service principal of a namespace for each value of the setting: with true,
// a holder of a grant on the installation is named; with false, it is not.
func TestPrincipalTokenManagersInstallationReach(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		reaches bool
	}{
		{name: "true", reaches: true},
		{name: "false", reaches: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mgrs := managersOfNamespacePrincipal(t, tc.reaches)
			if !tc.reaches {
				checkNoInstallationHolder(t, mgrs)

				return
			}

			if !slices.Contains(mgrs, "principal:prn-holder") {
				t.Fatalf("TokenManagers with true = %v, want principal:prn-holder", mgrs)
			}
		})
	}
}
