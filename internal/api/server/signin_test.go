package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

const (
	signInBase     = "https://dustpan.example"
	signInCallback = signInBase + "/api/server/sign-in/corp/callback"
	consoleOrigin  = "https://console.example"
)

// fakeLimiter records the failures of the sign-in routes, and refuses
// each request when refuse is set.
type fakeLimiter struct {
	mu       sync.Mutex
	failures int
	refuse   bool
}

func (l *fakeLimiter) Admit(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.refuse {
		return identity.ErrRateLimited
	}

	return nil
}

func (l *fakeLimiter) Fail(context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.failures++
}

func (l *fakeLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.failures
}

// signInEnv is a handler with one OpenID Connect provider, the test
// provider behind it, and the store under both.
type signInEnv struct {
	handler testHandler
	idp     *oauthtest.Provider
	store   *sqlite.Store
	limiter *fakeLimiter
	clock   *envClock
}

// envClock is the clock that the test provider, the OIDC client and the
// sessions of a signInEnv share. A test moves it.
type envClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *envClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// Add moves the clock by d.
func (c *envClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func alice() oauthtest.User {
	return oauthtest.User{
		Subject:           "248289761001",
		Name:              "Alice Doe",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
	}
}

func newSignInEnv(t *testing.T, autoCreate bool) signInEnv {
	t.Helper()

	return newSignInEnvWith(t, signInEnvOptions{
		autoCreate: autoCreate, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil,
	})
}

// signInEnvOptions are the options of a sign-in environment. autoCreate
// lets the sign-in create a principal. noPolicyRoutes gives the handler no
// session policy service, so it mounts no session policy route. stored,
// when set, gives the handler the service and the lookup of the stored
// providers, for the store and the clock of the environment.
type signInEnvOptions struct {
	stored         func(st *sqlite.Store, clock *envClock) (*session.ProviderAdmin, apiserver.StoredProviders)
	authz          identity.Authorizer
	namespaces     []namespace.Name
	autoCreate     bool
	noPolicyRoutes bool
	// hostile, when set, changes each Assertion that the method of the
	// provider corp returns, as a method that fails to check its answer
	// would.
	hostile func(a *session.Assertion)
	// passwords are the password providers of the handler. With one or
	// more, the handler gets the limiter of the environment as its password
	// budget, so it mounts the password route of a provider.
	passwords []apiserver.PasswordProvider
	// noGateEngine leaves the gate config service without a policy engine.
	noGateEngine bool
}

// seedNamespaces makes the namespaces names in svc.
func seedNamespaces(t *testing.T, svc *namespace.Service, names []namespace.Name) {
	t.Helper()

	for _, name := range names {
		if _, err := svc.Create(t.Context(), name); err != nil {
			t.Fatalf("seed namespace %s: %v", name, err)
		}
	}
}

func newSignInEnvWith(t *testing.T, opts signInEnvOptions) signInEnv {
	t.Helper()

	var noFaults oauthtest.Faults

	clock := &envClock{mu: sync.Mutex{}, now: time.Now().UTC().Truncate(time.Second)}

	idp := oauthtest.New(t, oauthtest.Config{
		Now: clock.Now, ClientID: "dustpan", ClientSecret: "client-secret-for-tests", RedirectURI: signInCallback,
		Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOIDC, Faults: noFaults, TLS: true,
	})

	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: idp.Client(), Now: clock.Now, Issuer: idp.Issuer(), ClientID: "dustpan",
		ClientSecret: "client-secret-for-tests", RedirectURL: signInCallback, Scopes: []string{"profile", "email"},
	})
	if err != nil {
		t.Fatalf("oauth client: %v", err)
	}

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), testNamespace); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	seedNamespaces(t, namespaces, opts.namespaces)

	authz := opts.authz
	if authz == nil {
		authz = selectiveAuthorizer{deny: map[identity.Action]bool{}}
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: clock.Now})
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Providers: st, Inactivity: nil, Now: clock.Now},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}
	principals := seedRoutePrincipals(t, st)

	policyRoutes := policies
	if opts.noPolicyRoutes {
		policyRoutes = nil
	}

	var (
		admin  *session.ProviderAdmin
		stored apiserver.StoredProviders
	)

	if opts.stored != nil {
		admin, stored = opts.stored(st, clock)
	}

	var engine gate.Engine
	if !opts.noGateEngine {
		engine = testGateEngine()
	}

	gates, err := gate.NewConfigService(gate.ConfigServiceConfig{
		Store: st, Namespaces: namespaces, Approvers: testApprovers{}, Engine: engine, Now: clock.Now,
	})
	if err != nil {
		t.Fatalf("gate config service: %v", err)
	}

	grants, allowlists := newSignInAccess(t, st, namespaces, clock)

	signIn := &apiserver.SignIn{
		Service:         newTestSignInService(t, st, sessions, principals),
		OnDevRefused:    nil,
		DevCode:         nil,
		DevAdmin:        noBootstrapAdmin,
		Sessions:        sessions,
		Limiter:         limiter,
		PasswordLimiter: nil,
		Passkeys:        nil, PasskeySignIn: nil, PasskeyBeginLimiter: nil, PasskeyLimiter: nil,
		Stored:            stored,
		Admin:             admin,
		PasswordProviders: nil, Origins: []string{signInBase, consoleOrigin},
		Providers: []apiserver.SignInProvider{
			{
				Method: hostileMethod(redirectMethod(t, client, "corp", clock.Now), opts.hostile), ID: "corp",
				DisplayName: "Corp",
				AutoCreate:  opts.autoCreate, Stored: false,
			},
		},
	}
	addPasswordProviders(signIn, opts.passwords, limiter)

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          st,
		Principals:       principals,
		Reach:            routeReach(st),
		Tokens:           seedRouteTokens(t, st),
		Access:           grants,
		Audit:            st,
		OnAuditFailure:   nil,
		DeletedUnits:     stubSweeper{},
		AuditSinks:       nil,
		AuditChain:       newFakeAuditChain(audit.ChainOn),
		IPAllowlists:     allowlists,
		Gates:            gates,
		SessionPolicy:    policyRoutes,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		SignIn:           signIn,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return signInEnv{handler: handler, idp: idp, store: st, limiter: limiter, clock: clock}
}

