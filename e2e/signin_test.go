//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
)

// e2eSession decodes the console session wire form.
type e2eSession struct {
	Subject   string `json:"subject"`
	Provider  string `json:"provider"`
	CSRFToken string `json:"csrfToken"`
}

// TestSignIn drives the console sign-in through the browser flow, against
// a test OpenID provider, on each store. No CLI signs in through a
// browser, so the test plays the browser with a cookie jar. It signs in,
// reads the session, proves that the APIs of the console take the session
// cookie and the TFE-compatible API does not, refuses a callback with
// another state, lets a session go idle and reach its maximum length on a
// shared test clock, and signs out.
func TestSignIn(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	tofuVersion := e2e.OpenTofu().Single()
	if tofuVersion == "" {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runSignInOnStore(t, cli, tofu, db)
		})
	}
}

// TestSignInSecretInVariable proves that the server takes the client secret
// of the OpenID Connect provider from DUSTPAN_OIDC_CLIENT_SECRET. The test
// provider refuses another secret at its token endpoint, so a server that
// sends the fingerprint of the snapshot in place of the secret fails the
// sign-in.
func TestSignInSecretInVariable(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	if len(databases) == 0 {
		t.Fatal("no store")
	}

	store := databases[0]
	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignInSecretInVariable(t.Context(), t, e2e.NoCLI(), store)

	checkReady(t.Context(), t, env)

	b := newBrowser(t, env)
	b.signIn(t)
}

// browser is the browser of one sign-in test: a client with a cookie jar
// for the server, and a client for the test provider.
type browser struct {
	env    *e2e.Env
	server *http.Client
	idp    *http.Client
}

func newBrowser(t *testing.T, env *e2e.Env) browser {
	t.Helper()

	idp := *env.IdentityProvider(t).Client()
	idp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	return browser{env: env, server: env.BrowserClient(t), idp: &idp}
}

// answer is the status, the Location header and the body of an answer.
type answer struct {
	status   int
	location string
	body     string
}

// do sends one request and returns its answer.
func (b browser) do(t *testing.T, client *http.Client, method, target string, header http.Header) answer {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, target, err)
	}

	// A browser marks a request to the server that sent the page; a caller
	// can replace the value.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	maps.Copy(req.Header, header)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}

	body, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read %s %s: %v", method, target, err)
	}

	return answer{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(body)}
}

// startAndAuthorize starts a sign-in and sends the browser to the test
// provider. It returns the callback URL that the provider answers.
func (b browser) startAndAuthorize(t *testing.T) *url.URL {
	t.Helper()

	// A fresh session of the administrator, in the same env, signs the provider
	// in as another person: sign the test person in again.
	b.env.IdentityProvider(t).SetUser(oauthtest.User{
		Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
	})

	start := b.do(t, b.server, http.MethodGet,
		b.env.URL+"/api/server/sign-in/"+e2e.SignInProvider+"?return=/console", nil)
	if start.status != http.StatusFound {
		t.Fatalf("sign-in start = %d: %s", start.status, start.body)
	}

	auth := b.do(t, b.idp, http.MethodGet, start.location, nil)
	if auth.status != http.StatusFound {
		t.Fatalf("authorization = %d: %s", auth.status, auth.body)
	}

	u, err := url.Parse(auth.location)
	if err != nil || !strings.HasPrefix(auth.location, b.env.URL+"/api/server/sign-in/") {
		t.Fatalf("callback URL = %q, %v", auth.location, err)
	}

	return u
}

// signIn runs a whole sign-in and returns the session.
func (b browser) signIn(t *testing.T) e2eSession {
	t.Helper()

	callback := b.startAndAuthorize(t)

	back := b.do(t, b.server, http.MethodGet, callback.String(), nil)
	if back.status != http.StatusSeeOther || back.location != "/console" {
		t.Fatalf("callback = %d to %q, want 303 to /console: %s", back.status, back.location, back.body)
	}

	sess, status := b.session(t)
	if status != http.StatusOK || sess.Subject != e2e.SignInProvider+":248289761001" || sess.CSRFToken == "" {
		t.Fatalf("session = %d %+v", status, sess)
	}

	return sess
}

// session reads GET /api/server/session with the cookie jar.
func (b browser) session(t *testing.T) (e2eSession, int) {
	t.Helper()

	a := b.do(t, b.server, http.MethodGet, b.env.URL+"/api/server/session", nil)

	var sess e2eSession
	if a.status == http.StatusOK {
		if err := json.Unmarshal([]byte(a.body), &sess); err != nil {
			t.Fatalf("decode session: %v", err)
		}
	}

	return sess, a.status
}

