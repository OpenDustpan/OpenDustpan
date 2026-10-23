package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestUnitRunPlanBuiltInRoles pins which built-in role holds unit:run-plan.
// The action is in the data family, so the roles that hold the whole data
// family hold it. Every other built-in role does not.
func TestUnitRunPlanBuiltInRoles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role string
		want bool
	}{
		{role: access.RoleViewer, want: false},
		{role: access.RoleAuditor, want: false},
		{role: access.RoleSecretViewer, want: false},
		{role: access.RoleWriter, want: false},
		{role: access.RoleMaintainer, want: false},
		{role: access.RoleAdmin, want: true},
		{role: access.RoleInstallationOperator, want: false},
		{role: access.RoleAccountAdministrator, want: true},
		{role: access.RoleSiteAdministrator, want: true},
		{role: access.RoleTokenManager, want: false},
	}

	if got, want := len(cases), len(access.BuiltInRoles()); got != want {
		t.Fatalf("table has %d roles, catalog has %d", got, want)
	}

	for _, tc := range cases {
		r, ok := access.BuiltInRole(tc.role)
		if !ok {
			t.Fatalf("no built-in role %q", tc.role)
		}

		if got := r.Has(identity.ActionUnitRunPlan); got != tc.want {
			t.Errorf("%s holds unit:run-plan = %t, want %t", tc.role, got, tc.want)
		}
	}
}