// addPasswordProviders gives in the password providers passwords and, with
// one or more, limiter as the password budget, so the handler mounts the
// password route of a provider.
func addPasswordProviders(in *apiserver.SignIn, passwords []apiserver.PasswordProvider, limiter *fakeLimiter) {
	if len(passwords) == 0 {
		return
	}

	in.PasswordProviders = passwords
	in.PasswordLimiter = limiter
}

// get sends a GET request with the cookies to the handler.
func (e signInEnv) get(t *testing.T, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

// cookieOf returns the cookie with the name that rec sets, or nil.
func cookieOf(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}

	return nil
}

// start begins a sign-in and returns the sign-in cookie and the URL of the
// identity provider.
func (e signInEnv) start(t *testing.T, returnPath string) (*http.Cookie, string) {
	t.Helper()

	q := url.Values{}
	if returnPath != "" {
		q.Set("return", returnPath)
	}

	return e.startQuery(t, q)
}

// startQuery begins a sign-in with the query q, as start does.
func (e signInEnv) startQuery(t *testing.T, q url.Values) (*http.Cookie, string) {
	t.Helper()

	target := "/api/server/sign-in/corp"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}

	rec := e.get(t, target)
	if rec.Code != http.StatusFound {
		t.Fatalf("start = %d, want 302: %s", rec.Code, rec.Body.String())
	}

	cookie := cookieOf(rec, apiserver.SignInCookie)
	if cookie == nil {
		t.Fatal("start set no sign-in cookie")
	}

	return cookie, rec.Header().Get("Location")
}

// authorize sends the browser request to the identity provider and returns
// the query of its redirect back to the callback.
func (e signInEnv) authorize(t *testing.T, authURL string) url.Values {
	t.Helper()

	browser := *e.idp.Client()
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

	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(back.String(), signInCallback) {
		t.Fatalf("redirect = %q, %v; want the callback", resp.Header.Get("Location"), err)
	}

	return back.Query()
}

// callback sends the callback with the query q and the cookies.
func (e signInEnv) callback(t *testing.T, q url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	return e.get(t, "/api/server/sign-in/corp/callback?"+q.Encode(), cookies...)
}

// signIn runs a whole sign-in and returns the session cookie.
func (e signInEnv) signIn(t *testing.T) *http.Cookie {
	t.Helper()

	binding, authURL := e.start(t, "/console/tokens")

	rec := e.callback(t, e.authorize(t, authURL), binding)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	sess := cookieOf(rec, apiserver.SessionCookie)
	if sess == nil {
		t.Fatal("callback set no session cookie")
	}

	return sess
}

// sessionBody is the answer of GET /api/server/session.
type sessionBody struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Provider    string `json:"provider"`
	CSRFToken   string `json:"csrfToken"`
}

// readSession answers GET /api/server/session with the session cookie.
func (e signInEnv) readSession(t *testing.T, cookie *http.Cookie) (int, sessionBody) {
	t.Helper()

	rec := e.get(t, "/api/server/session", cookie)

	var body sessionBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode session: %v", err)
		}
	}

	return rec.Code, body
}

// serverEntries returns the audit entries of @server.
func (e signInEnv) serverEntries(t *testing.T) []audit.Entry {
	t.Helper()

	listing, err := e.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	return listing.Entries
}

// refusedReason returns the reason of the last refused sign-in entry, or "".
func (e signInEnv) refusedReason(t *testing.T) (string, audit.Outcome) {
	t.Helper()

	entries := e.serverEntries(t)
	for i := range slices.Backward(entries) {
		en := &entries[i]
		if en.Action != session.ActionSignIn || en.Outcome == audit.OutcomeSuccess {
			continue
		}

		for _, c := range en.Changes {
			if c.Key == "reason" {
				return c.New, en.Outcome
			}
		}
	}

	return "", ""
}

// checkBrowserCookie fails the test when c is not a cookie of the console:
// Secure, HttpOnly, the SameSite mode wantSameSite, Path=/, no Domain, and a
// lifetime.
func checkBrowserCookie(t *testing.T, name string, c *http.Cookie, wantSameSite http.SameSite) {
	t.Helper()

	switch {
	case c == nil:
		t.Fatalf("no %s cookie", name)
	case !c.Secure, !c.HttpOnly, c.SameSite != wantSameSite:
		t.Errorf("%s cookie = %+v, want Secure, HttpOnly and SameSite mode %d", name, c, wantSameSite)
	case c.Path != "/", c.Domain != "", c.MaxAge <= 0:
		t.Errorf("%s cookie = %+v, want Path=/, no Domain and a Max-Age", name, c)
	default:
	}
}

// checkOIDCPrincipal fails the test when no oidc principal with the
// subject and the display name exists.
func (e signInEnv) checkOIDCPrincipal(t *testing.T, subject, displayName string) {
	t.Helper()

	p, err := e.store.GetPrincipalBySubject(t.Context(), subject)
	if err != nil || p.Source != principal.SourceOIDC || p.DisplayName != displayName {
		t.Fatalf("principal = %+v, %v; want an oidc person %s named %s", p, err, subject, displayName)
	}
}

func TestSignInFlow(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	binding, authURL := e.start(t, "/console/tokens")
	checkBrowserCookie(t, "sign-in", binding, http.SameSiteLaxMode)

	q := e.authorize(t, authURL)

	// The callback ignores a return path in its own query: the request
	// stored the return path.
	q.Set("return", "https://evil.example/")

	rec := e.callback(t, q, binding)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/console/tokens" {
		t.Fatalf("callback = %d to %q, want 303 to /console/tokens: %s", rec.Code, rec.Header().Get("Location"),
			rec.Body.String())
	}

	if cleared := cookieOf(rec, apiserver.SignInCookie); cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("callback did not delete the sign-in cookie: %+v", cleared)
	}

	sess := cookieOf(rec, apiserver.SessionCookie)
	if sess == nil {
		t.Fatal("the callback set no session cookie")
	}

	checkBrowserCookie(t, "session", sess, http.SameSiteStrictMode)

	if !strings.HasPrefix(sess.Value, session.SecretPrefix) {
		t.Errorf("session cookie value does not start with %q", session.SecretPrefix)
	}

	e.checkOIDCPrincipal(t, "corp:248289761001", "Alice Doe")

	code, body := e.readSession(t, sess)
	if code != http.StatusOK || body.Subject != "corp:248289761001" || body.Provider != "corp" || body.CSRFToken == "" {
		t.Fatalf("session = %d %+v", code, body)
	}
}

