//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/internal/webauthntest"
)

// TestPasskeyEnrollment drives the passkey routes of a person on each store.
// No CLI adds a passkey, so the test plays the browser with a cookie jar and
// a software authenticator. The server has an HTTPS public URL with the host
// name localhost, because a relying party ID is never an IP address. The test
// signs in with a password, which is a fresh authentication, adds a passkey,
// lists it and removes it.
func TestPasskeyEnrollment(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.OpenTofu().Single()
	if version == "" {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runPasskeysOnStore(t, e2e.NewEnvWithPasskeys(t.Context(), t, tofu, db))
		})
	}
}

// passkeyDocument is the body of a passkey.
type passkeyDocument struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// runPasskeysOnStore runs the steps of the passkeys on the server of env.
func runPasskeysOnStore(t *testing.T, env *e2e.Env) {
	t.Helper()

	c := localClient{env: env, browser: env.BrowserClient(t)}
	alice := signInAlice(t, c)

	base, err := url.Parse(env.URL)
	if err != nil {
		t.Fatalf("parse %s: %v", env.URL, err)
	}

	authenticator := webauthntest.New("localhost", "https://localhost:"+base.Port())

	cred, err := webauthntest.NewCredential([]byte(alice.principal), false)
	if err != nil {
		t.Fatalf("make the credential: %v", err)
	}

	empty := c.send(t, http.MethodGet, "/api/dustpan/v1/me/passkeys", "", asBrowser(alice.csrf))
	empty.want(t, "list before the add", http.StatusOK, "")

	if !strings.Contains(empty.body, `"passkeys":[]`) {
		t.Fatalf("list = %s, want no passkey", empty.body)
	}

	key := addPasskey(t, c, alice, authenticator, cred)

	listed := c.send(t, http.MethodGet, "/api/dustpan/v1/me/passkeys", "", asBrowser(alice.csrf))
	listed.want(t, "list after the add", http.StatusOK, "")

	if !strings.Contains(listed.body, key.ID) || strings.Contains(listed.body, "publicKey") {
		t.Fatalf("list = %s, want %s and no key", listed.body, key.ID)
	}

	c.send(t, http.MethodDelete, "/api/dustpan/v1/me/passkeys/"+key.ID, "", asAdmin()).
		want(t, "remove with a token", http.StatusForbidden, "mfa.fresh-step-required")

	c.send(t, http.MethodDelete, "/api/dustpan/v1/me/passkeys/"+key.ID, "", asBrowser(alice.csrf)).
		want(t, "remove", http.StatusNoContent, "")

	c.send(t, http.MethodDelete, "/api/dustpan/v1/me/passkeys/"+key.ID, "", asBrowser(alice.csrf)).
		want(t, "remove again", http.StatusNotFound, "mfa.passkey-not-found")

	gone := c.send(t, http.MethodGet, "/api/dustpan/v1/me/passkeys", "", asBrowser(alice.csrf))
	gone.want(t, "list after the removal", http.StatusOK, "")

	if !strings.Contains(gone.body, `"passkeys":[]`) {
		t.Fatalf("list = %s, want no passkey", gone.body)
	}
}

// addPasskey runs a register ceremony of the person with the credential cred,
// proves that the same response works once, and returns the new passkey.
func addPasskey(
	t *testing.T, c localClient, person signedInPerson, authenticator *webauthntest.Authenticator,
	cred *webauthntest.Credential,
) passkeyDocument {
	t.Helper()

	begin := c.send(t, http.MethodPost, "/api/dustpan/v1/me/passkeys/register/begin", "", asBrowser(person.csrf))
	begin.want(t, "begin the registration", http.StatusOK, "")

	challenge, err := webauthntest.ChallengeOf([]byte(begin.body))
	if err != nil {
		t.Fatalf("challenge of %s: %v", begin.body, err)
	}

	response, err := authenticator.Register(cred, webauthntest.For(challenge))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	body, err := json.Marshal(map[string]any{"name": "e2e-key", "response": json.RawMessage(response)})
	if err != nil {
		t.Fatalf("encode the finish body: %v", err)
	}

	finished := c.send(t, http.MethodPost, "/api/dustpan/v1/me/passkeys/register/finish", string(body),
		asBrowser(person.csrf))
	finished.want(t, "finish the registration", http.StatusCreated, "")

	var key passkeyDocument
	if err := json.Unmarshal([]byte(finished.body), &key); err != nil || !strings.HasPrefix(key.ID, "pky-") ||
		key.Name != "e2e-key" {
		t.Fatalf("passkey = %s (%v), want a passkey named e2e-key", finished.body, err)
	}

	c.send(t, http.MethodPost, "/api/dustpan/v1/me/passkeys/register/finish", string(body), asBrowser(person.csrf)).
		want(t, "finish the same response again", http.StatusBadRequest, "mfa.ceremony-not-open")

	return key
}

// signedInPerson is a person with a console session in the cookie jar.
type signedInPerson struct {
	principal string
	csrf      string
}

// signInAlice makes the local user alice and signs the jar in with her
// password. The password sign-in is a fresh authentication.
func signInAlice(t *testing.T, c localClient) signedInPerson {
	t.Helper()

	created := c.send(t, http.MethodPost, "/api/dustpan/v1/local-users",
		`{"userName":"alice","displayName":"Alice Example","reason":"e2e"}`, asAdmin())
	created.want(t, "create the local user", http.StatusCreated, "")

	var user struct {
		PrincipalID string `json:"principal"`
		Subject     string `json:"subject"`
		SetUpCode   string `json:"setUpCode"`
	}

	if err := json.Unmarshal([]byte(created.body), &user); err != nil {
		t.Fatalf("created = %s (%v)", created.body, err)
	}

	c.send(t, http.MethodPost, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+user.SetUpCode+`","password":"`+e2ePassword+`"}`, asBrowser("")).
		want(t, "set the password", http.StatusNoContent, "")

	c.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"alice","password":"`+e2ePassword+`"}`, asBrowser("")).
		want(t, "sign in", http.StatusNoContent, "")

	return signedInPerson{principal: user.PrincipalID, csrf: c.readSession(t, user.Subject)}
}
