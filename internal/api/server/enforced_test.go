package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// enforcedWindow is the stored window of the rules in these tests.
const enforcedWindow = 300

// enforcedDoc is the part of a rule or of an approval default that these tests
// read.
type enforcedDoc struct {
	FreshAuthSeconds *int  `json:"freshAuthSeconds"`
	Enforced         *bool `json:"enforced"`
}

// readEnforced decodes the answer body of one rule or default.
func readEnforced(t *testing.T, body string) enforcedDoc {
	t.Helper()

	var doc enforcedDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if doc.Enforced == nil {
		t.Fatalf("the answer has no enforced field: %s", body)
	}

	return doc
}

// enforcedWant is what a test expects of one answer: the value of enforced.
type enforcedWant struct {
	what     string
	enforced bool
}

// check fails unless doc shows the expected enforced and the stored window.
func (w enforcedWant) check(t *testing.T, doc enforcedDoc) {
	t.Helper()

	if *doc.Enforced != w.enforced {
		t.Errorf("%s: enforced = %v, want %v", w.what, *doc.Enforced, w.enforced)
	}

	if doc.FreshAuthSeconds == nil || *doc.FreshAuthSeconds != enforcedWindow {
		t.Errorf("%s: freshAuthSeconds = %v, want the stored %d", w.what, doc.FreshAuthSeconds, enforcedWindow)
	}
}

// getEnforced reads path with the access token of an administrator.
func (e providerEnv) getEnforced(t *testing.T, path string) enforcedDoc {
	t.Helper()

	rec := e.adminDo(t, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, rec.Code, rec.Body.String())
	}

	return readEnforced(t, rec.Body.String())
}

// TestStoredRulesShowNotEnforced checks that an approval rule with a
// freshAuthSeconds window and the approval default of a namespace with a
// window show "enforced": false while the setting freshAuthEnabled is off, and
// "enforced": true while it is on. The stored window stays the same in both
// states.
func TestStoredRulesShowNotEnforced(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)

	rule := gateBase + testNamespace + gateRulesURL + "/ops"
	def := gateBase + testNamespace + gateDefault

	for _, put := range []struct{ path, body string }{
		{rule, `{"count":1,"approvers":{"groups":["ops"]},"freshAuthSeconds":300}`},
		{def, `{"count":1,"approvers":{},"freshAuthSeconds":300}`},
	} {
		rec := e.sendWithSession(t, http.MethodPut, e.freshSignIn(t), put.path, put.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s, want 200", put.path, rec.Code, rec.Body.String())
		}
	}

	for _, on := range []bool{false, true} {
		e.freshOn.Store(on)

		enforcedWant{what: "rule", enforced: on}.check(t, e.getEnforced(t, rule))
		enforcedWant{what: "approval default", enforced: on}.check(t, e.getEnforced(t, def))
		enforcedWant{what: "rule in the list", enforced: on}.check(t, e.listedRule(t))
	}
}

// TestNullWindowShowsNotEnforced checks that a rule and an approval default
// with a null freshAuthSeconds show "enforced": false even while the setting
// freshAuthEnabled is on, because the gate never enforces a null window.
func TestNullWindowShowsNotEnforced(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)

	rule := gateBase + testNamespace + gateRulesURL + "/ops"
	def := gateBase + testNamespace + gateDefault

	for _, put := range []struct{ path, body string }{
		{rule, `{"count":1,"approvers":{"groups":["ops"]}}`},
		{def, `{"count":1,"approvers":{}}`},
	} {
		rec := e.sendWithSession(t, http.MethodPut, e.freshSignIn(t), put.path, put.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s, want 200", put.path, rec.Code, rec.Body.String())
		}
	}

	e.freshOn.Store(true)

	doc := e.getEnforced(t, rule)
	if doc.Enforced == nil || *doc.Enforced {
		t.Errorf("rule with a null window: enforced = %v, want false", doc.Enforced)
	}

	doc = e.getEnforced(t, def)
	if doc.Enforced == nil || *doc.Enforced {
		t.Errorf("approval default with a null window: enforced = %v, want false", doc.Enforced)
	}
}

// listedRule returns the one rule of the list route.
func (e providerEnv) listedRule(t *testing.T) enforcedDoc {
	t.Helper()

	list := e.adminDo(t, http.MethodGet, gateBase+testNamespace+gateRulesURL, "")

	var rules struct {
		Rules []enforcedDoc `json:"rules"`
	}

	if err := json.Unmarshal(list.Body.Bytes(), &rules); err != nil || len(rules.Rules) != 1 {
		t.Fatalf("list = %d %s (%v), want one rule", list.Code, list.Body.String(), err)
	}

	if rules.Rules[0].Enforced == nil {
		t.Fatalf("the listed rule has no enforced field: %s", list.Body.String())
	}

	return rules.Rules[0]
}
