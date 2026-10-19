package cedargo_test

import (
	"runtime/debug"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/gate/gatetest"
)

const costCap = "context.cost.available && context.cost.deltaMonthly > 10000"

func newEngine(t *testing.T) *cedargo.Engine {
	t.Helper()

	return cedargo.New(cedargo.WithAccessActions(gatetest.EngineAccessActions()...))
}

func TestConformance(t *testing.T) {
	t.Parallel()

	gatetest.RunEngine(t, func(t *testing.T) gate.Engine {
		t.Helper()

		return newEngine(t)
	})
}

// TestRunsEachLinkedCase checks that the suite runs each case that is not
// for an external adapter, so a new case cannot go without a runner.
func TestRunsEachLinkedCase(t *testing.T) {
	t.Parallel()

	runnable := map[string]bool{}
	for _, name := range gatetest.EngineRunnableCases() {
		runnable[name] = true
	}

	for _, c := range gatetest.EngineCases() {
		if strings.HasPrefix(c.Name, "external-") {
			continue
		}

		if !runnable[c.Name] {
			t.Errorf("case %s has no runner", c.Name)
		}
	}
}

// TestVersionIsTheVersionInGoMod checks that Version names the cedar-go
// version that the build links.
func TestVersionIsTheVersionInGoMod(t *testing.T) {
	t.Parallel()

	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build information")
	}

	for _, dep := range info.Deps {
		if dep.Path == "github.com/cedar-policy/cedar-go" {
			if want := "cedar-go/" + strings.TrimPrefix(dep.Version, "v"); newEngine(t).Version() != want {
				t.Fatalf("Version = %q, want %q", newEngine(t).Version(), want)
			}

			return
		}
	}

	t.Skip("cedar-go is not in the build information")
}

// TestAccessSchemaNeedsActions checks that an engine with no access action
// cannot use the access schema, and fails closed.
func TestAccessSchemaNeedsActions(t *testing.T) {
	t.Parallel()

	schema := gate.Schema{Version: gate.SchemaVersion{Subject: gate.SchemaAccess, Major: 1, Minor: 0}, PathSets: nil}

	if _, err := cedargo.New().Check(t.Context(), schema, nil); err == nil {
		t.Fatal("Check with the access schema and no access action returned nil, want an error")
	}
}

// TestRefusedPolicyIsCached checks that a second Check of a refused policy
// gives the same diagnostic as the first.
func TestRefusedPolicyIsCached(t *testing.T) {
	t.Parallel()

	e := newEngine(t)
	schema := gate.Schema{
		Version:  gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		PathSets: nil,
	}
	p := gate.Policy{
		ID: gate.PolicyID{Scope: gate.InstallationScope(), ID: "risk"}, Version: 1, Kind: gate.PolicyGate,
		Point: gate.PointChangePlanned, Condition: "context.risk.score > 3", Unless: "", Text: "",
	}

	first, err := e.Check(t.Context(), schema, []gate.Policy{p})
	if err != nil || len(first) != 1 {
		t.Fatalf("Check = %v, %v, want one diagnostic", first, err)
	}

	second, err := e.Check(t.Context(), schema, []gate.Policy{p})
	if err != nil || len(second) != 1 || second[0] != first[0] {
		t.Fatalf("second Check = %v, %v, want %v", second, err, first)
	}
}

// TestCheckGateText checks the text of a gate against the linked engine:
// a comment at the end of a clause, the injection probes, and the rule that
// each clause reads the flag available of a group that it reads.
func TestCheckGateText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		condition string
		unless    string
		refused   bool
	}{
		{"trailing comment in the condition", "context.plan.available // why", "", false},
		{"trailing comment in the unless", "", "context.plan.available // why", false},
		{
			"comment hides the text of an injection",
			"true // }; permit (principal, action, resource) when { true",
			"",
			false,
		},
		{"second policy", "true }; permit (principal, action, resource) when { true", "", true},
		{"second policy in the unless", "", "true }; permit (principal, action, resource) when { true", true},
		{"closing brace and a new clause", "true } unless { true", "", true},
		{
			"flag in the condition and in the unless",
			costCap,
			"context.cost.available && context.cost.deltaMonthly < 5",
			false,
		},
		{"flag in the condition only", costCap, "context.cost.deltaMonthly < 5", true},
		{"flag in the unless only", "context.cost.deltaMonthly > 5", "context.cost.available", true},
		{"flag read in both clauses of other groups", "context.plan.available", "context.cost.available", false},
	}

	schema := gate.Schema{
		Version:  gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		PathSets: nil,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := gate.Policy{
				ID: gate.PolicyID{Scope: gate.InstallationScope(), ID: "text"}, Version: 1, Kind: gate.PolicyGate,
				Point: gate.PointChangePlanned, Condition: tt.condition, Unless: tt.unless, Text: "",
			}

			diags, err := newEngine(t).Check(t.Context(), schema, []gate.Policy{p})
			if err != nil {
				t.Fatalf("Check = %v", err)
			}

			if got := len(diags) > 0; got != tt.refused {
				t.Fatalf("Check = %v, refused = %v, want refused = %v", diags, got, tt.refused)
			}
		})
	}
}

// TestParseErrorPointsIntoTheText checks that the line and the column of a
// parse error refer to the text of the author, not to the template.
func TestParseErrorPointsIntoTheText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		condition string
		unless    string
		want      string
	}{
		{"first line of the condition", "context.plan.available &&", "", "<input>:2:"},
		{"second line of the condition", "true &&\n(", "", "<input>:3:"},
		{"first line of the unless", "", "context.plan.available &&", "<input>:2:"},
	}

	schema := gate.Schema{
		Version:  gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		PathSets: nil,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := gate.Policy{
				ID: gate.PolicyID{Scope: gate.InstallationScope(), ID: "text"}, Version: 1, Kind: gate.PolicyGate,
				Point: gate.PointChangePlanned, Condition: tt.condition, Unless: tt.unless, Text: "",
			}

			diags, err := newEngine(t).Check(t.Context(), schema, []gate.Policy{p})
			if err != nil || len(diags) != 1 {
				t.Fatalf("Check = %v, %v, want one diagnostic", diags, err)
			}

			if !strings.Contains(diags[0].Message, tt.want) {
				t.Fatalf("message %q, want a position that starts with %q", diags[0].Message, tt.want)
			}
		})
	}
}
