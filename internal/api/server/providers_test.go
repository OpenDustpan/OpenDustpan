package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	betaSecret   = "client-secret-of-beta"
	betaCallback = signInBase + "/api/server/sign-in/beta/callback"
	hubSecret    = "client-secret-of-hub"
	hubCallback  = signInBase + "/api/server/sign-in/hub/callback"
	// installationKey is the key-encryption key of the tests: 32 bytes.
	installationKey = "0123456789abcdef0123456789abcdef"
)

// providerEnv is a sign-in env whose handler manages the stored providers,
// with the test provider beta for a stored provider next to the static
// provider corp.
type providerEnv struct {
	signInEnv

	beta  *oauthtest.Provider
	hub   *oauthtest.Provider
	stale *atomic.Bool
}

// testStored finds the enabled stored providers, as the server does, with
// the HTTP client of the test provider beta. While stale is set, it also
// finds a disabled provider, as a lookup that ran before the disable.
type testStored struct {
	admin  *session.ProviderAdmin
	client *http.Client
	now    func() time.Time
	stale  *atomic.Bool
}

func (s testStored) Provider(ctx context.Context, id string) (apiserver.SignInProvider, bool, error) {
	var none apiserver.SignInProvider

	p, err := s.admin.Get(ctx, id)
	if errors.Is(err, session.ErrProviderNotFound) || (err == nil && !p.Enabled && !s.stale.Load()) {
		return none, false, nil
	}

	if err != nil {
		return none, false, err
	}

	clientSecret, err := s.admin.ClientSecret(ctx, p)
	if err != nil {
		return none, false, err
	}

	var (
		client oauth.MethodClient
		kind   = session.MethodOIDC
	)

	if p.Mode == session.ModeOAuth {
		client, err = s.plainClient(&p, clientSecret)
		kind = session.MethodOAuth
	} else {
		client, err = s.openIDClient(&p, clientSecret)
	}

	if err != nil {
		return none, false, err
	}

	method, err := oauth.NewMethod(oauth.MethodConfig{
		Client: client, Enabled: func(context.Context) (bool, error) { return true, nil }, Now: s.now,
		ProviderID: id, Kind: kind,
	})
	if err != nil {
		return none, false, err
	}

	return apiserver.SignInProvider{
		Method:      method,
		ID:          id,
		DisplayName: p.DisplayName,
		AutoCreate:  p.AutoCreate,
		Stored:      true,
	}, true, nil
}

// plainClient makes the plain OAuth 2.0 client of p, as the server does
// for the oauth mode.
func (s testStored) plainClient(p *session.ProviderConfig, clientSecret string) (*oauth.PlainClient, error) {
	return oauth.NewPlain(oauth.PlainConfig{
		HTTPClient: s.client, Now: s.now, AuthorizationURL: p.OAuth.AuthorizationURL, TokenURL: p.OAuth.TokenURL,
		UserinfoURL: p.OAuth.UserinfoURL, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: signInBase + "/api/server/sign-in/" + p.ID + "/callback", Scopes: p.Scopes,
		Claims: oauth.Claims{
			Subject: p.OAuth.SubjectClaim, Name: p.OAuth.NameClaim, Username: p.OAuth.UsernameClaim,
			Email: p.OAuth.EmailClaim,
		},
	})
}

// openIDClient makes the OpenID client of p, as the server does for the
// oidc mode.
func (s testStored) openIDClient(p *session.ProviderConfig, clientSecret string) (*oauth.Lazy, error) {
	return oauth.NewLazy(oauth.Config{
		HTTPClient: s.client, Now: s.now, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: signInBase + "/api/server/sign-in/" + p.ID + "/callback", Scopes: p.Scopes,
	})
}

// sourceOf returns the principal source of a provider in mode, as the
// server sets it.
func newProviderEnv(t *testing.T) providerEnv {
	t.Helper()

	return newProviderEnvWith(t, nil)
}

