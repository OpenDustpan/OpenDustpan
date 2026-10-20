package gate_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

func def(scope gate.Scope, version int64, count int, groups ...string) gate.ApprovalDefault {
	return gate.ApprovalDefault{
		Scope: scope, Version: version, Count: count, FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{Groups: groups, Users: nil, UnitOwners: false}, Unless: "", Reads: nil,
	}
}

// want returns the requirement that a test expects, with the fields that no
// default sets at their zero value.
func want(
	name gate.RequirementName,
	version int64,
	count int,
	fresh gate.FreshAuth,
	groups ...string,
) gate.Requirement {
	return gate.Requirement{
		Name: name, Version: version, Approvers: gate.Approvers{Groups: groups, Users: nil, UnitOwners: false},
		Count: count, AuthorMayApprove: false, FreshAuth: fresh, TimeoutSeconds: 604800, Signatures: nil,
		Evaluate: false,
	}
}

func TestDefaultRequirementsMergeAndOrder(t *testing.T) {
	t.Parallel()

	ns := gate.NamespaceScope("acme")
	col := gate.CollectionScope("acme", "prod")
	none := gate.NoFreshAuth()

	window := func(d gate.ApprovalDefault, seconds int) gate.ApprovalDefault {
		d.FreshAuth = gate.FreshAuth{Set: true, Seconds: seconds}

		return d
	}

	tests := []struct {
		name     string
		defaults []gate.ApprovalDefault
		want     []gate.Requirement
	}{
		{"none", nil, nil},
		{
			"one default with no approvers",
			[]gate.ApprovalDefault{def(ns, 3, 2)},
			[]gate.Requirement{want("default", 3, 2, none)},
		},
		{
			"the highest count wins and gives the version",
			[]gate.ApprovalDefault{def(ns, 3, 2), def(col, 5, 4)},
			[]gate.Requirement{want("default", 5, 4, none)},
		},
		{
			"a narrower default cannot lower the count",
			[]gate.ApprovalDefault{def(ns, 3, 4), def(col, 5, 1)},
			[]gate.Requirement{want("default", 3, 4, none)},
		},
		{
			"the shortest window wins, and none is the longest",
			[]gate.ApprovalDefault{def(ns, 1, 1), window(def(col, 2, 1), 600), window(def(col, 3, 1), 120)},
			[]gate.Requirement{want("default", 1, 1, gate.FreshAuth{Set: true, Seconds: 120})},
		},
		{
			"the window 0 is the shortest",
			[]gate.ApprovalDefault{window(def(ns, 1, 1), 60), window(def(col, 2, 1), 0)},
			[]gate.Requirement{want("default", 1, 1, gate.FreshAuth{Set: true, Seconds: 0})},
		},
		{
			"a default with approvers is its own requirement, after default",
			[]gate.ApprovalDefault{def(ns, 2, 2, "platform"), def(col, 4, 1)},
			[]gate.Requirement{
				want("default", 4, 1, none),
				want("default/namespace/acme", 2, 2, none, "platform"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := gate.DefaultRequirements(tt.defaults)
			if err != nil {
				t.Fatalf("DefaultRequirements() = %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("got %d requirements %+v, want %d", len(got), got, len(tt.want))
			}

			for i := range got {
				g, w := got[i], tt.want[i]
				if g.Name != w.Name || g.Version != w.Version || g.Count != w.Count || g.FreshAuth != w.FreshAuth ||
					g.TimeoutSeconds != w.TimeoutSeconds || g.AuthorMayApprove || len(g.Approvers.Groups) != len(w.Approvers.Groups) {
					t.Fatalf("requirement %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

// TestDefaultRequirementsFailClosed proves that a stored default that is not
// valid, or that holds an exemption, gives an error and never a weaker
// requirement.
func TestDefaultRequirementsFailClosed(t *testing.T) {
	t.Parallel()

	ns := gate.NamespaceScope("acme")

	zero := def(ns, 1, 0)
	eleven := def(ns, 1, 11)
	window := def(ns, 1, 1)
	window.FreshAuth = gate.FreshAuth{Set: true, Seconds: 5}
	unless := def(ns, 1, 1)
	unless.Unless = "context.change.replace == 0"

	tests := []struct {
		name     string
		defaults []gate.ApprovalDefault
		want     error
	}{
		{"count zero", []gate.ApprovalDefault{def(ns, 1, 1), zero}, gate.ErrInvalid},
		{"count eleven", []gate.ApprovalDefault{eleven}, gate.ErrInvalid},
		{"window five", []gate.ApprovalDefault{window}, gate.ErrInvalid},
		{"unless body", []gate.ApprovalDefault{unless}, gate.ErrUnavailable},
	}

	for _, tt := range tests {
		got, err := gate.DefaultRequirements(tt.defaults)
		if !errors.Is(err, tt.want) || got != nil {
			t.Fatalf("%s: DefaultRequirements() = %v, %v; want nil and %v", tt.name, got, err, tt.want)
		}
	}
}
