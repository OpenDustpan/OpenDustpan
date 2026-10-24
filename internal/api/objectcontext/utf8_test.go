package objectcontext_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPatchRefusesInvalidUTF8 proves that a PATCH body with bytes that are
// not valid UTF-8, or with a lone surrogate escape, answers 400 and writes
// nothing. Before the check, the JSON decode replaced each bad byte and each
// lone surrogate with U+FFFD and the server stored the changed text.
func TestPatchRefusesInvalidUTF8(t *testing.T) {
	t.Parallel()

	const (
		nsPath   = "/api/dustpan/v1/namespaces/acme/context"
		unitPath = "/api/dustpan/v1/namespaces/acme/units/" + testUnitID + "/context"
		want     = `invalid request body: send valid UTF-8 text, with no invalid byte ` +
			`and no lone surrogate escape such as \ud800`
	)

	holds := []string{
		"namespace:read acme", "namespace:set-context acme",
		"unit:read opentofu:acme/net", "unit:set-context opentofu:acme/net",
	}

	cases := []struct {
		name, path, body string
	}{
		{name: "a summary with bytes ff fe", path: nsPath, body: "{\"summary\":\"\xff\xfe\"}"},
		{name: "an owner with a cut sequence", path: nsPath, body: "{\"owners\":[\"team-\xc3\"]}"},
		{name: "a runbook with a surrogate in UTF-8", path: nsPath, body: "{\"runbook\":\"\xed\xa0\x80\"}"},
		{name: "an on-call text of a unit", path: unitPath, body: "{\"onCall\":\"pager-\xff\"}"},
		{name: "a bad byte outside the strings", path: unitPath, body: "{\"summary\":\"ok\"}\xff"},
		{name: "a lone high surrogate escape", path: nsPath, body: `{"summary":"lone \ud800"}`},
		{name: "a lone low surrogate escape", path: nsPath, body: `{"runbook":"lone \udc00"}`},
		{name: "a high surrogate escape and a letter", path: unitPath, body: `{"owners":["team-\ud83dx"]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			rec := serve(t, newHandler(t, newAuthorizer(holds...), store),
				request{method: http.MethodPatch, path: tc.path, token: testToken, body: tc.body})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d %s, want 400", rec.Code, rec.Body.String())
			}

			var got struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode answer %s: %v", rec.Body.String(), err)
			}

			if got.Message != want {
				t.Errorf("message = %q, want %q", got.Message, want)
			}

			if n := store.written(); n != 0 {
				t.Errorf("store writes = %d, want 0", n)
			}
		})
	}
}

// TestPatchKeepsASurrogatePair proves that the check of the escapes refuses
// only a lone surrogate: a pair of escapes is one character, and the PATCH
// stores it.
func TestPatchKeepsASurrogatePair(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	holds := []string{"namespace:read acme", "namespace:set-context acme"}
	rec := serve(t, newHandler(t, newAuthorizer(holds...), store), request{
		method: http.MethodPatch, path: "/api/dustpan/v1/namespaces/acme/context", token: testToken,
		body: `{"summary":"pager \ud83d\udcdf"}`,
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "pager \U0001F4DF") {
		t.Errorf("answer = %s, want the summary with U+1F4DF", rec.Body.String())
	}
}
