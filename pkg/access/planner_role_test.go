package access_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestPlannerRole pins what the built-in planner role can and cannot do.
func TestPlannerRole(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action identity.Action
		want   bool
	}{
		{identity.ActionNamespaceRead, true},
		{identity.ActionCollectionRead, true},
		{identity.ActionUnitRead, true},
		{identity.ActionUnitReadOutputs, true},
		{identity.ActionProfileRead, true},
		{identity.ActionNamespaceReadGates, true},
		{identity.ActionViewCreate, true},
		{identity.ActionViewRead, true},
		{identity.ActionViewDelete, true},
		{identity.ActionUnitRunPlan, true},
		{identity.ActionJobReadPlans, true},
		{identity.ActionUnitRunApply, false},
		{identity.ActionUnitRunDestroy, false},
		{identity.ActionUnitWriteBack, false},
		{identity.ActionUnitDefineSteps, false},
		{identity.ActionJobApprove, false},
		{identity.ActionJobCancel, false},
		{identity.ActionUnitReadSecrets, false},
		{identity.ActionUnitWrite, false},
		{identity.ActionUnitEncrypt, false},
		{identity.ActionUnitDecrypt, false},
		{identity.ActionUnitSetContext, false},
		{identity.ActionUnitManage, false},
		{identity.ActionUnitLock, false},
		{identity.ActionNamespaceReadAudit, false},
		{identity.ActionNamespaceReadAccess, false},
	}

	role, ok := access.BuiltInRole(access.RolePlanner)
	if !ok {
		t.Fatalf("no built-in role %q", access.RolePlanner)
	}

	if !role.BuiltIn {
		t.Error("planner BuiltIn = false, want true")
	}

	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			t.Parallel()

			if got := role.Has(tc.action); got != tc.want {
				t.Errorf("planner holds %s = %t, want %t", tc.action, got, tc.want)
			}
		})
	}
}

// TestPlannerActionSet pins the actions of planner as a set: exactly the
// actions of viewer, unit:run-plan and job:read-plans. A new action in the
// role, or a viewer action that leaves it, fails this test.
func TestPlannerActionSet(t *testing.T) {
	t.Parallel()

	viewer, _ := access.BuiltInRole(access.RoleViewer)
	planner, _ := access.BuiltInRole(access.RolePlanner)

	want := slices.Clone(viewer.Actions)
	want = append(want, identity.ActionUnitRunPlan, identity.ActionJobReadPlans)
	slices.Sort(want)

	got := slices.Clone(planner.Actions)
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("planner actions = %v, want the viewer actions, unit:run-plan and job:read-plans: %v", got, want)
	}
}

// TestPlannerHoldsEveryViewerAction pins that planner reads at least what a
// viewer reads, so a new viewer action reaches the planner too.
func TestPlannerHoldsEveryViewerAction(t *testing.T) {
	t.Parallel()

	viewer, _ := access.BuiltInRole(access.RoleViewer)
	planner, _ := access.BuiltInRole(access.RolePlanner)

	for _, a := range viewer.Actions {
		if !planner.Has(a) {
			t.Errorf("planner lacks the viewer action %s", a)
		}
	}
}
