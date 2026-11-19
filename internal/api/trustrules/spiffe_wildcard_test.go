package trustrules_test

import (
	"net/http"
	"strings"
	"testing"
)

// withMTLS returns body, the body of a create, with the SPIFFE ID pattern and
// the bundle of an mTLS rule.
func withMTLS(body, pattern, bundle string) string {
	return strings.TrimSuffix(body, "}") + `,"spiffePattern":` + quote(pattern) + `,"bundle":` + quote(bundle) + `}`
}

// TestSpiffeWildcardRefused sends a create and an update with a
// spiffePattern that holds a * segment: each answers 400 with the code
// trust-rule.spiffe-wildcard and a message that names the field and asks for
// an exact SPIFFE ID, and the store and the audit log hold no new entry. A
// request with an exact SPIFFE ID answers as before.
func TestSpiffeWildcardRefused(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	bundle := e.mustSaveBundle()
	existing := e.mustCreate("deploy")
	entries := len(e.ruleEntries())

	for _, pattern := range []string{
		"spiffe://example.org/ns/acme/*",
		"spiffe://td/*",
		"spiffe://td/*/x/*",
	} {
		status, body := e.admin(http.MethodPost, rulesURL, withMTLS(ruleBody("wild", e.worker), pattern, bundle))
		checkWildcardAnswer(t, "create "+pattern, status, body)

		status, body = e.send(http.MethodPatch, rulesURL+"/"+existing.ID, adminToken,
			`{"spiffePattern":`+quote(pattern)+`,"bundle":`+quote(bundle)+`}`,
			map[string]string{"If-Match": `"1"`, sessionHeader: "1"})
		checkWildcardAnswer(t, "update "+pattern, status, body)
	}

	if list := e.mustList(); len(list) != 1 || list[0].ID != existing.ID {
		t.Errorf("rules = %+v, want only the first rule", list)
	}

	if got := len(e.ruleEntries()); got != entries {
		t.Errorf("rule audit entries = %d, want %d", got, entries)
	}

	if got := len(createFailureEntries(t, e)) + len(denialEntries(t, e)); got != 0 {
		t.Errorf("failure and denial entries = %d, want 0: a refused input writes none", got)
	}

	const exact = "spiffe://example.org/ns/acme/sa/deploy"

	status, body := e.admin(http.MethodPost, rulesURL, withMTLS(ruleBody("exact", e.worker), exact, bundle))
	if status != http.StatusCreated {
		t.Fatalf("create with an exact SPIFFE ID = %d %s, want 201", status, body)
	}

	status, body = e.send(http.MethodPatch, rulesURL+"/"+existing.ID, adminToken,
		`{"spiffePattern":"spiffe://example.org/ns/acme/sa/other","bundle":`+quote(bundle)+`}`,
		map[string]string{"If-Match": `"1"`, sessionHeader: "1"})
	if status != http.StatusOK {
		t.Fatalf("update with an exact SPIFFE ID = %d %s, want 200", status, body)
	}
}

// checkWildcardAnswer fails the test unless the answer is the refusal of a *
// in a SPIFFE ID pattern.
func checkWildcardAnswer(t *testing.T, name string, status int, body []byte) {
	t.Helper()

	if status != http.StatusBadRequest || errorCode(t, body) != "trust-rule.spiffe-wildcard" {
		t.Errorf("%s = %d %s, want 400 trust-rule.spiffe-wildcard", name, status, body)

		return
	}

	for _, want := range []string{"spiffePattern", "exact SPIFFE ID"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: body %s does not hold %q", name, body, want)
		}
	}
}
