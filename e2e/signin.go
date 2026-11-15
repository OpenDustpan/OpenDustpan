//go:build e2e

package e2e

import (
	"context"
	"encoding/pem"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// SignInProvider is the ID of the OpenID Connect provider of an env from
// NewEnvWithSignIn.
const SignInProvider = "corp"

// StoredSignInProvider is the ID that a test gives the second test
// provider of an env from NewEnvWithSignIn when it creates the provider
// through the API.
const StoredSignInProvider = "beta"

// Client of the server at the test provider. The test provider knows only
// this client, so the values are not secrets.
const (
	signInClientID     = "dustpan"
	signInClientSecret = "client-secret-for-e2e"
	// StoredClientID is the client of the server at the second test
	// provider.
	StoredClientID = "dustpan-beta"
	// StoredClientSecret is the client secret at the second test provider.
	StoredClientSecret = "client-secret-of-beta-for-e2e"
	// PlainClientID is the client of the server at the plain OAuth 2.0
	// test provider.
	PlainClientID = "dustpan-hub"
	// PlainClientSecret is the client secret at the plain OAuth 2.0 test
	// provider.
	PlainClientSecret = "client-secret-of-hub-for-e2e"
)

// PlainSignInProvider is the ID of the plain OAuth 2.0 provider that a
// test creates through the API.
const PlainSignInProvider = "hub"

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
	beta      *oauthtest.Provider
	hub       *oauthtest.Provider
	clock     *TestClock
	// ldap is the test LDAP directory of the LDAP providers, or nil.
	ldap   *ldaptest.Server
	shared bool
	// mu makes the sign-ins of PersonalToken take turns: the test
	// provider signs in one person at a time.
	mu sync.Mutex
	// adminMu guards adminID, the person that FreshAdminAPIStatus made.
	adminMu sync.Mutex
	adminID string
}

// options returns the server options of the sign-in.
func (s *signInSetup) options() []dustpan.Option {
	if s == nil {
		return nil
	}

	opts := []dustpan.Option{
		dustpan.WithPublicURL(s.publicURL),
		dustpan.WithOIDCProvider(dustpan.OIDCProvider{
			HTTPClient: s.idp.Client(), ID: SignInProvider, DisplayName: "Corp", Issuer: s.idp.Issuer(),
			ClientID: signInClientID, ClientSecret: signInClientSecret, Scopes: []string{"profile", "email"},
			AutoCreate: true,
		}),
		dustpan.WithSignInHTTPClient(s.beta.Client()),
	}

	if s.shared {
		opts = append(opts, dustpan.WithClock(s.clock.Now))
	}

	return append(opts, s.ldapOptions()...)
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
		User: testUser(),
		Mode: oauthtest.ModeOIDC, Faults: noFaults(),
		TLS: true,
	})

	beta := oauthtest.New(tb, oauthtest.Config{
		Now: now, ClientID: StoredClientID, ClientSecret: StoredClientSecret,
		RedirectURI: publicURL + "/api/server/sign-in/" + StoredSignInProvider + "/callback",
		Algorithm:   oauthtest.RS256,
		User:        testUser(),
		Mode:        oauthtest.ModeOIDC, Faults: noFaults(),
		TLS: true,
	})

	// The plain OAuth 2.0 provider serves HTTP on a loopback host, which a
	// provider in the oauth mode may use, so the binary server needs no
	// certificate for it.
	hub := oauthtest.New(tb, oauthtest.Config{
		Now: now, ClientID: PlainClientID, ClientSecret: PlainClientSecret,
		RedirectURI: publicURL + "/api/server/sign-in/" + PlainSignInProvider + "/callback",
		Algorithm:   oauthtest.RS256,
		User:        testUser(),
		Mode:        oauthtest.ModeOAuth, Faults: noFaults(), TLS: false,
	})

	return &signInSetup{
		publicURL: publicURL, idp: idp, beta: beta, hub: hub, clock: clock, ldap: nil, shared: shared != nil,
		mu:      sync.Mutex{},
		adminMu: sync.Mutex{}, adminID: "",
	}
}

// testUser returns the person whom each test provider signs in.
func testUser() oauthtest.User {
	return oauthtest.User{
		Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
	}
}

