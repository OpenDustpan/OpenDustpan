package server

import (
	"encoding/json"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
)

// TestPublicIssuerHostile sends hostile issuers directly to publicIssuer, as if
// the check of the setting did not stop them. No answer may hold user
// information, a query or a fragment.
func TestPublicIssuerHostile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, issuer, want string
	}{
		{name: "plain issuer", issuer: "https://idp.example", want: "https://idp.example"},
		{name: "issuer with a path", issuer: "https://idp.example/realms/a", want: "https://idp.example/realms/a"},
		{name: "user information", issuer: fakecred.URL("https", "user", "pw-secret", "idp.example", ""), want: "https://idp.example"},
		{name: "query", issuer: "https://idp.example/?token=tok-secret", want: "https://idp.example/"},
		{name: "fragment", issuer: "https://idp.example/#frag-secret", want: "https://idp.example/"},
		{name: "encoded CR and LF", issuer: "https://idp.example/a%0D%0Ab", want: "https://idp.example/a%0D%0Ab"},
		{name: "encoded slash", issuer: "https://idp.example/a%2Fb", want: "https://idp.example/a%2Fb"},
		{name: "javascript scheme", issuer: "javascript://idp.example/%0Aalert(1)", want: ""},
		{name: "scheme-relative", issuer: "//idp.example/p", want: ""},
		{name: "not a URL", issuer: "://pw-secret", want: ""},
		{name: "no host", issuer: "pw-secret", want: ""},
		{name: "empty", issuer: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := publicIssuer(tc.issuer)
			if got != tc.want {
				t.Fatalf("publicIssuer(%q) = %q, want %q", tc.issuer, got, tc.want)
			}

			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("publicIssuer(%q) = %q, want no CR or LF", tc.issuer, got)
			}

			if strings.Contains(got, "secret") {
				t.Fatalf("publicIssuer(%q) = %q, want no secret", tc.issuer, got)
			}
		})
	}
}

// TestStaticProviderOfHidesIssuerSecrets sends a static provider with a hostile
// issuer directly to the wire form of the list, as if the check of the issuer
// at start did not stop it. The answer must hold no user information, query or
// fragment. Defense in depth: the check at start refuses such an issuer, and
// this test does not rely on it.
func TestStaticProviderOfHidesIssuerSecrets(t *testing.T) {
	t.Parallel()

	hostile := fakecred.URL("https", "user", "pw-secret", "idp.example", "") + "/r?token=tok-secret#frag-secret"

	got := staticProviderOf(SignInProvider{
		Method:      nil,
		ID:          "corp",
		DisplayName: "",
		Issuer:      hostile,
		ClientID:    "",
		Scopes:      nil,
		AutoCreate:  false,
		Stored:      false,
	})
	if got.Issuer != "https://idp.example/r" {
		t.Fatalf("issuer = %q, want %q", got.Issuer, "https://idp.example/r")
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if strings.Contains(string(body), "secret") {
		t.Fatalf("the answer holds a secret: %s", body)
	}
}
