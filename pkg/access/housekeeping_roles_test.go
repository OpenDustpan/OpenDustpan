package access_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// housekeepingRoleCase is one row of the table "Roles of the housekeeping
// principals": the role and the exact actions that it holds.
type housekeepingRoleCase struct {
	role    string
	actions []identity.Action
}

// housekeepingRoles returns the rows of the table, in name order.
func housekeepingRoles() []housekeepingRoleCase {
	return []housekeepingRoleCase{
		{
			role: access.RoleHousekeepingAgentReaper,
			actions: []identity.Action{
				identity.ActionNamespaceManageAgentRuns,
				identity.ActionServerSweepAgentReveals,
			},
		},
		{
			role:    access.RoleHousekeepingArtifactSweep,
			actions: []identity.Action{identity.ActionServerSweepArtifacts},
		},
		{
			role:    access.RoleHousekeepingBlobSweep,
			actions: []identity.Action{identity.ActionServerSweepBlobs},
		},
		{
			role:    access.RoleHousekeepingCheckpointSweep,
			actions: []identity.Action{identity.ActionServerSweepCheckpoints},
		},
		{
			role:    access.RoleHousekeepingHistoryRetention,
			actions: []identity.Action{identity.ActionUnitRedact},
		},
		{
			role:    access.RoleHousekeepingIssuerRotation,
			actions: []identity.Action{identity.ActionNamespaceManageIssuer},
		},
		{
			role:    access.RoleHousekeepingJobReconcile,
			actions: []identity.Action{identity.ActionJobReconcile},
		},
		{
			role:    access.RoleHousekeepingNameLockPrune,
			actions: []identity.Action{identity.ActionServerPruneNameLocks},
		},
		{
			role:    access.RoleHousekeepingSavedViewCleanup,
			actions: []identity.Action{identity.ActionViewDelete},
		},
		{
			role:    access.RoleHousekeepingTempAdminExpiry,
			actions: []identity.Action{identity.ActionServerExpireTemporaryAdministrators},
		},
		{
			role:    access.RoleHousekeepingTokenSweep,
			actions: []identity.Action{identity.ActionServerManageTokenPolicy},
		},
	}
}

func TestHousekeepingRoles(t *testing.T) {
	t.Parallel()

	for _, tc := range housekeepingRoles() {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()

			checkHousekeepingRole(t, tc)
		})
	}
}

// checkHousekeepingRole checks that the role of tc holds exactly the actions
// of its row of the table, each one once.
func checkHousekeepingRole(t *testing.T, tc housekeepingRoleCase) {
	t.Helper()

	r, ok := access.BuiltInRole(tc.role)
	if !ok {
		t.Fatalf("no built-in role %q", tc.role)
	}

	if len(r.Actions) != len(tc.actions) {
		t.Fatalf("role %q action count = %d, want %d", tc.role, len(r.Actions), len(tc.actions))
	}

	got := slices.Clone(r.Actions)
	want := slices.Clone(tc.actions)
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("role %q actions = %v, want %v", tc.role, got, want)
	}
}

// TestHousekeepingRolesHoldNoForbiddenAction checks that no housekeeping role
// holds a delegation action, a token action or server:manage-principals.
func TestHousekeepingRolesHoldNoForbiddenAction(t *testing.T) {
	t.Parallel()

	housekeeping := make(map[string]bool, len(housekeepingRoles()))
	for _, tc := range housekeepingRoles() {
		housekeeping[tc.role] = true
	}

	forbidden := map[identity.Action]bool{
		identity.ActionServerManagePrincipals: true,
		identity.ActionServerReadTokens:       true,
		identity.ActionServerManageTokens:     true,
	}
	for _, a := range access.Actions(access.FamilyDelegation) {
		forbidden[a] = true
	}

	for _, r := range access.BuiltInRoles() {
		if !housekeeping[r.ID] {
			continue
		}

		for _, a := range r.Actions {
			if forbidden[a] {
				t.Errorf("housekeeping role %q holds forbidden action %s", r.ID, a)
			}
		}
	}
}

// TestHousekeepingActionsInNoOtherRole checks that the actions that exist for
// the housekeeping roles are in no other built-in role. The actions
// view:delete, namespace:manage-issuer and server:manage-token-policy are not
// in the list: roles of main hold them already.
func TestHousekeepingActionsInNoOtherRole(t *testing.T) {
	t.Parallel()

	owner := make(map[identity.Action]string)

	for _, tc := range housekeepingRoles() {
		for _, a := range tc.actions {
			owner[a] = tc.role
		}
	}

	exclusive := []identity.Action{
		identity.ActionNamespaceManageAgentRuns,
		identity.ActionServerSweepAgentReveals,
		identity.ActionServerSweepArtifacts,
		identity.ActionServerSweepBlobs,
		identity.ActionServerSweepCheckpoints,
		identity.ActionUnitRedact,
		identity.ActionJobReconcile,
		identity.ActionServerPruneNameLocks,
		identity.ActionServerExpireTemporaryAdministrators,
	}

	for _, a := range exclusive {
		if owner[a] == "" {
			t.Fatalf("action %s is in no housekeeping role", a)
		}
	}

	for _, r := range access.BuiltInRoles() {
		for _, a := range r.Actions {
			want, isExclusive := owner[a], slices.Contains(exclusive, a)
			if isExclusive && r.ID != want {
				t.Errorf("role %q holds %s, which only role %q holds", r.ID, a, want)
			}
		}
	}
}

// TestHousekeepingActionFamilies checks the family of each action of the
// table: the two actions that the family map classifies and the seven new
// actions.
func TestHousekeepingActionFamilies(t *testing.T) {
	t.Parallel()

	checks := []struct {
		action identity.Action
		family access.Family
	}{
		{action: identity.ActionJobReconcile, family: access.FamilyData},
		{action: identity.ActionUnitRedact, family: access.FamilyData},
		{action: identity.ActionNamespaceManageAgentRuns, family: access.FamilyData},
		{action: identity.ActionServerSweepArtifacts, family: access.FamilyOperator},
		{action: identity.ActionServerSweepBlobs, family: access.FamilyOperator},
		{action: identity.ActionServerSweepCheckpoints, family: access.FamilyOperator},
		{action: identity.ActionServerPruneNameLocks, family: access.FamilyOperator},
		{action: identity.ActionServerExpireTemporaryAdministrators, family: access.FamilyOperator},
		{action: identity.ActionServerSweepAgentReveals, family: access.FamilyOperator},
	}

	for _, tc := range checks {
		got, ok := access.FamilyOf(tc.action)
		if !ok || got != tc.family {
			t.Errorf("FamilyOf(%q) = (%q,%v), want (%q,true)", tc.action, got, ok, tc.family)
		}
	}
}
