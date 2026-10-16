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
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	betaSecret   = "client-secret-of-beta"
	betaCallback = signInBase + "/api/server/sign-in/beta/callback"
	// installationKey is the key-encryption key of the tests: 32 bytes.
	installationKey = "0123456789abcdef0123456789abcdef"
)

// providerEnv is a sign-in env whose handler manages the stored providers,
// with the test provider beta for a stored provider next to the static
// provider corp.
type providerEnv struct {
	signInEnv

	beta *oauthtest.Provider
}

// testStored finds the enabled stored providers, as the server does, with
// the HTTP client of the test provider beta.
type testStored struct {
	admin  *session.ProviderAdmin
	client *http.Client
	now    func() time.Time
}

func (s testStored) Provider(ctx context.Context, id string) (apiserver.SignInProvider, bool, error) {
	var none apiserver.SignInProvider

	p, err := s.admin.Get(ctx, id)
	if errors.Is(err, session.ErrProviderNotFound) || (err == nil && !p.Enabled) {
		return none, false, nil
	}

	if err != nil {
		return none, false, err
	}

	clientSecret, err := s.admin.ClientSecret(ctx, p)
	if err != nil {
		return none, false, err
	}

	client, err := oauth.NewLazy(oauth.Config{
		HTTPClient: s.client, Now: s.now, Issuer: p.Issuer, ClientID: p.ClientID, ClientSecret: clientSecret,
		RedirectURL: signInBase + "/api/server/sign-in/" + id + "/callback", Scopes: p.Scopes,
	})
	if err != nil {
		return none, false, err
	}

	return apiserver.SignInProvider{
		Client:      client,
		ID:          id,
		DisplayName: p.DisplayName,
		AutoCreate:  p.AutoCreate,
	}, true, nil
}

func newProviderEnv(t *testing.T) providerEnv {
	t.Helper()

	var beta *oauthtest.Provider

	var noFaults oauthtest.Faults

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false,
		stored: func(st *sqlite.Store, clock *envClock) (*session.ProviderAdmin, apiserver.StoredProviders) {
			beta = oauthtest.New(t, oauthtest.Config{
				Now: clock.Now, ClientID: "dustpan-beta", ClientSecret: betaSecret, RedirectURI: betaCallback,
				Algorithm: oauthtest.RS256, User: alice(), Mode: oauthtest.ModeOIDC, Faults: noFaults, TLS: true,
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
				Store: st, Binder: st, Secrets: secrets, Now: clock.Now, Static: []string{"corp"},
			})
			if err != nil {
				t.Fatalf("provider admin: %v", err)
			}

			return admin, testStored{admin: admin, client: beta.Client(), now: clock.Now}
		},
	})

	return providerEnv{signInEnv: e, beta: beta}
}

// adminDo sends a request with the access token of an administrator.
func (e providerEnv) adminDo(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

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
	ID              string   `json:"id"`
	Source          string   `json:"source"`
	Issuer          string   `json:"issuer"`
	DisplayName     string   `json:"displayName"`
	Scopes          []string `json:"scopes"`
	Version         int64    `json:"version"`
	Enabled         bool     `json:"enabled"`
	ClientSecretSet bool     `json:"clientSecretSet"`
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

	cb := e.get(t, "/api/server/sign-in/"+id+"/callback?"+back.Query().Encode(), binding)

	sess := cookieOf(cb, apiserver.SessionCookie)
	if cb.Code != http.StatusSeeOther || sess == nil {
		t.Fatalf("callback of %s = %d %s, want 303 with a session", id, cb.Code, cb.Body.String())
	}

	_, body := e.readSession(t, sess)

	return body.Subject
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
// provider refuses the start of a sign-in, that enabling it again applies
// with no restart, and that a provider with principals is not deleted.
func TestSignInStoredProviderChangesApplyAtOnce(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	created := e.createBeta(t)

	if subject := e.signInWith(t, e.beta, "beta"); subject != "beta:248289761001" {
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
}
