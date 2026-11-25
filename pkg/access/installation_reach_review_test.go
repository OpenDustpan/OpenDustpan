package access_test

import (
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

// createInstallationPrincipal stores a service principal of the installation.
func createInstallationPrincipal(t *testing.T, f *fixture, at time.Time) {
	t.Helper()

	pInst := reviewPrincipal(at, "prn-inst", "inst", "")
	createErr := f.store.CreatePrincipal(
		t.Context(),
		pInst,
		storetest.ServerChange(at, "principal:prn-inst"),
	)
	if createErr != nil {
		t.Fatalf("create principal: %v", createErr)
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

// checkNoServerHolder fails when a holder of a grant on @server is named.
func checkNoServerHolder(t *testing.T, mgrs []string) {
	t.Helper()

	for _, m := range mgrs {
		if m == "prn-holder" || m == "holder" {
			t.Fatalf(
				"TokenManagers of namespace principal with false names holder of @server grant: %v",
				mgrs,
			)
		}
	}
}

// managersOfNamespacePrincipal builds the fixture with the reach value and
// returns the token managers of a service principal of a namespace.
func managersOfNamespacePrincipal(t *testing.T, reaches bool) []string {
	t.Helper()

	settings := access.FixedSettings(reaches)
	f := newFixtureWithSettings(t, nil, settings)

	at := time.Unix(0, 0)

	createInstallationPrincipal(t, f, at)
	f.grant(t, prn("holder"), access.RoleViewer, identity.ServerObject())

	mgrs, mgrErr := f.svc.TokenManagers(t.Context(), "prn-inst")
	if mgrErr != nil {
		t.Fatalf("TokenManagers: %v", mgrErr)
	}
	_ = mgrs

	createNamespacePrincipal(t, f, at)

	mgrsNs, nsErr := f.svc.TokenManagers(t.Context(), "prn-ns")
	if nsErr != nil {
		t.Fatalf("TokenManagers: %v", nsErr)
	}

	return mgrsNs
}

// TestPrincipalReviewScopeInstallationReach checks the token managers of a
// service principal for each value of the setting.
func TestPrincipalReviewScopeInstallationReach(t *testing.T) {
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

			mgrsNs := managersOfNamespacePrincipal(t, tc.reaches)
			if !tc.reaches {
				checkNoServerHolder(t, mgrsNs)
			}
		})
	}
}
