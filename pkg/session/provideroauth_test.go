package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/session"
)

// hubOAuth returns the OAuth settings of a plain OAuth 2.0 provider such
// as GitHub.
func hubOAuth() session.OAuthSettings {
	return session.OAuthSettings{ //nolint:gosec // Test fixture: the URLs of a provider, no credential.
		AuthorizationURL: "https://hub.example/login/oauth/authorize",
		TokenURL:         "https://hub.example/login/oauth/access_token",
		UserinfoURL:      "https://api.hub.example/user", SubjectClaim: "id", NameClaim: "name",
		UsernameClaim: "login", EmailClaim: "email",
	}
}

// validOAuthConfig returns a valid provider in the OAuth mode.
func validOAuthConfig() session.ProviderConfig {
	p := validProviderConfig()
	p.ID, p.Mode, p.Issuer, p.OAuth = "hub", session.ModeOAuth, "", hubOAuth()

	return p
}

func TestProviderConfigValidateOAuth(t *testing.T) {
	t.Parallel()

	if err := validOAuthConfig().Validate(); err != nil {
		t.Fatalf("valid provider in the OAuth mode: %v", err)
	}

	loopback := validOAuthConfig()
	loopback.OAuth.TokenURL = "http://127.0.0.1:9000/token"

	if err := loopback.Validate(); err != nil {
		t.Fatalf("http token URL on a loopback host: %v", err)
	}

	for _, tc := range []struct {
		name   string
		change func(*session.ProviderConfig)
	}{
		{"no mode", func(p *session.ProviderConfig) { p.Mode = "" }},
		{"other mode", func(p *session.ProviderConfig) { p.Mode = "saml" }},
		{"an issuer", func(p *session.ProviderConfig) { p.Issuer = "https://hub.example" }},
		{"http user-info URL", func(p *session.ProviderConfig) { p.OAuth.UserinfoURL = "http://api.hub.example/user" }},
		{"no token URL", func(p *session.ProviderConfig) { p.OAuth.TokenURL = "" }},
		{"relative authorization URL", func(p *session.ProviderConfig) { p.OAuth.AuthorizationURL = "/authorize" }},
		{"user in the URL", func(p *session.ProviderConfig) {
			p.OAuth.UserinfoURL = fakecred.URL("https", "a", "b", "api.hub.example", "/user")
		}},
		{"fragment", func(p *session.ProviderConfig) { p.OAuth.UserinfoURL = "https://api.hub.example/user#x" }},
		{"long URL", func(p *session.ProviderConfig) { p.OAuth.UserinfoURL += "/" + strings.Repeat("a", 512) }},
		{"no subject claim", func(p *session.ProviderConfig) { p.OAuth.SubjectClaim = "" }},
		{"claim with a space", func(p *session.ProviderConfig) { p.OAuth.NameClaim = "full name" }},
		{"long claim", func(p *session.ProviderConfig) { p.OAuth.EmailClaim = strings.Repeat("e", 65) }},
	} {
		p := validOAuthConfig()
		tc.change(&p)

		if err := p.Validate(); !errors.Is(err, session.ErrInvalidProvider) {
			t.Errorf("%s: Validate = %v, want ErrInvalidProvider", tc.name, err)
		}
	}

	oidc := validProviderConfig()
	oidc.OAuth = hubOAuth()

	if err := oidc.Validate(); !errors.Is(err, session.ErrInvalidProvider) {
		t.Errorf("OIDC provider with OAuth settings: Validate = %v, want ErrInvalidProvider", err)
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"HTTPS://API.Hub.Example/User":          "https://api.hub.example/User",
		"https://api.hub.example:443/user":      "https://api.hub.example/user",
		"https://api.hub.example:8443/user":     "https://api.hub.example:8443/user",
		"http://LOCALHOST:80/u/":                "http://localhost/u/",
		"http://127.0.0.1:9000/userinfo":        "http://127.0.0.1:9000/userinfo",
		"https://[::1]:443/x":                   "https://[::1]/x",
		"https://api.hub.example/user/":         "https://api.hub.example/user/",
		"https://api.hub.example/User?Fields=A": "https://api.hub.example/User?Fields=A",
		"https://api.hub.example":               "https://api.hub.example",
		"not a URL":                             "not a URL",
	} {
		if got := session.NormalizeEndpoint(raw); got != want {
			t.Errorf("NormalizeEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestBindProvidersOAuth proves the binding of a plain OAuth 2.0 provider:
// the user-info URL in the form of NormalizeEndpoint, "#", and the subject
// claim. Another form of the scheme, the host or a default port is the
// same binding; another path, another subject claim or an issuer is not.
func TestBindProvidersOAuth(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}
	hub := session.Provider{ID: "hub", Issuer: "", UserinfoURL: "https://api.hub.example/user", SubjectClaim: "id"}

	if err := session.BindProviders(ctx, store, []session.Provider{hub}, time.Now()); err != nil {
		t.Fatalf("first binding: %v", err)
	}

	if got := store.issuers["hub"]; got != "https://api.hub.example/user#id" {
		t.Fatalf("recorded binding = %q, want the user-info URL and the subject claim", got)
	}

	same := hub
	same.UserinfoURL = "HTTPS://API.Hub.Example:443/user"

	if err := session.BindProviders(ctx, store, []session.Provider{same}, time.Now()); err != nil {
		t.Fatalf("binding with another form of the same URL: %v", err)
	}

	for name, change := range map[string]func(*session.Provider){
		"a trailing slash": func(p *session.Provider) { p.UserinfoURL += "/" },
		"another path":     func(p *session.Provider) { p.UserinfoURL = "https://api.hub.example/users/me" },
		"another claim":    func(p *session.Provider) { p.SubjectClaim = "login" },
		"an OpenID issuer": func(p *session.Provider) { p.Issuer, p.UserinfoURL, p.SubjectClaim = "https://hub.example", "", "" },
		"another port":     func(p *session.Provider) { p.UserinfoURL = "https://api.hub.example:8443/user" },
	} {
		other := hub
		change(&other)

		err := session.BindProviders(ctx, store, []session.Provider{other}, time.Now())
		if !errors.Is(err, session.ErrIssuerChanged) {
			t.Errorf("binding with %s = %v, want ErrIssuerChanged", name, err)
		}
	}

	other := hub
	other.SubjectClaim = "login"

	err := session.BindProviders(ctx, store, []session.Provider{other}, time.Now())

	want := `sign-in provider "hub" identifies its people with "https://api.hub.example/user#id" in the database ` +
		`and with "https://api.hub.example/user#login" in the request, so set the user-info URL and the subject ` +
		`claim back, or use a new provider ID only if the identity provider changed`
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// hubInput returns a new plain OAuth 2.0 provider with only its subject
// claim set.
func hubInput() session.ProviderInput {
	oauth := hubOAuth()
	oauth.NameClaim, oauth.EmailClaim = "", ""

	return session.ProviderInput{
		ID: "hub", Mode: session.ModeOAuth, Issuer: "", DisplayName: "Hub", ClientID: "dustpan",
		ClientSecret: "client-secret-value", Scopes: []string{"read:user"}, OAuth: oauth,
		AutoCreate: true, Enabled: true,
	}
}

// TestProviderAdminOAuth proves that the service creates a plain OAuth
// 2.0 provider with the default claim names, binds its ID to the user-info
// URL and the subject claim, records its settings, refuses a change of the
// mode or of what identifies its people, and accepts a change of a name
// claim.
func TestProviderAdminOAuth(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := newAdminFixture(t)

	p, err := f.admin.Create(ctx, sessionCallerEntry(), hubInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	checkHubCreated(t, f, p)

	checkOAuthRefusals(t, f)

	name := "display_name"
	rename := oauthChange(1, func(c *session.ProviderChange) { c.OAuth.NameClaim = &name })

	if _, err := f.admin.Update(ctx, sessionCallerEntry(), "hub", rename); err != nil {
		t.Fatalf("change of the name claim: %v", err)
	}

	changed := changeKeys(f.store.entries[len(f.store.entries)-1])
	if c := changed["claims.name"]; c.Old == nil || *c.Old != "name" || c.New != name {
		t.Fatalf("change entry = %+v, want claims.name from name to %s", changed, name)
	}
}

// checkHubCreated proves that the provider hub has the given claims and
// the defaults, that its ID is bound to the user-info URL and the subject
// claim, and that the create entry records the mode and the OAuth settings
// and no empty issuer.
func checkHubCreated(t *testing.T, f adminFixture, p session.ProviderConfig) {
	t.Helper()

	if p.OAuth.NameClaim != "name" || p.OAuth.EmailClaim != "email" || p.OAuth.UsernameClaim != "login" {
		t.Fatalf("claims = %+v, want the given ones and the defaults", p.OAuth)
	}

	if got := f.binder.issuers["hub"]; got != "https://api.hub.example/user#id" {
		t.Fatalf("binding = %q, want the user-info URL and the subject claim", got)
	}

	created := changeKeys(f.store.entries[len(f.store.entries)-1])
	if created["mode"].New != "oauth" || created["userinfoURL"].New != "https://api.hub.example/user" ||
		created["claims.subject"].New != "id" {
		t.Fatalf("create changes = %+v, want the mode and the OAuth settings", created)
	}

	if _, has := created["issuer"]; has {
		t.Fatal("the create entry records an empty issuer")
	}
}

// checkOAuthRefusals proves that a change of the mode, of the user-info
// URL or of the subject claim of the provider hub is refused.
func checkOAuthRefusals(t *testing.T, f adminFixture) {
	t.Helper()

	oidc, userinfo, subject := session.ModeOIDC, "https://api.hub.example/users/me", "login"

	for name, tc := range map[string]struct {
		change func(*session.ProviderChange)
		want   error
	}{
		"mode":          {change: func(c *session.ProviderChange) { c.Mode = &oidc }, want: session.ErrInvalidProvider},
		"user-info URL": {change: func(c *session.ProviderChange) { c.OAuth.UserinfoURL = &userinfo }, want: session.ErrIssuerChanged},
		"subject claim": {change: func(c *session.ProviderChange) { c.OAuth.SubjectClaim = &subject }, want: session.ErrIssuerChanged},
	} {
		if _, err := f.admin.Update(t.Context(), sessionCallerEntry(), "hub", oauthChange(1, tc.change)); !errors.Is(
			err, tc.want,
		) {
			t.Errorf("change of the %s = %v, want %v", name, err, tc.want)
		}
	}
}

// noOAuthChange returns a change of no OAuth setting.
func noOAuthChange() session.OAuthChange {
	return session.OAuthChange{
		AuthorizationURL: nil, TokenURL: nil, UserinfoURL: nil, SubjectClaim: nil, NameClaim: nil,
		UsernameClaim: nil, EmailClaim: nil,
	}
}

// oauthChange returns a change of version with the fields that set sets.
func oauthChange(version int64, set func(*session.ProviderChange)) session.ProviderChange {
	c := session.ProviderChange{
		Mode: nil, Issuer: nil, DisplayName: nil, ClientID: nil, ClientSecret: nil, Scopes: nil,
		OAuth: noOAuthChange(), AutoCreate: nil, Enabled: nil, Version: version,
	}
	set(&c)

	return c
}