// newProviderEnvWith builds the env of newProviderEnv with the password
// providers passwords next to the static provider corp.
func newProviderEnvWith(t *testing.T, passwords []apiserver.PasswordProvider) providerEnv {
	t.Helper()

	static := make([]string, 0, 1+len(passwords))
	static = append(static, "corp")

	for i := range passwords {
		static = append(static, passwords[i].ID)
	}

	var beta, hub *oauthtest.Provider

	var noFaults oauthtest.Faults

	stale := &atomic.Bool{}

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, authz: nil, namespaces: nil, hostile: nil,
		passwords: passwords,
		stored: func(st *sqlite.Store, clock *envClock) (*session.ProviderAdmin, apiserver.StoredProviders) {
			beta = oauthtest.New(t, oauthtest.Config{
				Now: clock.Now, ClientID: "dustpan-beta", ClientSecret: betaSecret, RedirectURI: betaCallback,
				Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOIDC, Faults: noFaults, TLS: true,
			})
			// A plain OAuth 2.0 provider, on HTTP on a loopback host, so the
			// HTTP client of beta reaches it too.
			hub = oauthtest.New(t, oauthtest.Config{
				Now: clock.Now, ClientID: "dustpan-hub", ClientSecret: hubSecret, RedirectURI: hubCallback,
				Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOAuth, Faults: noFaults, TLS: false,
			})

			wrapper, err := secret.NewBuiltinWrapper([]byte(installationKey))
			if err != nil {
				t.Fatalf("key wrapper: %v", err)
			}

			secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{
				Keys: st, Wrapper: wrapper, Now: clock.Now,
			})
			if err != nil {
				t.Fatalf("installation secrets: %v", err)
			}

			admin, err := session.NewProviderAdmin(session.ProviderAdminConfig{
				Store: st, Binder: st, Secrets: secrets, Now: clock.Now, Static: static,
			})
			if err != nil {
				t.Fatalf("provider admin: %v", err)
			}

			return admin, testStored{admin: admin, client: beta.Client(), now: clock.Now, stale: stale}
		},
	})

	return providerEnv{signInEnv: e, beta: beta, hub: hub, stale: stale}
}

