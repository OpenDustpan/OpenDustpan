//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/ldaptest"
)

// Values of the LDAP sign-in steps.
const (
	ldapE2EPassword = "right-password-of-alice"
	ldapE2EUUID     = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// TestLDAPSignIn signs a person in through the password route of an LDAP
// provider, end to end, on each store: over TLS, the session works. A
// connection with no TLS is refused while egress.allowPlainConnections is
// off, works after an administrator turns it on, and is refused again after
// the administrator turns it off.
func TestLDAPSignIn(t *testing.T) {
	t.Parallel()

	if e2e.UseBinaryServer() {
		t.Skip("the LDAP steps run against the in-process server: the binary refuses a directory on the loopback")
	}

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
			runLDAPSignInOnStore(t, cli, db)
		})
	}
}

// runLDAPSignInOnStore runs the LDAP sign-in steps on store.
func runLDAPSignInOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()

	dir := ldaptest.Start(t)
	dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"entryUUID": {ldapE2EUUID}}, UserName: "alice", Password: ldapE2EPassword,
	})

	env := e2e.NewEnvWithLDAP(ctx, t, cli, store, dir)
	checkReady(ctx, t, env)

	c := ldapClient{env: env, browser: env.BrowserClient(t)}

	c.signIn(ctx, t, e2e.LDAPTLSProvider, http.StatusNoContent)
	c.checkSession(ctx, t)

	c.signIn(ctx, t, e2e.LDAPPlainProvider, http.StatusServiceUnavailable)
	setPlainConnections(ctx, t, env, "true")
	c.signIn(ctx, t, e2e.LDAPPlainProvider, http.StatusNoContent)
	setPlainConnections(ctx, t, env, "false")
	c.signIn(ctx, t, e2e.LDAPPlainProvider, http.StatusServiceUnavailable)
}

// ldapClient is a browser with a cookie jar at the server of an env.
type ldapClient struct {
	env     *e2e.Env
	browser *http.Client
}

// do sends req with the browser and returns the status and the body.
func (c ldapClient) do(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()

	resp, err := c.browser.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("%s %s: read the answer: %v, %v", req.Method, req.URL.Path, readErr, closeErr)
	}

	return resp.StatusCode, body
}

// signIn sends the password sign-in of alice to provider, and fails unless
// it answers want with no password in the answer.
func (c ldapClient) signIn(ctx context.Context, t *testing.T, provider string, want int) {
	t.Helper()

	body := `{"userName":"alice","password":"` + ldapE2EPassword + `"}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.env.URL+"/api/dustpan/v1/sign-in/providers/"+provider+"/password", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the sign-in request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	status, got := c.do(t, req)
	if status != want {
		t.Fatalf("sign-in at %s = %d %s, want %d", provider, status, got, want)
	}

	if strings.Contains(string(got), ldapE2EPassword) {
		t.Fatalf("the answer of the sign-in at %s holds the password", provider)
	}
}

// checkSession reads the session of the browser and fails unless it is the
// session of alice at the TLS provider.
func (c ldapClient) checkSession(ctx context.Context, t *testing.T) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.env.URL+"/api/server/session", http.NoBody)
	if err != nil {
		t.Fatalf("build the session request: %v", err)
	}

	status, body := c.do(t, req)

	var session struct {
		Subject  string `json:"subject"`
		Provider string `json:"provider"`
	}

	if err := json.Unmarshal(body, &session); err != nil || status != http.StatusOK {
		t.Fatalf("session = %d %s, %v", status, body, err)
	}

	if session.Subject != e2e.LDAPTLSProvider+":"+ldapE2EUUID || session.Provider != e2e.LDAPTLSProvider {
		t.Fatalf("session = %+v, want the subject of alice at %s", session, e2e.LDAPTLSProvider)
	}
}

// setPlainConnections sets egress.allowPlainConnections to value, true or
// false, as an administrator with a fresh sign-in, through the settings route.
func setPlainConnections(ctx context.Context, t *testing.T, env *e2e.Env, value string) {
	t.Helper()

	read, err := env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: env.AdminToken(), Method: http.MethodGet, Path: settingsRoute, ContentType: "", Body: "", IfMatch: "",
	})
	if err != nil || read.Status != http.StatusOK || read.Header.Get("ETag") == "" {
		t.Fatalf("read the settings = %+v, %v; want 200 with an ETag", read, err)
	}

	status, body := env.FreshAdminMergePatch(ctx, t, settingsRoute, read.Header.Get("ETag"),
		`{"egress.allowPlainConnections":`+value+`}`)
	if status != http.StatusOK {
		t.Fatalf("set egress.allowPlainConnections to %s = %d %s, want 200", value, status, body)
	}
}
