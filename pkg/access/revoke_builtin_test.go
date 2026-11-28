package access_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// builtinInstallationGrants returns the current grants on @server that a
// housekeeping principal holds, ordered by role.
func builtinInstallationGrants(t *testing.T, f *fixture) []access.Grant {
	t.Helper()

	list, _, err := f.store.ListGrantsOn(t.Context(), identity.ServerObject(), access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: access.MaxLimit,
	})
	if err != nil {
		t.Fatalf("list grants on @server: %v", err)
	}

	var out []access.Grant

	for i := range list {
		if g := list[i].Grant; strings.HasPrefix(g.Role, "housekeeping-") {
			out = append(out, g)
		}
	}

	return out
}

// builtinNamespaceGrants returns the grants of the housekeeping principals
// on the namespace name, ordered by role.
func builtinNamespaceGrants(t *testing.T, f *fixture, name namespace.Name) []access.Grant {
	t.Helper()

	got := housekeepingGrantsOn(t, f, name)

	out := make([]access.Grant, 0, len(got))
	for role := range got {
		out = append(out, got[role])
	}

	return out
}

// TestRevokeBuiltinNeedsManageAccess proves the revoke rule of a grant that a
// principal of the source builtin holds: the caller needs the manage-access
// action of the grant's object, and not every action that the grant gives.
// A caller without that action is refused, also when the grant gives only
// one action that the caller holds.
func TestRevokeBuiltinNeedsManageAccess(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	site := f.person(t, "site")
	f.grant(t, prn("site"), access.RoleSiteAdministrator, identity.ServerObject())

	account := f.person(t, "account")
	if _, err := f.svc.CreateNamespace(t.Context(), testEntry(), account, housekeepingNamespace("gamma")); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	checkBuiltinGrantCounts(t, f)
	revokeInstallationBuiltinGrants(t, f, site)
	revokeNamespaceBuiltinGrants(t, f, site)
	checkRefusedBuiltinRevoke(t, f)
}

// checkBuiltinGrantCounts proves that the fixture holds a grant on the
// installation for each purpose that acts there, and one on gamma for each
// purpose that acts inside a namespace.
func checkBuiltinGrantCounts(t *testing.T, f *fixture) {
	t.Helper()

	installation := builtinInstallationGrants(t, f)
	namespaces := builtinNamespaceGrants(t, f, "gamma")

	wantInstallation, wantNamespaces := 0, 0

	for _, p := range principal.HousekeepingPurposes() {
		if p.ActsOnInstallation() {
			wantInstallation++
		}

		if p.ActsInsideNamespace() {
			wantNamespaces++
		}
	}

	if len(installation) != wantInstallation || len(namespaces) != wantNamespaces {
		t.Fatalf("builtin grants = %d on @server and %d on gamma, want %d and %d",
			len(installation), len(namespaces), wantInstallation, wantNamespaces)
	}
}

// revokeInstallationBuiltinGrants revokes each housekeeping grant on @server
// as the stored site administrator and checks that each grant is gone.
func revokeInstallationBuiltinGrants(t *testing.T, f *fixture, site identity.Principal) {
	t.Helper()

	grants := builtinInstallationGrants(t, f)

	for i := range grants {
		g := grants[i]
		if _, err := f.svc.Revoke(t.Context(), testEntry(), site, identity.ServerObject(), g.ID); err != nil {
			t.Fatalf("stored site administrator revoke of %s on @server: %v", g.Role, err)
		}

		if _, err := f.store.GetGrant(t.Context(), g.ID); !errors.Is(err, access.ErrNotFound) {
			t.Fatalf("grant %s after the revoke = %v, want ErrNotFound", g.ID, err)
		}
	}
}

// revokeNamespaceBuiltinGrants revokes each housekeeping grant on gamma as
// the stored site administrator.
func revokeNamespaceBuiltinGrants(t *testing.T, f *fixture, site identity.Principal) {
	t.Helper()

	grants := builtinNamespaceGrants(t, f, "gamma")

	for i := range grants {
		g := grants[i]
		obj := identity.NamespaceObject("gamma")
		if _, err := f.svc.Revoke(t.Context(), testEntry(), site, obj, g.ID); err != nil {
			t.Fatalf("stored site administrator revoke of %s on gamma: %v", g.Role, err)
		}
	}
}

