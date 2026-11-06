package trustruletest

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// createCases are the refusals of Create.
func createCases() []suiteCase {
	return []suiteCase{
		{"CreateRefusesAnInvalidRule", testCreateInvalid},
		{"CreateRefusesANameTakenInTheNamespace", testCreateNameTaken},
		{"CreateTakesANameOfAnotherNamespace", testCreateNameOtherNamespace},
		{"CreateRefusesATakenID", testCreateIDTaken},
		{"CreateRefusesAPrincipalOutsideTheNamespace", testCreatePrincipal},
		{"CreateRefusesTheRuleAfterTheLimit", testCreateLimit},
	}
}

func testCreateInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	p := workload(t, h, nsAcme)

	tests := []struct {
		name   string
		change func(r *trustrule.Rule)
	}{
		{"no condition on the subject", func(r *trustrule.Rule) {
			r.Conditions = []trustrule.Condition{{Claim: "repository", Values: []string{"acme/infra"}}}
		}},
		{"no condition", func(r *trustrule.Rule) { r.Conditions = nil }},
		{"an http issuer", func(r *trustrule.Rule) { r.Issuer = "http" + "://token.actions.example.com" }},
		{"an issuer with a query", func(r *trustrule.Rule) { r.Issuer = "https://token.actions.example.com?a=1" }},
		{"an empty audience", func(r *trustrule.Rule) { r.Audience = "" }},
		{"an unknown key source", func(r *trustrule.Rule) { r.Keys = "jku" }},
		{"static keys with discovery", func(r *trustrule.Rule) { r.StaticKeys = `{"keys":[]}` }},
		{"a private static key", func(r *trustrule.Rule) {
			r.Keys = trustrule.KeySourceStatic
			r.StaticKeys = `{"keys":[{"kty":"OKP","crv":"Ed25519","x":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",` +
				`"d":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}]}`
		}},
		{"an empty name", func(r *trustrule.Rule) { r.Name = "" }},
		{"an invalid ID", func(r *trustrule.Rule) { r.ID = "trr-a b" }},
	}

	for _, tc := range tests {
		r := newRule(nsAcme, "deploy", p)
		tc.change(&r)

		err := h.Store.Create(t.Context(), r, fence, auditEntry(nsAcme, "create", r.ID))
		if !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("%s: create = %v, want ErrInvalid", tc.name, err)
		}
	}

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}

	rules, err := h.Store.List(t.Context(), nsAcme)
	if err != nil || len(rules) != 0 {
		t.Fatalf("list = %v, %v; want no rule", rules, err)
	}
}

func testCreateNameTaken(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	p := workload(t, h, nsAcme)

	first := newRule(nsAcme, "deploy", p)
	mustCreate(t, h, first)

	second := newRule(nsAcme, "deploy", p)

	err := h.Store.Create(t.Context(), second, fence, auditEntry(nsAcme, "create", second.ID))
	if !errors.Is(err, trustrule.ErrExists) {
		t.Fatalf("create with a taken name = %v, want ErrExists", err)
	}

	mustBeGone(t, h, nsAcme, second.ID)

	if got := auditCount(t, h, nsAcme); got != 1 {
		t.Errorf("audit entries = %d, want 1", got)
	}
}

func testCreateNameOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	mustCreate(t, h, newRule(nsAcme, "deploy", workload(t, h, nsAcme)))
	mustCreate(t, h, newRule(nsOther, "deploy", workload(t, h, nsOther)))
}

func testCreateIDTaken(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	fence := h.Namespace(t, nsOther)

	first := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, first)

	// The same ID in another namespace is taken too: an ID names one rule of
	// the installation.
	second := newRule(nsOther, "other name", workload(t, h, nsOther))
	second.ID = first.ID

	err := h.Store.Create(t.Context(), second, fence, auditEntry(nsOther, "create", second.ID))
	if !errors.Is(err, trustrule.ErrExists) {
		t.Fatalf("create with a taken ID = %v, want ErrExists", err)
	}

	if got := auditCount(t, h, nsOther); got != 0 {
		t.Errorf("audit entries of %s = %d, want 0", nsOther, got)
	}
}

func testCreatePrincipal(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	deleted := workload(t, h, nsAcme)
	h.DeletePrincipal(t, deleted)

	tests := []struct {
		name string
		id   string
	}{
		{"an unknown principal", "prn-unknownunknown00"},
		{"a person of the installation", h.Principal(t, "", principal.KindPerson)},
		{"a service principal of the installation", h.Principal(t, "", principal.KindService)},
		{"a workload of the installation", h.Principal(t, "", principal.KindWorkload)},
		{"a workload of the namespace", h.Principal(t, nsAcme, principal.KindWorkload)},
		{"a person of the namespace", h.Principal(t, nsAcme, principal.KindPerson)},
		{"a service principal of another namespace", workload(t, h, nsOther)},
		{"a deleted service principal of the namespace", deleted},
	}

	for _, tc := range tests {
		r := newRule(nsAcme, "deploy", tc.id)

		err := h.Store.Create(t.Context(), r, fence, auditEntry(nsAcme, "create", r.ID))
		if !errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("%s: create = %v, want ErrPrincipal", tc.name, err)
		}
	}

	if got := auditCount(t, h, nsAcme); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testCreateLimit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	p := workload(t, h, nsAcme)

	for i := range trustrule.MaxPerNamespace {
		mustCreate(t, h, newRule(nsAcme, fmt.Sprintf("rule %03d", i), p))
	}

	last := newRule(nsAcme, "one too many", p)

	err := h.Store.Create(t.Context(), last, fence, auditEntry(nsAcme, "create", last.ID))
	if !errors.Is(err, trustrule.ErrLimit) {
		t.Fatalf("create after the limit = %v, want ErrLimit", err)
	}

	mustBeGone(t, h, nsAcme, last.ID)

	if got := auditCount(t, h, nsAcme); got != trustrule.MaxPerNamespace {
		t.Errorf("audit entries = %d, want %d", got, trustrule.MaxPerNamespace)
	}

	// The limit counts one namespace: another namespace takes a rule.
	h.Namespace(t, nsOther)
	mustCreate(t, h, newRule(namespace.Name(nsOther), "first", workload(t, h, nsOther)))
}
