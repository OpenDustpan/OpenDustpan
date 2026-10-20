package drifttest

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func policyCases() []suiteCase {
	return []suiteCase{
		{"policy: a scope that sets none is not found", testPolicyNotFound},
		{"policy: put sets and replaces a policy", testPolicyPut},
		{"policy: a scope or a policy that fails validation changes nothing", testPolicyInvalid},
		{"policy: delete removes the policy and a second delete is not found", testPolicyDelete},
		{"policy: effective applies unit, collection, namespace and defaults to off", testPolicyEffective},
		{"policy: a stale fence refuses each write", testPolicyFenced},
		{"policy: each write adds one audit entry and a refused write adds none", testPolicyAudit},
		{"policy: a namespace never reads the policy of another namespace", testPolicyIsolation},
	}
}

func daily() drift.Policy {
	return drift.Policy{Frequency: drift.Frequency{Preset: drift.PresetDaily, Cron: ""}, Enabled: true}
}

func weekly() drift.Policy {
	return drift.Policy{Frequency: drift.Frequency{Preset: drift.PresetWeekly, Cron: ""}, Enabled: true}
}

func off() drift.Policy {
	return drift.Policy{Frequency: drift.Frequency{Preset: "", Cron: ""}, Enabled: false}
}

func nsScope(ns string) drift.Scope {
	return drift.Scope{Namespace: namespace.Name(ns), Kind: drift.ScopeNamespace, Name: ""}
}

func collScope(ns, name string) drift.Scope {
	return drift.Scope{Namespace: namespace.Name(ns), Kind: drift.ScopeCollection, Name: name}
}

func unitScope(ns, id string) drift.Scope {
	return drift.Scope{Namespace: namespace.Name(ns), Kind: drift.ScopeUnit, Name: id}
}

func testPolicyNotFound(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")

	_, err := h.Policies.GetPolicy(t.Context(), nsScope("acme"))
	wantErr(t, "get with no policy", err, drift.ErrNotFound)
}

func testPolicyPut(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := collScope("acme", "payments")

	must(t, "put", h.Policies.PutPolicy(t.Context(), s, daily(), f, auditOf("drift.policy.set")))

	got, err := h.Policies.GetPolicy(t.Context(), s)
	must(t, "get", err)

	if got != daily() {
		t.Errorf("policy = %+v, want %+v", got, daily())
	}

	must(t, "replace", h.Policies.PutPolicy(t.Context(), s, weekly(), f, auditOf("drift.policy.set")))

	got, err = h.Policies.GetPolicy(t.Context(), s)
	must(t, "get after replace", err)

	if got != weekly() {
		t.Errorf("policy after replace = %+v, want %+v", got, weekly())
	}

	// A disabled policy keeps its frequency.
	keep := drift.Policy{Frequency: weekly().Frequency, Enabled: false}
	must(t, "put disabled", h.Policies.PutPolicy(t.Context(), s, keep, f, auditOf("drift.policy.set")))

	got, err = h.Policies.GetPolicy(t.Context(), s)
	must(t, "get disabled", err)

	if got != keep {
		t.Errorf("disabled policy = %+v, want %+v", got, keep)
	}
}

func testPolicyInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	good := nsScope("acme")
	e := auditOf("drift.policy.set")

	// Each hostile value goes straight to the store, as if the route had not
	// checked it (defense in depth).
	badPolicies := map[string]drift.Policy{
		"enabled with no frequency": {Frequency: drift.Frequency{Preset: "", Cron: ""}, Enabled: true},
		"preset and cron": {
			Frequency: drift.Frequency{Preset: drift.PresetDaily, Cron: "0 3 * * *"},
			Enabled:   true,
		},
		"command in cron": {Frequency: drift.Frequency{Preset: "", Cron: "0 3 * * $(id)"}, Enabled: true},
		"long cron": {
			Frequency: drift.Frequency{Preset: "", Cron: strings.Repeat("* ", drift.MaxCronBytes)},
			Enabled:   true,
		},
	}

	for name, p := range badPolicies {
		wantErr(t, name, h.Policies.PutPolicy(t.Context(), good, p, f, e), drift.ErrInvalid)
	}

	badScopes := map[string]drift.Scope{
		"unit with no name":     {Namespace: acme, Kind: drift.ScopeUnit, Name: ""},
		"namespace with a name": {Namespace: acme, Kind: drift.ScopeNamespace, Name: "x"},
		"unknown kind":          {Namespace: acme, Kind: "installation", Name: ""},
		"newline in the name":   {Namespace: acme, Kind: drift.ScopeCollection, Name: "a\nb"},
	}

	for name, s := range badScopes {
		wantErr(t, name, h.Policies.PutPolicy(t.Context(), s, daily(), f, e), drift.ErrInvalid)

		_, err := h.Policies.GetPolicy(t.Context(), s)
		wantErr(t, "get of "+name, err, drift.ErrInvalid)
	}

	_, err := h.Policies.GetPolicy(t.Context(), good)
	wantErr(t, "get after refused writes", err, drift.ErrNotFound)
}

