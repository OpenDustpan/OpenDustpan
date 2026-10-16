package oauth_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
)

// plainSetup is a provider in the OAuth mode and a plain client of it.
type plainSetup struct {
	provider *oauthtest.Provider
	client   *oauth.PlainClient
	clock    *clock
}

// newPlainSetup starts a provider in the OAuth mode with the faults f, and
// makes a plain client of it with the claims c.
func newPlainSetup(t *testing.T, f oauthtest.Faults, c oauth.Claims) plainSetup {
	t.Helper()

	clk := newClock()
	p := oauthtest.New(t, oauthtest.Config{
		Now: clk.Now, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI,
		Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOAuth, Faults: f, TLS: true,
	})

	client, err := oauth.NewPlain(oauth.PlainConfig{
		HTTPClient: p.Client(), Now: clk.Now, AuthorizationURL: p.AuthorizationEndpoint(),
		TokenURL: p.TokenEndpoint(), UserinfoURL: p.UserinfoEndpoint(), ClientID: clientID,
		ClientSecret: clientSecret, RedirectURL: redirectURI, Scopes: []string{"read:user"}, Claims: c,
	})
	if err != nil {
		t.Fatalf("new plain client: %v", err)
	}

	return plainSetup{provider: p, client: client, clock: clk}
}

// plainResult is what a sign-in through a plain client gives: the
// identity or the error, with the code of the callback and the pending
// request, so a test can look for them in an error text.
type plainResult struct {
	err     error
	id      oauth.Identity
	code    string
	pending oauth.Pending
}

// signIn runs Begin, the browser step and Finish with opts.
func (s plainSetup) signIn(t *testing.T, opts oauth.Options) plainResult {
	t.Helper()

	authURL, pending, err := s.client.Begin(t.Context(), opts)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	back := authorize(t, s.provider, authURL)

	if stateErr := oauth.CheckState(back.state, pending); stateErr != nil {
		t.Fatalf("state of the callback: %v", stateErr)
	}

	id, err := s.client.Finish(t.Context(), back.code, pending)

	return plainResult{err: err, id: id, code: back.code, pending: pending}
}

// TestPlainSignIn proves a sign-in through a plain OAuth 2.0 provider:
// the request carries a state and a PKCE challenge, and no nonce, no
// max_age and no openid scope; the identity comes from the user-info
// answer; the time of the authentication is the time of Finish.
func TestPlainSignIn(t *testing.T) {
	t.Parallel()

	s := newPlainSetup(t, noFaults(), oauth.Claims{Subject: "", Name: "", Username: "", Email: ""})

	got := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: true})
	if got.err != nil {
		t.Fatalf("finish: %v", got.err)
	}

	want := alice()
	if got.id.Subject != want.Subject || got.id.Name != want.Name ||
		got.id.PreferredUsername != want.PreferredUsername || got.id.Email != want.Email ||
		!got.id.AuthTime.Equal(s.clock.Now()) {
		t.Fatalf("identity = %+v, want alice at %v", got.id, s.clock.Now())
	}

	checkPlainQuery(t, s.provider.LastAuthorizationQuery())
}

// checkPlainQuery checks that the authorization query q has a state and a
// PKCE S256 challenge, no nonce and no max_age, and the scopes as given.
func checkPlainQuery(t *testing.T, q url.Values) {
	t.Helper()

	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" {
		t.Fatalf("authorization query %v, want a state and a PKCE S256 challenge", q)
	}

	for _, name := range []string{"nonce", "max_age"} {
		if q.Has(name) {
			t.Errorf("authorization query has %s: %v", name, q)
		}
	}

	if q.Get("scope") != "read:user" {
		t.Errorf("scope = %q, want the configured scopes as given", q.Get("scope"))
	}
}

// TestPlainClaimMapping proves that the mapping names the claims, and that
// a subject that is a whole number, as GitHub sends its user IDs, is read
// in base 10.
func TestPlainClaimMapping(t *testing.T) {
	t.Parallel()

	body := `{"id":12345678901,"login":"octocat","name":"Mona Octocat","email":null}`
	s := newPlainSetup(t, fault(func(f *oauthtest.Faults) { f.UserinfoBody = body }),
		oauth.Claims{Subject: "id", Name: "name", Username: "login", Email: "email"})

	got := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	if got.err != nil {
		t.Fatalf("finish: %v", got.err)
	}

	if got.id.Subject != "12345678901" || got.id.Name != "Mona Octocat" || got.id.PreferredUsername != "octocat" ||
		got.id.Email != "" {
		t.Fatalf("identity = %+v, want the mapped claims", got.id)
	}
}

