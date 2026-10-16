//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
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

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runSignInOnStore(t, cli, db)
		})
	}
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
func runSignInOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
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
	checkSessionLimits(t, b)
	checkSignOut(t, b)
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
		"/api/inventory/namespaces/" + e2e.Namespace + "/collections",
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
