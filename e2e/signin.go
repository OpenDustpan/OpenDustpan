//go:build e2e

package e2e

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// SignInProvider is the ID of the OpenID Connect provider of an env from
// NewEnvWithSignIn.
const SignInProvider = "corp"

// Client of the server at the test provider. The test provider knows only
// this client, so the values are not secrets.
const (
	signInClientID     = "dustpan"
	signInClientSecret = "client-secret-for-e2e"
)

// TestClock is a clock that a test moves. The server and the test provider
// of an env from NewEnvWithSignIn share it.
type TestClock struct {
	mu  sync.Mutex
	now time.Time
}

// newTestClock returns a TestClock at the current time, to the second.
func newTestClock() *TestClock {
	return &TestClock{mu: sync.Mutex{}, now: time.Now().UTC().Truncate(time.Second)}
}

// Now returns the time of the clock.
func (c *TestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// Advance moves the clock by d.
func (c *TestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// signInSetup is the console sign-in of an env: the public URL, the test
// provider and its clock. The in-process server shares the clock; the
// binary server keeps the real time, and so does the provider then.
type signInSetup struct {
	publicURL string
	idp       *oauthtest.Provider
	clock     *TestClock
	shared    bool
}

// options returns the server options of the sign-in.
func (s *signInSetup) options() []dustpan.Option {
	if s == nil {
		return nil
	}

	return []dustpan.Option{
		dustpan.WithClock(s.clock.Now),
		dustpan.WithPublicURL(s.publicURL),
		dustpan.WithOIDCProvider(dustpan.OIDCProvider{
			HTTPClient: s.idp.Client(), ID: SignInProvider, DisplayName: "Corp", Issuer: s.idp.Issuer(),
			ClientID: signInClientID, ClientSecret: signInClientSecret, Scopes: []string{"profile", "email"},
			AutoCreate: true,
		}),
	}
}

// newSignInSetup starts a test provider for a server at publicURL. With a
// shared clock, the provider runs on it and the server shares it; with
// nil, the provider runs on the real time, as a binary server does.
func newSignInSetup(tb testing.TB, publicURL string, shared *TestClock) *signInSetup {
	tb.Helper()

	clock, now := shared, time.Now
	if shared != nil {
		now = shared.Now
	} else {
		clock = newTestClock()
	}

	idp := oauthtest.New(tb, oauthtest.Config{
		Now: now, ClientID: signInClientID, ClientSecret: signInClientSecret,
		RedirectURI: publicURL + "/api/server/sign-in/" + SignInProvider + "/callback", Algorithm: oauthtest.RS256,
		User: oauthtest.User{
			Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
		},
		Mode: oauthtest.ModeOIDC, Faults: oauthtest.Faults{
			WrongIssuer: false, WrongAudience: false, WrongNonce: false, Expired: false, BadSignature: false,
			AlgNone: false, UnknownKey: false, SkipPKCE: false, ExtraAudience: false, AuthorizedParty: "",
			IssuedIn: 0, IgnoreMaxAge: false,
		},
		TLS: true,
	})

	return &signInSetup{publicURL: publicURL, idp: idp, clock: clock, shared: shared != nil}
}

// writeBinarySignIn writes the client secret and the certificate of the
// test provider into dir for a binary backend, and returns its sign-in.
// The secret file is readable by its owner only, as serve needs.
func writeBinarySignIn(tb testing.TB, dir string, s *signInSetup) *binarySignIn {
	tb.Helper()

	caFile := filepath.Join(dir, "oidc-ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Headers: nil, Bytes: s.idp.Certificate().Raw})

	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		tb.Fatalf("write the certificate of the test provider: %v", err)
	}

	secretFile := filepath.Join(dir, "oidc-client-secret")
	if err := os.WriteFile(secretFile, []byte(signInClientSecret+"\n"), 0o600); err != nil {
		tb.Fatalf("write the client secret: %v", err)
	}

	return &binarySignIn{publicURL: s.publicURL, issuer: s.idp.Issuer(), caFile: caFile, secretFile: secretFile}
}

// NewEnvWithSignIn starts a server as NewEnv does, with the console sign-in
// through a test OpenID provider, over HTTPS. The in-process server and
// the provider share a TestClock. With E2E_SERVER=binary, the binary gets
// the sign-in from its flags and environment variables, trusts the
// provider through SSL_CERT_FILE, and keeps the real time.
func NewEnvWithSignIn(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	in := envInput{
		deltaCutoff:       0,
		disableJournaling: false,
		ipAllowlists:      false,
		signIn:            true,
		journalRefuser:    nil,
		tb:                tb,
		cli:               cli,
		store:             store,
		dir:               tb.TempDir(),
		token:             newToken(tb),
		tokenB:            newToken(tb),
		rec:               &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}},
	}

	if UseBinaryServer() {
		return newBinaryEnv(ctx, in)
	}

	return newInprocessEnv(ctx, in)
}

// IdentityProvider returns the test provider of an env from
// NewEnvWithSignIn. It fails the test for any other env.
func (e *Env) IdentityProvider(tb testing.TB) *oauthtest.Provider {
	tb.Helper()

	if e.signIn == nil || e.signIn.idp == nil {
		tb.Fatal("the env has no test OpenID provider: use NewEnvWithSignIn")
	}

	return e.signIn.idp
}

// SharesClock reports whether the server of the env runs on the TestClock
// of its sign-in: true for the in-process server of NewEnvWithSignIn.
func (e *Env) SharesClock() bool {
	return e.signIn != nil && e.signIn.shared
}

// Clock returns the shared clock of an env from NewEnvWithSignIn. It fails
// the test for an env whose server does not share it (see SharesClock).
func (e *Env) Clock(tb testing.TB) *TestClock {
	tb.Helper()

	if !e.SharesClock() || e.signIn.clock == nil {
		tb.Fatal("the server of the env does not share a test clock: use the in-process NewEnvWithSignIn")
	}

	return e.signIn.clock
}

// BrowserClient returns an HTTP client with its own cookie jar that trusts
// the server and the test provider, and that follows no redirect, so a
// test drives each step of a browser sign-in.
func (e *Env) BrowserClient(tb testing.TB) *http.Client {
	tb.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		tb.Fatalf("cookie jar: %v", err)
	}

	client := *e.client
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return &client
}
