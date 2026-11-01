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
)

// Passwords of the local user steps. Each one passes every default rule.
const (
	e2ePassword    = "violet-harbor-tin-81-ledger"
	e2eNewPassword = "copper-meadow-quiet-47-lantern"
)

// TestLocalPasswordSignIn drives the local users and the password sign-in on
// each store. No CLI makes a local user or signs in with a password, so the
// test plays the administrator with the token and the browser with a cookie
// jar. The server has an HTTPS public URL and no identity provider, so the
// password sign-in is on by default.
func TestLocalPasswordSignIn(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.OpenTofu().Versions()
	if len(versions) == 0 {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runLocalPasswordOnStore(t, e2e.NewEnv(t.Context(), t, tofu, db))
		})
	}
}

// localClient is the browser and the administrator of one local user run.
type localClient struct {
	env     *e2e.Env
	browser *http.Client
}

// localAnswer is the status, the error code and the body of an answer.
type localAnswer struct {
	status int
	code   string
	body   string
}

// send sends one request with the token when token is true, and with the
// cookie jar of the browser and the CSRF value when csrf is not empty.
func (c localClient) send(t *testing.T, method, path, body string, token bool, csrf string) localAnswer {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, c.env.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	if token {
		req.Header.Set("Authorization", "Bearer "+c.env.AdminToken())
	}

	if csrf != "" {
		req.Header.Set("X-Dustpan-CSRF", csrf)
	}

	resp, err := c.browser.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close the body: %v", closeErr)
		}
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}

	var coded struct {
		ErrorCode string `json:"errorCode"`
	}

	if len(raw) > 0 && resp.StatusCode >= http.StatusBadRequest {
		if jsonErr := json.Unmarshal(raw, &coded); jsonErr != nil {
			t.Fatalf("%s %s: the error answer is not JSON: %s", method, path, raw)
		}
	}

	return localAnswer{status: resp.StatusCode, code: coded.ErrorCode, body: string(raw)}
}

// want fails unless a has the status and the error code.
func (a localAnswer) want(t *testing.T, step string, status int, code string) {
	t.Helper()

	if a.status != status || a.code != code {
		t.Fatalf("%s: answer = %d %q, want %d %q: %s", step, a.status, a.code, status, code, a.body)
	}
}

// runLocalPasswordOnStore runs the steps of the local users on the server of
// env.
func runLocalPasswordOnStore(t *testing.T, env *e2e.Env) {
	t.Helper()

	c := localClient{env: env, browser: env.BrowserClient(t)}

	policy := c.send(t, http.MethodGet, "/api/dustpan/v1/local-users/policy", "", true, "")
	policy.want(t, "read the policy", http.StatusOK, "")

	if !strings.Contains(policy.body, `"enabled":true`) || !strings.Contains(policy.body, `"minLength":15`) {
		t.Fatalf("policy = %s, want the password sign-in on with a minimum of 15", policy.body)
	}

	created := c.send(t, http.MethodPost, "/api/dustpan/v1/local-users",
		`{"userName":"alice","displayName":"Alice Example","reason":"e2e"}`, true, "")
	created.want(t, "create the local user", http.StatusCreated, "")

	var user struct {
		PrincipalID string `json:"principalId"`
		Subject     string `json:"subject"`
		SetUpCode   string `json:"setUpCode"`
	}

	if err := json.Unmarshal([]byte(created.body), &user); err != nil || len(user.SetUpCode) != 43 {
		t.Fatalf("created = %s (%v)", created.body, err)
	}

	c.send(t, http.MethodPost, "/api/dustpan/v1/local-users/redeem",
		`{"code":"`+user.SetUpCode+`","password":"short"}`, false, "").
		want(t, "set a short password", http.StatusBadRequest, "password.too-short")

	c.send(t, http.MethodPost, "/api/dustpan/v1/local-users/redeem",
		`{"code":"`+user.SetUpCode+`","password":"`+e2ePassword+`"}`, false, "").
		want(t, "set the password", http.StatusNoContent, "")

	c.send(t, http.MethodPost, "/api/dustpan/v1/local-users/redeem",
		`{"code":"`+user.SetUpCode+`","password":"`+e2eNewPassword+`"}`, false, "").
		want(t, "use the set-up code again", http.StatusBadRequest, "local-user.code-invalid")

	c.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"alice","password":"`+e2eNewPassword+`"}`, false, "").
		want(t, "sign in with a wrong password", http.StatusUnauthorized, "local-user.sign-in-failed")

	c.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"ALICE","password":"`+e2ePassword+`"}`, false, "").
		want(t, "sign in", http.StatusNoContent, "")

	csrf := c.readSession(t, user.Subject)

	c.send(t, http.MethodPost, "/api/dustpan/v1/local-users/reset",
		`{"principalId":"`+user.PrincipalID+`","reason":"e2e"}`, true, "").
		want(t, "reset with a token", http.StatusForbidden, "mfa.fresh-step-required")

	c.send(t, http.MethodPost, "/api/dustpan/v1/me/password",
		`{"currentPassword":"`+e2ePassword+`","newPassword":"`+e2eNewPassword+`"}`, false, csrf).
		want(t, "change the password", http.StatusNoContent, "")

	// The jar holds the new session cookie of the change.
	c.readSession(t, user.Subject)

	c.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"alice","password":"`+e2ePassword+`"}`, false, "").
		want(t, "sign in with the old password", http.StatusUnauthorized, "local-user.sign-in-failed")

	c.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"alice","password":"`+e2eNewPassword+`"}`, false, "").
		want(t, "sign in with the new password", http.StatusNoContent, "")
}

// readSession reads the console session of the jar, checks its subject and
// its provider, and returns its CSRF value.
func (c localClient) readSession(t *testing.T, subject string) string {
	t.Helper()

	a := c.send(t, http.MethodGet, "/api/server/session", "", false, "")
	a.want(t, "read the session", http.StatusOK, "")

	var s e2eSession
	if err := json.Unmarshal([]byte(a.body), &s); err != nil {
		t.Fatalf("decode the session: %v", err)
	}

	if s.Subject != subject || s.Provider != "local" || s.CSRFToken == "" {
		t.Fatalf("session = %+v, want %s with the provider local", s, subject)
	}

	return s.CSRFToken
}