// noFaults returns the faults of a test provider that breaks no rule.
func noFaults() oauthtest.Faults {
	return oauthtest.Faults{
		WrongIssuer: false, WrongAudience: false, WrongNonce: false, Expired: false, BadSignature: false,
		AlgNone: false, UnknownKey: false, SkipPKCE: false, ExtraAudience: false, AuthorizedParty: "",
		IssuedIn: 0, IgnoreMaxAge: false, TokenType: "", UserinfoStatus: 0, UserinfoBody: "", UserinfoType: "",
		UserinfoRedirect: "", TokenRedirect: "", AuthTime: time.Time{},
	}
}

// writeBinarySignIn writes the client secret and the certificate of the
// test provider into dir for a binary backend, and returns its sign-in.
// The secret file is readable by its owner only, as serve needs. With
// secretInVariable, the sign-in passes the secret in the variable in place
// of the file.
func writeBinarySignIn(tb testing.TB, dir string, s *signInSetup, secretInVariable bool) *binarySignIn {
	tb.Helper()

	// One file trusts both test providers: the provider of the flags and
	// the provider that a test creates through the API.
	caFile := filepath.Join(dir, "oidc-ca.pem")
	ca := pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Headers: nil, Bytes: s.idp.Certificate().Raw})
	ca = append(
		ca,
		pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Headers: nil, Bytes: s.beta.Certificate().Raw})...,
	)

	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		tb.Fatalf("write the certificate of the test provider: %v", err)
	}

	secretFile := filepath.Join(dir, "oidc-client-secret")
	if err := os.WriteFile(secretFile, []byte(signInClientSecret+"\n"), 0o600); err != nil {
		tb.Fatalf("write the client secret: %v", err)
	}

	secret, err := fs.ReadFile(os.DirFS(dir), "oidc-client-secret")
	if err != nil {
		tb.Fatalf("read the client secret: %v", err)
	}

	return &binarySignIn{
		publicURL:        s.publicURL,
		issuer:           s.idp.Issuer(),
		caFile:           caFile,
		secretFile:       secretFile,
		secret:           strings.TrimSpace(string(secret)),
		secretInVariable: secretInVariable,
	}
}

// NewEnvWithSignIn starts a server as NewEnv does, with the console sign-in
// through a test OpenID provider, over HTTPS. The in-process server and
// the provider share a TestClock. With E2E_SERVER=binary, the binary gets
// the sign-in from its flags and environment variables, trusts the
// provider through SSL_CERT_FILE, and keeps the real time.
func NewEnvWithSignIn(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	return startSignInEnv(ctx, tb, cli, store, false)
}

// NewEnvWithSignInSecretInVariable starts a server as NewEnvWithSignIn does,
// with the client secret in DUSTPAN_OIDC_CLIENT_SECRET in place of
// DUSTPAN_OIDC_CLIENT_SECRET_FILE. It always runs the server binary.
func NewEnvWithSignInSecretInVariable(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	return startSignInEnvWith(ctx, tb, cli, store, signInEnvOptions{
		issuer: nil, ldap: nil, realTime: false, ipAllowlists: false, secretInVariable: true,
	})
}

// NewEnvWithRealTimeSignIn starts a server as NewEnvWithSignIn does, but the
// in-process server and the test provider keep the real time, as the
// server binary does. Use it for a test that signs persons in with
// PersonalToken and needs time to pass on the server.
func NewEnvWithRealTimeSignIn(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	return startSignInEnv(ctx, tb, cli, store, true)
}

// NewEnvWithIssuer starts a server as NewEnvWithRealTimeSignIn does, with the
// reads of the keys of an issuer allowed on the loopback and the certificate
// of issuer trusted, so a test can exchange the tokens of the fake issuer.
// Only the in-process server takes the issuer.
func NewEnvWithIssuer(ctx context.Context, tb testing.TB, cli Install, store Store, issuer *OIDCIssuerFake) *Env {
	tb.Helper()

	return startSignInEnvWith(
		ctx,
		tb,
		cli,
		store,
		signInEnvOptions{issuer: issuer, ldap: nil, realTime: true, ipAllowlists: false, secretInVariable: false},
	)
}

