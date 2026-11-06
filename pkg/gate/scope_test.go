package gate_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// mustUnit returns the scope of a unit of acme, or fails the test.
func mustUnit(t *testing.T, tool, name string) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope("acme", tool, name)
	if err != nil {
		t.Fatalf("UnitScope(acme, %q, %q): %v", tool, name, err)
	}

	return s
}

func TestScopeKeyRoundTrip(t *testing.T) {
	t.Parallel()

	blueprint, err := gate.BlueprintScope("web-service", 12)
	if err != nil {
		t.Fatal(err)
	}

	profile, err := gate.ProfileScope("acme", "prod/eu")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		scope gate.Scope
		key   string
		level gate.Level
	}{
		{"installation", gate.InstallationScope(), "installation", gate.LevelInstallation},
		{"namespace", gate.NamespaceScope("acme"), "namespace/acme", gate.LevelNamespace},
		{"namespace with dot", gate.NamespaceScope("acme.eu_1"), "namespace/acme.eu_1", gate.LevelNamespace},
		{
			"collection with space and case", gate.CollectionScope("acme", "Prod EU"),
			"collection/acme/prod%20eu", gate.LevelCollection,
		},
		{
			"pulumi stack with slash",
			mustUnit(t, "pulumi", "web/prod"),
			"unit/acme/pulumi/web%2Fprod",
			gate.LevelUnit,
		},
		{"unit keeps case", mustUnit(t, "opentofu", "Net-A"), "unit/acme/opentofu/Net-A", gate.LevelUnit},
		{
			"unit with reserved bytes",
			mustUnit(t, "pulumi", "a b%c~d"),
			"unit/acme/pulumi/a%20b%25c~d",
			gate.LevelUnit,
		},
		{"unit with UTF-8", mustUnit(t, "pulumi", "é"), "unit/acme/pulumi/%C3%A9", gate.LevelUnit},
		{"blueprint", blueprint, "blueprint/web-service/12", gate.LevelBlueprint},
		{"profile", profile, "profile/acme/prod%2Feu", gate.LevelProfile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRoundTrip(t, tc.scope, tc.key, tc.level)
		})
	}
}

// checkRoundTrip checks that scope has the given key, and that the key
// parses back to scope.
func checkRoundTrip(t *testing.T, scope gate.Scope, key string, level gate.Level) {
	t.Helper()

	if got := scope.Key(); got != key {
		t.Fatalf("Key() = %q, want %q", got, key)
	}

	got, err := gate.ParseScopeKey(key)
	if err != nil {
		t.Fatalf("ParseScopeKey(%q): %v", key, err)
	}

	if got != scope {
		t.Fatalf("ParseScopeKey(%q) = %#v, want %#v", key, got, scope)
	}

	if got.Level() != level || got.String() != key {
		t.Fatalf("level %q, string %q, want %q, %q", got.Level(), got.String(), level, key)
	}
}

func TestParseScopeKeyRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, key string }{
		{"empty", ""},
		{"unknown level", "team/acme"},
		{"installation with segment", "installation/acme"},
		{"namespace with no segment", "namespace"},
		{"empty namespace", "namespace/"},
		{"bad namespace", "namespace/-acme"},
		{"namespace extra segment", "namespace/acme/x"},
		{"collection one segment", "collection/acme"},
		{"collection uppercase", "collection/acme/Prod"},
		{"collection too short", "collection/acme/pr"},
		{"unit two segments", "unit/acme/pulumi"},
		{"unit raw slash", "unit/acme/pulumi/web/prod"},
		{"unit bad tool", "unit/acme/Pu%20mi/web"},
		{"unit name with colon", "unit/acme/pulumi/a%3Ab"},
		{"lowercase hex", "unit/acme/pulumi/web%2fprod"},
		{"encoded unreserved", "unit/acme/pulumi/%61"},
		{"truncated escape", "unit/acme/pulumi/web%2"},
		{"lone percent", "unit/acme/pulumi/web%"},
		{"raw space", "unit/acme/pulumi/a b"},
		{"raw reserved", "unit/acme/pulumi/a:b"},
		{"blueprint empty version", "blueprint/web/"},
		{"blueprint version 0", "blueprint/web/0"},
		{"blueprint leading zero", "blueprint/web/01"},
		{"blueprint dotted version", "blueprint/web/1.2.0"},
		{"blueprint uppercase name", "blueprint/Web/1"},
		{"blueprint encoded name", "blueprint/web%20service/1"},
		{"profile bad namespace", "profile/@acme/p"},
		{"level case", "Namespace/acme"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if s, err := gate.ParseScopeKey(tc.key); !errors.Is(err, gate.ErrInvalid) {
				t.Fatalf("ParseScopeKey(%q) = %v, %v, want ErrInvalid", tc.key, s, err)
			}
		})
	}
}

