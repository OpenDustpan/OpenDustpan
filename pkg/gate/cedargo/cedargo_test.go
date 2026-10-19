package cedargo_test

import (
	"runtime/debug"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/gate/gatetest"
)

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

// TestDuplicatePolicyIDIsInvalid checks that a set with two policies of one
// ID is refused, so a matched ID is never ambiguous.
func TestDuplicatePolicyIDIsInvalid(t *testing.T) {
	t.Parallel()

	schema := gate.Schema{
		Version:  gate.SchemaVersion{Subject: string(gate.PointChangePlanned), Major: 1, Minor: 0},
		PathSets: nil,
	}
	p := gate.Policy{
		ID: gate.PolicyID{Scope: gate.InstallationScope(), ID: "always"}, Version: 1, Kind: gate.PolicyGate,
		Point: gate.PointChangePlanned, Condition: "", Unless: "", Text: "",
	}

	_, err := newEngine(t).Evaluate(t.Context(), gate.PolicySet{Schema: schema, Policies: []gate.Policy{p, p}}, nil)
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("Evaluate = %v, want an error that names the duplicate", err)
	}
}