// NewEnvWithIPAllowlistsAndSignIn starts a server as NewEnvWithIPAllowlists
// does, with the console sign-in of NewEnvWithRealTimeSignIn, so a step can
// change an allowlist with the fresh console session that the change needs.
func NewEnvWithIPAllowlistsAndSignIn(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	return startSignInEnvWith(
		ctx,
		tb,
		cli,
		store,
		signInEnvOptions{
			issuer: nil, ldap: nil, realTime: true, ipAllowlists: true, secretInVariable: false,
		},
	)
}

// signInEnvOptions are the switches of a sign-in env.
type signInEnvOptions struct {
	issuer           *OIDCIssuerFake
	ldap             *ldaptest.Server
	realTime         bool
	ipAllowlists     bool
	secretInVariable bool
}

// startSignInEnv starts a sign-in env. realTime runs the in-process
// server on the real time instead of a TestClock.
func startSignInEnv(ctx context.Context, tb testing.TB, cli Install, store Store, realTime bool) *Env {
	tb.Helper()

	return startSignInEnvWith(
		ctx,
		tb,
		cli,
		store,
		signInEnvOptions{issuer: nil, ldap: nil, realTime: realTime, ipAllowlists: false, secretInVariable: false},
	)
}

// startSignInEnvWith starts a sign-in env with the switches opts.
func startSignInEnvWith(ctx context.Context, tb testing.TB, cli Install, store Store, opts signInEnvOptions) *Env {
	tb.Helper()

	realTime := opts.realTime

	in := envInput{
		deltaCutoff:            0,
		sinkSenders:            nil,
		auditSinks:             nil,
		disableJournaling:      false,
		ipAllowlists:           opts.ipAllowlists,
		issuer:                 opts.issuer,
		ldap:                   opts.ldap,
		signIn:                 true,
		signInSecretInVariable: opts.secretInVariable,
		signInRealTime:         realTime,
		passkeys:               false,
		journalRefuser:         nil,
		journalCap:             nil,
		tb:                     tb,
		cli:                    cli,
		store:                  store,
		dir:                    tb.TempDir(),
		token:                  newToken(tb),
		tokenB:                 newToken(tb),
		rec:                    &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}},
	}

	// The variable reaches only the server binary, so a sign-in with the
	// secret in the variable always runs the binary.
	if UseBinaryServer() || opts.secretInVariable {
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

// PlainIdentityProvider returns the plain OAuth 2.0 test provider of an
// env from NewEnvWithSignIn, which a test creates through the API as the
// provider PlainSignInProvider. It fails the test for any other env.
func (e *Env) PlainIdentityProvider(tb testing.TB) *oauthtest.Provider {
	tb.Helper()

	if e.signIn == nil || e.signIn.hub == nil {
		tb.Fatal("the env has no plain OAuth 2.0 test provider: use NewEnvWithSignIn")
	}

	return e.signIn.hub
}

// StoredIdentityProvider returns the second test provider of an env from
// NewEnvWithSignIn, which a test creates through the API as the provider
// StoredSignInProvider. It fails the test for any other env.
func (e *Env) StoredIdentityProvider(tb testing.TB) *oauthtest.Provider {
	tb.Helper()

	if e.signIn == nil || e.signIn.beta == nil {
		tb.Fatal("the env has no second test OpenID provider: use NewEnvWithSignIn")
	}

	return e.signIn.beta
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

// RunTofuWithToken runs the OpenTofu CLI tofu with args in a new project
// of the workspace Workspace in Namespace, with token as the credential of
// the server in its CLI configuration. A non-zero exit is reported in the
// Result, not as an error.
func (e *Env) RunTofuWithToken(
	ctx context.Context, tb testing.TB, tofu Install, token string, args ...string,
) (Result, error) {
	tb.Helper()

	dir := tb.TempDir()
	rc := filepath.Join(dir, "tofurc")

	e.write(tb, filepath.Join(dir, "main.tf"), fmt.Sprintf(tofuConfig, e.Host(), Namespace, Workspace))
	e.write(tb, rc, fmt.Sprintf(tofuCLIConfig, e.Host(), token))

	return e.RunAs(ctx, tofu, dir, []string{"TF_CLI_CONFIG_FILE=" + rc}, args...)
}