func TestScopeConstructorsRefuse(t *testing.T) {
	t.Parallel()

	if _, err := gate.UnitScope("acme", "pulumi", ""); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("UnitScope with no name: %v, want ErrInvalid", err)
	}

	if _, err := gate.UnitScope("acme", "", "web"); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("UnitScope with no tool: %v, want ErrInvalid", err)
	}

	for _, name := range []string{"", "Web", "web service", "1web", strings.Repeat("a", 64)} {
		if _, err := gate.BlueprintScope(name, 1); !errors.Is(err, gate.ErrInvalid) {
			t.Fatalf("BlueprintScope(%q) = %v, want ErrInvalid", name, err)
		}
	}

	if _, err := gate.BlueprintScope("web", 0); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("BlueprintScope with version 0: %v, want ErrInvalid", err)
	}

	if _, err := gate.ProfileScope("acme", "\xff"); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("ProfileScope with bad UTF-8: %v, want ErrInvalid", err)
	}
}

func TestScopeAccessors(t *testing.T) {
	t.Parallel()

	unit := mustUnit(t, "pulumi", "web/prod")

	obj, ok := unit.Unit()
	if !ok || obj.String() != "pulumi:acme/web/prod" {
		t.Fatalf("Unit() = %v, %v, want pulumi:acme/web/prod", obj, ok)
	}

	if unit.Namespace() != "acme" || !unit.HoldsConfig() || !unit.HoldsApprovalDefault() {
		t.Fatalf(
			"unit scope: namespace %q, config %v, default %v",
			unit.Namespace(),
			unit.HoldsConfig(),
			unit.HoldsApprovalDefault(),
		)
	}
}

func TestScopeAccessorsOtherLevels(t *testing.T) {
	t.Parallel()

	coll := gate.CollectionScope("acme", collection.Name("Prod"))
	if coll.Collection() != "prod" || coll.Namespace() != "acme" {
		t.Fatalf("collection scope: %q in %q", coll.Collection(), coll.Namespace())
	}

	if _, ok := coll.Profile(); ok {
		t.Fatal("a collection scope has no profile")
	}
}

func TestScopeAccessorsWideLevels(t *testing.T) {
	t.Parallel()

	inst := gate.InstallationScope()
	if !inst.HoldsConfig() || inst.HoldsApprovalDefault() || inst.Namespace() != "" {
		t.Fatal("the installation holds the config routes and no approval default")
	}

	bp, err := gate.BlueprintScope("web", 2)
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := bp.Blueprint(); !ok || v.Name != "web" || v.Version != 2 || bp.HoldsConfig() {
		t.Fatalf("blueprint scope: %#v %v config %v", v, ok, bp.HoldsConfig())
	}

	var zero gate.Scope
	if !zero.IsZero() || zero.Key() != "" {
		t.Fatalf("zero scope: key %q", zero.Key())
	}
}

func TestScopeWithUnitID(t *testing.T) {
	t.Parallel()

	unit, err := gate.UnitScope("acme", "opentofu", "web")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		scope gate.Scope
		id    string
		want  error
	}{
		{"a stack ID", unit, "stk-01HZX2", nil},
		{"a workspace ID with an underscore", unit, "ws-a_b", nil},
		{"an empty ID", unit, "", gate.ErrInvalid},
		{"an ID with a slash", unit, "ws/../x", gate.ErrInvalid},
		{"an ID with a quote", unit, "ws-'x", gate.ErrInvalid},
		{"an ID of 129 bytes", unit, strings.Repeat("a", 129), gate.ErrInvalid},
		{"a namespace scope", gate.NamespaceScope("acme"), "ws-x", gate.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.scope.WithUnitID(tt.id)
			if !errors.Is(err, tt.want) {
				t.Fatalf("WithUnitID(%q) = %v, want %v", tt.id, err, tt.want)
			}

			if err == nil && (got.UnitID() != tt.id || got.Key() != tt.scope.Key()) {
				t.Fatalf("WithUnitID(%q) = %s with ID %q, want the same key with the ID", tt.id, got, got.UnitID())
			}
		})
	}
}

func TestUnitChainCarriesTheUnitID(t *testing.T) {
	t.Parallel()

	obj, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"", "ws-1"} {
		chain, cerr := gate.UnitChain(obj.WithUnitID(id), "")
		if cerr != nil {
			t.Fatalf("UnitChain(%q) = %v", id, cerr)
		}

		u := chain[len(chain)-1]
		if u.UnitID() != id || u.Key() != "unit/acme/opentofu/web" {
			t.Fatalf("UnitChain(%q) unit scope = %s with ID %q", id, u, u.UnitID())
		}
	}

	if _, err = gate.UnitChain(obj.WithUnitID("ws/1"), ""); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("UnitChain(bad ID) = %v, want ErrInvalid", err)
	}
}