// adminDo sends a request as an administrator. A read uses the access token
// of the administrator. A write uses a new console session, because an
// access token never gives the fresh authentication that a write needs.
func (e providerEnv) adminDo(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	if method != http.MethodGet {
		return e.sendWithSession(t, method, e.freshSignIn(t), target, body)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

// betaBody is the create body of the stored provider beta.
func (e providerEnv) betaBody() string {
	return `{"id":"beta","issuer":"` + e.beta.Issuer() + `","displayName":"Beta","clientID":"dustpan-beta",` +
		`"clientSecret":"` + betaSecret + `","scopes":["profile","email"]}`
}

// providerDoc is a provider on the wire, in tests.
type providerDoc struct {
	ID              string     `json:"id"`
	Source          string     `json:"source"`
	Issuer          string     `json:"issuer"`
	DisplayName     string     `json:"displayName"`
	Scopes          []string   `json:"scopes"`
	Version         int64      `json:"version"`
	Enabled         bool       `json:"enabled"`
	ClientSecretSet bool       `json:"clientSecretSet"`
	Mode            string     `json:"mode"`
	UserinfoURL     string     `json:"userinfoURL"`
	Claims          *claimsDoc `json:"claims"`
}

// claimsDoc is the claim mapping of a provider answer.
type claimsDoc struct {
	Subject  string `json:"subject"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

func decodeProvider(t *testing.T, rec *httptest.ResponseRecorder) providerDoc {
	t.Helper()

	var doc providerDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode provider: %v: %s", err, rec.Body.String())
	}

	return doc
}

// createBeta creates the stored provider beta through the API.
func (e providerEnv) createBeta(t *testing.T) providerDoc {
	t.Helper()

	rec := e.adminDo(t, http.MethodPost, "/api/server/sign-in-providers", e.betaBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create beta = %d %s, want 201", rec.Code, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), betaSecret) {
		t.Fatal("the answer holds the client secret")
	}

	return decodeProvider(t, rec)
}

func TestSignInProvidersCreateAndList(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)

	created := e.createBeta(t)
	if created.Source != "api" || created.Version != 1 || !created.ClientSecretSet || !created.Enabled ||
		created.Issuer != e.beta.Issuer() {
		t.Fatalf("created = %+v, want an enabled API provider of version 1 with a secret", created)
	}

	checkProviderList(t, e)
	checkNoSecretInAudit(t, e)
}

// checkProviderList checks that the list holds corp (static) and beta
// (api), and no client secret.
func checkProviderList(t *testing.T, e providerEnv) {
	t.Helper()

	rec := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers", "")

	var list struct {
		Providers []providerDoc `json:"providers"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, %v", rec.Code, rec.Body.String(), err)
	}

	if len(list.Providers) != 2 || list.Providers[0].ID != "corp" || list.Providers[0].Source != "static" ||
		list.Providers[1].ID != "beta" || list.Providers[1].Source != "api" {
		t.Fatalf("list = %+v, want corp (static) and beta (api)", list.Providers)
	}

	if strings.Contains(rec.Body.String(), betaSecret) {
		t.Fatal("the list holds the client secret")
	}
}

// checkNoSecretInAudit checks that no installation audit entry holds the
// client secret of beta.
func checkNoSecretInAudit(t *testing.T, e providerEnv) {
	t.Helper()

	entries := e.serverEntries(t)
	for i := range entries {
		if strings.Contains(audit.EncodeChanges(entries[i].Changes), betaSecret) {
			t.Fatalf("the audit entry %s holds the client secret", entries[i].Action)
		}
	}
}

func TestSignInProvidersRefusals(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	e.createBeta(t)

	for _, tc := range []struct {
		method, target, body, want string
		code                       int
	}{
		{
			http.MethodPost, "/api/server/sign-in-providers", strings.Replace(e.betaBody(), `"beta"`, `"corp"`, 1),
			"so change it there", http.StatusConflict,
		},
		{
			http.MethodPatch, "/api/server/sign-in-providers/corp", `{"version":0,"enabled":false}`,
			"so change it there", http.StatusConflict,
		},
		{http.MethodDelete, "/api/server/sign-in-providers/corp", "", "so change it there", http.StatusConflict},
		{http.MethodPost, "/api/server/sign-in-providers", e.betaBody(), "already exists", http.StatusConflict},
		{
			http.MethodPost, "/api/server/sign-in-providers", `{"id":"gamma"}`, "invalid sign-in provider body",
			http.StatusBadRequest,
		},
		{
			http.MethodPatch, "/api/server/sign-in-providers/beta", `{"version":1}`, "invalid sign-in provider change",
			http.StatusBadRequest,
		},
		{
			http.MethodPatch, "/api/server/sign-in-providers/beta", `{"version":7,"enabled":false}`,
			"changed since it was read", http.StatusConflict,
		},
		{
			http.MethodPatch, "/api/server/sign-in-providers/beta", `{"version":1,"issuer":"https://other.example"}`,
			"set a new provider ID only if the identity provider changed", http.StatusConflict,
		},
		{http.MethodGet, "/api/server/sign-in-providers/none", "", "not found", http.StatusNotFound},
		{
			http.MethodPost, "/api/server/sign-in-providers", strings.Replace(e.betaBody(), `"beta"`, `"dev"`, 1),
			"reserved for the dev sign-in", http.StatusBadRequest,
		},
	} {
		rec := e.adminDo(t, tc.method, tc.target, tc.body)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s %s = %d %s, want %d with %q", tc.method, tc.target, rec.Code, rec.Body.String(), tc.code,
				tc.want)
		}
	}
}

// signInWith runs a whole sign-in through the provider id at idp and
// returns the subject of the new session.
func (e providerEnv) signInWith(t *testing.T, idp *oauthtest.Provider, id string) string {
	t.Helper()

	subject, _ := e.signInSession(t, idp, id)

	return subject
}

// signInSession signs in through the provider id at idp and returns the
// subject and the session cookie.
func (e providerEnv) signInSession(t *testing.T, idp *oauthtest.Provider, id string) (string, *http.Cookie) {
	t.Helper()

	binding, back := e.authorizeWith(t, idp, id)

	cb := e.get(t, "/api/server/sign-in/"+id+"/callback?"+back.Encode(), binding)

	sess := cookieOf(cb, apiserver.SessionCookie)
	if cb.Code != http.StatusSeeOther || sess == nil {
		t.Fatalf("callback of %s = %d %s, want 303 with a session", id, cb.Code, cb.Body.String())
	}

	_, body := e.readSession(t, sess)

	return body.Subject, sess
}

// authorizeWith starts a sign-in through the provider id and passes the
// authorization at idp. It returns the binding cookie and the query of
// the callback.
func (e providerEnv) authorizeWith(t *testing.T, idp *oauthtest.Provider, id string) (*http.Cookie, url.Values) {
	t.Helper()

	rec := e.get(t, "/api/server/sign-in/"+id)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), idp.Issuer()) {
		t.Fatalf("start of %s = %d to %q, want 302 to %s", id, rec.Code, rec.Header().Get("Location"), idp.Issuer())
	}

	binding := cookieOf(rec, apiserver.SignInCookie)

	browser := *idp.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rec.Header().Get("Location"), http.NoBody)
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
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}

	return binding, back.Query()
}