// runSignInOnStore runs the sign-in steps on one store.
func runSignInOnStore(t *testing.T, cli, tofu e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	b := newBrowser(t, env)
	b.signIn(t)

	checkConsoleAPIsTakeTheCookie(t, b)
	checkCallbackOfAnotherState(t, b)
	checkTokensFromSession(t, b, tofu)

	// The token freshness window, the idle limit and the maximum length
	// need server time to pass, so they run on the clock that the
	// in-process server shares. The binary server keeps the real time.
	if env.SharesClock() {
		checkTokenFreshness(t, b)
		checkSessionLimits(t, b)
	} else {
		t.Log("binary server: the token freshness window, the idle limit and the maximum length run with " +
			"the in-process server only")
	}

	checkSignOut(t, b)
	checkStoredProvider(t, env)
	checkPlainProvider(t, env)
	checkStoredProviderDelete(t, env)
	checkProviderAuditHoldsNoSecret(t, env)
}

// doBody sends one request with body and the headers, and returns its
// answer.
func (b browser) doBody(t *testing.T, method, target, body string, header http.Header) answer {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request %s %s: %v", method, target, err)
	}

	req.Header.Set("Sec-Fetch-Site", "same-origin")
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.server.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}

	raw, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read %s %s: %v", method, target, err)
	}

	return answer{status: resp.StatusCode, location: resp.Header.Get("Location"), body: string(raw)}
}

// tokenChange sends a token create or rotation with the session cookie
// and its CSRF value.
func (b browser) tokenChange(t *testing.T, path, body string) answer {
	t.Helper()

	sess, status := b.session(t)
	if status != http.StatusOK {
		t.Fatalf("session before a token change = %d", status)
	}

	return b.doBody(t, http.MethodPost, b.env.URL+path, body, http.Header{"X-Dustpan-Csrf": {sess.CSRFToken}})
}

// ownToken creates an own token from the console session and returns it.
func (b browser) ownToken(t *testing.T, body string) e2eToken {
	t.Helper()

	a := b.tokenChange(t, "/api/server/caller/tokens", body)
	if a.status != http.StatusCreated {
		t.Fatalf("own token from the session = %d, want 201: %s", a.status, a.body)
	}

	var tok e2eToken
	if err := json.Unmarshal([]byte(a.body), &tok); err != nil || tok.Secret == "" || tok.ID == "" {
		t.Fatalf("decode the own token: %v", err)
	}

	return tok
}

// rotateOwnToken rotates the own token id from the console session and
// returns the new token.
func (b browser) rotateOwnToken(t *testing.T, id string) e2eToken {
	t.Helper()

	a := b.tokenChange(t, "/api/server/caller/tokens/"+id+"/rotate", `{}`)
	if a.status != http.StatusCreated {
		t.Fatalf("rotation from the session = %d, want 201: %s", a.status, a.body)
	}

	var rotation struct {
		Token e2eToken `json:"token"`
	}

	if err := json.Unmarshal([]byte(a.body), &rotation); err != nil || rotation.Token.Secret == "" {
		t.Fatalf("decode the rotation: %v", err)
	}

	return rotation.Token
}

// checkTokensFromSession creates an own token from the console session,
// uses it with the Pulumi CLI and the OpenTofu CLI after an administrator
// grants the person the role maintainer on the namespace, and rotates it.
func checkTokensFromSession(t *testing.T, b browser, tofu e2e.Install) {
	t.Helper()

	ctx := t.Context()
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	tok := b.ownToken(t, `{"name":"laptop","expiresAt":"`+expires+`"}`)

	grant := `{"subject":"principal:` + tok.PrincipalID + `","role":"maintainer"}`

	status, body := b.env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/namespaces/" + e2e.Namespace + "/grants", Body: grant,
	})
	if status != http.StatusCreated {
		t.Fatalf("grant maintainer = %d: %s", status, body)
	}

	checkPulumiWhoami(ctx, t, b.env, tok.Secret)

	init, err := b.env.RunTofuWithToken(ctx, t, tofu, tok.Secret, "init", "-input=false")
	if err != nil || init.ExitCode != 0 {
		t.Fatalf("tofu init with the token of the session = %+v, %v", init, err)
	}

	rotated := b.rotateOwnToken(t, tok.ID)
	if rotated.Secret == tok.Secret {
		t.Fatal("the rotation answered the old secret")
	}

	checkPulumiWhoami(ctx, t, b.env, rotated.Secret)
}