// checkRefusedBuiltinRevoke proves that a caller with the action of a
// housekeeping grant but without the manage-access action of its object is
// refused, and that the grant stays.
func checkRefusedBuiltinRevoke(t *testing.T, f *fixture) {
	t.Helper()

	ctx := t.Context()

	// The bootstrap administrator made the role, so the caller holds no
	// other action.
	role, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "token-policy-only", Description: "", Actions: []identity.Action{identity.ActionServerManageTokenPolicy},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	policy := f.person(t, "policy")
	f.grant(t, prn("policy"), role.ID, identity.ServerObject())

	// Give the token-sweep grant back to the builtin principal, so that the
	// caller has a grant to revoke.
	tokenSweep := principal.HousekeepingPurposes()[0]
	for _, p := range principal.HousekeepingPurposes() {
		if p.InstallationRole() == access.RoleHousekeepingTokenSweep {
			tokenSweep = p
		}
	}

	g, err := f.svc.Grant(ctx, testEntry(), admin(), access.GrantInput{
		Object: identity.ServerObject(), Role: access.RoleHousekeepingTokenSweep,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: tokenSweep.ID()},
	})
	if err != nil {
		t.Fatalf("grant the token-sweep role to the builtin principal: %v", err)
	}

	_, err = f.svc.Revoke(ctx, testEntry(), policy, identity.ServerObject(), g.ID)
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("revoke by a caller with the grant action but no manage-access = %v, want identity.ErrDenied", err)
	}

	if _, err := f.store.GetGrant(ctx, g.ID); err != nil {
		t.Fatalf("grant %s after the refused revoke = %v, want it to stay", g.ID, err)
	}
}

// TestRevokeOfAnotherSourceKeepsEscalation proves that the builtin rule
// changes nothing for a grant whose holder has another source: the caller
// still needs every action that the grant gives.
func TestRevokeOfAnotherSourceKeepsEscalation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	f.person(t, "holder")
	f.grant(t, prn("holder"), access.RoleHousekeepingTokenSweep, identity.ServerObject())

	role, err := f.svc.CreateRole(ctx, testEntry(), admin(), access.RoleInput{
		Name: "manage-access-only", Description: "", Actions: []identity.Action{identity.ActionServerManageAccess},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	caller := f.person(t, "caller")
	f.grant(t, prn("caller"), role.ID, identity.ServerObject())

	list, _, err := f.store.ListGrantsOn(ctx, identity.ServerObject(), access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: access.MaxLimit,
	})
	if err != nil {
		t.Fatalf("list grants on @server: %v", err)
	}

	var target access.Grant

	for i := range list {
		g := list[i].Grant
		if g.Subject == prn("holder") && g.Role == access.RoleHousekeepingTokenSweep {
			target = g
		}
	}

	if target.ID == "" {
		t.Fatal("no grant of the token-sweep role on @server for the holder")
	}

	_, err = f.svc.Revoke(ctx, testEntry(), caller, identity.ServerObject(), target.ID)
	if !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("revoke of a grant of a local holder = %v, want ErrEscalation", err)
	}
}

// TestDeleteBuiltinPrincipalIsRefused proves that a principal of the source
// builtin cannot be deleted, also by an administrator. Its grants stay.
func TestDeleteBuiltinPrincipalIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	principals := f.principals(t)

	const id = "prn-housekeeping-token-sweep"

	site := f.person(t, "site-delete")
	f.grant(t, prn("site-delete"), access.RoleSiteAdministrator, identity.ServerObject())

	for _, tc := range []struct {
		name   string
		caller identity.Principal
	}{
		{name: "bootstrap administrator", caller: admin()},
		{name: "stored site administrator", caller: site},
	} {
		err := principals.DeletePrincipal(ctx, testEntry(), tc.caller, id)
		if !errors.Is(err, principal.ErrReservedSubject) {
			t.Fatalf("%s deletes the builtin principal = %v, want %v",
				tc.name, err, principal.ErrReservedSubject)
		}

		if _, err := principals.GetPrincipal(ctx, id); err != nil {
			t.Fatalf("builtin principal after the refused delete by the %s = %v, want it to stay", tc.name, err)
		}
	}
}