// TestSignInTwoProvidersSideBySide proves that a static provider and a
// stored provider each send the browser to their own issuer, and that the
// same sub at both makes two subjects and two principals.
func TestSignInTwoProvidersSideBySide(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	e.createBeta(t)

	corp := e.signInWith(t, e.idp, "corp")
	beta := e.signInWith(t, e.beta, "beta")

	if corp != "corp:248289761001" || beta != "beta:248289761001" {
		t.Fatalf("subjects = %q and %q, want one for each provider", corp, beta)
	}

	a, errA := e.store.GetPrincipalBySubject(t.Context(), corp)
	b, errB := e.store.GetPrincipalBySubject(t.Context(), beta)

	if errA != nil || errB != nil || a.ID == b.ID {
		t.Fatalf("principals %q and %q (%v, %v), want two", a.ID, b.ID, errA, errB)
	}
}

// TestSignInStoredProviderChangesApplyAtOnce proves that a disabled stored
// provider refuses the start of a sign-in and ends the sessions of its
// people, and not the sessions of another provider, that enabling it again applies
// with no restart, and that a provider with principals is not deleted.
func TestSignInStoredProviderChangesApplyAtOnce(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	created := e.createBeta(t)

	_, corpSession := e.signInSession(t, e.idp, "corp")

	subject, betaSession := e.signInSession(t, e.beta, "beta")
	if subject != "beta:248289761001" {
		t.Fatalf("subject = %q", subject)
	}

	version := strconv.FormatInt(created.Version, 10)

	rec := e.adminDo(
		t,
		http.MethodPatch,
		"/api/server/sign-in-providers/beta",
		`{"version":`+version+`,"enabled":false}`,
	)
	if rec.Code != http.StatusOK || decodeProvider(t, rec).Version != 2 {
		t.Fatalf("disable = %d %s, want 200 with version 2", rec.Code, rec.Body.String())
	}

	if start := e.get(t, "/api/server/sign-in/beta"); start.Code != http.StatusNotFound {
		t.Fatalf("start of a disabled provider = %d, want 404", start.Code)
	}

	if code, _ := e.readSession(t, betaSession); code != http.StatusUnauthorized {
		t.Fatalf("session of a disabled provider = %d, want 401", code)
	}

	if code, _ := e.readSession(t, corpSession); code != http.StatusOK {
		t.Fatalf("session of another provider after the disable = %d, want 200", code)
	}

	rec = e.adminDo(t, http.MethodDelete, "/api/server/sign-in-providers/beta", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `disable it with PATCH`) {
		t.Fatalf("delete of a provider with principals = %d %s, want 409 that says to disable it", rec.Code,
			rec.Body.String())
	}

	rec = e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/beta", `{"version":2,"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", rec.Code, rec.Body.String())
	}

	if subject := e.signInWith(t, e.beta, "beta"); subject != "beta:248289761001" {
		t.Fatalf("subject after the provider is enabled again = %q", subject)
	}
}

