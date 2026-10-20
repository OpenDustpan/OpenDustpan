package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// deniedEntryWire is the part of an audit entry that the denial test reads.
type deniedEntryWire struct {
	Subject string `json:"subject"`
	Action  string `json:"action"`
	Object  string `json:"object"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// deniedEntries returns the denied entries of the scope, read through the
// audit API with the administrator token.
func (w *contextWorld) deniedEntries(t *testing.T, scope string) []deniedEntryWire {
	t.Helper()

	code, body := w.call(t, http.MethodGet, "/api/audit/entries?organization="+scope+"&page[size]=100",
		ctxAdminToken, "")
	if code != http.StatusOK {
		t.Fatalf("list %s entries: status = %d: %s", scope, code, body)
	}

	var list struct {
		Entries []deniedEntryWire `json:"entries"`
	}

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode entries %s: %v", body, err)
	}

	var denied []deniedEntryWire

	for _, e := range list.Entries {
		if e.Outcome == "denied" {
			denied = append(denied, e)
		}
	}

	return denied
}

// deniedCase is one refused request to an adapter and what its entry must say.
type deniedCase struct {
	name       string
	call       deniedCall
	scope      string
	wantAction string
}

// deniedCall is one request of the denial test. The installation API reads the
// scheme Bearer; the other APIs read the scheme token.
type deniedCall struct {
	method, target, body, scheme string
}

// TestDeniedRequestsAreAudited sends refused requests to the adapters of the
// inventory, context, audit and installation APIs with a principal that lacks
// the actions. Each refusal answers 403 and writes a denied entry in the scope
// of its object. A flood of the same refusal writes one entry.
func TestDeniedRequestsAreAudited(t *testing.T) {
	t.Parallel()

	cases := []deniedCase{
		{
			name: "audit API",
			call: deniedCall{
				method: http.MethodGet,
				target: "/api/audit/entries?organization=acme",
				body:   "",
				scheme: "token",
			},
			scope:      "acme",
			wantAction: "namespace:read-audit",
		},
		{
			name: "inventory API",
			call: deniedCall{
				method: http.MethodPost, target: "/api/dustpan/v1/namespaces/acme/collections",
				body: `{"name":"c1"}`, scheme: "token",
			},
			scope: "acme", wantAction: "",
		},
		{
			name:  "installation API",
			call:  deniedCall{method: http.MethodGet, target: "/api/server/principals", body: "", scheme: "Bearer"},
			scope: "@server", wantAction: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newContextWorld(t)

			for range 25 {
				code, body := w.callAs(t, tc.call, ctxServiceToken)
				if code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403: %s", code, body)
				}
			}

			checkOneDeniedEntry(t, w.deniedEntries(t, tc.scope), tc.wantAction)
		})
	}
}

// checkOneDeniedEntry fails the test unless entries holds exactly one entry of
// svc-reader, with the policy reason and, when wantAction is set, that action.
func checkOneDeniedEntry(t *testing.T, entries []deniedEntryWire, wantAction string) {
	t.Helper()

	var mine []deniedEntryWire

	for _, e := range entries {
		if e.Subject == "svc-reader" {
			mine = append(mine, e)
		}
	}

	if len(mine) != 1 {
		t.Fatalf("denied entries of svc-reader = %+v, want 1 for 25 refusals", mine)
	}

	if wantAction != "" && mine[0].Action != wantAction {
		t.Fatalf("action = %q, want %q", mine[0].Action, wantAction)
	}

	if !strings.HasPrefix(mine[0].Reason, "denied by policy on ") {
		t.Fatalf("reason = %q, want it to name the policy and the route", mine[0].Reason)
	}
}

// callAs serves one request with the scheme of the call.
func (w *contextWorld) callAs(t *testing.T, c deniedCall, token string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, strings.NewReader(c.body))
	req.Header.Set("Authorization", c.scheme+" "+token)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	w.srv.Handler().ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}
