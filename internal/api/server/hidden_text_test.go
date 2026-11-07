package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// hiddenRunes returns the text that a field of a request must refuse. The
// tests below send it straight to the handler, as if the domain check had
// failed.
func hiddenRunes() []struct{ name, text string } {
	return []struct{ name, text string }{
		{"line separator", "a\u2028b"},
		{"paragraph separator", "a\u2029b"},
		{"right-to-left override", "a\u202eb"},
	}
}

// quoteJSON returns s as a JSON string.
func quoteJSON(t *testing.T, s string) string {
	t.Helper()

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}

	return string(b)
}

// wantHiddenRefusal checks that the answer is the 400 that only the adapter
// check gives: its text starts with "invalid text" and names the rune.
func wantHiddenRefusal(t *testing.T, message string) {
	t.Helper()

	if !strings.HasPrefix(message, "invalid text:") || !strings.Contains(message, "U+20") {
		t.Fatalf("message = %q, want the adapter refusal that names the rune", message)
	}
}

// TestPrincipalRoutesRefuseHiddenText proves that the principal and group
// routes refuse a hidden character with 400 before they call the service.
func TestPrincipalRoutesRefuseHiddenText(t *testing.T) {
	t.Parallel()

	for _, hr := range hiddenRunes() {
		t.Run(hr.name, func(t *testing.T) {
			t.Parallel()

			h := newPrincipalHandler(t, newTestPrincipals(t))

			var alice principalDoc

			mustCall(t, h, apiCall{
				http.MethodPost, "/api/server/principals",
				`{"subject":"alice@example.com","kind":"person"}`, http.StatusCreated,
			}, &alice)

			var group groupDoc

			mustCall(t, h, apiCall{
				http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated,
			}, &group)

			bad := quoteJSON(t, hr.text)

			const (
				subjectBody = `{"subject":"bob@example.com","kind":"person","displayName":`
				nsBody      = `{"subject":"ci","displayName":`
			)

			calls := []apiCall{
				{http.MethodPost, "/api/server/principals", subjectBody + bad + `}`, http.StatusBadRequest},
				{http.MethodPatch, "/api/server/principals/" + alice.ID, `{"displayName":` + bad + `}`, 400},
				{http.MethodPost, "/api/server/groups", `{"name":"dev","description":` + bad + `}`, 400},
				{http.MethodPatch, "/api/server/groups/" + group.ID, `{"description":` + bad + `}`, 400},
				{http.MethodPost, "/api/server/namespaces/acme/principals", nsBody + bad + `}`, 400},
			}

			for _, c := range calls {
				var doc errorDoc

				mustCall(t, h, c, &doc)
				wantHiddenRefusal(t, doc.Message)
			}
		})
	}
}

// TestTokenRoutesRefuseHiddenName proves that the token create route refuses
// a hidden character in the name with 400 before it calls the service.
func TestTokenRoutesRefuseHiddenName(t *testing.T) {
	t.Parallel()

	for _, hr := range hiddenRunes() {
		t.Run(hr.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixture(t)

			var doc errorDoc

			f.as(t, "admin", apiCall{
				http.MethodPost, "/api/server/principals/prn-alice/tokens",
				`{"name":` + quoteJSON(t, hr.text) + `}`,
				http.StatusBadRequest,
			}, &doc)
			wantHiddenRefusal(t, doc.Message)
		})
	}
}

// TestApprovalRoutesRefuseHiddenApproverUser proves that the approval default
// and rule routes refuse a hidden character in an approver user name with 422
// before they call the store.
func TestApprovalRoutesRefuseHiddenApproverUser(t *testing.T) {
	t.Parallel()

	for _, hr := range hiddenRunes() {
		t.Run(hr.name, func(t *testing.T) {
			t.Parallel()

			_, h := newGateHandler(t, adminGates())
			body := `{"count":1,"approvers":{"users":[` + quoteJSON(t, hr.text) + `]}}`

			for _, target := range []string{
				gateBase + "acme" + gateDefault,
				gateBase + "acme" + gateRulesURL + "/ops",
			} {
				rec := serveGate(t, h, gateCall{http.MethodPut, target, body, ""})
				wantStatus(t, rec, http.StatusUnprocessableEntity)

				if got := rec.Body.String(); !strings.Contains(got, "invalid approver user name: the name holds") {
					t.Fatalf("PUT %s body = %s, want the adapter refusal", target, got)
				}
			}
		})
	}
}