// TestSignInProviderDisableRunsInMaintenanceMode proves that the disable of a
// stored provider answers 200 in maintenance mode, through the route, the
// service and the store, and ends the sessions of the provider. A change that
// sets another field with the disable, and the enable, answer 503: only the
// change that disables and nothing else narrows access.
func TestSignInProviderDisableRunsInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	created := e.createBeta(t)

	_, betaSession := e.signInSession(t, e.beta, "beta")

	// A sign-in is refused in maintenance mode, so the console session of the
	// change exists before it.
	admin := e.freshSignIn(t)
	version := strconv.FormatInt(created.Version, 10)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	target := "/api/server/sign-in-providers/beta"

	rec := e.sendWithSession(
		t,
		http.MethodPatch,
		admin,
		target,
		`{"version":`+version+`,"enabled":false,"displayName":"Renamed"}`,
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disable with a rename in maintenance mode = %d %s, want 503", rec.Code, rec.Body.String())
	}

	rec = e.sendWithSession(t, http.MethodPatch, admin, target, `{"version":`+version+`,"enabled":false}`)
	if rec.Code != http.StatusOK || decodeProvider(t, rec).Version != 2 || decodeProvider(t, rec).Enabled {
		t.Fatalf("disable in maintenance mode = %d %s, want 200 with version 2, disabled", rec.Code, rec.Body.String())
	}

	rec = e.sendWithSession(t, http.MethodPatch, admin, target, `{"version":2,"enabled":true}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("enable in maintenance mode = %d %s, want 503", rec.Code, rec.Body.String())
	}

	if err := e.store.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	if code, _ := e.readSession(t, betaSession); code != http.StatusUnauthorized {
		t.Fatalf("session of a provider that was disabled in maintenance mode = %d, want 401", code)
	}
}

// TestSignInProviderDeleteWithNoPrincipal proves that a provider with no
// principal is deleted and that its start then answers 404.
func TestSignInProviderDeleteWithNoPrincipal(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	e.createBeta(t)

	rec := e.adminDo(t, http.MethodDelete, "/api/server/sign-in-providers/beta", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s, want 204", rec.Code, rec.Body.String())
	}

	if rec := e.get(t, "/api/server/sign-in/beta"); rec.Code != http.StatusNotFound {
		t.Fatalf("start of a deleted provider = %d, want 404", rec.Code)
	}
}

// TestSignInProviderDeleteRunsInMaintenanceMode proves that the delete of a
// stored provider answers in maintenance mode, through the route, the service
// and the store: 204 for a provider that no principal uses, and 409 with the
// provider kept for a provider that a principal uses.
func TestSignInProviderDeleteRunsInMaintenanceMode(t *testing.T) {
	t.Parallel()

	cases := []providerDeleteCase{
		{name: "a provider with no principal", hasPrincipal: false, want: http.StatusNoContent, gone: true},
		{name: "a provider with a principal", hasPrincipal: true, want: http.StatusConflict, gone: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkProviderDeleteInMaintenance(t, tc)
		})
	}
}

// providerDeleteCase is one delete of a stored provider in maintenance mode.
type providerDeleteCase struct {
	name         string
	hasPrincipal bool
	want         int
	gone         bool
}

// checkProviderDeleteInMaintenance creates the stored provider beta, enters
// maintenance mode, deletes beta and checks the status and whether the start
// of beta still exists.
func checkProviderDeleteInMaintenance(t *testing.T, tc providerDeleteCase) {
	t.Helper()

	e := newProviderEnv(t)
	e.createBeta(t)

	if tc.hasPrincipal {
		e.signInSession(t, e.beta, "beta")
	}

	// A sign-in is refused in maintenance mode, so the console
	// session of the delete exists before it.
	admin := e.freshSignIn(t)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	rec := e.sendWithSession(t, http.MethodDelete, admin, "/api/server/sign-in-providers/beta", "")
	if rec.Code != tc.want {
		t.Fatalf("delete in maintenance mode = %d %s, want %d", rec.Code, rec.Body.String(), tc.want)
	}

	if err := e.store.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode: %v", err)
	}

	if got := e.get(t, "/api/server/sign-in/beta"); (got.Code == http.StatusNotFound) != tc.gone {
		t.Fatalf("start of the provider after the delete = %d, want not found = %v", got.Code, tc.gone)
	}
}

// TestSignInProviderChangeNeedsAFreshSession proves that a console session
// whose sign-in is older than the token freshness window cannot create a
// provider.
func TestSignInProviderChangeNeedsAFreshSession(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	stale := e.staleSession(t)

	rec := e.postWithSession(t, stale, "/api/server/sign-in-providers", e.betaBody())
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "reauthenticate") {
		t.Fatalf("create from a stale session = %d %s, want 401 with reauthenticate", rec.Code, rec.Body.String())
	}

	if rec := e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/beta", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("provider after a refused create = %d, want 404", rec.Code)
	}

	e.createBeta(t)

	refused := []*httptest.ResponseRecorder{
		e.sendWithSession(t, http.MethodPatch, stale, "/api/server/sign-in-providers/beta",
			`{"enabled":false,"version":1}`),
		e.sendWithSession(t, http.MethodDelete, stale, "/api/server/sign-in-providers/beta", ""),
	}

	for _, rec := range refused {
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "reauthenticate") {
			t.Fatalf("change from a stale session = %d %s, want 401 with reauthenticate", rec.Code, rec.Body.String())
		}
	}

	got := decodeProvider(t, e.adminDo(t, http.MethodGet, "/api/server/sign-in-providers/beta", ""))
	if got.Version != 1 || !got.Enabled {
		t.Fatalf("provider after a refused change and delete = version %d, enabled %t; want version 1, enabled",
			got.Version, got.Enabled)
	}
}

// TestSignInProviderReadsHoldNoSecret proves that neither the read of one
// provider nor the list holds the client secret, only clientSecretSet.
func TestSignInProviderReadsHoldNoSecret(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	e.createBeta(t)

	for _, target := range []string{"/api/server/sign-in-providers/beta", "/api/server/sign-in-providers"} {
		rec := e.adminDo(t, http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s, want 200", target, rec.Code, rec.Body.String())
		}

		body := rec.Body.String()
		if strings.Contains(body, betaSecret) || strings.Contains(body, `"clientSecret"`) {
			t.Fatalf("GET %s holds the client secret: %s", target, body)
		}

		if !strings.Contains(body, `"clientSecretSet":true`) {
			t.Fatalf("GET %s = %s, want clientSecretSet true", target, body)
		}
	}
}

// TestSignInProviderRefusalNamesNoInvalidID proves that the failure entry
// of a change of an ID that is not valid names no part of that ID.
func TestSignInProviderRefusalNamesNoInvalidID(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	long := strings.Repeat("x", 300)

	rec := e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/"+long, `{"enabled":false,"version":1}`)
	if rec.Code == http.StatusOK {
		t.Fatal("change of an invalid ID = 200, want a refusal")
	}

	var objects []string

	for _, entry := range e.serverEntries(t) {
		if entry.Outcome == audit.OutcomeFailure && strings.HasPrefix(entry.Object, "sign-in-provider:") {
			objects = append(objects, entry.Object)
		}
	}

	if len(objects) != 1 || objects[0] != "sign-in-provider:?" {
		t.Fatalf("failure entries of the change = %q, want one of sign-in-provider:?", objects)
	}
}

// TestSignInDisableBeforeTheCallback proves that a sign-in whose provider
// is disabled between the start and the callback gets no session: when the
// callback finds the provider disabled, and when its lookup ran before the
// disable, so that the write of the session refuses it.
func TestSignInDisableBeforeTheCallback(t *testing.T) {
	t.Parallel()

	for _, stale := range []bool{false, true} {
		e := newProviderEnv(t)
		e.createBeta(t)
		e.stale.Store(stale)

		binding, back := e.authorizeWith(t, e.beta, "beta")

		rec := e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/beta", `{"version":1,"enabled":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("disable = %d %s, want 200", rec.Code, rec.Body.String())
		}

		cb := e.get(t, "/api/server/sign-in/beta/callback?"+back.Encode(), binding)
		if cb.Code != http.StatusNotFound || cookieOf(cb, apiserver.SessionCookie) != nil {
			t.Fatalf("callback after a disable (stale lookup %t) = %d %s, want 404 with no session",
				stale, cb.Code, cb.Body.String())
		}
	}
}

