package gatetest_test

import (
	"reflect"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/gatetest"
)

// portTypes maps each port to the interface types whose methods a case
// can name. A case of gatetest.PortObjects names a method of each kind.
func portTypes() map[gatetest.Port][]reflect.Type {
	return map[gatetest.Port][]reflect.Type{
		gatetest.PortObjects: {
			reflect.TypeFor[gate.Objects[gate.Gate]](),
			reflect.TypeFor[gate.Objects[gate.AccessLimit]](),
			reflect.TypeFor[gate.Objects[gate.ApprovalRule]](),
			reflect.TypeFor[gate.Objects[gate.OwnershipRule]](),
			reflect.TypeFor[gate.Objects[gate.PathSet]](),
		},
		gatetest.PortSettings:   {reflect.TypeFor[gate.Settings]()},
		gatetest.PortStateStore: {reflect.TypeFor[gate.StateStore]()},
		gatetest.PortRecords:    {reflect.TypeFor[gate.Records]()},
		gatetest.PortEngine:     {reflect.TypeFor[gate.Engine]()},
	}
}

func TestCasesNameMethodsOfThePorts(t *testing.T) {
	t.Parallel()

	types := portTypes()

	for _, list := range [][]gatetest.Case{gatetest.StoreCases(), gatetest.EngineCases()} {
		for _, c := range list {
			ts, ok := types[c.Port]
			if !ok {
				t.Fatalf("case %s: unknown port %q", c.Name, c.Port)
			}

			for _, typ := range ts {
				if _, ok := typ.MethodByName(c.Method); !ok {
					t.Fatalf("case %s: %s has no method %s", c.Name, typ, c.Method)
				}
			}
		}
	}
}

func TestCaseCounts(t *testing.T) {
	t.Parallel()

	if n := len(gatetest.StoreCases()); n != 51 {
		t.Fatalf("StoreCases() has %d cases, want 51: a record of the table does not parse", n)
	}

	if n := len(gatetest.EngineCases()); n != 26 {
		t.Fatalf("EngineCases() has %d cases, want 26: a record of the table does not parse", n)
	}
}

func TestCasesAreWellFormed(t *testing.T) {
	t.Parallel()

	kinds := []gatetest.Kind{
		gatetest.KindGate, gatetest.KindAccessLimit, gatetest.KindApprovalRule,
		gatetest.KindOwnershipRule, gatetest.KindPathSet,
	}

	for _, list := range [][]gatetest.Case{gatetest.StoreCases(), gatetest.EngineCases()} {
		seen := map[string]bool{}

		for _, c := range list {
			if c.Name == "" || c.Checks == "" || seen[c.Name] {
				t.Fatalf("case %q: a case has a unique name and a behavior to check", c.Name)
			}

			seen[c.Name] = true

			for _, k := range c.Kinds {
				if c.Port != gatetest.PortObjects || !slices.Contains(kinds, k) {
					t.Fatalf("case %s: kind %q", c.Name, k)
				}
			}
		}
	}
}

// TestCaseFixturesParse checks each scope key and policy ID of the cases
// with the functions of package gate, so the cases use the grammar that the
// adapters store.
func TestCaseFixturesParse(t *testing.T) {
	t.Parallel()

	for _, list := range [][]gatetest.Case{gatetest.StoreCases(), gatetest.EngineCases()} {
		for _, c := range list {
			checkFixtures(t, c)
		}
	}
}

// checkFixtures checks that each scope key and policy ID of c parses and
// formats back to the same text.
func checkFixtures(t *testing.T, c gatetest.Case) {
	t.Helper()

	for _, key := range c.Scopes {
		s, err := gate.ParseScopeKey(key)
		if err != nil || s.Key() != key {
			t.Fatalf("case %s: scope key %q: %v", c.Name, key, err)
		}
	}

	for _, id := range c.Policies {
		p, err := gate.ParsePolicyID(id)
		if err != nil || p.String() != id {
			t.Fatalf("case %s: policy ID %q: %v", c.Name, id, err)
		}
	}
}

// TestObjectCasesUseRoutedScopes checks that each case of a write of
// gate.Objects that is to commit uses scopes with the configuration routes.
// The case scope-without-routes-refused is the one case that writes to
// other scopes.
func TestObjectCasesUseRoutedScopes(t *testing.T) {
	t.Parallel()

	for _, c := range gatetest.StoreCases() {
		if c.Port != gatetest.PortObjects || c.Name == "scope-without-routes-refused" {
			continue
		}

		for _, key := range c.Scopes {
			s, err := gate.ParseScopeKey(key)
			if err != nil || !s.HoldsConfig() {
				t.Fatalf("case %s: scope %q holds no configuration routes", c.Name, key)
			}
		}
	}
}
