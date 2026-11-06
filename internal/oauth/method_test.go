package oauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// methodProvider is the provider ID of the methods of the tests.
const methodProvider = "corp"

// methodSetup is a test provider and a method over it.
type methodSetup struct {
	provider *oauthtest.Provider
	method   *oauth.Method
	clock    *clock
	off      *atomic.Bool
}

// newMethodSetup starts a provider in the mode and makes the method of the
// kind over it, with the client that the server uses for the mode.
func newMethodSetup(t *testing.T, mode oauthtest.Mode) methodSetup {
	t.Helper()

	clk := newClock()
	p := oauthtest.New(t, oauthtest.Config{
		Now: clk.Now, ClientID: clientID, ClientSecret: clientSecret, RedirectURI: redirectURI,
		Algorithm: oauthtest.RS256, User: alice(), Mode: mode, Faults: noFaults(), TLS: true,
	})

	var (
		client oauth.MethodClient
		err    error
		kind   = session.MethodOIDC
	)

	if mode == oauthtest.ModeOAuth {
		kind = session.MethodOAuth
		client, err = oauth.NewPlain(oauth.PlainConfig{
			HTTPClient: p.Client(), Now: clk.Now, AuthorizationURL: p.AuthorizationEndpoint(),
			TokenURL: p.TokenEndpoint(), UserinfoURL: p.UserinfoEndpoint(), ClientID: clientID,
			ClientSecret: clientSecret, RedirectURL: redirectURI, Scopes: []string{"read:user"},
			Claims: oauth.Claims{Subject: "", Name: "", Username: "", Email: ""},
		})
	} else {
		client, err = oauth.NewLazy(oauth.Config{
			HTTPClient: p.Client(), Now: clk.Now, Issuer: p.Issuer(), ClientID: clientID,
			ClientSecret: clientSecret, RedirectURL: redirectURI, Scopes: []string{"profile", "email"},
		})
	}

	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	off := &atomic.Bool{}

	m, err := oauth.NewMethod(oauth.MethodConfig{
		Client:  client,
		Enabled: func(context.Context) (bool, error) { return !off.Load(), nil },
		Now:     clk.Now, ProviderID: methodProvider, Kind: kind,
	})
	if err != nil {
		t.Fatalf("new method: %v", err)
	}

	return methodSetup{provider: p, method: m, clock: clk, off: off}
}

// browse sends the browser to the URL of r and returns the callback of the
// redirect back to the server, with the error of the provider when it sent
// one.
func (s methodSetup) browse(t *testing.T, r session.Redirect) session.Callback {
	t.Helper()

	browser := *s.provider.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.URL, http.NoBody)
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

	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization answer = %d, %v", resp.StatusCode, err)
	}

	pending := r.Pending
	q := back.Query()

	return session.Callback{
		Pending: &pending, State: q.Get("state"), Code: q.Get("code"), Ticket: "",
		ProviderError: q.Get("error"), SAMLResponse: nil,
	}
}

// fixture returns the fixture of the suite over s.
func (s methodSetup) fixture(mode oauthtest.Mode) sessiontest.RedirectFixture {
	answer := func(t *testing.T, r session.Redirect, p sessiontest.TestPerson, f oauthtest.Faults) session.Callback {
		t.Helper()

		s.provider.SetUser(oauthtest.User{
			Subject: p.Subject, Name: p.Name, PreferredUsername: p.UserName, Email: p.Email,
		})

		f.AuthTime = p.AuthTime
		s.provider.SetFaults(f)

		return s.browse(t, r)
	}

	return sessiontest.RedirectFixture{
		Method: s.method,
		Answer: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
			t.Helper()

			return answer(t, r, p, noFaults())
		},
		Broken: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
			t.Helper()

			if mode == oauthtest.ModeOAuth {
				return answer(t, r, p, fault(func(f *oauthtest.Faults) { f.UserinfoBody = `[1]` }))
			}

			return answer(t, r, p, fault(func(f *oauthtest.Faults) { f.BadSignature = true }))
		},
		AsksFresh: func(t *testing.T, r session.Redirect) bool {
			t.Helper()

			u, err := url.Parse(r.URL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}

			return u.Query().Get("max_age") == "0"
		},
		Stop:       func(*testing.T) { s.provider.Close() },
		Disable:    func(*testing.T) { s.off.Store(true) },
		Now:        s.clock.Now,
		ProviderID: methodProvider, AuthTime: mode == oauthtest.ModeOIDC, Groups: false, ReportsDisabled: false,
	}
}

// TestMethodConformance runs the suite of session.RedirectMethod on the
// OIDC method and on the plain OAuth 2.0 method.
func TestMethodConformance(t *testing.T) {
	t.Parallel()

	for name, mode := range map[string]oauthtest.Mode{"oidc": oauthtest.ModeOIDC, "oauth": oauthtest.ModeOAuth} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sessiontest.RunRedirectMethod(t, func(t *testing.T) sessiontest.RedirectFixture {
				t.Helper()

				return newMethodSetup(t, mode).fixture(mode)
			})
		})
	}
}