// hubBody is the body of the plain OAuth 2.0 provider hub, with only its
// subject claim mapped.
func (e providerEnv) hubBody() string {
	return `{"id":"hub","mode":"oauth","displayName":"Hub","clientID":"dustpan-hub","clientSecret":"` + hubSecret +
		`","authorizationURL":"` + e.hub.AuthorizationEndpoint() + `","tokenURL":"` + e.hub.TokenEndpoint() +
		`","userinfoURL":"` + e.hub.UserinfoEndpoint() + `","claims":{"subject":"sub"},"autoCreate":true}`
}

// TestSignInPlainOAuthProvider proves a stored provider in the oauth mode:
// the API creates it with the default claims and answers its settings,
// a person signs in through it with a subject of its own, and a change of
// its mode or of its user-info URL is refused.
func TestSignInPlainOAuthProvider(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)

	rec := e.adminDo(t, http.MethodPost, "/api/server/sign-in-providers", e.hubBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", rec.Code, rec.Body.String())
	}

	got := decodeProvider(t, rec)
	if got.Mode != "oauth" || got.Issuer != "" || got.UserinfoURL != e.hub.UserinfoEndpoint() || got.Claims == nil ||
		got.Claims.Subject != "sub" || got.Claims.Name != "name" || got.Claims.Email != "email" {
		t.Fatalf("provider = %+v, want the oauth mode with the default claims", got)
	}

	if strings.Contains(rec.Body.String(), hubSecret) {
		t.Fatal("the answer holds the client secret")
	}

	if subject := e.signInWith(t, e.hub, "hub"); subject != "hub:248289761001" {
		t.Fatalf("subject = %q, want hub:248289761001", subject)
	}

	e.checkSource(t, "hub:248289761001", principal.SourceOAuth)
	e.checkHubChanges(t)
}

