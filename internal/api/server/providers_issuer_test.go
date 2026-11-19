package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// hostileProviderIssuers returns issuers that the provider routes must
// refuse, by reason.
func hostileProviderIssuers() map[string]string {
	return map[string]string{
		"query and fragment":    "https://idp.example?x=1#f",
		"user information":      "https://user:" + "pw" + "@idp.example",
		"space":                 "https://idp.example/a b",
		"control character":     "https://idp.example/\x01",
		"back slash":            "https://idp.example\\@evil.example",
		"http on a public host": "http://idp.example",
		"more than 512 bytes":   "https://idp.example/" + strings.Repeat("a", 600),
		"upper-case scheme":     "HTTPS://idp.example",
		"empty":                 "",
	}
}

// TestSignInProviderRoutesRefuseAHostileIssuer sends a hostile issuer to POST
// and PATCH of the provider routes, as if the session layer did not check it.
// The route answers 400, stores nothing and never echoes the issuer.
func TestSignInProviderRoutesRefuseAHostileIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range hostileProviderIssuers() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newProviderEnv(t)
			beta := e.createBeta(t)

			issuerJSON, err := json.Marshal(issuer)
			if err != nil {
				t.Fatalf("marshal issuer: %v", err)
			}

			post := `{"id":"gamma","issuer":` + string(issuerJSON) + `,"displayName":"Gamma",` +
				`"clientID":"dustpan-gamma","clientSecret":"` + betaSecret + `"}`
			patch := `{"version":1,"issuer":` + string(issuerJSON) + `}`

			for _, tc := range []struct{ method, target, body string }{
				{http.MethodPost, "/api/server/sign-in-providers", post},
				{http.MethodPatch, "/api/server/sign-in-providers/beta", patch},
			} {
				rec := e.adminDo(t, tc.method, tc.target, tc.body)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s = %d %s, want 400", tc.method, rec.Code, rec.Body.String())
				}

				if strings.Contains(rec.Body.String(), "pw@") || strings.Contains(rec.Body.String(), "evil.example") {
					t.Errorf("%s: the answer holds the issuer: %s", tc.method, rec.Body.String())
				}
			}

			if rec := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/gamma", ""); rec.Code != http.StatusNotFound {
				t.Errorf("GET gamma = %d, want 404: the refused POST stored a provider", rec.Code)
			}

			rec := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/beta", "")

			var got providerDoc
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode beta: %v", err)
			}

			if got.Issuer != beta.Issuer || got.Version != beta.Version {
				t.Errorf("beta = issuer %q version %d, want issuer %q version %d: the refused PATCH changed it",
					got.Issuer, got.Version, beta.Issuer, beta.Version)
			}
		})
	}
}