// TestMethodAuthTimeOfEachKind checks that an OIDC method carries the
// auth_time claim of the ID token, and that a plain OAuth 2.0 method never
// reports a time of the authentication.
func TestMethodAuthTimeOfEachKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		mode  oauthtest.Mode
		known bool
	}{
		{"oidc carries auth_time", oauthtest.ModeOIDC, true},
		{"oauth gives no auth time", oauthtest.ModeOAuth, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, authTime := signInAuthTimeAgo(t, tc.mode)

			want := time.Time{}
			if tc.known {
				want = authTime.Truncate(time.Second)
			}

			if a.AuthTimeKnown != tc.known || !a.AuthTime.Equal(want) {
				t.Fatalf("auth time = %v (known %v), want %v (known %v)", a.AuthTime, a.AuthTimeKnown, want, tc.known)
			}
		})
	}
}

// signInAuthTimeAgo signs in through a method of the mode whose provider
// sends an auth_time claim 10 minutes before now. It returns the Assertion,
// which passes the check of the sign-in service, and the time of the claim.
func signInAuthTimeAgo(t *testing.T, mode oauthtest.Mode) (session.Assertion, time.Time) {
	t.Helper()

	s := newMethodSetup(t, mode)
	authTime := s.clock.Now().Add(-10 * time.Minute)
	s.provider.SetFaults(fault(func(f *oauthtest.Faults) { f.AuthTime = authTime }))

	r, err := s.method.Begin(
		t.Context(),
		session.BeginInput{ProviderID: methodProvider, State: "c3RhdGU", Fresh: false},
	)
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}

	a, err := s.method.Finish(t.Context(), s.browse(t, r))
	if err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	if err := session.CheckAssertion(a, s.clock.Now()); err != nil {
		t.Fatalf("CheckAssertion() = %v", err)
	}

	return a, authTime
}

// stubClient is a client that answers what a test sets, as a client of
// another form might. It lets a test send hostile values to the checks of
// the method alone.
type stubClient struct {
	beginErr error
	finish   error
	authURL  string
	id       oauth.Identity
}

func (c stubClient) Begin(_ context.Context, opts oauth.Options) (string, oauth.Pending, error) {
	return c.authURL, oauth.Pending{
		State: opts.State, Nonce: "n", Verifier: "v", MaxAge: opts.MaxAge, HasMaxAge: opts.HasMaxAge,
	}, c.beginErr
}

func (c stubClient) Finish(context.Context, string, oauth.Pending) (oauth.Identity, error) {
	return c.id, c.finish
}

// stubNow is the clock of the stub methods.
func stubNow() time.Time {
	return time.Date(2026, time.November, 5, 9, 0, 0, 0, time.UTC)
}

// Errors of the stub clients.
var (
	errRefusedConnection = errors.New("connection refused")
	errNewKind           = errors.New("something new")
	errStoreDown         = errors.New("store down")
)

// stubMethod returns an OIDC method over c whose switch answers enabled.
func stubMethod(t *testing.T, c stubClient, enabled func(context.Context) (bool, error)) *oauth.Method {
	t.Helper()

	m, err := oauth.NewMethod(oauth.MethodConfig{
		Client: c, Enabled: enabled, Now: stubNow, ProviderID: methodProvider,
		Kind: session.MethodOIDC,
	})
	if err != nil {
		t.Fatalf("new method: %v", err)
	}

	return m
}

// alwaysOn is a switch of a provider that is on.
func alwaysOn(context.Context) (bool, error) { return true, nil }

// stubIdentity returns a valid identity with a known auth time.
func stubIdentity() oauth.Identity {
	return oauth.Identity{
		AuthTime: stubNow().Add(-time.Second), Subject: "alice-1", Name: "Alice", PreferredUsername: "alice",
		Email: "alice@example.com", AuthTimeKnown: true,
	}
}

// stubFinish runs Begin and Finish of an OIDC method over c.
func stubFinish(t *testing.T, c stubClient, fresh bool) (session.Assertion, error) {
	t.Helper()

	m := stubMethod(t, c, alwaysOn)

	r, err := m.Begin(t.Context(), session.BeginInput{ProviderID: methodProvider, State: "c3RhdGU", Fresh: fresh})
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}

	return m.Finish(t.Context(), session.Callback{
		Pending: &r.Pending, State: "c3RhdGU", Code: "code", Ticket: "", ProviderError: "", SAMLResponse: nil,
	})
}