func TestSignInAuditEntries(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	e.signIn(t)

	var principalEntry, sessionEntry bool

	for _, en := range e.serverEntries(t) {
		if en.Action != session.ActionSignIn || en.Outcome != audit.OutcomeSuccess {
			continue
		}

		if en.Subject != "corp:248289761001" || en.IDPSubject != "248289761001" || en.Channel != audit.ChannelConsole {
			t.Errorf("sign-in entry = %+v", en)
		}

		principalEntry = principalEntry ||
			strings.HasPrefix(en.Object, "principal:") && !strings.Contains(en.Object, "#")
		sessionEntry = sessionEntry || strings.Contains(en.Object, "#session:ses-")
	}

	if !principalEntry || !sessionEntry {
		t.Fatalf("entries for the new principal %v and the session %v, want both", principalEntry, sessionEntry)
	}
}

func TestSignInSessionRequests(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	code, body := e.readSession(t, sess)
	if code != http.StatusOK {
		t.Fatalf("session = %d", code)
	}

	signOut := func(headers map[string]string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/sign-out", http.NoBody)
		req.AddCookie(sess)

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)

		return rec.Code
	}

	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{name: "no CSRF header", headers: map[string]string{"Origin": consoleOrigin}, want: http.StatusForbidden},
		{name: "wrong CSRF header", headers: map[string]string{
			apiserver.CSRFHeader: "wrong", "Origin": consoleOrigin,
		}, want: http.StatusForbidden},
		{name: "no origin and no Fetch Metadata", headers: map[string]string{
			apiserver.CSRFHeader: body.CSRFToken,
		}, want: http.StatusForbidden},
		{name: "no origin and same-site Fetch Metadata", headers: map[string]string{
			apiserver.CSRFHeader: body.CSRFToken, "Sec-Fetch-Site": "same-site",
		}, want: http.StatusForbidden},
		{name: "other origin", headers: map[string]string{
			apiserver.CSRFHeader: body.CSRFToken, "Origin": "https://evil.example",
		}, want: http.StatusForbidden},
		{name: "cross-site fetch", headers: map[string]string{
			apiserver.CSRFHeader: body.CSRFToken, "Sec-Fetch-Site": "cross-site",
		}, want: http.StatusForbidden},
	} {
		if got := signOut(tc.headers); got != tc.want {
			t.Errorf("sign-out with %s = %d, want %d", tc.name, got, tc.want)
		}
	}

	allowed := map[string]string{apiserver.CSRFHeader: body.CSRFToken, "Origin": consoleOrigin}
	if got := signOut(allowed); got != http.StatusNoContent {
		t.Fatalf("sign-out = %d, want 204", got)
	}

	if code, _ := e.readSession(t, sess); code != http.StatusUnauthorized {
		t.Fatalf("session after sign-out = %d, want 401", code)
	}
}

// TestSignOutRunsInMaintenanceMode proves that a sign-out answers 204 while the
// installation is in maintenance mode, through the route, the service and the
// store, and that the session is over afterwards.
func TestSignOutRunsInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	_, body := e.readSession(t, sess)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/sign-out", http.NoBody)
	req.AddCookie(sess)
	req.Header.Set(apiserver.CSRFHeader, body.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", consoleOrigin)

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("sign-out in maintenance mode = %d %s, want 204", rec.Code, rec.Body.String())
	}

	if err := e.store.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	if code, _ := e.readSession(t, sess); code != http.StatusUnauthorized {
		t.Fatalf("session after the sign-out in maintenance mode = %d, want 401", code)
	}
}

func TestSignInHeaderWinsOverCookie(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", http.NoBody)
	req.AddCookie(sess)
	req.Header.Set("Authorization", "Bearer not-a-token")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("request with a bad token and a good cookie = %d, want 401", rec.Code)
	}

	// A valid token wins too: the caller has no console session then.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/session", http.NoBody)
	req.AddCookie(sess)
	req.Header.Set("Authorization", "Bearer test-token")

	rec = httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("request with a good token and a good cookie = %d, want 404", rec.Code)
	}
}

// tokenTargets are the eight routes that create or rotate a token.
func tokenTargets() []string {
	principalTokens := "/api/server/principals/" + testOtherPrincipalID + "/tokens"

	return []string{
		"/api/server/service-tokens",
		"/api/server/service-tokens/" + testServiceTokenID + "/rotate",
		"/api/server/caller/tokens",
		"/api/server/caller/tokens/" + testServiceTokenID + "/rotate",
		principalTokens,
		principalTokens + "/" + testServiceTokenID + "/rotate",
	}
}

// postWithSession sends a POST with the session cookie and its CSRF value.
func (e signInEnv) postWithSession(t *testing.T, sess *http.Cookie, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	return e.sendWithSession(t, http.MethodPost, sess, target, body)
}