// checkPulumiWhoami runs pulumi whoami with token, and fails unless it
// names the person of the test provider.
func checkPulumiWhoami(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	res, err := env.Run(ctx, []string{"PULUMI_ACCESS_TOKEN=" + token}, "whoami")
	if err != nil || res.ExitCode != 0 || !strings.Contains(res.Stdout, e2e.SignInProvider+":248289761001") {
		t.Fatalf("pulumi whoami with the token of the session = %+v, %v", res, err)
	}
}

// checkTokenFreshness proves the token freshness window: a session whose
// sign-in is older than 15 minutes cannot create a token, and gets the
// path of a new sign-in; that sign-in asks the provider for a new
// authentication, and its session creates a token.
func checkTokenFreshness(t *testing.T, b browser) {
	t.Helper()

	clock := b.env.Clock(t)
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"name":"fresh","expiresAt":"` + expires + `"}`

	b.signIn(t)
	clock.Advance(10 * time.Minute)
	b.session(t)
	clock.Advance(6 * time.Minute)

	a := b.tokenChange(t, "/api/server/caller/tokens", body)

	var refusal struct {
		Reauthenticate string `json:"reauthenticate"`
	}

	if err := json.Unmarshal([]byte(a.body), &refusal); err != nil || a.status != http.StatusUnauthorized ||
		refusal.Reauthenticate != "/api/server/sign-in/"+e2e.SignInProvider+"?fresh=1" {
		t.Fatalf("token from a stale session = %d %s, want 401 with the path of a new sign-in", a.status, a.body)
	}

	signIns := b.env.IdentityProvider(t).SignIns()

	start := b.do(t, b.server, http.MethodGet, b.env.URL+refusal.Reauthenticate+"&return=/console", nil)
	if start.status != http.StatusFound || !strings.Contains(start.location, "max_age=0") {
		t.Fatalf("fresh sign-in start = %d to %q, want 302 with max_age=0", start.status, start.location)
	}

	auth := b.do(t, b.idp, http.MethodGet, start.location, nil)

	back := b.do(t, b.server, http.MethodGet, auth.location, nil)
	if back.status != http.StatusSeeOther || b.env.IdentityProvider(t).SignIns() != signIns+1 {
		t.Fatalf("fresh callback = %d, want 303 after a new authentication: %s", back.status, back.body)
	}

	b.ownToken(t, body)
}

// checkConsoleAPIsTakeTheCookie proves that the APIs of the console take
// the session cookie, and that the TFE-compatible API does not. The person
// has no grant, so a read of one namespace answers 403, not 401. A change
// with the cookie and no CSRF value answers 403 that names the header.
func checkConsoleAPIsTakeTheCookie(t *testing.T, b browser) {
	t.Helper()

	if a := b.do(t, b.server, http.MethodGet, b.env.URL+"/api/user", nil); a.status != http.StatusOK ||
		!strings.Contains(a.body, e2e.SignInProvider+":248289761001") {
		t.Errorf("GET /api/user with the session cookie = %d %s, want 200 with the subject", a.status, a.body)
	}

	for _, path := range []string{
		"/api/dustpan/v1/namespaces/" + e2e.Namespace + "/collections",
		"/api/audit/entries?organization=" + e2e.Namespace,
	} {
		if a := b.do(t, b.server, http.MethodGet, b.env.URL+path, nil); a.status != http.StatusForbidden {
			t.Errorf("GET %s with the session cookie and no grant = %d, want 403: %s", path, a.status, a.body)
		}
	}

	rename := b.env.URL + "/api/stacks/" + e2e.Namespace + "/proj/dev/rename"
	if a := b.do(t, b.server, http.MethodPost, rename, nil); a.status != http.StatusForbidden ||
		!strings.Contains(a.body, "X-Dustpan-CSRF") {
		t.Errorf("POST with the session cookie and no CSRF value = %d %s, want 403 that names the header",
			a.status, a.body)
	}

	// A request with the cookie and no Fetch Metadata, or marked cross-site,
	// is refused before the CSRF check.
	for _, site := range []string{"", "cross-site", "same-site"} {
		header := http.Header{"Sec-Fetch-Site": {site}}
		if a := b.do(t, b.server, http.MethodPost, rename, header); a.status != http.StatusForbidden ||
			!strings.Contains(a.body, "another site") {
			t.Errorf("POST with the session cookie and Sec-Fetch-Site %q = %d %s, want 403 from another site",
				site, a.status, a.body)
		}
	}

	tfe := "/api/v2/organizations/" + e2e.Namespace + "/workspaces"
	if a := b.do(t, b.server, http.MethodGet, b.env.URL+tfe, nil); a.status != http.StatusUnauthorized {
		t.Errorf("GET %s with the session cookie = %d, want 401: %s", tfe, a.status, a.body)
	}
}

// checkCallbackOfAnotherState proves that a callback whose state differs
// from the sign-in cookie fails, and that the session stays.
func checkCallbackOfAnotherState(t *testing.T, b browser) {
	t.Helper()

	callback := b.startAndAuthorize(t)
	q := callback.Query()
	q.Set("state", q.Get("state")+"x")
	callback.RawQuery = q.Encode()

	if a := b.do(t, b.server, http.MethodGet, callback.String(), nil); a.status != http.StatusBadRequest {
		t.Fatalf("callback with another state = %d, want 400: %s", a.status, a.body)
	}
}

// checkSessionLimits proves the idle limit and the maximum length of the
// default policy on the shared clock: 15 minutes and 12 hours.
func checkSessionLimits(t *testing.T, b browser) {
	t.Helper()

	clock := b.env.Clock(t)

	clock.Advance(16 * time.Minute)

	if _, status := b.session(t); status != http.StatusUnauthorized {
		t.Fatalf("session after 16 idle minutes = %d, want 401", status)
	}

	b.signIn(t)

	// A use every 10 minutes keeps the session until its maximum length.
	for elapsed := 10 * time.Minute; elapsed < 12*time.Hour; elapsed += 10 * time.Minute {
		clock.Advance(10 * time.Minute)

		if _, status := b.session(t); status != http.StatusOK {
			t.Fatalf("session after %s with a use every 10 minutes = %d, want 200", elapsed, status)
		}
	}

	clock.Advance(10 * time.Minute)

	if _, status := b.session(t); status != http.StatusUnauthorized {
		t.Fatalf("session after its maximum length = %d, want 401", status)
	}
}

// checkSignOut proves that a sign-out needs the CSRF value, and ends the
// session.
func checkSignOut(t *testing.T, b browser) {
	t.Helper()

	sess := b.signIn(t)
	signOut := b.env.URL + "/api/server/sign-out"

	if a := b.do(t, b.server, http.MethodPost, signOut, nil); a.status != http.StatusForbidden {
		t.Fatalf("sign-out without the CSRF header = %d, want 403", a.status)
	}

	header := http.Header{"X-Dustpan-Csrf": {sess.CSRFToken}}
	if a := b.do(t, b.server, http.MethodPost, signOut, header); a.status != http.StatusNoContent {
		t.Fatalf("sign-out = %d, want 204: %s", a.status, a.body)
	}

	if _, status := b.session(t); status != http.StatusUnauthorized {
		t.Fatalf("session after sign-out = %d, want 401", status)
	}
}

// providerAnswer is a sign-in provider on the wire, in the e2e steps.
type providerAnswer struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Version int64  `json:"version"`
}

// storedProviderBody is the create body of the provider beta.
func storedProviderBody(issuer string) string {
	return `{"id":"` + e2e.StoredSignInProvider + `","issuer":"` + issuer + `","displayName":"Beta",` +
		`"clientID":"` + e2e.StoredClientID + `","clientSecret":"` + e2e.StoredClientSecret + `",` +
		`"scopes":["profile","email"]}`
}

// signInWith runs a sign-in through the provider id with the browser
// client of its test provider, and returns the answer of the callback.
func (b browser) signInWith(t *testing.T, idp *http.Client, id string) answer {
	t.Helper()

	start := b.do(t, b.server, http.MethodGet, b.env.URL+"/api/server/sign-in/"+id+"?return=/console", nil)
	if start.status != http.StatusFound {
		return start
	}

	auth := b.do(t, idp, http.MethodGet, start.location, nil)
	if auth.status != http.StatusFound {
		t.Fatalf("authorization at %s = %d: %s", id, auth.status, auth.body)
	}

	return b.do(t, b.server, http.MethodGet, auth.location, nil)
}

// patchStored changes the provider beta through the API and returns its
// new version.
func patchStored(ctx context.Context, t *testing.T, env *e2e.Env, body string) int64 {
	t.Helper()

	status, raw := env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPatch, Path: "/api/server/sign-in-providers/" + e2e.StoredSignInProvider, Body: body,
	})
	if status != http.StatusOK {
		t.Fatalf("change of the stored provider %s = %d: %s", body, status, raw)
	}

	var p providerAnswer
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("decode the provider: %v", err)
	}

	return p.Version
}

// checkStoredProvider proves the sign-in providers of the API:
//   - the bootstrap token creates the provider beta, and a person signs in
//     through it, with a subject of its own;
//   - a wrong client secret makes the next sign-in fail, and the right one
//     makes it work again, with no restart;
//   - the provider of the configuration cannot be changed;
//   - a provider with principals is not deleted, and a disabled provider
//     refuses the start of a sign-in.
func checkStoredProvider(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	beta := env.StoredIdentityProvider(t)

	status, raw := env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/sign-in-providers", Body: storedProviderBody(beta.Issuer()),
	})
	if status != http.StatusCreated || strings.Contains(raw, e2e.StoredClientSecret) {
		t.Fatalf("create the stored provider = %d: %s", status, raw)
	}

	idp := *beta.Client()
	idp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := browser{env: env, server: env.BrowserClient(t), idp: &idp}

	if back := b.signInWith(t, b.idp, e2e.StoredSignInProvider); back.status != http.StatusSeeOther {
		t.Fatalf("sign-in through the stored provider = %d: %s", back.status, back.body)
	}

	if sess, code := b.session(t); code != http.StatusOK || sess.Subject != e2e.StoredSignInProvider+":248289761001" {
		t.Fatalf("session of the stored provider = %d %+v", code, sess)
	}

	version := patchStored(ctx, t, env, `{"version":1,"clientSecret":"a-wrong-secret"}`)
	if back := b.signInWith(t, b.idp, e2e.StoredSignInProvider); back.status == http.StatusSeeOther {
		t.Fatal("a sign-in with a wrong client secret passed")
	}

	version = patchStored(ctx, t, env,
		`{"version":`+strconv.FormatInt(version, 10)+`,"clientSecret":"`+e2e.StoredClientSecret+`"}`)
	if back := b.signInWith(t, b.idp, e2e.StoredSignInProvider); back.status != http.StatusSeeOther {
		t.Fatalf("sign-in after the right secret = %d: %s", back.status, back.body)
	}

	checkStoredProviderRefusals(ctx, t, env)

	patchStored(ctx, t, env, `{"version":`+strconv.FormatInt(version, 10)+`,"enabled":false}`)

	start := b.do(t, b.server, http.MethodGet, env.URL+"/api/server/sign-in/"+e2e.StoredSignInProvider, nil)
	if start.status != http.StatusNotFound {
		t.Fatalf("start of a disabled provider = %d, want 404", start.status)
	}
}

// checkStoredProviderRefusals proves that the provider of the
// configuration cannot be changed and that a provider with principals is
// not deleted, and that the list holds both providers.
func checkStoredProviderRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, raw := env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPatch,
		Path:   "/api/server/sign-in-providers/" + e2e.SignInProvider,
		Body:   `{"version":0,"enabled":false}`,
	})
	if status != http.StatusConflict {
		t.Fatalf("change of the provider of the configuration = %d: %s", status, raw)
	}

	status, raw = env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodDelete, Path: "/api/server/sign-in-providers/" + e2e.StoredSignInProvider, Body: "",
	})
	if status != http.StatusConflict || !strings.Contains(raw, "disable it") {
		t.Fatalf("delete of a provider with principals = %d: %s", status, raw)
	}

	checkStoredProviderList(ctx, t, env)
}

// checkStoredProviderList proves that the list holds the static corp and
// the stored beta, and that no read holds the client secret.
func checkStoredProviderList(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, raw, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/sign-in-providers", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list of the providers = %d, %v: %s", status, err, raw)
	}

	var list struct {
		Providers []providerAnswer `json:"providers"`
	}

	decodeErr := json.Unmarshal([]byte(raw), &list)
	if decodeErr != nil || len(list.Providers) != 2 || list.Providers[0].Source != "static" ||
		list.Providers[1].Source != "api" {
		t.Fatalf("providers = %+v, %v; want the static corp and the stored beta", list.Providers, decodeErr)
	}

	status, raw, err = env.ServerAPIStatus(ctx, http.MethodGet,
		"/api/server/sign-in-providers/"+e2e.StoredSignInProvider, "")
	if err != nil || status != http.StatusOK || strings.Contains(raw, e2e.StoredClientSecret) {
		t.Fatalf("read of the stored provider = %d, %v: %s", status, err, raw)
	}
}

// storedProviderGamma is a stored provider with no principal, which the
// API deletes.
const storedProviderGamma = "gamma"

// checkStoredProviderDelete proves that the API deletes a stored provider
// with no principal: the delete answers 204, and a read then answers 404.
func checkStoredProviderDelete(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	body := `{"id":"` + storedProviderGamma + `","issuer":"` + env.StoredIdentityProvider(t).Issuer() + `",` +
		`"displayName":"Gamma","clientID":"` + e2e.StoredClientID + `","clientSecret":"` + e2e.StoredClientSecret + `"}`

	status, raw := env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/sign-in-providers", Body: body,
	})
	if status != http.StatusCreated {
		t.Fatalf("create the provider %s = %d: %s", storedProviderGamma, status, raw)
	}

	target := "/api/server/sign-in-providers/" + storedProviderGamma

	status, raw = env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodDelete, Path: target, Body: "",
	})
	if status != http.StatusNoContent {
		t.Fatalf("delete of a provider with no principal = %d: %s", status, raw)
	}

	status, raw, err := env.ServerAPIStatus(ctx, http.MethodGet, target, "")
	if err != nil || status != http.StatusNotFound {
		t.Fatalf("read of a deleted provider = %d, %v: %s", status, err, raw)
	}
}

// checkProviderAuditHoldsNoSecret proves that the audit log of the
// installation holds the changes of the stored providers and no client
// secret: not the first one, not the rotated one, and not the one of the
// plain OAuth provider.
func checkProviderAuditHoldsNoSecret(t *testing.T, env *e2e.Env) {
	t.Helper()

	const size = 200

	ctx := t.Context()
	providerEntries := 0

	for number := 1; ; number++ {
		path := "/api/audit/entries?organization=@server&page[number]=" + strconv.Itoa(number) +
			"&page[size]=" + strconv.Itoa(size)

		status, raw, err := env.APIStatus(ctx, http.MethodGet, path, "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("list @server audit entries = %d, %v: %s", status, err, raw)
		}

		for _, secret := range []string{e2e.StoredClientSecret, "a-wrong-secret", e2e.PlainClientSecret} {
			if strings.Contains(raw, secret) {
				t.Fatal("the audit log of the installation holds a client secret of a stored provider")
			}
		}

		providerEntries += strings.Count(raw, `"sign-in-provider:`)

		var page struct {
			Entries []json.RawMessage `json:"entries"`
		}

		if decodeErr := json.Unmarshal([]byte(raw), &page); decodeErr != nil {
			t.Fatalf("decode @server audit entries: %v", decodeErr)
		}

		if len(page.Entries) < size {
			break
		}
	}

	if providerEntries == 0 {
		t.Fatal("the audit log of the installation holds no change of a stored provider")
	}
}

