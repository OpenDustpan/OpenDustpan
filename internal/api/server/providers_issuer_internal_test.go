package server

import (
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

			if strings.Contains(got, "secret") {
				t.Fatalf("publicIssuer(%q) = %q, want no secret", tc.issuer, got)
			}
		})
	}
}