// checkSource proves that the principal of subject has the source want.
func (e providerEnv) checkSource(t *testing.T, subject string, want principal.Source) {
	t.Helper()

	made, err := e.store.GetPrincipalBySubject(t.Context(), subject)
	if err != nil || made.Source != want {
		t.Fatalf("principal of the first sign-in = %+v, %v; want the source %s", made, err, want)
	}
}

// checkHubChanges proves that a change of the mode, the user-info URL or
// the subject claim of hub is refused, and that a change of its name claim
// works.
func (e providerEnv) checkHubChanges(t *testing.T) {
	t.Helper()

	for body, want := range map[string]int{
		`{"version":1,"mode":"oidc"}`:                                       http.StatusBadRequest,
		`{"version":1,"userinfoURL":"` + e.hub.UserinfoEndpoint() + `/me"}`: http.StatusConflict,
		`{"version":1,"claims":{"subject":"email"}}`:                        http.StatusConflict,
	} {
		refused := e.adminDo(t, http.MethodPatch, "/api/server/sign-in-providers/hub", body)
		if refused.Code != want {
			t.Errorf("change %s = %d %s, want %d", body, refused.Code, refused.Body.String(), want)
		}
	}

	rec := e.adminDo(
		t,
		http.MethodPatch,
		"/api/server/sign-in-providers/hub",
		`{"version":1,"claims":{"name":"login"}}`,
	)
	if rec.Code != http.StatusOK || decodeProvider(t, rec).Claims.Name != "login" {
		t.Fatalf("change of the name claim = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestSignInPlainOAuthProviderRefusals proves that a create in the oauth
// mode needs its endpoints and no issuer, and that a create in the oidc
// mode needs an issuer.
func TestSignInPlainOAuthProviderRefusals(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)

	for _, body := range []string{
		`{"id":"hub","mode":"oauth","clientID":"c","clientSecret":"s","tokenURL":"https://h.example/t",` +
			`"authorizationURL":"https://h.example/a"}`,
		`{"id":"hub","mode":"oauth","clientID":"c","clientSecret":"s","issuer":"https://h.example",` +
			`"authorizationURL":"https://h.example/a","tokenURL":"https://h.example/t","userinfoURL":"https://h.example/u"}`,
		`{"id":"hub","mode":"oauth","clientID":"c","clientSecret":"s","authorizationURL":"http://h.example/a",` +
			`"tokenURL":"https://h.example/t","userinfoURL":"https://h.example/u"}`,
		`{"id":"hub","mode":"saml","clientID":"c","clientSecret":"s","issuer":"https://h.example"}`,
		`{"id":"hub","clientID":"c","clientSecret":"s"}`,
	} {
		rec := e.adminDo(t, http.MethodPost, "/api/server/sign-in-providers", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create %s = %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
}

// TestSignInPlainOAuthProviderNoAutoCreate proves that a provider in the
// oauth mode makes no principal at a first sign-in unless the create asks
// for it, and that a provider in the oidc mode still does.
func TestSignInPlainOAuthProviderNoAutoCreate(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	body := strings.Replace(e.hubBody(), `,"autoCreate":true`, "", 1)

	rec := e.adminDo(t, http.MethodPost, "/api/server/sign-in-providers", body)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), `"autoCreate":true`) {
		t.Fatalf("create with no autoCreate = %d %s, want 201 with autoCreate false", rec.Code, rec.Body.String())
	}

	binding, back := e.authorizeWith(t, e.hub, "hub")

	cb := e.get(t, "/api/server/sign-in/hub/callback?"+back.Encode(), binding)
	if cb.Code == http.StatusSeeOther || cookieOf(cb, apiserver.SessionCookie) != nil {
		t.Fatalf("first sign-in with no principal = %d %s, want a refusal", cb.Code, cb.Body.String())
	}

	if _, err := e.store.GetPrincipalBySubject(t.Context(), "hub:248289761001"); err == nil {
		t.Fatal("a refused first sign-in made a principal")
	}

	rec = e.adminDo(t, http.MethodPost, "/api/server/sign-in-providers", e.betaBody())
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"autoCreate":true`) {
		t.Fatalf("create of an OpenID provider = %d %s, want autoCreate true", rec.Code, rec.Body.String())
	}
}
