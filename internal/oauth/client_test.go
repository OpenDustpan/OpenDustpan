package oauth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
)

const (
	clientID     = "dustpan"
	clientSecret = "client-secret-for-tests"
	redirectURI  = "https://dustpan.example/api/server/sign-in/corp/callback"
)

// alice is the person whom the providers of the tests sign in.
func alice() oauthtest.User {
	return oauthtest.User{
		Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
	}
}

// clock is a clock that a test moves. The provider and the client share it.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{mu: sync.Mutex{}, now: time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// noFaults returns faults that break nothing.
func noFaults() oauthtest.Faults {
	var none oauthtest.Faults

	return none
}

// fault returns faults with the changes of set.
func fault(set func(*oauthtest.Faults)) oauthtest.Faults {
	f := noFaults()
	set(&f)

	return f
}

// setup is a provider and a client of it.
type setup struct {
	provider *oauthtest.Provider
	client   *oauth.Client
	clock    *clock
}

// newSetup starts a provider that signs with alg and has the faults f,
// and makes a client of it.
func newSetup(t *testing.T, alg string, f oauthtest.Faults) setup {
	t.Helper()

	c := newClock()
	p := oauthtest.New(t, oauthtest.Config{
		Now: c.Now, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI, Algorithm: alg,
		User: alice(), Mode: oauthtest.ModeOIDC, Faults: f, TLS: true,
	})

	client, err := oauth.New(t.Context(), oauth.Config{
		HTTPClient: p.Client(), Now: c.Now, Issuer: p.Issuer(), ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: []string{"profile", "email", "openid"},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return setup{provider: p, client: client, clock: c}
}

// callback is the query of the redirect back to the server.
type callback struct {
	code  string
	state string
}

// authorize sends the browser request to authURL and returns the callback
// of the redirect back to the server.
func authorize(t *testing.T, p *oauthtest.Provider, authURL string) callback {
	t.Helper()

	browser := *p.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, authURL, http.NoBody)
	if err != nil {
		t.Fatalf("authorization request: %v", err)
	}

	resp, err := browser.Do(req)
	if err != nil {
		t.Fatalf("authorization request: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization answer = %d, want %d", resp.StatusCode, http.StatusFound)
	}

	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}

	if e := back.Query().Get("error"); e != "" {
		t.Fatalf("provider refused the request: %s: %s", e, back.Query().Get("error_description"))
	}

	return callback{code: back.Query().Get("code"), state: back.Query().Get("state")}
}

// signIn runs Begin, the browser step and Finish with opts.
func (s setup) signIn(t *testing.T, opts oauth.Options) (oauth.Identity, error) {
	t.Helper()

	authURL, pending := s.client.Begin(opts)
	back := authorize(t, s.provider, authURL)

	if err := oauth.CheckState(back.state, pending); err != nil {
		t.Fatalf("state of the callback: %v", err)
	}

	return s.client.Finish(t.Context(), back.code, pending)
}

func TestSignInEachAlgorithm(t *testing.T) {
	t.Parallel()

	for _, alg := range []string{oauthtest.RS256, oauthtest.ES256, oauthtest.EdDSA} {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, alg, noFaults())

			got, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
			if err != nil {
				t.Fatalf("sign in: %v", err)
			}

			want := oauth.Identity{
				AuthTime: s.clock.Now().Truncate(time.Second), Subject: "248289761001", Name: "Alice Doe",
				PreferredUsername: "alice", Email: "alice@example.com",
			}
			if !got.AuthTime.Equal(want.AuthTime) {
				t.Errorf("AuthTime = %v, want %v", got.AuthTime, want.AuthTime)
			}

			got.AuthTime = want.AuthTime
			if got != want {
				t.Errorf("identity = %+v, want %+v", got, want)
			}
		})
	}
}

