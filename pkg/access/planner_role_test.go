package access_test

import (
	"errors"
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
		{identity.ActionJobReadLogs, true},
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
// actions of viewer, unit:run-plan, job:read-plans and job:read-logs. A new action in the
// role, or a viewer action that leaves it, fails this test.
func TestPlannerActionSet(t *testing.T) {
	t.Parallel()

	viewer, _ := access.BuiltInRole(access.RoleViewer)
	planner, _ := access.BuiltInRole(access.RolePlanner)

	want := slices.Clone(viewer.Actions)
	want = append(want, identity.ActionUnitRunPlan, identity.ActionJobReadPlans, identity.ActionJobReadLogs)
	slices.Sort(want)

	got := slices.Clone(planner.Actions)
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf(
			"planner actions = %v, want the viewer actions, unit:run-plan, job:read-plans and job:read-logs: %v",
			got,
			want,
		)
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

// TestPlannerAccessDecision proves the Authorizer decision for a planner:
// planner reads are allowed, writes, reveal and a token for another
// principal are refused with access.denied (identity.ErrDenied).
func TestPlannerAccessDecision(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	dev := f.person(t, "planner-dev")
	f.person(t, "planner-other")
	unit := unitObject(t, "acme", "web/prod")
	f.grant(t, prn("planner-dev"), access.RolePlanner, identity.NamespaceObject("acme"))

	assertChecks(t, f.authz, dev, []check{
		{obj: unit, action: identity.ActionUnitRead, allow: true},
		{obj: unit, action: identity.ActionUnitReadOutputs, allow: true},
		{obj: unit, action: identity.ActionUnitRunPlan, allow: true},
		{obj: unit, action: identity.ActionUnitRunApply, allow: false},
		{obj: unit, action: identity.ActionUnitRunDestroy, allow: false},
		{obj: unit, action: identity.ActionUnitReadSecrets, allow: false},
		{obj: unit, action: identity.ActionUnitWrite, allow: false},
		{obj: unit, action: identity.ActionUnitDecrypt, allow: false},
	})

	err := f.authz.Authorize(
		t.Context(),
		dev,
		identity.ActionPrincipalManageTokens,
		identity.PrincipalObject("prn-planner-other"),
	)
	if !errors.Is(err, identity.ErrDenied) {
		t.Errorf("planner token for another principal = %v, want ErrDenied", err)
	}
}

// TestPlannerHostileInputLimited proves the fail-closed layers for a
// planner: an unknown action is denied, and a grant never reaches outside
// its object.
func TestPlannerHostileInputLimited(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	dev := f.person(t, "planner-hostile")
	unit := unitObject(t, "acme", "web/prod")
	f.grant(t, prn("planner-hostile"), access.RolePlanner, identity.NamespaceObject("acme"))

	if err := f.authz.Authorize(t.Context(), dev, identity.Action("unit:unknown-action"), unit); !errors.Is(
		err,
		identity.ErrDenied,
	) {
		t.Errorf("unknown action = %v, want ErrDenied", err)
	}
}

// TestPlannerGrantContainment proves a planner grant on one collection never
// reaches a unit of another collection.
func TestPlannerGrantContainment(t *testing.T) {
	t.Parallel()

	home := unitObject(t, "acme", "web/prod")
	away := unitObject(t, "acme", "api/prod")
	mapping := collections{home.String(): "team-a", away.String(): "team-b"}
	f := newFixtureWithCollections(t, mapping)
	f.units = mapping
	dev := f.person(t, "planner-contained")
	f.grant(t, prn("planner-contained"), access.RolePlanner, identity.CollectionObject("acme", "team-a"))

	assertChecks(t, f.authz, dev, []check{
		{obj: home, action: identity.ActionUnitRead, allow: true},
		{obj: away, action: identity.ActionUnitRead, allow: false},
		{obj: away, action: identity.ActionUnitRunPlan, allow: false},
	})
}
