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
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
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
	handler *apiserver.Handler
	idp     *oauthtest.Provider
	store   *sqlite.Store
	limiter *fakeLimiter
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

	var noFaults oauthtest.Faults

	idp := oauthtest.New(t, oauthtest.Config{
		Now: nil, ClientID: "dustpan", ClientSecret: "client-secret-for-tests", RedirectURI: signInCallback,
		Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOIDC, Faults: noFaults, TLS: true,
	})

	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: idp.Client(), Now: nil, Issuer: idp.Issuer(), ClientID: "dustpan",
		ClientSecret: "client-secret-for-tests", RedirectURL: signInCallback, Scopes: []string{"profile", "email"},
	})
	if err != nil {
		t.Fatalf("oauth client: %v", err)
	}

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), testNamespace); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Policies: policies, Inactivity: nil, Now: nil},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}
	principals := seedRoutePrincipals(t, st)

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         st,
		Journal:        st,
		Principals:     principals,
		Tokens:         seedRouteTokens(t, st),
		Access:         nil,
		Audit:          st,
		OnAuditFailure: nil,
		DeletedUnits:   stubSweeper{},
		AuditSinks:     nil,
		AuditChain:     nil,
		IPAllowlists:   nil,
		SessionPolicy:  policies,
		SignIn: &apiserver.SignIn{
			Sessions: sessions, Limiter: limiter, Origins: []string{signInBase, consoleOrigin},
			Providers: []apiserver.SignInProvider{
				{Client: client, ID: "corp", DisplayName: "Corp", AutoCreate: autoCreate},
			},
		},
		CheckpointEpochs: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return signInEnv{handler: handler, idp: idp, store: st, limiter: limiter}
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

	target := "/api/server/sign-in/corp"
	if returnPath != "" {
		target += "?return=" + url.QueryEscape(returnPath)
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
		if en.Action == session.ActionSignIn && en.Outcome != audit.OutcomeSuccess && len(en.Changes) == 1 {
			return en.Changes[0].New, en.Outcome
		}
	}

	return "", ""
}

// checkBrowserCookie fails the test when c is not a cookie of the console:
// Secure, HttpOnly, SameSite=Lax, Path=/, no Domain, and a lifetime.
func checkBrowserCookie(t *testing.T, name string, c *http.Cookie) {
	t.Helper()

	switch {
	case c == nil:
		t.Fatalf("no %s cookie", name)
	case !c.Secure, !c.HttpOnly, c.SameSite != http.SameSiteLaxMode:
		t.Errorf("%s cookie = %+v, want Secure, HttpOnly and SameSite=Lax", name, c)
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
	checkBrowserCookie(t, "sign-in", binding)

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

	checkBrowserCookie(t, "session", sess)

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
		{name: "no CSRF header", headers: nil, want: http.StatusForbidden},
		{name: "wrong CSRF header", headers: map[string]string{apiserver.CSRFHeader: "wrong"}, want: http.StatusForbidden},
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

// TestSignInRefusesTokenFromSession proves that a console session mints no
// token: each route that creates or rotates a token answers 403.
func TestSignInRefusesTokenFromSession(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.signIn(t)
	_, body := e.readSession(t, sess)

	principalTokens := "/api/server/principals/" + testOtherPrincipalID + "/tokens"

	for _, target := range []string{
		"/api/server/tokens",
		"/api/server/tokens/" + testServiceTokenID + "/rotate",
		"/api/server/service-tokens",
		"/api/server/service-tokens/" + testServiceTokenID + "/rotate",
		"/api/server/caller/tokens",
		"/api/server/caller/tokens/" + testServiceTokenID + "/rotate",
		principalTokens,
		principalTokens + "/" + testServiceTokenID + "/rotate",
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target,
			strings.NewReader(`{"name":"laptop","principalId":"`+testOtherPrincipalID+`"}`))
		req.AddCookie(sess)
		req.Header.Set(apiserver.CSRFHeader, body.CSRFToken)

		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cannot create or rotate a token") {
			t.Errorf("POST %s from a session = %d %s, want 403", target, rec.Code, rec.Body.String())
		}
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
			name: "no sign-in cookie", reason: "state does not match",
			mutate: func(_ *testing.T, _ signInEnv, _ *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				return q, nil
			},
		},
		{
			name: "other state", reason: "state does not match",
			mutate: func(_ *testing.T, _ signInEnv, b *http.Cookie, q url.Values) (url.Values, []*http.Cookie) {
				q.Set("state", q.Get("state")+"x")

				return q, []*http.Cookie{b}
			},
		},
		{
			name: "cookie of another sign-in", reason: "state does not match",
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
			name: "wrong code", reason: "ID token was refused",
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

	if reason, _ := e.refusedReason(t); reason != "sign-in request is unknown or expired" {
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

// downOIDC is a provider that cannot be reached.
type downOIDC struct{}

func (downOIDC) Begin(context.Context, oauth.Options) (string, oauth.Pending, error) {
	return "", oauth.Pending{State: "", Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false}, oauth.ErrProvider
}

func (downOIDC) Finish(context.Context, string, oauth.Pending) (oauth.Identity, error) {
	return oauth.Identity{AuthTime: time.Time{}, Subject: "", Name: "", PreferredUsername: "", Email: ""},
		oauth.ErrProvider
}

func TestSignInProviderDown(t *testing.T) {
	t.Parallel()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Policies: policies, Inactivity: nil, Now: nil},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}

	principals, err := principal.NewService(principal.Config{
		Store: st, Guard: nil, Namespaces: nil, Tokens: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("principals: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         st,
		Journal:        st,
		Principals:     principals,
		Tokens:         nil,
		Access:         nil,
		Audit:          st,
		OnAuditFailure: nil,
		DeletedUnits:   stubSweeper{},
		AuditSinks:     nil,
		AuditChain:     nil,
		IPAllowlists:   nil,
		SessionPolicy:  policies,
		SignIn: &apiserver.SignIn{
			Sessions: sessions,
			Limiter:  limiter,
			Origins:  nil,
			Providers: []apiserver.SignInProvider{
				{Client: downOIDC{}, ID: "corp", DisplayName: "Corp", AutoCreate: true},
			},
		},
		CheckpointEpochs: nil,
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
