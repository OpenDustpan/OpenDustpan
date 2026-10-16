package dustpan_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
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

// signInServer builds a server on st with the console sign-in of the
// provider corp and the issuer issuer, and a listener of its own.
func signInServer(t *testing.T, st *sqlite.Store, issuer string) (*dustpan.Server, string) {
	t.Helper()

	p := oidcProvider("corp")
	p.Issuer = issuer
	ln := openTestListener(t)

	opts := append(auditSinkServerOptions(t, st),
		dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithOIDCProvider(p), dustpan.WithListener(ln))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv, ln.Addr().String()
}

// TestRunRefusesAnotherIssuer proves that the first start binds the
// provider ID to its issuer, and that a later start with another issuer
// for the same provider ID fails before it serves.
func TestRunRefusesAnotherIssuer(t *testing.T) {
	t.Parallel()

	st := openAuditStore(t)

	first, addr := signInServer(t, st, "https://idp.example")

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, first)
	waitForLive(t, chainServer{srv: first, stop: cancel, base: "http://" + addr})
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run of the first server: %v", err)
	}

	again, _ := signInServer(t, st, "https://idp.example")

	ctx, cancel = context.WithCancel(t.Context())
	done = runServer(ctx, again)

	select {
	case err := <-done:
		t.Fatalf("run with the same issuer ended: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run with the same issuer: %v", err)
	}

	moved, _ := signInServer(t, st, "https://other.example")

	// A start that does not refuse serves until the deadline, and then
	// answers nil.
	runCtx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()

	err := moved.Run(runCtx)
	if err == nil || !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("run with another issuer = %v, want ErrIssuerChanged", err)
	}

	if text := err.Error(); !strings.Contains(text, `"https://idp.example"`) ||
		!strings.Contains(text, `"https://other.example"`) {
		t.Fatalf("run with another issuer = %q, want both issuers named", text)
	}
}