// TestMethodChecksTheIdentityOfTheClient sends each hostile identity of a
// client to the checks of the method alone, as if the client had failed
// to refuse it. Each one is refused with ErrMethodAnswer and the zero
// Assertion.
func TestMethodChecksTheIdentityOfTheClient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(id *oauth.Identity)
		name   string
		fresh  bool
	}{
		{func(id *oauth.Identity) { id.Subject = "" }, "empty subject", false},
		{
			func(id *oauth.Identity) { id.Subject = strings.Repeat("s", session.MaxAssertionSubject+1) },
			"subject over the limit", false,
		},
		{func(id *oauth.Identity) { id.Subject = "alice\r\nadmin" }, "subject with a newline", false},
		{func(id *oauth.Identity) { id.Subject = "alice\u0085" }, "subject with a C1 control", false},
		{
			func(id *oauth.Identity) { id.Name = strings.Repeat("n", session.MaxAssertionName+1) },
			"name over the limit", false,
		},
		{func(id *oauth.Identity) { id.Email = "a\x00@example.com" }, "email with a control", false},
		{func(id *oauth.Identity) { id.AuthTime = stubNow().Add(time.Hour) }, "future auth time", false},
		{func(id *oauth.Identity) { id.AuthTimeKnown = false }, "fresh with no auth time", true},
		{func(id *oauth.Identity) { id.AuthTime = stubNow().Add(-time.Hour) }, "fresh older than its request", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := stubIdentity()
			tc.change(&id)

			a, err := stubFinish(t, stubClient{beginErr: nil, finish: nil, authURL: "https://idp.example/a", id: id},
				tc.fresh)
			if !errors.Is(err, session.ErrMethodAnswer) || a.Subject != "" || a.ProviderID != "" {
				t.Fatalf("Finish() = %+v, %v, want ErrMethodAnswer and the zero Assertion", a, err)
			}
		})
	}
}

// TestMethodErrorsOfTheClient checks the error of package session that the
// method gives for each class of error of a client. An error of no known
// class fails closed as ErrMethodAnswer.
func TestMethodErrorsOfTheClient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want error
		name string
	}{
		{oauth.ErrIDToken, session.ErrMethodAnswer, "ID token"},
		{oauth.ErrUserInfo, session.ErrMethodAnswer, "user-info answer"},
		{
			&url.Error{Op: "Post", URL: "https://idp.example/t", Err: errRefusedConnection},
			session.ErrMethodUnavailable, "network",
		},
		{context.DeadlineExceeded, session.ErrMethodUnavailable, "timeout"},
		{errNewKind, session.ErrMethodAnswer, "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, err := stubFinish(t, stubClient{
				beginErr: nil, finish: tc.err, authURL: "https://idp.example/a", id: stubIdentity(),
			}, false)
			if !errors.Is(err, tc.want) || a.Subject != "" {
				t.Fatalf("Finish() = %+v, %v, want %v", a, err, tc.want)
			}
		})
	}
}

// TestMethodBeginFailsClosed checks that Begin refuses an authorization URL
// that is not absolute and secure, a client that fails, and a switch that
// cannot be read.
func TestMethodBeginFailsClosed(t *testing.T) {
	t.Parallel()

	broken := func(context.Context) (bool, error) { return false, errStoreDown }

	cases := []struct {
		enabled func(context.Context) (bool, error)
		want    error
		client  stubClient
		name    string
	}{
		{alwaysOn, session.ErrMethodAnswer, stubClient{nil, nil, "http://idp.example/a", stubIdentity()}, "http URL"},
		{alwaysOn, session.ErrMethodAnswer, stubClient{nil, nil, "/a", stubIdentity()}, "relative URL"},
		{
			alwaysOn, session.ErrMethodAnswer,
			stubClient{nil, nil, "javascript:alert(1)", stubIdentity()},
			"script URL",
		},
		{
			alwaysOn, session.ErrMethodUnavailable,
			stubClient{oauth.ErrProvider, nil, "", stubIdentity()},
			"client fails",
		},
		{
			broken, session.ErrMethodUnavailable,
			stubClient{nil, nil, "https://idp.example/a", stubIdentity()},
			"switch cannot be read",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := stubMethod(t, tc.client, tc.enabled).Begin(t.Context(), session.BeginInput{
				ProviderID: methodProvider, State: "c3RhdGU", Fresh: false,
			})
			if !errors.Is(err, tc.want) || r.URL != "" {
				t.Fatalf("Begin() = %q, %v, want %v", r.URL, err, tc.want)
			}
		})
	}
}

// TestNewMethodRefusesConfig checks that a method needs a client, a switch,
// a valid provider ID and a redirect kind.
func TestNewMethodRefusesConfig(t *testing.T) {
	t.Parallel()

	c := stubClient{beginErr: nil, finish: nil, authURL: "https://idp.example/a", id: stubIdentity()}

	cases := []struct {
		cfg  oauth.MethodConfig
		name string
	}{
		{
			oauth.MethodConfig{Client: nil, Enabled: alwaysOn, Now: nil, ProviderID: "corp", Kind: session.MethodOIDC},
			"no client",
		},
		{
			oauth.MethodConfig{Client: c, Enabled: nil, Now: nil, ProviderID: "corp", Kind: session.MethodOIDC},
			"no switch",
		},
		{
			oauth.MethodConfig{Client: c, Enabled: alwaysOn, Now: nil, ProviderID: "Corp:1", Kind: session.MethodOIDC},
			"provider ID",
		},
		{
			oauth.MethodConfig{Client: c, Enabled: alwaysOn, Now: nil, ProviderID: "corp", Kind: session.MethodSAML},
			"kind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := oauth.NewMethod(tc.cfg); !errors.Is(err, oauth.ErrConfig) {
				t.Fatalf("NewMethod() = %v, want ErrConfig", err)
			}
		})
	}
}