// sendWithSession sends a request with method, the session cookie and its
// CSRF value.
func (e signInEnv) sendWithSession(
	t *testing.T, method string, sess *http.Cookie, target, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	_, s := e.readSession(t, sess)

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.AddCookie(sess)
	req.Header.Set(apiserver.CSRFHeader, s.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

// ownTokenBody is the body of an own token that expires in one day.
func ownTokenBody() string {
	return `{"name":"laptop","expiresAt":"` + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) + `"}`
}

// staleSession signs in and moves the clock past the default token
// freshness window, with one use of the session on the way, so the
// session is still active.
func (e signInEnv) staleSession(t *testing.T) *http.Cookie {
	t.Helper()

	sess := e.signIn(t)

	e.clock.Add(10 * time.Minute)
	e.readSession(t, sess)
	e.clock.Add(session.DefaultTokenFreshness - 10*time.Minute + time.Minute)

	if code, _ := e.readSession(t, sess); code != http.StatusOK {
		t.Fatalf("stale session = %d, want an active session", code)
	}

	return sess
}

// TestSignInTokenFreshness proves that a console session creates a token
// while its sign-in is fresh, and that each route that creates or rotates
// a token answers 401 with the path of a new sign-in once the sign-in is
// older than the token freshness window.
func TestSignInTokenFreshness(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	rec := e.postWithSession(t, e.signIn(t), "/api/server/caller/tokens", ownTokenBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("own token from a fresh session = %d %s, want 201", rec.Code, rec.Body.String())
	}

	stale := e.staleSession(t)

	for _, target := range tokenTargets() {
		rec := e.postWithSession(t, stale, target, `{"name":"laptop","principalId":"`+testOtherPrincipalID+`"}`)

		var body struct {
			Message        string `json:"message"`
			Reauthenticate string `json:"reauthenticate"`
			Code           int    `json:"code"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the answer of %s: %v", target, err)
		}

		if rec.Code != http.StatusUnauthorized || body.Reauthenticate != "/api/server/sign-in/corp?fresh=1" ||
			!strings.Contains(body.Message, "sign in again") {
			t.Errorf("POST %s from a stale session = %d %s, want 401 with the path of a new sign-in", target,
				rec.Code, rec.Body.String())
		}
	}
}

// TestSignInFreshSignIn proves that a sign-in with fresh=1 asks the
// identity provider for a new authentication (max_age=0), and that its new
// session creates a token.
func TestSignInFreshSignIn(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	e.staleSession(t)

	binding, authURL := e.startQuery(t, url.Values{"fresh": {"1"}, "return": {"/console/tokens"}})

	u, err := url.Parse(authURL)
	if err != nil || u.Query().Get("max_age") != "0" {
		t.Fatalf("authorization URL = %q, %v; want max_age=0", authURL, err)
	}

	rec := e.callback(t, e.authorize(t, authURL), binding)
	if rec.Code != http.StatusSeeOther || e.idp.SignIns() != 2 {
		t.Fatalf("fresh callback = %d with %d authentications, want 303 after a second one: %s", rec.Code,
			e.idp.SignIns(), rec.Body.String())
	}

	fresh := cookieOf(rec, apiserver.SessionCookie)
	if fresh == nil {
		t.Fatal("the fresh sign-in set no session cookie")
	}

	if rec := e.postWithSession(t, fresh, "/api/server/caller/tokens", ownTokenBody()); rec.Code != http.StatusCreated {
		t.Fatalf("own token after a fresh sign-in = %d %s, want 201", rec.Code, rec.Body.String())
	}

	if rec := e.get(t, "/api/server/sign-in/corp?fresh=yes"); rec.Code != http.StatusBadRequest {
		t.Fatalf("start with fresh=yes = %d, want 400", rec.Code)
	}
}

// TestSignInFreshRefusesOldAuthentication proves that the callback of a
// sign-in with fresh=1 refuses an authentication that is older than the
// max_age of the request, when the identity provider does not honor it.
func TestSignInFreshRefusesOldAuthentication(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	e.staleSession(t)

	var faults oauthtest.Faults

	faults.IgnoreMaxAge = true
	e.idp.SetFaults(faults)

	binding, authURL := e.startQuery(t, url.Values{"fresh": {"1"}})

	rec := e.callback(t, e.authorize(t, authURL), binding)
	if rec.Code != http.StatusBadRequest || cookieOf(rec, apiserver.SessionCookie) != nil {
		t.Fatalf("callback with an old authentication = %d, want 400 with no session: %s", rec.Code,
			rec.Body.String())
	}

	if reason, _ := e.refusedReason(t); reason != "ID token was refused" {
		t.Fatalf("audit reason = %q, want the refusal of the ID token", reason)
	}
}

// policyChangeEntry is the audit entry of a change of the session policy
// at the time at.
func policyChangeEntry(at time.Time) audit.Entry {
	return audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "server:manage-sign-in", Object: session.PolicyRef, Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// TestSignInFreshnessWindowOff proves that a window of 0 lets a session
// with an old sign-in create a token.
func TestSignInFreshnessWindowOff(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	stale := e.staleSession(t)

	_, err := e.store.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = 0, false, e.clock.Now()

		return old, policyChangeEntry(e.clock.Now()), nil
	})
	if err != nil {
		t.Fatalf("turn the window off: %v", err)
	}

	if rec := e.postWithSession(t, stale, "/api/server/caller/tokens", ownTokenBody()); rec.Code != http.StatusCreated {
		t.Fatalf("own token with the window off = %d %s, want 201", rec.Code, rec.Body.String())
	}
}

func TestSignInCallbackRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, e signInEnv, binding *http.Cookie, q url.Values) (url.Values, []*http.Cookie)
		reason string
	}{
		{
			name: "no sign-in cookie", reason: "callback does not match the sign-in request",
			mutate: func(_ *testing.T, _ signInEnv, _ *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				return q, nil
			},
		},
		{
			name: "other state", reason: "callback does not match the sign-in request",
			mutate: func(_ *testing.T, _ signInEnv, b *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				q.Set("state", q.Get("state")+"x")

				return q, []*http.Cookie{b}
			},
		},
		{
			name: "cookie of another sign-in", reason: "callback does not match the sign-in request",
			mutate: func(t *testing.T, e signInEnv, _ *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				t.Helper()

				other, _ := e.start(t, "")

				return q, []*http.Cookie{other}
			},
		},
		{
			name: "error of the provider", reason: "identity provider answered an error",
			mutate: func(_ *testing.T, _ signInEnv, b *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				q.Set("error", "access_denied")

				return q, []*http.Cookie{b}
			},
		},
		{
			name: "wrong code", reason: "identity provider answered an error",
			mutate: func(_ *testing.T, _ signInEnv, b *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				q.Set("code", "not-a-code")

				return q, []*http.Cookie{b}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSignInEnv(t, true)
			binding, authURL := e.start(t, "")
			q, cookies := tc.mutate(t, e, binding, e.authorize(t, authURL))
			before := e.limiter.count()

			rec := e.callback(t, q, cookies...)
			if rec.Code != http.StatusBadRequest || cookieOf(rec, apiserver.SessionCookie) != nil {
				t.Fatalf("callback = %d, want 400 with no session: %s", rec.Code, rec.Body.String())
			}

			if reason, outcome := e.refusedReason(t); reason != tc.reason || outcome != audit.OutcomeFailure {
				t.Errorf("audit reason = %q %s, want %q failure", reason, outcome, tc.reason)
			}

			if got := e.limiter.count() - before; got != 1 {
				t.Errorf("failures counted by the callback = %d, want 1", got)
			}
		})
	}
}

func TestSignInCallbackWorksOnce(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	binding, authURL := e.start(t, "")
	q := e.authorize(t, authURL)

	if rec := e.callback(t, q, binding); rec.Code != http.StatusSeeOther {
		t.Fatalf("first callback = %d, want 303", rec.Code)
	}

	rec := e.callback(t, q, binding)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("second callback = %d, want 400", rec.Code)
	}

	if reason, _ := e.refusedReason(t); reason != "callback does not match the sign-in request" {
		t.Errorf("audit reason = %q", reason)
	}
}

func TestSignInWithoutAutoCreate(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, false)
	binding, authURL := e.start(t, "")

	rec := e.callback(t, e.authorize(t, authURL), binding)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "no principal") {
		t.Fatalf("callback = %d %s, want 403 that names no principal", rec.Code, rec.Body.String())
	}

	// A refused sign-in is not a missing permission: a grant does not help,
	// so the answer carries sign-in.refused, not access.denied.
	if !strings.Contains(rec.Body.String(), `"errorCode":"`+errcode.SignInRefused+`"`) {
		t.Errorf("a refused sign-in answers another code than %s: %s", errcode.SignInRefused, rec.Body.String())
	}

	if reason, outcome := e.refusedReason(t); reason != "no principal" || outcome != audit.OutcomeDenied {
		t.Errorf("audit = %q %s, want no principal, denied", reason, outcome)
	}

	_, err := e.store.GetPrincipalBySubject(t.Context(), "corp:248289761001")
	if !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("a refused sign-in made a principal: %v", err)
	}
}

func TestSignInStartRefusals(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	for _, tc := range []struct {
		target string
		want   int
	}{
		{target: "/api/server/sign-in/other", want: http.StatusNotFound},
		{target: "/api/server/sign-in/corp?return=" + url.QueryEscape("https://evil.example/"), want: http.StatusBadRequest},
		{target: "/api/server/sign-in/corp?return=" + url.QueryEscape("//evil.example/"), want: http.StatusBadRequest},
		{target: "/api/server/sign-in/other/callback?state=x&code=y", want: http.StatusNotFound},
	} {
		if rec := e.get(t, tc.target); rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.target, rec.Code, tc.want)
		}
	}
}

func TestSignInLimited(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	e.limiter.mu.Lock()
	e.limiter.refuse = true
	e.limiter.mu.Unlock()

	for _, target := range []string{"/api/server/sign-in/corp", "/api/server/sign-in/corp/callback?state=x&code=y"} {
		if rec := e.get(t, target); rec.Code != http.StatusTooManyRequests {
			t.Errorf("GET %s over the limit = %d, want 429", target, rec.Code)
		}
	}
}

func TestSignInStartSpendsBudget(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	e.start(t, "")

	if got := e.limiter.count(); got != 1 {
		t.Fatalf("failures counted by one start that works = %d, want 1", got)
	}

	if rec := e.get(t, "/api/server/sign-in/unknown"); rec.Code != http.StatusNotFound || e.limiter.count() != 2 {
		t.Fatalf("start of an unknown provider = %d with %d failures counted, want 404 with 2", rec.Code,
			e.limiter.count())
	}
}

func TestSignInSessionCookieOnlyForSignIn(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	// The session reaches a route of the installation API that needs no
	// grant.
	if rec := e.get(t, "/api/server/namespaces", sess); rec.Code != http.StatusOK {
		t.Fatalf("GET namespaces with the session = %d, want 200", rec.Code)
	}

	// A cookie that is not a session authenticates nothing.
	bad := &http.Cookie{
		Name: apiserver.SessionCookie, Value: session.SecretPrefix + strings.Repeat("A", 43), Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	if rec := e.get(t, "/api/server/namespaces", bad); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET namespaces with an unknown session = %d, want 401", rec.Code)
	}
}

// redirectMethod returns the OIDC sign-in method over client for the
// provider id with the clock now, as the server makes it. The provider is
// always on.
func redirectMethod(t *testing.T, client oauth.MethodClient, id string, now func() time.Time) *oauth.Method {
	t.Helper()

	m, err := oauth.NewMethod(oauth.MethodConfig{
		Client: client, Enabled: func(context.Context) (bool, error) { return true, nil }, Now: now,
		ProviderID: id, Kind: session.MethodOIDC,
	})
	if err != nil {
		t.Fatalf("sign-in method: %v", err)
	}

	return m
}

// downOIDC is a provider that cannot be reached.
type downOIDC struct{}

func (downOIDC) Begin(context.Context, oauth.Options) (string, oauth.Pending, error) {
	return "", oauth.Pending{State: "", Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false}, oauth.ErrProvider
}

func (downOIDC) Finish(context.Context, string, oauth.Pending) (oauth.Identity, error) {
	return oauth.Identity{
		AuthTime: time.Time{}, Subject: "", Name: "", PreferredUsername: "", Email: "", AuthTimeKnown: false,
	}, oauth.ErrProvider
}

func TestSignInProviderDown(t *testing.T) {
	t.Parallel()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Providers: nil, Inactivity: nil, Now: nil},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st, Guard: allowAllGuard{}, GroupReach: allowAllReach{}, Namespaces: nil, Tokens: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("principals: %v", err)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          st,
		Principals:       principals,
		Tokens:           nil,
		Access:           nil,
		Audit:            st,
		OnAuditFailure:   nil,
		DeletedUnits:     stubSweeper{},
		AuditSinks:       nil,
		AuditChain:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    policies,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		SignIn: &apiserver.SignIn{
			Service:      newTestSignInService(t, st, sessions, principals),
			OnDevRefused: nil,
			DevCode:      nil,
			DevAdmin:     noBootstrapAdmin,
			Sessions:     sessions, Stored: nil, Admin: nil,
			Limiter:           limiter,
			PasswordProviders: nil, Origins: nil,
			PasswordLimiter: nil,
			Passkeys:        nil, PasskeySignIn: nil, PasskeyBeginLimiter: nil, PasskeyLimiter: nil,
			Providers: []apiserver.SignInProvider{
				{
					Method: redirectMethod(t, downOIDC{}, "corp", time.Now), ID: "corp", DisplayName: "Corp",
					AutoCreate: true, Stored: false,
				},
			},
		},
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/sign-in/corp", http.NoBody),
	)

	if rec.Code != http.StatusBadGateway || cookieOf(rec, apiserver.SignInCookie) != nil {
		t.Fatalf("start with the provider down = %d, want 502 with no cookie: %s", rec.Code, rec.Body.String())
	}

	if limiter.count() != 1 {
		t.Fatalf("failures counted by a start with the provider down = %d, want 1", limiter.count())
	}
}

// TestSignInSessionChangeRecordsTheConsoleChannel proves that the audit
// entry of a change by a console session has the channel console, also
// when the request declares another channel.
func TestSignInSessionChangeRecordsTheConsoleChannel(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)
	_, body := e.readSession(t, sess)
	before := e.sessionChanges(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/server/caller/tokens",
		strings.NewReader(`{"name":"laptop"}`))
	req.AddCookie(sess)
	req.Header.Set(apiserver.CSRFHeader, body.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Dustpan-Channel", "api")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	after := e.sessionChanges(t)
	if len(after) != len(before)+1 {
		t.Fatalf("change entries of the session = %d after the request and %d before, want one more (answer %d %s)",
			len(after), len(before), rec.Code, rec.Body.String())
	}

	for i := range after {
		if after[i].Channel != audit.ChannelConsole {
			t.Fatalf("entry %s of the session has the channel %q, want console", after[i].Action, after[i].Channel)
		}
	}
}

// sessionChanges returns the change entries of the signed-in person.
func (e signInEnv) sessionChanges(t *testing.T) []audit.Entry {
	t.Helper()

	entries := e.serverEntries(t)

	var changes []audit.Entry

	for i := range entries {
		if entries[i].Kind == audit.KindChange && entries[i].Subject == "corp:248289761001" {
			changes = append(changes, entries[i])
		}
	}

	return changes
}

// freshSignIn signs in with fresh=1, so the identity provider
// authenticates again, and returns the session cookie.
func (e signInEnv) freshSignIn(t *testing.T) *http.Cookie {
	t.Helper()

	binding, authURL := e.startQuery(t, url.Values{"fresh": {"1"}})

	rec := e.callback(t, e.authorize(t, authURL), binding)

	sess := cookieOf(rec, apiserver.SessionCookie)
	if rec.Code != http.StatusSeeOther || sess == nil {
		t.Fatalf("fresh callback = %d %s, want 303 with a session", rec.Code, rec.Body.String())
	}

	return sess
}

// TestSignInSessionPolicyChangeNeedsAFreshSignIn proves that a console
// session whose sign-in is older than the token freshness window cannot
// change the session policy: it answers 401 with the path of a new sign-in
// and the policy stays. A fresh session changes it.
func TestSignInSessionPolicyChangeNeedsAFreshSignIn(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	stale := e.staleSession(t)

	rec := e.sendWithSession(t, http.MethodPatch, stale, "/api/server/session-policy", `{"tokenFreshnessSeconds":0}`)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "/api/server/sign-in/corp?fresh=1") {
		t.Fatalf("policy change from a stale session = %d %s, want 401 with the path of a new sign-in",
			rec.Code, rec.Body.String())
	}

	policy, err := e.store.GetSessionPolicy(t.Context())
	if err != nil {
		t.Fatalf("read the policy: %v", err)
	}

	if !policy.TokenFreshnessDefault {
		t.Fatalf("token freshness after a refused change = %s (default %t), want the default",
			policy.TokenFreshness, policy.TokenFreshnessDefault)
	}

	rec = e.sendWithSession(t, http.MethodPatch, e.freshSignIn(t), "/api/server/session-policy",
		`{"tokenFreshnessSeconds":600}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("policy change from a fresh session = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestSignInFreshnessWithNoPolicyService proves that a token change from a
// console session uses the default token freshness window when the
// handler has no session policy service: it fails closed.
func TestSignInFreshnessWithNoPolicyService(t *testing.T) {
	t.Parallel()

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: true, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil,
	})

	created := e.postWithSession(t, e.signIn(t), "/api/server/caller/tokens", ownTokenBody())
	if created.Code != http.StatusCreated {
		t.Fatalf("own token from a fresh session = %d %s, want 201", created.Code, created.Body.String())
	}

	rec := e.postWithSession(t, e.staleSession(t), "/api/server/caller/tokens", ownTokenBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("own token from a stale session with no policy service = %d %s, want 401",
			rec.Code, rec.Body.String())
	}
}

// TestSignInTokenEntryNamesTheSession proves that the audit entry of a
// token that a console session creates records the session ID, and that
// the sign-in entry of that session names the same session.
func TestSignInTokenEntryNamesTheSession(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	created := e.postWithSession(t, e.signIn(t), "/api/server/caller/tokens", ownTokenBody())
	if created.Code != http.StatusCreated {
		t.Fatalf("own token from a fresh session = %d %s, want 201", created.Code, created.Body.String())
	}

	entries := e.serverEntries(t)

	sessionID := tokenEntrySession(entries)
	if sessionID == "" {
		t.Fatal("the token entry records no session ID")
	}

	for i := range entries {
		if entries[i].Action == session.ActionSignIn && strings.HasSuffix(entries[i].Object, sessionID) {
			return
		}
	}

	t.Fatalf("no sign-in entry names the session %q of the token entry", sessionID)
}

// tokenEntrySession returns the session ID that the successful own token
// entry of entries records, or "".
func tokenEntrySession(entries []audit.Entry) string {
	for i := range entries {
		if entries[i].Action != string(identity.ActionTokenManageOwn) || entries[i].Outcome != audit.OutcomeSuccess {
			continue
		}

		for _, c := range entries[i].Changes {
			if c.Key == "sessionId" && c.Old == nil {
				return c.New
			}
		}
	}

	return ""
}

// TestSignInPolicyEntriesNameTheSession proves that the audit entries of a
// change of the token policy and of the session policy from a console
// session record the session ID next to the changed settings.
func TestSignInPolicyEntriesNameTheSession(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)

	for _, tc := range []struct {
		target, body, key string
	}{
		{"/api/server/token-policy", `{"inactivitySeconds":0}`, "inactivitySeconds"},
		{"/api/server/session-policy", `{"tokenFreshnessSeconds":600}`, "tokenFreshnessSeconds"},
	} {
		if rec := e.sendWithSession(t, http.MethodPatch, sess, tc.target, tc.body); rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s from a session = %d %s, want 200", tc.target, rec.Code, rec.Body.String())
		}

		if !sessionChangeHas(e.sessionChanges(t), tc.key) {
			t.Errorf("no entry of the session records %s and the session ID", tc.key)
		}
	}
}

// sessionChangeHas reports whether one of entries records the key and a
// session ID.
func sessionChangeHas(entries []audit.Entry, key string) bool {
	for i := range entries {
		var hasKey, hasSession bool

		for _, c := range entries[i].Changes {
			hasKey = hasKey || c.Key == key
			hasSession = hasSession || (c.Key == "sessionId" && c.New != "")
		}

		if hasKey && hasSession {
			return true
		}
	}

	return false
}

// checkSubjectOverLimitRefused signs in with the sub and proves the
// composed subject is refused for length with an audit entry that names
// the provider only and holds no sub value.
func checkSubjectOverLimitRefused(t *testing.T, sub, display string) {
	t.Helper()

	e := newSignInEnv(t, true)
	e.idp.SetUser(oauthtest.User{
		Subject:           sub,
		Name:              display,
		PreferredUsername: "longsub",
		Email:             "longsub@example.com",
	})

	binding, authURL := e.start(t, "")
	rec := e.callback(t, e.authorize(t, authURL), binding)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("callback = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), "subject over 255 bytes") {
		t.Fatalf("body misses the limit message: %s", rec.Body.String())
	}

	if cookieOf(rec, apiserver.SessionCookie) != nil {
		t.Fatal("refused sign-in set a session cookie")
	}

	if reason, outcome := e.refusedReason(t); reason != "subject over 255 bytes" ||
		outcome != audit.OutcomeDenied {
		t.Fatalf("audit reason = %q %s, want %q denied", reason, outcome, "subject over 255 bytes")
	}

	checkLengthRefusalEntry(t, e, sub)
}

// checkLengthRefusalEntry proves the audit entry of the length refusal
// names the provider only and no entry keeps the sub.
func checkLengthRefusalEntry(t *testing.T, e signInEnv, sub string) {
	t.Helper()

	var found bool

	entries := e.serverEntries(t)
	for i := range entries {
		if strings.Contains(entries[i].Subject+entries[i].IDPSubject, sub) {
			t.Fatalf("audit entry keeps the long subject: %+v", entries[i])
		}

		if entries[i].Action != session.ActionSignIn ||
			!slices.ContainsFunc(entries[i].Changes, func(c audit.Change) bool {
				return c.Key == "reason" && c.New == "subject over 255 bytes"
			}) {
			continue
		}

		found = true

		if entries[i].Subject != "sign-in:corp" || entries[i].IDPSubject != "" {
			t.Fatalf("refused entry names %q with sub value %q, want the provider only",
				entries[i].Subject, entries[i].IDPSubject)
		}
	}

	if !found {
		t.Fatal("no audit entry records the length refusal")
	}
}

// TestSignInSubjectOverLimitRefused proves a composed subject over 255
// bytes is refused with the documented message and an audit entry that
// names the provider only and holds no sub value.
func TestSignInSubjectOverLimitRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		sub     string
		display string
	}{
		{name: "one byte over the composed limit", sub: strings.Repeat("a", 251), display: "Long Sub"},
		{
			name:    "sub of 255 bytes is over the composed limit",
			sub:     strings.Repeat("a", 255),
			display: "Capped Sub",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSubjectOverLimitRefused(t, tc.sub, tc.display)
		})
	}
}

