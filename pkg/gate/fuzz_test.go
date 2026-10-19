package gate_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// FuzzScopeKey checks that each text that ParseScopeKey takes formats back
// to the same text, so each scope has one key.
func FuzzScopeKey(f *testing.F) {
	for _, s := range []string{
		"installation", "namespace/acme", "collection/acme/prod", "unit/acme/pulumi/web%2Fprod",
		"blueprint/web/1", "profile/acme/prod%2Feu", "unit/acme/pulumi/%C3%A9", "unit/acme/pulumi/a%3Ab",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		scope, err := gate.ParseScopeKey(s)
		if err != nil {
			return
		}

		if scope.Key() != s {
			t.Fatalf("ParseScopeKey(%q).Key() = %q", s, scope.Key())
		}
	})
}

// FuzzScopeSegments checks that each unit and profile scope that the
// constructors take gives a key that parses back to the same scope.
func FuzzScopeSegments(f *testing.F) {
	f.Add("acme", "pulumi", "web/prod")
	f.Add("acme", "opentofu", "é ü/%2F")

	f.Fuzz(func(t *testing.T, ns, tool, name string) {
		parsed, err := namespace.ParseName(ns)
		if err != nil {
			return
		}

		for _, build := range []func() (gate.Scope, error){
			func() (gate.Scope, error) { return gate.UnitScope(parsed, tool, name) },
			func() (gate.Scope, error) { return gate.ProfileScope(parsed, name) },
		} {
			scope, err := build()
			if err != nil {
				continue
			}

			back, err := gate.ParseScopeKey(scope.Key())
			if err != nil || back != scope {
				t.Fatalf("ParseScopeKey(%q) = %v, %v, want %v", scope.Key(), back, err, scope)
			}
		}
	})
}

// FuzzParsePolicyID checks that each text that ParsePolicyID takes formats
// back to the same text.
func FuzzParsePolicyID(f *testing.F) {
	for _, s := range []string{
		"installation/builtin/plan-readable", "namespace/acme/no-destroy", "unit/acme/pulumi/web%2Fprod/cost-cap",
		"collection/acme/prod/a", "blueprint/web/3/validate",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		id, err := gate.ParsePolicyID(s)
		if err != nil {
			return
		}

		if id.String() != s {
			t.Fatalf("ParsePolicyID(%q).String() = %q", s, id.String())
		}
	})
}

// FuzzParseRequirementName checks that each text that ParseRequirementName
// takes formats back to the same text.
func FuzzParseRequirementName(f *testing.F) {
	for _, s := range []string{
		"default", "default/namespace/acme", "default/unit/acme/pulumi/web%2Fprod", "owners/network", "dba-review",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		name, err := gate.ParseRequirementName(s)
		if err != nil {
			return
		}

		if string(name) != s {
			t.Fatalf("ParseRequirementName(%q) = %q", s, name)
		}
	})
}
