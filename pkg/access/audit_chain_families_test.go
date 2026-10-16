package access_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestAuditChainFamilies pins the family of each audit chain action.
func TestAuditChainFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action identity.Action
		want   access.Family
	}{
		{action: identity.ActionServerSetAuditChain, want: access.FamilyOperator},
		{action: identity.ActionServerDisableAuditChain, want: access.FamilyAdministration},
	}

	for _, tc := range cases {
		got, ok := access.FamilyOf(tc.action)
		if !ok {
			t.Errorf("family of %q is not classified, want %q", tc.action, tc.want)

			continue
		}

		if got != tc.want {
			t.Errorf("family of %q = %q, want %q", tc.action, got, tc.want)
		}
	}
}

// TestAuditChainRoleHoldings pins which of the three roles holds each audit
// chain action.
func TestAuditChainRoleHoldings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role        string
		wantSet     bool
		wantDisable bool
	}{
		{role: access.RoleInstallationOperator, wantSet: true, wantDisable: false},
		{role: access.RoleAccountAdministrator, wantSet: false, wantDisable: true},
		{role: access.RoleSiteAdministrator, wantSet: true, wantDisable: true},
	}

	for _, tc := range cases {
		r, ok := access.BuiltInRole(tc.role)
		if !ok {
			t.Fatalf("no built-in role %q", tc.role)
		}

		if got := r.Has(identity.ActionServerSetAuditChain); got != tc.wantSet {
			t.Errorf("%s holds set-audit-chain = %t, want %t", tc.role, got, tc.wantSet)
		}

		if got := r.Has(identity.ActionServerDisableAuditChain); got != tc.wantDisable {
			t.Errorf("%s holds disable-audit-chain = %t, want %t", tc.role, got, tc.wantDisable)
		}
	}
}

// TestOnlySiteAdministratorHoldsBothAuditChainActions checks every built-in
// role and wants only the site administrator to hold both actions.
func TestOnlySiteAdministratorHoldsBothAuditChainActions(t *testing.T) {
	t.Parallel()

	roles := access.BuiltInRoles()

	for i := range roles {
		r := &roles[i]
		got := r.Has(identity.ActionServerSetAuditChain) && r.Has(identity.ActionServerDisableAuditChain)
		want := r.ID == access.RoleSiteAdministrator

		if got != want {
			t.Errorf("role %q holds both audit chain actions = %t, want %t", r.ID, got, want)
		}
	}
}