func TestBeginRequest(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	firstURL, first := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	_, second := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})

	if first.State == second.State || first.Nonce == second.Nonce || first.Verifier == second.Verifier {
		t.Error("two requests share a state, a nonce or a verifier")
	}

	u, err := url.Parse(firstURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	q := u.Query()

	for name, want := range map[string]string{
		"response_type": "code", "client_id": clientID, "redirect_uri": redirectURI,
		"scope": "openid profile email", "state": first.State, "nonce": first.Nonce,
		"code_challenge_method": "S256",
	} {
		if got := q.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	if q.Get("code_challenge") == "" || q.Get("code_challenge") == first.Verifier {
		t.Errorf("code_challenge = %q, want the S256 hash of the verifier", q.Get("code_challenge"))
	}

	if q.Has("max_age") {
		t.Errorf("max_age = %q, want none", q.Get("max_age"))
	}

	if !strings.HasPrefix(firstURL, s.provider.AuthorizationEndpoint()+"?") {
		t.Errorf("URL %q does not start with the authorization endpoint", firstURL)
	}
}

func TestBeginMaxAge(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	for _, tc := range []struct {
		maxAge time.Duration
		want   string
	}{
		{maxAge: 0, want: "0"},
		{maxAge: 15 * time.Minute, want: "900"},
		{maxAge: 1500 * time.Millisecond, want: "1"},
		{maxAge: -time.Minute, want: "0"},
	} {
		authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: tc.maxAge, HasMaxAge: true})

		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("parse URL: %v", err)
		}

		if got := u.Query().Get("max_age"); got != tc.want {
			t.Errorf("max_age for %s = %q, want %q", tc.maxAge, got, tc.want)
		}

		if !pending.HasMaxAge || pending.MaxAge != mustSeconds(t, tc.want) {
			t.Errorf("pending max age for %s = %s, want %s seconds", tc.maxAge, pending.MaxAge, tc.want)
		}
	}
}

// mustSeconds parses a number of seconds.
func mustSeconds(t *testing.T, s string) time.Duration {
	t.Helper()

	d, err := time.ParseDuration(s + "s")
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}

	return d
}

func TestFinishRefusesBadIDToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		faults oauthtest.Faults
	}{
		{name: "wrong issuer", faults: fault(func(f *oauthtest.Faults) { f.WrongIssuer = true })},
		{name: "wrong audience", faults: fault(func(f *oauthtest.Faults) { f.WrongAudience = true })},
		{name: "wrong nonce", faults: fault(func(f *oauthtest.Faults) { f.WrongNonce = true })},
		{name: "expired", faults: fault(func(f *oauthtest.Faults) { f.Expired = true })},
		{name: "bad signature", faults: fault(func(f *oauthtest.Faults) { f.BadSignature = true })},
		{name: "alg none", faults: fault(func(f *oauthtest.Faults) { f.AlgNone = true })},
		{name: "unknown key", faults: fault(func(f *oauthtest.Faults) { f.UnknownKey = true })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, oauthtest.RS256, tc.faults)

			authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
			code := authorize(t, s.provider, authURL).code

			_, err := s.client.Finish(t.Context(), code, pending)
			if !errors.Is(err, oauth.ErrIDToken) {
				t.Fatalf("Finish error = %v, want ErrIDToken", err)
			}

			checkNoSecret(t, err, code, pending)
		})
	}
}

// checkNoSecret fails the test when the text of err holds the code or a
// secret of the pending request.
func checkNoSecret(t *testing.T, err error, code string, p oauth.Pending) {
	t.Helper()

	for name, secret := range map[string]string{
		"code": code, "state": p.State, "nonce": p.Nonce, "verifier": p.Verifier, "client secret": clientSecret,
	} {
		if secret != "" && strings.Contains(err.Error(), secret) {
			t.Errorf("error text holds the %s: %v", name, err)
		}
	}
}

func TestFinishRefusesOtherNonce(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.ES256, noFaults())

	authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	code := authorize(t, s.provider, authURL).code

	// The callback of another request: the same code, with the nonce of
	// another pending request.
	_, other := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	pending.Nonce = other.Nonce

	_, err := s.client.Finish(t.Context(), code, pending)
	if !errors.Is(err, oauth.ErrIDToken) {
		t.Fatalf("Finish error = %v, want ErrIDToken", err)
	}
}

func TestFinishRefusesEmptyPendingNonce(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	code := authorize(t, s.provider, authURL).code

	pending.Nonce = ""

	_, err := s.client.Finish(t.Context(), code, pending)
	if !errors.Is(err, oauth.ErrIDToken) {
		t.Fatalf("Finish error = %v, want ErrIDToken", err)
	}
}