// TestSignInSubjectAtLimitPassesTheLengthCheck proves a composed subject of
// exactly 255 bytes passes the length check: the sub uses letters and
// digits only, so the principal check accepts it and the sign-in works.
func TestSignInSubjectAtLimitPassesTheLengthCheck(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sub := strings.Repeat("a", 250)
	subject := "corp:" + sub

	if len(subject) != 255 {
		t.Fatalf("composed subject length = %d, want 255", len(subject))
	}

	if len(sub) == 255 {
		t.Fatal("test checks the sub length instead of the composed length")
	}

	e.idp.SetUser(oauthtest.User{
		Subject:           sub,
		Name:              "Capped Sub",
		PreferredUsername: "cappedsub",
		Email:             "cappedsub@example.com",
	})

	binding, authURL := e.start(t, "")
	rec := e.callback(t, e.authorize(t, authURL), binding)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if cookieOf(rec, apiserver.SessionCookie) == nil {
		t.Fatal("sign-in at the limit set no session cookie")
	}

	if reason, _ := e.refusedReason(t); reason == "subject over 255 bytes" {
		t.Fatalf("audit reason = %q, want no length refusal", reason)
	}

	e.checkOIDCPrincipal(t, subject, "Capped Sub")
}

// makeSiteAdministrator signs the test person in once, so that the person
// exists, and gives it the site administrator role on the installation. A
// grant or a role change by a console session then passes the check that the
// granter holds what the grant gives.
func (e signInEnv) makeSiteAdministrator(t *testing.T) {
	t.Helper()

	_, body := e.readSession(t, e.freshSignIn(t))

	person, err := e.store.GetPrincipalBySubject(t.Context(), body.Subject)
	if err != nil {
		t.Fatalf("read the signed-in person: %v", err)
	}

	at := e.clock.Now()
	obj := identity.ServerObject()
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-site-" + person.ID, Role: access.RoleSiteAdministrator,
		CreatedBy: "admin", Subject: access.Subject{Kind: access.SubjectPrincipal, ID: person.ID},
	}

	fence, entry := storetest.GrantWrite(t, e.store, obj, at, "grant:"+grt.ID)
	if err := e.store.CreateGrant(t.Context(), grt, fence, entry, nil); err != nil {
		t.Fatalf("make the person a site administrator: %v", err)
	}
}

