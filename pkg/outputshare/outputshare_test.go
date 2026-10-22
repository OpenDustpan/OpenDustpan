package outputshare_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

const (
	acme = namespace.Name("acme")
	beta = namespace.Name("beta")
)

func unitObject(t *testing.T, tool string, ns namespace.Name, name string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatalf("unit object %s:%s/%s: %v", tool, ns, name, err)
	}

	return o
}

func rule(producer, consumer identity.Object) outputshare.Rule {
	return outputshare.Rule{
		CreatedAt: time.Time{},
		ID:        "r-1",
		CreatedBy: "p-1",
		Producer:  producer,
		Consumer:  consumer,
	}
}

func TestRuleValidate(t *testing.T) {
	t.Parallel()

	network := unitObject(t, "pulumi", acme, "net/prod")
	web := unitObject(t, "opentofu", acme, "web")
	otherWeb := unitObject(t, "opentofu", beta, "web")
	platform := identity.CollectionObject(acme, "platform")
	otherPlatform := identity.CollectionObject(beta, "platform")
	principal := identity.PrincipalObject("p-2")

	for _, tc := range []struct {
		name string
		rule outputshare.Rule
		ok   bool
	}{
		{"unit to unit", rule(network, web), true},
		{"unit to a unit of another tool", rule(web, network), true},
		{"unit to collection", rule(network, platform), true},
		{"collection to namespace", rule(platform, identity.NamespaceObject(acme)), true},
		{"namespace to namespace", rule(identity.NamespaceObject(acme), identity.NamespaceObject(acme)), true},
		{"unit to a unit of another namespace", rule(network, otherWeb), true},
		{"unit to a collection of another namespace", rule(network, otherPlatform), true},
		{"unit to a whole other namespace", rule(network, identity.NamespaceObject(beta)), false},
		{"unit to a principal", rule(network, principal), false},
		{"principal as producer", rule(principal, web), false},
		{"unit to the installation", rule(network, identity.ServerObject()), false},
		{"unit to the caller", rule(network, identity.CallerObject()), false},
		{"unit to the zero object", rule(network, identity.Object{}), false},
		{"zero producer", rule(identity.Object{}, web), false},
		{"empty ID", withID(rule(network, web), ""), false},
		{"ID with an upper case byte", withID(rule(network, web), "R-1"), false},
		{"ID with a slash", withID(rule(network, web), "r/1"), false},
		{"ID too long", withID(rule(network, web), outputshare.ID(strings.Repeat("r", outputshare.MaxIDBytes+1))), false},
		{"no saver", withSaver(rule(network, web), ""), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.rule.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func withID(r outputshare.Rule, id outputshare.ID) outputshare.Rule {
	r.ID = id

	return r
}

func withSaver(r outputshare.Rule, saver string) outputshare.Rule {
	r.CreatedBy = saver

	return r
}

func TestRuleCrossNamespace(t *testing.T) {
	t.Parallel()

	network := unitObject(t, "pulumi", acme, "net/prod")

	if rule(network, unitObject(t, "opentofu", acme, "web")).CrossNamespace() {
		t.Fatal("a consumer of the same namespace is not across namespaces")
	}

	if !rule(network, identity.CollectionObject(beta, "platform")).CrossNamespace() {
		t.Fatal("a collection of another namespace is across namespaces")
	}
}

func TestDefaultValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value outputshare.Default
		ok    bool
	}{
		{outputshare.DefaultNobody, true},
		{outputshare.DefaultNamespace, true},
		{"", false},
		{"everyone", false},
		{"Nobody", false},
	} {
		err := tc.value.Validate()
		if tc.ok != (err == nil) {
			t.Fatalf("Default(%q).Validate() = %v, want ok %t", tc.value, err, tc.ok)
		}

		if err != nil && !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Default(%q).Validate() = %v, want ErrInvalid", tc.value, err)
		}
	}
}

func TestPlacementValidate(t *testing.T) {
	t.Parallel()

	web := unitObject(t, "opentofu", acme, "web").WithUnitID("ws-1")
	platform := identity.CollectionObject(acme, "platform").WithCollectionID("col-1")

	for _, tc := range []struct {
		name      string
		placement outputshare.Placement
		ok        bool
	}{
		{"unit with no collection", outputshare.Placement{Unit: web, Collection: identity.Object{}}, true},
		{"unit with its collection", outputshare.Placement{Unit: web, Collection: platform}, true},
		{
			"unit with no stable ID",
			outputshare.Placement{Unit: unitObject(t, "opentofu", acme, "web"), Collection: identity.Object{}},
			false,
		},
		{"namespace as unit", outputshare.Placement{Unit: identity.NamespaceObject(acme), Collection: identity.Object{}}, false},
		{
			"collection with no stable ID",
			outputshare.Placement{Unit: web, Collection: identity.CollectionObject(acme, "platform")},
			false,
		},
		{
			"collection of another namespace",
			outputshare.Placement{Unit: web, Collection: identity.CollectionObject(beta, "platform").WithCollectionID("col-2")},
			false,
		},
		{"namespace as collection", outputshare.Placement{Unit: web, Collection: identity.NamespaceObject(acme)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.placement.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok %t", err, tc.ok)
			}

			if err != nil && !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDeniedError(t *testing.T) {
	t.Parallel()

	producer := unitObject(t, "pulumi", acme, "net/prod")
	consumer := unitObject(t, "opentofu", beta, "web")

	byJob := &outputshare.DeniedError{
		Err: outputshare.ErrNoRule, Check: outputshare.CheckRule, Producer: producer, Consumer: consumer,
	}

	var err error = byJob
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, outputshare.ErrNoRule) {
		t.Fatalf("errors.Is(%v) misses identity.ErrDenied or ErrNoRule", err)
	}

	for _, want := range []string{producer.String(), consumer.String(), outputshare.CheckRule} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Error() = %q, want %q in it", err.Error(), want)
		}
	}

	byPrincipal := &outputshare.DeniedError{
		Err: nil, Check: outputshare.CheckGrant, Producer: producer, Consumer: identity.Object{},
	}

	if !errors.Is(byPrincipal, identity.ErrDenied) || strings.Contains(byPrincipal.Error(), "job") {
		t.Fatalf("a denial of a principal = %q, want identity.ErrDenied and no job", byPrincipal.Error())
	}
}