func TestFinishRefusedExchange(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(code *string, p *oauth.Pending)
	}{
		{name: "wrong verifier", change: func(_ *string, p *oauth.Pending) { p.Verifier = strings.Repeat("v", 43) }},
		{name: "unknown code", change: func(code *string, _ *oauth.Pending) { *code = "unknown-code-" + *code }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, oauthtest.RS256, noFaults())

			authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
			code := authorize(t, s.provider, authURL).code
			tc.change(&code, &pending)

			_, err := s.client.Finish(t.Context(), code, pending)
			if !errors.Is(err, oauth.ErrProvider) {
				t.Fatalf("Finish error = %v, want ErrProvider", err)
			}

			if !strings.Contains(err.Error(), `"invalid_grant"`) {
				t.Errorf("error = %v, want the OAuth error code invalid_grant", err)
			}

			checkNoSecret(t, err, code, pending)
		})
	}
}

func TestFinishCodeUsedOnce(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	code := authorize(t, s.provider, authURL).code

	if _, err := s.client.Finish(t.Context(), code, pending); err != nil {
		t.Fatalf("first Finish: %v", err)
	}

	if _, err := s.client.Finish(t.Context(), code, pending); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("second Finish error = %v, want ErrProvider", err)
	}
}

func TestMaxAgeSignsInAgain(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.EdDSA, noFaults())

	first, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	s.clock.Add(10 * time.Minute)

	// The authentication is 10 minutes old, inside a max_age of 15
	// minutes: the provider does not authenticate again.
	kept, err := s.signIn(t, oauth.Options{State: "", MaxAge: 15 * time.Minute, HasMaxAge: true})
	if err != nil {
		t.Fatalf("sign-in inside max_age: %v", err)
	}

	if !kept.AuthTime.Equal(first.AuthTime) || s.provider.SignIns() != 1 {
		t.Errorf("AuthTime = %v, sign-ins = %d, want %v and 1", kept.AuthTime, s.provider.SignIns(), first.AuthTime)
	}

	// max_age=0 makes the provider authenticate again, so auth_time is now.
	again, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: true})
	if err != nil {
		t.Fatalf("sign-in with max_age 0: %v", err)
	}

	if !again.AuthTime.Equal(s.clock.Now()) || s.provider.SignIns() != 2 {
		t.Errorf("AuthTime = %v, sign-ins = %d, want %v and 2", again.AuthTime, s.provider.SignIns(), s.clock.Now())
	}
}

func TestFinishRefusesAuthenticationOlderThanMaxAge(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	if _, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false}); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	s.clock.Add(10 * time.Minute)

	// A provider that ignores max_age: the request sends none, but the
	// pending request expects an authentication of at most 5 minutes.
	authURL, pending := s.client.Begin(oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	code := authorize(t, s.provider, authURL).code

	pending.MaxAge, pending.HasMaxAge = 5*time.Minute, true

	_, err := s.client.Finish(t.Context(), code, pending)
	if !errors.Is(err, oauth.ErrIDToken) || !strings.Contains(err.Error(), "max_age") {
		t.Fatalf("Finish error = %v, want ErrIDToken about max_age", err)
	}
}

func TestCheckAuthTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *int64 {
		sec := now.Add(d).Unix()

		return &sec
	}
	noMaxAge := oauth.Pending{State: "", Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false}
	maxAge := func(d time.Duration) oauth.Pending {
		return oauth.Pending{State: "", Nonce: "", Verifier: "", MaxAge: d, HasMaxAge: true}
	}

	for _, tc := range []struct {
		name    string
		claim   *int64
		pending oauth.Pending
		want    time.Time
		wantErr bool
	}{
		{name: "no claim, no max_age", claim: nil, pending: noMaxAge, want: now, wantErr: false},
		{name: "no claim with max_age", claim: nil, pending: maxAge(time.Hour), want: time.Time{}, wantErr: true},
		{name: "claim in the past", claim: at(-time.Hour), pending: noMaxAge, want: now.Add(-time.Hour), wantErr: false},
		{name: "claim inside the skew", claim: at(30 * time.Second), pending: noMaxAge, want: now.Add(30 * time.Second), wantErr: false},
		{name: "claim in the future", claim: at(2 * time.Minute), pending: noMaxAge, want: time.Time{}, wantErr: true},
		{name: "inside max_age", claim: at(-14 * time.Minute), pending: maxAge(15 * time.Minute), want: now.Add(-14 * time.Minute), wantErr: false},
		{name: "max_age plus skew", claim: at(-16 * time.Minute), pending: maxAge(15 * time.Minute), want: now.Add(-16 * time.Minute), wantErr: false},
		{name: "older than max_age", claim: at(-17 * time.Minute), pending: maxAge(15 * time.Minute), want: time.Time{}, wantErr: true},
		{name: "max_age 0, fresh", claim: at(0), pending: maxAge(0), want: now, wantErr: false},
		{name: "max_age 0, old", claim: at(-2 * time.Minute), pending: maxAge(0), want: time.Time{}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := oauth.CheckAuthTimeForTest(tc.claim, tc.pending, now)
			if tc.wantErr {
				if !errors.Is(err, oauth.ErrIDToken) {
					t.Fatalf("error = %v, want ErrIDToken", err)
				}

				return
			}

			if err != nil || !got.Equal(tc.want) {
				t.Fatalf("checkAuthTime = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestNewRefusesConfig(t *testing.T) {
	t.Parallel()

	valid := oauth.Config{
		HTTPClient: nil, Now: nil, Issuer: "https://idp.example", ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	}

	for _, tc := range []struct {
		name   string
		change func(*oauth.Config)
	}{
		{name: "no client ID", change: func(c *oauth.Config) { c.ClientID = "" }},
		{name: "no client secret", change: func(c *oauth.Config) { c.ClientSecret = "" }},
		{name: "no redirect URL", change: func(c *oauth.Config) { c.RedirectURL = "" }},
		{name: "relative issuer", change: func(c *oauth.Config) { c.Issuer = "idp.example" }},
		{name: "http issuer", change: func(c *oauth.Config) { c.Issuer = "http://idp.example" }},
		{name: "other scheme", change: func(c *oauth.Config) { c.Issuer = "ftp://idp.example" }},
		{name: "http on a private address", change: func(c *oauth.Config) { c.Issuer = "http://10.0.0.1" }},
		{name: "http on a name like loopback", change: func(c *oauth.Config) { c.Issuer = "http://localhost.example" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := valid
			tc.change(&cfg)

			if _, err := oauth.New(t.Context(), cfg); !errors.Is(err, oauth.ErrConfig) {
				t.Fatalf("New error = %v, want ErrConfig", err)
			}
		})
	}
}

func TestNewAllowsHTTPOnLoopback(t *testing.T) {
	t.Parallel()

	p := oauthtest.New(t, oauthtest.Config{
		Now: nil, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI, Algorithm: oauthtest.RS256,
		User: alice(), Mode: oauthtest.ModeOIDC, Faults: noFaults(), TLS: false,
	})

	_, err := oauth.New(t.Context(), oauth.Config{
		HTTPClient: p.Client(), Now: nil, Issuer: p.Issuer(), ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})
	if err != nil {
		t.Fatalf("New on a loopback http issuer: %v", err)
	}
}

func TestNewRefusesProviderWithoutDiscovery(t *testing.T) {
	t.Parallel()

	p := oauthtest.New(t, oauthtest.Config{
		Now: nil, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI, Algorithm: oauthtest.RS256,
		User: alice(), Mode: oauthtest.ModeOAuth, Faults: noFaults(), TLS: true,
	})

	_, err := oauth.New(t.Context(), oauth.Config{
		HTTPClient: p.Client(), Now: nil, Issuer: p.Issuer(), ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})
	if !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("New error = %v, want ErrProvider", err)
	}
}

func TestNewRefusesUnsupportedAlgorithms(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		algs string
	}{
		{name: "HMAC only", algs: `["HS256"]`},
		{name: "none only", algs: `["none"]`},
		{name: "no list", algs: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var srv *httptest.Server

			srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				body := `{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/authorize",` +
					`"token_endpoint":"` + srv.URL + `/token","jwks_uri":"` + srv.URL + `/jwks",` +
					`"id_token_signing_alg_values_supported":` + tc.algs + `}`
				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("write discovery: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			_, err := oauth.New(t.Context(), oauth.Config{
				HTTPClient: srv.Client(), Now: nil, Issuer: srv.URL, ClientID: clientID, ClientSecret: clientSecret,
				RedirectURL: redirectURI, Scopes: nil,
			})
			if !errors.Is(err, oauth.ErrProvider) {
				t.Fatalf("New error = %v, want ErrProvider", err)
			}
		})
	}
}