// newSignInAccess returns the grant service and the IP allowlist service of
// a sign-in env, over st.
func newSignInAccess(
	t *testing.T, st *sqlite.Store, namespaces *namespace.Service, clock *envClock,
) (*access.Service, *ipallow.Service) {
	t.Helper()

	grantAuthz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: stubCollections{}, Units: st, Principals: st},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: grantAuthz,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
		},
		Now: clock.Now, NewID: nil,
		BootstrapAdmins: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	allowlists, err := ipallow.NewService(ipallow.Config{
		Store: st, Namespaces: namespaces, Now: clock.Now, Enabled: true,
	})
	if err != nil {
		t.Fatalf("ip allowlist service: %v", err)
	}

	return grants, allowlists
}

// TestSignInJoinsNoGroup proves the rule of the group claims while the
// server has no group mapping: a sign-in joins no group, also when groups
// exist whose names an identity provider could send. The methods of today
// return no groups, and no mapping exists, so no membership changes.
func TestSignInJoinsNoGroup(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	for _, name := range []string{"engineering", "corp", alice().PreferredUsername} {
		g := principal.Group{
			CreatedAt: e.clock.Now(), UpdatedAt: e.clock.Now(), ID: "grp-" + name, Name: name, Description: "",
			Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
		}
		if err := e.store.CreateGroup(t.Context(), g, policyChangeEntry(e.clock.Now())); err != nil {
			t.Fatalf("create group %s: %v", name, err)
		}
	}

	e.signIn(t)

	p, err := e.store.GetPrincipalBySubject(t.Context(), "corp:"+alice().Subject)
	if err != nil {
		t.Fatalf("principal of the sign-in: %v", err)
	}

	groups, total, err := e.store.ListGroupsOf(
		t.Context(),
		p.ID,
		principal.Query{After: "", Low: "", High: "", Limit: 10},
	)
	if err != nil || total != 0 || len(groups) != 0 {
		t.Fatalf("groups of the person = %v (%d), %v, want none", groups, total, err)
	}
}

