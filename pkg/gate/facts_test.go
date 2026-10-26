package gate_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

func TestSchemaVersion(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"change.planned/1.0", "state.write/1.0", "access/1.0", "profile.open/0.12", "change.apply/10.3"} {
		v, err := gate.ParseSchemaVersion(in)
		if err != nil || v.String() != in {
			t.Fatalf("ParseSchemaVersion(%q) = %v, %v", in, v, err)
		}
	}

	v, err := gate.ParseSchemaVersion("change.planned/2.1")
	if err != nil || v.Subject != "change.planned" || v.Major != 2 || v.Minor != 1 {
		t.Fatalf("ParseSchemaVersion = %#v, %v", v, err)
	}

	for _, in := range []string{
		"", "change.planned", "change.planned/1", "change.planned/1.", "change.planned/.1",
		"change.planned/01.0", "change.planned/1.00", "change.planned/-1.0", "change.planned/1.0.0",
		"change.planned/+1.0", "job.start/1.0", "change.planned/1.a", "change.planned/1234567.0",
	} {
		if _, err := gate.ParseSchemaVersion(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseSchemaVersion(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestGroupsOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		point   gate.DecisionPoint
		count   int
		holds   []gate.Group
		lacks   []gate.Group
		comment string
	}{
		{
			gate.PointChangePropose, 8,
			[]gate.Group{gate.GroupChange, gate.GroupQuota, gate.GroupPaths},
			[]gate.Group{gate.GroupPlan, gate.GroupCost, gate.GroupState},
			"before a plan",
		},
		{
			gate.PointChangePlanned,
			15,
			[]gate.Group{gate.GroupPlan, gate.GroupCost, gate.GroupDependents},
			[]gate.Group{gate.GroupState},
			"after the plan",
		},
		{
			gate.PointChangeApply,
			15,
			[]gate.Group{gate.GroupPlan, gate.GroupExternalChecks},
			[]gate.Group{gate.GroupState},
			"as change.planned",
		},
		{
			gate.PointStateWrite, 7,
			[]gate.Group{gate.GroupPlan, gate.GroupState, gate.GroupDocument},
			[]gate.Group{gate.GroupChange, gate.GroupCost, gate.GroupPaths},
			"no plan",
		},
		{gate.PointProfileSave, 1, []gate.Group{gate.GroupProfile}, []gate.Group{gate.GroupPlan}, "profile facts"},
		{gate.PointProfileOpen, 1, []gate.Group{gate.GroupProfile}, []gate.Group{gate.GroupPlan}, "profile facts"},
	}

	for _, tc := range cases {
		t.Run(string(tc.point), func(t *testing.T) {
			t.Parallel()

			gs := gate.GroupsOf(tc.point)
			if len(gs) != tc.count {
				t.Fatalf("GroupsOf(%q) has %d groups, want %d (%s)", tc.point, len(gs), tc.count, tc.comment)
			}

			checkGroups(t, tc.point, gs, tc.holds, tc.lacks)
		})
	}

	if gate.GroupsOf("job.start") != nil {
		t.Fatal("an unknown point has no groups")
	}
}

// checkGroups checks that gs holds each group of holds and none of lacks.
func checkGroups(t *testing.T, point gate.DecisionPoint, gs, holds, lacks []gate.Group) {
	t.Helper()

	for _, g := range holds {
		if !slices.Contains(gs, g) {
			t.Fatalf("GroupsOf(%q) lacks %q", point, g)
		}
	}

	for _, g := range lacks {
		if slices.Contains(gs, g) {
			t.Fatalf("GroupsOf(%q) holds %q", point, g)
		}
	}
}

func TestHasAvailable(t *testing.T) {
	t.Parallel()

	with := []gate.Group{
		gate.GroupPlan, gate.GroupCost, gate.GroupBudget, gate.GroupDrift, gate.GroupResourcePolicy,
		gate.GroupQuota, gate.GroupDependents, gate.GroupPaths, gate.GroupState,
	}
	without := []gate.Group{
		gate.GroupPrincipal, gate.GroupSource, gate.GroupUnit, gate.GroupOperation, gate.GroupChange,
		gate.GroupExternalChecks, gate.GroupDocument, gate.GroupProfile,
	}

	for _, g := range with {
		if !g.HasAvailable() {
			t.Fatalf("%q has the flag available", g)
		}
	}

	for _, g := range without {
		if g.HasAvailable() {
			t.Fatalf("%q has no flag available", g)
		}
	}
}

func TestParseFreshAuth(t *testing.T) {
	t.Parallel()

	for _, s := range []int{0, 60, 300, 900} {
		f, err := gate.ParseFreshAuth(s)
		if err != nil || !f.Set || f.Seconds != s {
			t.Fatalf("ParseFreshAuth(%d) = %#v, %v", s, f, err)
		}
	}

	for _, s := range []int{-1, 1, 59, 901, 86400} {
		if _, err := gate.ParseFreshAuth(s); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseFreshAuth(%d): err = %v, want ErrInvalid", s, err)
		}
	}

	if gate.NoFreshAuth().Set {
		t.Fatal("NoFreshAuth asks for no fresh authentication")
	}
}

func TestParseCarryOver(t *testing.T) {
	t.Parallel()

	inst := gate.InstallationScope()
	ns := gate.NamespaceScope("acme")

	cases := []struct {
		scope gate.Scope
		in    string
		want  bool
	}{
		{ns, "off", true},
		{ns, "on", true},
		{ns, "forbidden", true},
		{inst, "off", true},
		{inst, "forbidden", true},
		{inst, "on", false},
		{ns, "", false},
		{ns, "On", false},
	}

	for _, tc := range cases {
		got, err := gate.ParseCarryOver(tc.scope, tc.in)
		if tc.want && (err != nil || string(got) != tc.in) {
			t.Fatalf("ParseCarryOver(%s, %q) = %q, %v", tc.scope, tc.in, got, err)
		}

		if !tc.want && !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseCarryOver(%s, %q): err = %v, want ErrInvalid", tc.scope, tc.in, err)
		}
	}
}

func TestApproversEmpty(t *testing.T) {
	t.Parallel()

	if !(gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: false}).Empty() {
		t.Fatal("no entry is empty")
	}

	if (gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UnitOwners: true}).Empty() {
		t.Fatal("unitOwners is an entry")
	}
}