func TestNewAcceptsHTTPOnIPv6Loopback(t *testing.T) {
	t.Parallel()

	// Nothing listens on port 1: the configuration passes, and the
	// discovery fails.
	_, err := oauth.New(t.Context(), oauth.Config{
		HTTPClient: nil, Now: nil, Issuer: "http://[::1]:1", ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})
	if errors.Is(err, oauth.ErrConfig) || !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("New error = %v, want ErrProvider and not ErrConfig", err)
	}
}

// discoveryServer serves the discovery document that doc returns for the
// base URL of the server, with HTTPS.
func discoveryServer(t *testing.T, doc func(base string) map[string]any) *httptest.Server {
	t.Helper()

	var srv *httptest.Server

	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, err := json.Marshal(doc(srv.URL))
		if err != nil {
			t.Errorf("encode discovery: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")

		if _, err := w.Write(body); err != nil {
			t.Errorf("write discovery: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// discoveryDoc returns a valid discovery document for base.
func discoveryDoc(base string) map[string]any {
	return map[string]any{
		"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
		"jwks_uri": base + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
	}
}

// newFrom makes a client of the discovery server srv.
func newFrom(t *testing.T, srv *httptest.Server) error {
	t.Helper()

	_, err := oauth.New(t.Context(), oauth.Config{
		HTTPClient: srv.Client(), Now: nil, Issuer: srv.URL, ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})

	return err
}

func TestNewAcceptsValidDiscovery(t *testing.T) {
	t.Parallel()

	if err := newFrom(t, discoveryServer(t, discoveryDoc)); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNewRefusesInsecureEndpoints(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri"} {
		for _, value := range []string{"http://idp.example/x", "/relative", ""} {
			t.Run(field+" "+value, func(t *testing.T) {
				t.Parallel()

				srv := discoveryServer(t, func(base string) map[string]any {
					doc := discoveryDoc(base)
					doc[field] = value

					return doc
				})

				if err := newFrom(t, srv); !errors.Is(err, oauth.ErrProvider) {
					t.Fatalf("New error = %v, want ErrProvider", err)
				}
			})
		}
	}
}

func TestNewRefusesOtherIssuer(t *testing.T) {
	t.Parallel()

	srv := discoveryServer(t, func(base string) map[string]any {
		doc := discoveryDoc(base)
		doc["issuer"] = "https://other-issuer.example"

		return doc
	})

	if err := newFrom(t, srv); !errors.Is(err, oauth.ErrProvider) {
		t.Fatalf("New error = %v, want ErrProvider", err)
	}
}

func TestNewRefusesLargeAnswer(t *testing.T) {
	t.Parallel()

	srv := discoveryServer(t, func(base string) map[string]any {
		doc := discoveryDoc(base)
		doc["padding"] = strings.Repeat("x", 2<<20)

		return doc
	})

	err := newFrom(t, srv)
	if !errors.Is(err, oauth.ErrProvider) || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("New error = %v, want ErrProvider about 1 MiB", err)
	}
}

func TestNewRefusesRedirectToHTTP(t *testing.T) {
	t.Parallel()

	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)

	// The provider sends every request to one fixed HTTP URL.
	target := plain.URL + "/.well-known/openid-configuration"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	err := newFrom(t, srv)
	if !errors.Is(err, oauth.ErrProvider) || !strings.Contains(err.Error(), "from https to http") {
		t.Fatalf("New error = %v, want ErrProvider about the redirect", err)
	}
}

func TestFinishRefusesEmptySubject(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())
	s.provider.SetUser(oauthtest.User{Subject: "", Name: "No One", PreferredUsername: "", Email: ""})

	_, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
	if !errors.Is(err, oauth.ErrIDToken) || !strings.Contains(err.Error(), "sub") {
		t.Fatalf("Finish error = %v, want ErrIDToken about sub", err)
	}
}

func TestFinishAuthorizedParty(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		faults  oauthtest.Faults
		wantErr bool
	}{
		{name: "two audiences, no azp", faults: fault(func(f *oauthtest.Faults) { f.ExtraAudience = true }), wantErr: true},
		{name: "two audiences, azp of this client", faults: fault(func(f *oauthtest.Faults) {
			f.ExtraAudience, f.AuthorizedParty = true, clientID
		}), wantErr: false},
		{name: "two audiences, azp of another client", faults: fault(func(f *oauthtest.Faults) {
			f.ExtraAudience, f.AuthorizedParty = true, "another-client"
		}), wantErr: true},
		{name: "one audience, azp of this client", faults: fault(func(f *oauthtest.Faults) {
			f.AuthorizedParty = clientID
		}), wantErr: false},
		{name: "one audience, azp of another client", faults: fault(func(f *oauthtest.Faults) {
			f.AuthorizedParty = "another-client"
		}), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, oauthtest.RS256, tc.faults)

			_, err := s.signIn(t, oauth.Options{State: "", MaxAge: 0, HasMaxAge: false})
			if tc.wantErr != (err != nil) {
				t.Fatalf("Finish error = %v, want an error: %v", err, tc.wantErr)
			}

			if tc.wantErr && !errors.Is(err, oauth.ErrIDToken) {
				t.Fatalf("Finish error = %v, want ErrIDToken", err)
			}
		})
	}
}

