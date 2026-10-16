package dustpan_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// oidcProvider returns a valid provider with the ID id.
func oidcProvider(id string) dustpan.OIDCProvider {
	return dustpan.OIDCProvider{
		HTTPClient: nil, ID: id, DisplayName: "Corp", Issuer: "https://idp.example", ClientID: "dustpan",
		ClientSecret: "client-secret-for-tests", Scopes: []string{"profile"}, AutoCreate: true,
	}
}

// TestNewServerSignInSetup proves that NewServer checks the console
// sign-in: the public URL, the provider IDs and the issuer. A valid setup
// starts with no request to the provider.
func TestNewServerSignInSetup(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		publicURL string
		providers []dustpan.OIDCProvider
		want      string
	}{
		{name: "valid", publicURL: "https://dustpan.example", providers: []dustpan.OIDCProvider{oidcProvider("corp")}, want: ""},
		{name: "loopback http", publicURL: "http://127.0.0.1:8080", providers: []dustpan.OIDCProvider{oidcProvider("corp")}, want: ""},
		{name: "no public URL", publicURL: "", providers: []dustpan.OIDCProvider{oidcProvider("corp")}, want: "--public-url"},
		{
			name: "http public URL", publicURL: "http://dustpan.example",
			providers: []dustpan.OIDCProvider{oidcProvider("corp")}, want: "must use https",
		},
		{
			name: "upper-case ID", publicURL: "https://dustpan.example",
			providers: []dustpan.OIDCProvider{oidcProvider("Corp")}, want: "provider ID",
		},
		{
			name: "ID with a colon", publicURL: "https://dustpan.example",
			providers: []dustpan.OIDCProvider{oidcProvider("corp:a")}, want: "provider ID",
		},
		{
			name: "same ID two times", publicURL: "https://dustpan.example",
			providers: []dustpan.OIDCProvider{oidcProvider("corp"), oidcProvider("corp")}, want: "two times",
		},
		{
			name: "http issuer", publicURL: "https://dustpan.example",
			providers: []dustpan.OIDCProvider{func() dustpan.OIDCProvider {
				p := oidcProvider("corp")
				p.Issuer = "http://idp.example"

				return p
			}()}, want: "must use https",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append(auditSinkServerOptions(t, openAuditStore(t)), dustpan.WithPublicURL(tc.publicURL))
			for _, p := range tc.providers {
				opts = append(opts, dustpan.WithOIDCProvider(p))
			}

			_, err := dustpan.NewServer(opts...)

			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("NewServer: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("NewServer error = %v, want one that holds %q", err, tc.want)
			default:
			}
		})
	}
}
