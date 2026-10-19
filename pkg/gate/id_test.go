package gate_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"a", true},
		{"0", true},
		{"prod-destroy", true},
		{"a-", true},
		{"a" + strings.Repeat("b", 62), true},
		{"a" + strings.Repeat("b", 63), false},
		{"", false},
		{"-a", false},
		{"Prod", false},
		{"a_b", false},
		{"a.b", false},
		{"a/b", false},
		{"builtin/plan-readable", false},
		{"é", false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			id, err := gate.ParseID(tc.in)
			if tc.want && (err != nil || string(id) != tc.in) {
				t.Fatalf("ParseID(%q) = %q, %v, want the ID", tc.in, id, err)
			}

			if !tc.want && !errors.Is(err, gate.ErrInvalid) {
				t.Fatalf("ParseID(%q): err = %v, want ErrInvalid", tc.in, err)
			}
		})
	}
}

func TestParseRuleIDRefusesReserved(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"default", "owners", "Default", "owners/x", ""} {
		if _, err := gate.ParseRuleID(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseRuleID(%q): err = %v, want ErrInvalid", in, err)
		}
	}

	for _, in := range []string{"security", "defaults", "owner", "dba"} {
		if _, err := gate.ParseRuleID(in); err != nil {
			t.Fatalf("ParseRuleID(%q): %v", in, err)
		}
	}
}

func TestParseGateID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    bool
		builtin bool
	}{
		{"cost-cap", true, false},
		{"builtin/plan-readable", true, true},
		{"builtin/mandatory-resource-policy", true, true},
		{"builtin/", false, false},
		{"builtin/Plan", false, false},
		{"builtin/a/b", false, false},
		{"other/a", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			id, err := gate.ParseGateID(tc.in)
			if !tc.want {
				if !errors.Is(err, gate.ErrInvalid) {
					t.Fatalf("ParseGateID(%q): err = %v, want ErrInvalid", tc.in, err)
				}

				return
			}

			if err != nil || string(id) != tc.in || id.IsBuiltin() != tc.builtin {
				t.Fatalf("ParseGateID(%q) = %q, %v, builtin %v", tc.in, id, err, id.IsBuiltin())
			}
		})
	}

	if got := gate.BuiltinID("plan-readable"); got != "builtin/plan-readable" || !got.IsBuiltin() {
		t.Fatalf("BuiltinID = %q", got)
	}
}

func TestPolicyIDRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   gate.PolicyID
		want string
	}{
		{"installation", gate.PolicyID{Scope: gate.InstallationScope(), ID: "cost-cap"}, "installation/cost-cap"},
		{
			"built-in",
			gate.PolicyID{Scope: gate.InstallationScope(), ID: gate.BuiltinID("plan-readable")},
			"installation/builtin/plan-readable",
		},
		{"namespace", gate.PolicyID{Scope: gate.NamespaceScope("acme"), ID: "no-destroy"}, "namespace/acme/no-destroy"},
		{
			"unit",
			gate.PolicyID{Scope: mustUnit(t, "pulumi", "web/prod"), ID: "freeze-eu"},
			"unit/acme/pulumi/web%2Fprod/freeze-eu",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.id.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}

			got, err := gate.ParsePolicyID(tc.want)
			if err != nil || got != tc.id {
				t.Fatalf("ParsePolicyID(%q) = %#v, %v, want %#v", tc.want, got, err, tc.id)
			}
		})
	}

	g := gate.Gate{
		Scope: gate.NamespaceScope("acme"), ID: "no-destroy", Version: 0, DecisionPoint: gate.PointChangePlanned,
		Condition: "", Unless: "", Outcome: gate.OutcomeDeny, Message: "", Mode: gate.ModeEnforce,
		OnError: gate.OnErrorFail, Final: false, State: gate.StateValid, Reads: nil,
	}
	if g.PolicyID().String() != "namespace/acme/no-destroy" {
		t.Fatalf("Gate.PolicyID() = %q", g.PolicyID())
	}
}

func TestParsePolicyIDRefuses(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"",
		"installation",
		"installation/",
		"namespace/acme",
		"namespace/acme/",
		"namespace/acme/Bad",
		"namespace/acme/builtin/plan-readable",
		"unit/acme/pulumi/web/prod/freeze-eu",
		"installation/a/b",
		"team/acme/x",
	} {
		if got, err := gate.ParsePolicyID(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParsePolicyID(%q) = %v, %v, want ErrInvalid", in, got, err)
		}
	}
}

func TestRequirementNames(t *testing.T) {
	t.Parallel()

	coll := gate.CollectionScope("acme", "prod")

	cases := []struct {
		name string
		req  gate.RequirementName
		want string
	}{
		{"rule", gate.RuleRequirement("security"), "security"},
		{"default", gate.DefaultRequirement(), "default"},
		{"scoped default", gate.ScopedDefaultRequirement(coll), "default/collection/acme/prod"},
		{"owners", gate.OwnersRequirement("net-cidr"), "owners/net-cidr"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if string(tc.req) != tc.want {
				t.Fatalf("name = %q, want %q", tc.req, tc.want)
			}

			got, err := gate.ParseRequirementName(tc.want)
			if err != nil || got != tc.req {
				t.Fatalf("ParseRequirementName(%q) = %q, %v", tc.want, got, err)
			}
		})
	}

	for _, in := range []string{
		"", "owners", "owners/", "owners/Bad", "default/", "default/installation",
		"default/namespace/-x", "default/blueprint/web/1", "Security",
	} {
		if got, err := gate.ParseRequirementName(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseRequirementName(%q) = %q, %v, want ErrInvalid", in, got, err)
		}
	}
}

func TestRecordID(t *testing.T) {
	t.Parallel()

	seen := map[gate.RecordID]bool{}

	for range 100 {
		id := gate.NewRecordID()

		if _, err := gate.ParseRecordID(string(id)); err != nil {
			t.Fatalf("ParseRecordID(NewRecordID() = %q): %v", id, err)
		}

		if seen[id] {
			t.Fatalf("NewRecordID gave %q twice", id)
		}

		seen[id] = true
	}

	for _, in := range []string{
		"", "gdr-", "gdr-abc", "gdr-" + strings.Repeat("a", 21), "gdr-" + strings.Repeat("A", 20),
		"cho-" + strings.Repeat("a", 20), "gdr-" + strings.Repeat("a", 19) + "-",
	} {
		if _, err := gate.ParseRecordID(in); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("ParseRecordID(%q): err = %v, want ErrInvalid", in, err)
		}
	}
}