func TestCheckState(t *testing.T) {
	t.Parallel()

	pending := func(state string) oauth.Pending {
		return oauth.Pending{State: state, Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false}
	}

	for _, tc := range []struct {
		name    string
		got     string
		pending oauth.Pending
		wantErr bool
	}{
		{name: "same state", got: "state-1", pending: pending("state-1"), wantErr: false},
		{name: "other state", got: "state-2", pending: pending("state-1"), wantErr: true},
		{name: "prefix", got: "state", pending: pending("state-1"), wantErr: true},
		{name: "empty state", got: "", pending: pending("state-1"), wantErr: true},
		{name: "empty pending", got: "", pending: pending(""), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := oauth.CheckState(tc.got, tc.pending)
			if tc.wantErr && !errors.Is(err, oauth.ErrState) {
				t.Fatalf("CheckState error = %v, want ErrState", err)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("CheckState error = %v, want nil", err)
			}
		})
	}
}

func TestBeginWithState(t *testing.T) {
	t.Parallel()

	s := newSetup(t, oauthtest.RS256, noFaults())

	const state = "dps_state-of-the-caller"

	authURL, pending := s.client.Begin(oauth.Options{State: state, MaxAge: 0, HasMaxAge: false})

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	if got := u.Query().Get("state"); got != state || pending.State != state {
		t.Fatalf("state = %q, pending %q; want %q", got, pending.State, state)
	}
}

func TestLazyReadsDiscoveryAtFirstUse(t *testing.T) {
	t.Parallel()

	var ready atomic.Bool

	inner := discoveryServer(t, discoveryDoc)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)

			return
		}

		inner.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	// The discovery of inner names inner as the issuer: serve it under the
	// URL of srv instead.
	inner.URL = srv.URL

	lazy, err := oauth.NewLazy(oauth.Config{
		HTTPClient: srv.Client(), Now: nil, Issuer: srv.URL, ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})
	if err != nil {
		t.Fatalf("NewLazy: %v", err)
	}

	opts := oauth.Options{State: "", MaxAge: 0, HasMaxAge: false}

	if _, _, downErr := lazy.Begin(t.Context(), opts); !errors.Is(downErr, oauth.ErrProvider) {
		t.Fatalf("Begin with the provider down = %v, want ErrProvider", downErr)
	}

	ready.Store(true)

	authURL, pending, err := lazy.Begin(t.Context(), opts)
	if err != nil || pending.State == "" || !strings.HasPrefix(authURL, srv.URL+"/authorize?") {
		t.Fatalf("Begin with the provider up = %q, %+v, %v", authURL, pending, err)
	}
}

func TestNewLazyRefusesConfig(t *testing.T) {
	t.Parallel()

	_, err := oauth.NewLazy(oauth.Config{
		HTTPClient: nil, Now: nil, Issuer: "http://idp.example", ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: redirectURI, Scopes: nil,
	})
	if !errors.Is(err, oauth.ErrConfig) {
		t.Fatalf("NewLazy error = %v, want ErrConfig", err)
	}
}
