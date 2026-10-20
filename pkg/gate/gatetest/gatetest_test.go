package gatetest_test

import (
	"reflect"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/gatetest"
)

// newHarness returns a fresh store in memory, which also makes the
// namespaces.
func newHarness(*testing.T) gatetest.Harness {
	s := newMemStore()

	return gatetest.Harness{Store: s, Domain: s}
}

func TestObjectsConformance(t *testing.T) {
	t.Parallel()

	t.Run("gate", func(t *testing.T) {
		t.Parallel()
		gatetest.RunObjects[gate.Gate](t, newHarness)
	})
	t.Run("access-limit", func(t *testing.T) {
		t.Parallel()
		gatetest.RunObjects[gate.AccessLimit](t, newHarness)
	})
	t.Run("approval-rule", func(t *testing.T) {
		t.Parallel()
		gatetest.RunObjects[gate.ApprovalRule](t, newHarness)
	})
	t.Run("ownership-rule", func(t *testing.T) {
		t.Parallel()
		gatetest.RunObjects[gate.OwnershipRule](t, newHarness)
	})
	t.Run("path-set", func(t *testing.T) {
		t.Parallel()
		gatetest.RunObjects[gate.PathSet](t, newHarness)
	})
}

// portOf maps each port to the interface types of its methods. A case of
// gatetest.PortObjects names the method of one kind.
func portOf() map[gatetest.Port][]reflect.Type {
	return map[gatetest.Port][]reflect.Type{
		gatetest.PortObjects: {
			reflect.TypeFor[gate.Objects[gate.Gate]](),
			reflect.TypeFor[gate.Objects[gate.AccessLimit]](),
			reflect.TypeFor[gate.Objects[gate.ApprovalRule]](),
			reflect.TypeFor[gate.Objects[gate.OwnershipRule]](),
			reflect.TypeFor[gate.Objects[gate.PathSet]](),
		},
		gatetest.PortSettings: {reflect.TypeFor[gate.Settings]()},
		gatetest.PortStore:    {reflect.TypeFor[gate.Store]()},
		gatetest.PortRecords:  {reflect.TypeFor[gate.Records]()},
	}
}

// TestStoreCaseMethodsMatchPorts checks that the method expression of each
// case takes the interface of its port as its first argument.
func TestStoreCaseMethodsMatchPorts(t *testing.T) {
	t.Parallel()

	ports := portOf()

	for _, c := range gatetest.StoreCases() {
		types, ok := ports[c.Port]
		if !ok {
			t.Fatalf("case %s: unknown port %q", c.Name, c.Port)
		}

		checkMethod(t, c.Name, c.Method, types)
	}

	for _, c := range gatetest.EngineCases() {
		checkMethod(t, c.Name, c.Method, []reflect.Type{reflect.TypeFor[gate.Engine]()})
	}
}

func checkMethod(t *testing.T, name string, method any, ports []reflect.Type) {
	t.Helper()

	m := reflect.TypeOf(method)
	if m == nil || m.Kind() != reflect.Func || m.NumIn() == 0 || !slices.Contains(ports, m.In(0)) {
		t.Fatalf("case %s: method %T is not a method expression of %v", name, method, ports)
	}
}

func TestStoreCasesAreWellFormed(t *testing.T) {
	t.Parallel()

	kinds := []gatetest.Kind{
		gatetest.KindGate, gatetest.KindAccessLimit, gatetest.KindApprovalRule,
		gatetest.KindOwnershipRule, gatetest.KindPathSet,
	}
	seen := map[string]bool{}

	for _, c := range gatetest.StoreCases() {
		if c.Name == "" || c.Checks == "" || seen[c.Name] {
			t.Fatalf("case %q: a case has a unique name and a behavior to check", c.Name)
		}

		seen[c.Name] = true

		for _, k := range c.Kinds {
			if c.Port != gatetest.PortObjects || !slices.Contains(kinds, k) {
				t.Fatalf("case %s: kind %q", c.Name, k)
			}
		}

		checkFixtures(t, c.Name, c.Scopes, c.Policies)
	}
}

func TestEngineCasesAreWellFormed(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, c := range gatetest.EngineCases() {
		if c.Name == "" || c.Checks == "" || seen[c.Name] {
			t.Fatalf("case %q: a case has a unique name and a behavior to check", c.Name)
		}

		seen[c.Name] = true

		checkFixtures(t, c.Name, c.Scopes, c.Policies)
	}
}

// checkFixtures checks that each scope and policy ID of a case formats to
// a text that parses back to the same value, so the cases use the grammar
// that the adapters store.
func checkFixtures(t *testing.T, name string, scopes []gate.Scope, policies []gate.PolicyID) {
	t.Helper()

	for _, s := range scopes {
		got, err := gate.ParseScopeKey(s.Key())
		if err != nil || got != s {
			t.Fatalf("case %s: scope %q: %v", name, s.Key(), err)
		}
	}

	for _, p := range policies {
		got, err := gate.ParsePolicyID(p.String())
		if err != nil || got != p {
			t.Fatalf("case %s: policy ID %q: %v", name, p, err)
		}
	}
}

// TestObjectCasesUseRoutedScopes checks that each case of gate.Objects
// uses scopes with the configuration routes. The case
// scope-without-routes-refused is the one case that writes to other scopes.
func TestObjectCasesUseRoutedScopes(t *testing.T) {
	t.Parallel()

	for _, c := range gatetest.StoreCases() {
		if c.Port != gatetest.PortObjects || c.Name == "scope-without-routes-refused" {
			continue
		}

		for _, s := range c.Scopes {
			if !s.HoldsConfig() {
				t.Fatalf("case %s: scope %q holds no configuration routes", c.Name, s)
			}
		}
	}
}

// TestEveryStoreCaseRuns checks that RunStore holds a runner for each case of
// StoreCases, and no runner for a name that is no case.
func TestEveryStoreCaseRuns(t *testing.T) {
	t.Parallel()

	runnable := gatetest.RunnableCases()
	cases := gatetest.StoreCases()

	known := make([]string, len(cases))
	for i := range cases {
		known[i] = cases[i].Name
	}

	for _, name := range known {
		if !slices.Contains(runnable, name) {
			t.Errorf("the case %q has no runner in RunStore", name)
		}
	}

	for _, name := range runnable {
		if !slices.Contains(known, name) {
			t.Errorf("RunStore runs %q, which is no case of StoreCases", name)
		}
	}
}

func newDevHarness(*testing.T) gatetest.Harness {
	s := newMemStoreOfKind(true)

	return gatetest.Harness{Store: s, Domain: s}
}

func TestApprovers(t *testing.T) {
	t.Parallel()

	gatetest.RunApprovers(t, newHarness, newDevHarness)
}