func testPolicyDelete(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	s := unitScope("acme", "wsp-1")
	e := auditOf("drift.policy.delete")

	wantErr(t, "delete with no policy", h.Policies.DeletePolicy(t.Context(), s, f, e), drift.ErrNotFound)

	must(t, "put", h.Policies.PutPolicy(t.Context(), s, daily(), f, auditOf("drift.policy.set")))
	must(t, "delete", h.Policies.DeletePolicy(t.Context(), s, f, e))

	_, err := h.Policies.GetPolicy(t.Context(), s)
	wantErr(t, "get after delete", err, drift.ErrNotFound)
	wantErr(t, "second delete", h.Policies.DeletePolicy(t.Context(), s, f, e), drift.ErrNotFound)
}

func testPolicyEffective(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	e := auditOf("drift.policy.set")

	got, err := h.Policies.EffectivePolicy(t.Context(), u, "payments")
	must(t, "effective with no policy", err)

	if got != off() {
		t.Errorf("effective with no policy = %+v, want off", got)
	}

	must(t, "put namespace", h.Policies.PutPolicy(t.Context(), nsScope("acme"), daily(), f, e))

	got, err = h.Policies.EffectivePolicy(t.Context(), u, "payments")
	must(t, "effective", err)

	if got != daily() {
		t.Errorf("effective = %+v, want the namespace policy", got)
	}

	must(t, "put collection", h.Policies.PutPolicy(t.Context(), collScope("acme", "payments"), weekly(), f, e))

	got, err = h.Policies.EffectivePolicy(t.Context(), u, "payments")
	must(t, "effective", err)

	if got != weekly() {
		t.Errorf("effective = %+v, want the collection policy", got)
	}

	got, err = h.Policies.EffectivePolicy(t.Context(), u, "")
	must(t, "effective with no collection", err)

	if got != daily() {
		t.Errorf("effective for a unit with no collection = %+v, want the namespace policy", got)
	}

	must(t, "put unit", h.Policies.PutPolicy(t.Context(), unitScope("acme", "wsp-1"), off(), f, e))

	got, err = h.Policies.EffectivePolicy(t.Context(), u, "payments")
	must(t, "effective", err)

	if got != off() {
		t.Errorf("effective = %+v, want the disabled unit policy to win", got)
	}

	_, err = h.Policies.EffectivePolicy(t.Context(), unit("acme", ""), "")
	wantErr(t, "effective of an invalid unit", err, drift.ErrInvalid)
}

func testPolicyFenced(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	old := stale(f)
	s := nsScope("acme")
	e := auditOf("drift.policy.set")

	wantErr(t, "put with a stale fence", h.Policies.PutPolicy(t.Context(), s, daily(), old, e), namespace.ErrFenced)

	must(t, "put", h.Policies.PutPolicy(t.Context(), s, daily(), f, e))

	wantErr(t, "delete with a stale fence", h.Policies.DeletePolicy(t.Context(), s, old, e), namespace.ErrFenced)

	got, err := h.Policies.GetPolicy(t.Context(), s)
	must(t, "get", err)

	if got != daily() {
		t.Errorf("policy after a fenced delete = %+v, want unchanged", got)
	}
}

func testPolicyAudit(t *testing.T, h Harness) {
	t.Helper()

	if h.Audited == nil {
		t.Skip("the adapter gives no audit hook")
	}

	f := h.Namespace(t, "acme")
	s := nsScope("acme")
	e := auditOf("drift.policy.set")
	want := h.Audited(t, "acme") + 1

	must(t, "put", h.Policies.PutPolicy(t.Context(), s, daily(), f, e))

	if got := h.Audited(t, "acme"); got != want {
		t.Errorf("audit entries after put = %d, want %d", got, want)
	}

	wantErr(
		t,
		"refused put",
		h.Policies.PutPolicy(
			t.Context(),
			s,
			drift.Policy{Frequency: drift.Frequency{Preset: "x", Cron: ""}, Enabled: true},
			f,
			e,
		),
		drift.ErrInvalid,
	)
	wantErr(t, "fenced put", h.Policies.PutPolicy(t.Context(), s, daily(), stale(f), e), namespace.ErrFenced)

	if got := h.Audited(t, "acme"); got != want {
		t.Errorf("audit entries after refused writes = %d, want %d", got, want)
	}

	must(t, "delete", h.Policies.DeletePolicy(t.Context(), s, f, e))

	want++

	if got := h.Audited(t, "acme"); got != want {
		t.Errorf("audit entries after delete = %d, want %d", got, want)
	}
}

func testPolicyIsolation(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	h.Namespace(t, "other")

	must(t, "put", h.Policies.PutPolicy(t.Context(), nsScope("acme"), daily(), f, auditOf("drift.policy.set")))

	_, err := h.Policies.GetPolicy(t.Context(), nsScope("other"))
	wantErr(t, "get in another namespace", err, drift.ErrNotFound)

	got, err := h.Policies.EffectivePolicy(t.Context(), unit("other", "wsp-1"), "")
	must(t, "effective in another namespace", err)

	if got != off() {
		t.Errorf("effective in another namespace = %+v, want off", got)
	}
}