// checkPlainProvider proves a stored provider in the plain OAuth 2.0
// mode: the bootstrap token creates it through the API with its endpoints
// and its subject claim, the answer holds no client secret, and a person
// signs in through it with a subject of its own.
func checkPlainProvider(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	hub := env.PlainIdentityProvider(t)
	body := `{"id":"` + e2e.PlainSignInProvider + `","mode":"oauth","displayName":"Hub",` +
		`"clientID":"` + e2e.PlainClientID + `","clientSecret":"` + e2e.PlainClientSecret + `",` +
		`"authorizationURL":"` + hub.AuthorizationEndpoint() + `","tokenURL":"` + hub.TokenEndpoint() + `",` +
		`"userinfoURL":"` + hub.UserinfoEndpoint() + `","claims":{"subject":"sub"},"autoCreate":true}`

	status, raw := env.FreshAdminAPIStatus(ctx, t, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/sign-in-providers", Body: body,
	})
	if status != http.StatusCreated || strings.Contains(raw, e2e.PlainClientSecret) ||
		!strings.Contains(raw, `"mode":"oauth"`) {
		t.Fatalf("create the plain OAuth provider = %d: %s", status, raw)
	}

	idp := *hub.Client()
	idp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := browser{env: env, server: env.BrowserClient(t), idp: &idp}

	if back := b.signInWith(t, b.idp, e2e.PlainSignInProvider); back.status != http.StatusSeeOther {
		t.Fatalf("sign-in through the plain OAuth provider = %d: %s", back.status, back.body)
	}

	sess, code := b.session(t)
	if code != http.StatusOK || sess.Subject != e2e.PlainSignInProvider+":248289761001" {
		t.Fatalf("session of the plain OAuth provider = %d %+v", code, sess)
	}
}
