package access_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRunnerAndStepActionsInBuiltInRoles pins the family of the runner and
// step actions, and the built-in roles that hold each of them. Each row
// fails when the family entry or the role entry is removed.
func TestRunnerAndStepActionsInBuiltInRoles(t *testing.T) {
	t.Parallel()

	actions := []identity.Action{
		identity.ActionNamespaceReadRunners,
		identity.ActionNamespaceManageRunners,
		identity.ActionUnitDefineSteps,
	}

	for _, a := range actions {
		if family, ok := access.FamilyOf(a); !ok || family != access.FamilyData {
			t.Errorf("family of %s = %q, %t, want %q", a, family, ok, access.FamilyData)
		}
	}

	read := []string{
		access.RoleViewer, access.RoleSecretViewer, access.RolePlanner, access.RoleAuditor,
		access.RoleWriter, access.RoleMaintainer, access.RoleAdmin,
		access.RoleAccountAdministrator, access.RoleSiteAdministrator,
	}
	manage := []string{
		access.RoleMaintainer, access.RoleAdmin,
		access.RoleAccountAdministrator, access.RoleSiteAdministrator,
	}
	roles := []string{
		access.RoleViewer, access.RoleSecretViewer, access.RolePlanner, access.RoleAuditor,
		access.RoleWriter, access.RoleMaintainer, access.RoleAdmin,
		access.RoleAccountAdministrator, access.RoleSiteAdministrator,
		access.RoleTokenManager, access.RoleInstallationOperator,
	}

	for _, id := range roles {
		r, ok := access.BuiltInRole(id)
		if !ok {
			t.Fatalf("no built-in role %q", id)
		}

		for _, tc := range []struct {
			action identity.Action
			holds  []string
		}{
			{identity.ActionNamespaceReadRunners, read},
			{identity.ActionNamespaceManageRunners, manage},
			{identity.ActionUnitDefineSteps, manage},
		} {
			if got, want := slices.Contains(r.Actions, tc.action), slices.Contains(tc.holds, id); got != want {
				t.Errorf("%s holds %s = %t, want %t", id, tc.action, got, want)
			}
		}
	}
}

// TestSiteAdministratorPassesRunnerAndStepActions proves the policy check:
// a site administrator passes the three actions on a namespace, and a viewer
// does not pass the manage actions.
func TestSiteAdministratorPassesRunnerAndStepActions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	site := f.person(t, "site")
	f.grant(t, prn("site"), access.RoleSiteAdministrator, identity.ServerObject())

	viewer := f.person(t, "viewer")
	f.grant(t, prn("viewer"), access.RoleViewer, identity.NamespaceObject("acme"))

	ns := identity.NamespaceObject("acme")

	assertChecks(t, f.authz, site, []check{
		{ns, identity.ActionNamespaceReadRunners, true},
		{ns, identity.ActionNamespaceManageRunners, true},
		{ns, identity.ActionUnitDefineSteps, true},
	})
	assertChecks(t, f.authz, viewer, []check{
		{ns, identity.ActionNamespaceReadRunners, true},
		{ns, identity.ActionNamespaceManageRunners, false},
		{ns, identity.ActionUnitDefineSteps, false},
	})
}