// hostileRedirect is a sign-in method that changes each Assertion of the
// method it wraps, as a method that fails to check the answer of its
// provider would.
type hostileRedirect struct {
	session.RedirectMethod

	change func(a *session.Assertion)
}

func (m hostileRedirect) Finish(ctx context.Context, cb session.Callback) (session.Assertion, error) {
	a, err := m.RedirectMethod.Finish(ctx, cb)
	if err == nil && m.change != nil {
		m.change(&a)
	}

	return a, err
}

// hostileMethod returns m behind hostileRedirect, which changes each
// Assertion of m with change. A nil change changes nothing.
func hostileMethod(m *oauth.Method, change func(a *session.Assertion)) hostileRedirect {
	return hostileRedirect{RedirectMethod: m, change: change}
}

// TestSignInCallbackRefusesAHostileAssertion sends a hostile Assertion of a
// method straight to the check of the callback, as if the method had failed
// to refuse the answer of its provider. The callback refuses it with the
// reason of an Assertion that is not valid, sets no session cookie and makes
// no principal.
func TestSignInCallbackRefusesAHostileAssertion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		change func(a *session.Assertion)
		name   string
	}{
		{func(a *session.Assertion) { a.Subject = "" }, "empty subject"},
		{func(a *session.Assertion) { a.AuthTime = time.Now().Add(24 * time.Hour) }, "future auth time"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSignInEnvWith(t, signInEnvOptions{
				autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil,
				namespaces: nil, hostile: tc.change, passwords: nil,
			})

			binding, authURL := e.start(t, "/console/tokens")
			rec := e.callback(t, e.authorize(t, authURL), binding)

			checkHostileRefusal(t, e, rec)
		})
	}
}

// checkHostileRefusal fails unless the callback rec refused the sign-in with
// the reason of an Assertion that is not valid, set no session cookie and
// made no principal.
func checkHostileRefusal(t *testing.T, e signInEnv, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "sign-in failed") {
		t.Fatalf("callback = %d %s, want 400 and the refusal of the sign-in", rec.Code, rec.Body.String())
	}

	if c := cookieOf(rec, apiserver.SessionCookie); c != nil && c.Value != "" {
		t.Fatal("the callback of a hostile Assertion set a session cookie")
	}

	if reason, _ := e.refusedReason(t); reason != "sign-in assertion is not valid" {
		t.Fatalf("audit reason = %q, want the reason of an Assertion that is not valid", reason)
	}

	for _, subject := range []string{"corp:" + alice().Subject, "corp:"} {
		p, err := e.store.GetPrincipalBySubject(t.Context(), subject)
		if !errors.Is(err, principal.ErrNotFound) {
			t.Fatalf("principal of %q = %+v, %v, want none", subject, p, err)
		}
	}
}
