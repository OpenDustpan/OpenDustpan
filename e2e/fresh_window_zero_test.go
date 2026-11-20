//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// freshZeroLocalSignIn is the path that the refusal of a local user names.
const freshZeroLocalSignIn = "/api/dustpan/v1/sign-in/password"

// TestFreshWindowZeroAskedEachTime drives a token freshness window of 0
// through the real binary, on each store. No CLI calls these routes, so the
// test plays a person with a console session of a password sign-in. The
// person is a site administrator, so the guarded step is allowed for them and
// only the fresh authentication is missing. Under a window of 0 the step
// answers 403 with mfa.fresh-step-required and the path of the password
// sign-in, also right after a new sign-in, and it changes nothing. A request
// with no session stays 401.
func TestFreshWindowZeroAskedEachTime(t *testing.T) {
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

			runFreshWindowZero(t, e2e.NewEnv(t.Context(), t, tofu, db))
		})
	}
}

// runFreshWindowZero runs the steps of the window of 0 on the server of env.
func runFreshWindowZero(t *testing.T, env *e2e.Env) {
	t.Helper()

	c := localClient{env: env, browser: env.BrowserClient(t)}
	alice := signInAlice(t, c)

	// The operator offers the first administrator role, and alice redeems it
	// with her new password sign-in, which is a fresh authentication.
	offered := c.send(t, http.MethodPost, firstAdminPath, firstAdminOfferBody(alice.principal), asAdmin())
	offered.want(t, "offer", http.StatusCreated, "")

	var offer firstAdminOffer
	if err := json.Unmarshal([]byte(offered.body), &offer); err != nil || len(offer.Code) != 43 {
		t.Fatalf("offer = %s (%v)", offered.body, err)
	}

	c.send(t, http.MethodPost, firstAdminRedeem, `{"code":"`+offer.Code+`"}`, asBrowser(alice.csrf)).
		want(t, "redeem", http.StatusOK, "")

	// The default window of 15 minutes lets this new sign-in set the window to 0.
	set := c.send(
		t, http.MethodPatch, "/api/server/session-policy", `{"tokenFreshnessSeconds":0}`, asBrowser(alice.csrf),
	)
	set.want(t, "set the window to 0", http.StatusOK, "")

	if !strings.Contains(set.body, `"tokenFreshnessSeconds":0`) {
		t.Fatalf("set the window = %s, want the window 0", set.body)
	}

	signIn := func(step string) string {
		c.send(t, http.MethodPost, freshZeroLocalSignIn,
			`{"userName":"alice","password":"`+e2ePassword+`"}`, asBrowser("")).
			want(t, step, http.StatusNoContent, "")

		session := c.send(t, http.MethodGet, "/api/server/session", "", asBrowser(""))
		session.want(t, "read the session", http.StatusOK, "")

		var s e2eSession
		if err := json.Unmarshal([]byte(session.body), &s); err != nil || s.CSRFToken == "" {
			t.Fatalf("session = %s (%v)", session.body, err)
		}

		return s.CSRFToken
	}

	// The step is allowed for alice and the sign-in is new, yet the window of
	// 0 refuses it, and the answer names the path of the password sign-in.
	for _, step := range []string{"first sign-in", "second sign-in"} {
		csrf := signIn(step)

		refused := c.send(t, http.MethodPatch, "/api/server/session-policy", `{"idleSeconds":1800}`, asBrowser(csrf))
		refused.want(t, "change the policy after the "+step, http.StatusForbidden, "mfa.fresh-step-required")

		var answer struct {
			Reauthenticate string `json:"reauthenticate"`
		}

		err := json.Unmarshal([]byte(refused.body), &answer)
		if err != nil || answer.Reauthenticate != freshZeroLocalSignIn {
			t.Fatalf(
				"refusal after the %s = %s (%v), want reauthenticate %s", step, refused.body, err, freshZeroLocalSignIn,
			)
		}

		if !strings.Contains(refused.body, freshZeroLocalSignIn) {
			t.Fatalf("refusal after the %s = %s, want the text to name %s", step, refused.body, freshZeroLocalSignIn)
		}
	}

	// The refusal changes nothing.
	read := c.send(t, http.MethodGet, "/api/server/session-policy", "", asAdmin())
	read.want(t, "read the policy", http.StatusOK, "")

	if !strings.Contains(read.body, `"idleSeconds":900`) {
		t.Fatalf("policy = %s, want the idle limit unchanged", read.body)
	}

	// A request with no session is not a fresh-step refusal: it stays 401.
	anonymous := localClient{env: env, browser: env.BrowserClient(t)}
	anonymous.send(t, http.MethodPatch, "/api/server/session-policy", `{"idleSeconds":1800}`, asBrowser("x")).
		want(t, "change the policy with no session", http.StatusUnauthorized, "auth.unauthenticated")
}
