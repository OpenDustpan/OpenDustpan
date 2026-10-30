package trustrulestore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// validRule returns a rule that passes Validate.
func validRule() trustrule.Rule {
	return trustrule.Rule{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ID: trustrule.NewID(), Namespace: "acme",
		Name: "deploy", Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: "prn-worker", CreatedBy: "ada",
		Conditions: []trustrule.Condition{{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}},
	}
}

func TestRowRoundTrip(t *testing.T) {
	t.Parallel()

	r := validRule()

	row, err := trustrulestore.CheckCreate(&r, namespace.Fence{Namespace: "acme", FailoverVersion: 1})
	if err != nil {
		t.Fatalf("CheckCreate = %v", err)
	}

	got, err := trustrulestore.Rule(row)
	if err != nil {
		t.Fatalf("Rule = %v", err)
	}

	if got.ID != r.ID || got.Conditions[0].Values[0] != r.Conditions[0].Values[0] || !got.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("round trip = %+v, want %+v", got, r)
	}
}

// TestRuleRefusesACorruptRow reads rows that a backup, a restore or another
// version could have written. Each one gives ErrCorrupt.
func TestRuleRefusesACorruptRow(t *testing.T) {
	t.Parallel()

	r := validRule()

	good, err := trustrulestore.RowOf(&r)
	if err != nil {
		t.Fatalf("RowOf = %v", err)
	}

	cases := map[string]func(row *trustrulestore.Row){
		"conditions that are not JSON":   func(row *trustrulestore.Row) { row.ConditionsJSON = "sub=x" },
		"an unknown field":               func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"sub","values":["x"],"glob":true}]` },
		"text after the conditions":      func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"sub","values":["x"]}] []` },
		"no condition on the subject":    func(row *trustrulestore.Row) { row.ConditionsJSON = `[{"claim":"ref","values":["x"]}]` },
		"an unknown key source":          func(row *trustrulestore.Row) { row.KeySource = "jku" },
		"an http issuer":                 func(row *trustrulestore.Row) { row.Issuer = "http" + "://token.actions.example.com" },
		"a namespace that is not a name": func(row *trustrulestore.Row) { row.Namespace = "Not A Name" },
		"an ID of another kind":          func(row *trustrulestore.Row) { row.ID = "tok-1" },
	}

	for name, change := range cases {
		row := good
		change(&row)

		if _, err := trustrulestore.Rule(row); !errors.Is(err, trustrulestore.ErrCorrupt) {
			t.Errorf("%s: Rule = %v, want ErrCorrupt", name, err)
		}
	}
}

// TestCheckCreateRefusesTheFenceOfAnotherNamespace sends a rule with the
// fence of another namespace, as if the service had sent the wrong fence.
func TestCheckCreateRefusesTheFenceOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	r := validRule()
	acme := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	other := namespace.Fence{Namespace: "other", FailoverVersion: 1}

	_, err := trustrulestore.CheckCreate(&r, other)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckCreate = %v, want ErrFenced", err)
	}

	err = trustrulestore.CheckDelete("acme", r.ID, other)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("CheckDelete = %v, want ErrFenced", err)
	}

	err = trustrulestore.CheckDelete("acme", "trr-a/b", acme)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("CheckDelete of an invalid ID = %v, want ErrInvalid", err)
	}

	r.Conditions = nil

	_, err = trustrulestore.CheckCreate(&r, acme)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("CheckCreate of an invalid rule = %v, want ErrInvalid", err)
	}
}

// TestCheckPrincipal checks the principal of a rule as the transaction of a
// create reads it.
func TestCheckPrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		p    trustrulestore.PrincipalRow
		ok   bool
	}{
		{
			"a service principal of the namespace",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "acme", DeletedAtMs: 0, Found: true},
			true,
		},
		{"no principal", trustrulestore.PrincipalRow{Kind: "", Namespace: "", DeletedAtMs: 0, Found: false}, false},
		{
			"a deleted principal",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "acme", DeletedAtMs: 1, Found: true},
			false,
		},
		{
			"a person",
			trustrulestore.PrincipalRow{Kind: "person", Namespace: "acme", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a principal of the installation",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "", DeletedAtMs: 0, Found: true},
			false,
		},
		{
			"a principal of another namespace",
			trustrulestore.PrincipalRow{Kind: "service", Namespace: "other", DeletedAtMs: 0, Found: true},
			false,
		},
	}

	for _, tc := range cases {
		err := trustrulestore.CheckPrincipal(tc.p, "acme")
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrPrincipal)) {
			t.Errorf("%s: CheckPrincipal = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}