// TestPlainRefusesAnswers proves that Finish refuses a token that is not a
// bearer token, a user-info answer that is not 200, not JSON or not one
// object, and a subject that is missing, empty or not a whole number, and
// that no error text holds the code or a secret of the request.
func TestPlainRefusesAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		set  func(*oauthtest.Faults)
		want error
		name string
	}{
		{name: "token type mac", set: func(f *oauthtest.Faults) { f.TokenType = "mac" }, want: oauth.ErrProvider},
		{
			name: "user-info 401", set: func(f *oauthtest.Faults) { f.UserinfoStatus = http.StatusUnauthorized },
			want: oauth.ErrProvider,
		},
		{name: "user-info as HTML", set: func(f *oauthtest.Faults) {
			f.UserinfoType, f.UserinfoBody = "text/html", `{"sub":"alice"}`
		}, want: oauth.ErrUserInfo},
		{
			name: "a JSON list", set: func(f *oauthtest.Faults) { f.UserinfoBody = `[{"sub":"alice"}]` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "data after the object", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":"a"}{"sub":"b"}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "no subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"name":"Alice"}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "empty subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":""}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "fraction subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":1.5}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "negative subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":-1}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "exponent subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":1e3}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "boolean subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":true}` },
			want: oauth.ErrUserInfo,
		},
		{
			name: "object subject", set: func(f *oauthtest.Faults) { f.UserinfoBody = `{"sub":{"id":"a"}}` },
			want: oauth.ErrUserInfo,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newPlainSetup(t, fault(tc.set), oauth.Claims{Subject: "", Name: "", Username: "", Email: ""})

			got := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
			if !errors.Is(got.err, tc.want) {
				t.Fatalf("finish = %v, want %v", got.err, tc.want)
			}

			checkNoSecret(t, got.err, got.code, got.pending)
		})
	}
}

// TestPlainNeedsTheVerifier proves that the code exchange sends the PKCE
// verifier of the request, so that a code is no use with another
// verifier.
func TestPlainNeedsTheVerifier(t *testing.T) {
	t.Parallel()

	s := newPlainSetup(t, noFaults(), oauth.Claims{Subject: "", Name: "", Username: "", Email: ""})

	authURL, pending, err := s.client.Begin(t.Context(), oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	back := authorize(t, s.provider, authURL)
	pending.Verifier = "another-verifier-of-43-characters-at-least-xyz"

	if _, err := s.client.Finish(t.Context(), back.code, pending); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("finish with another verifier = %v, want ErrProvider", err)
	}
}

// TestNewPlainRefusesConfig proves that NewPlain refuses a configuration
// with a missing value or an endpoint that is not HTTPS, except HTTP on a
// loopback host.
func TestNewPlainRefusesConfig(t *testing.T) {
	t.Parallel()

	valid := func() oauth.PlainConfig {
		return oauth.PlainConfig{ //nolint:gosec // Test fixture: fake URLs and the fake client secret of the tests.
			HTTPClient: nil, Now: time.Now, AuthorizationURL: "https://idp.example/authorize",
			TokenURL: "https://idp.example/token", UserinfoURL: "https://idp.example/userinfo", ClientID: clientID,
			ClientSecret: clientSecret, RedirectURL: redirectURI, Scopes: nil,
			Claims: oauth.Claims{Subject: "", Name: "", Username: "", Email: ""},
		}
	}

	if _, err := oauth.NewPlain(valid()); err != nil {
		t.Fatalf("valid configuration: %v", err)
	}

	loopback := valid()
	loopback.TokenURL = "http://127.0.0.1:9000/token"

	if _, err := oauth.NewPlain(loopback); err != nil {
		t.Fatalf("http token URL on a loopback host: %v", err)
	}

	for name, change := range map[string]func(*oauth.PlainConfig){
		"no client ID":           func(c *oauth.PlainConfig) { c.ClientID = "" },
		"no client secret":       func(c *oauth.PlainConfig) { c.ClientSecret = "" },
		"no redirect URL":        func(c *oauth.PlainConfig) { c.RedirectURL = "" },
		"http authorization URL": func(c *oauth.PlainConfig) { c.AuthorizationURL = "http://idp.example/authorize" },
		"http token URL":         func(c *oauth.PlainConfig) { c.TokenURL = "http://idp.example/token" },
		"http user-info URL":     func(c *oauth.PlainConfig) { c.UserinfoURL = "http://idp.example/userinfo" },
		"relative user-info URL": func(c *oauth.PlainConfig) { c.UserinfoURL = "/userinfo" },
		"user-info fragment":     func(c *oauth.PlainConfig) { c.UserinfoURL = "https://idp.example/userinfo#x" },
		"no token URL":           func(c *oauth.PlainConfig) { c.TokenURL = "" },
	} {
		cfg := valid()
		change(&cfg)

		if _, err := oauth.NewPlain(cfg); !errors.Is(err, oauth.ErrConfig) {
			t.Errorf("%s: NewPlain = %v, want ErrConfig", name, err)
		}
	}
}

// TestClaimsWithDefaults proves the default claim names.
func TestClaimsWithDefaults(t *testing.T) {
	t.Parallel()

	got := oauth.Claims{Subject: "id", Name: "", Username: "login", Email: ""}.WithDefaults()
	want := oauth.Claims{Subject: "id", Name: "name", Username: "login", Email: "email"}

	if got != want {
		t.Fatalf("WithDefaults = %+v, want %+v", got, want)
	}
}
