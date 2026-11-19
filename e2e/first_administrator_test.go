//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// Paths and text of the first administrator steps. The guide
// docs/guides/first-administrator.md shows the same bodies.
const (
	firstAdminPath   = "/api/server/first-administrator"
	firstAdminRedeem = "/api/server/first-administrator/redeem"
	firstAdminReason = "first administrator of the new installation"
	siteAdminRole    = "site-administrator"
)

// firstAdminOffer is the answer of the offer.
type firstAdminOffer struct {
	Principal string `json:"principal"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

// firstAdminRedeemed is the answer of the redeem.
type firstAdminRedeemed struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
	Grant     string `json:"grant"`
	MadeAt    string `json:"madeAt"`
}

// TestFirstAdministratorPath drives the path of the first administrator
// through the real binary, on each store. No CLI calls these routes, so the
// test plays the operator with the bootstrap admin token and the person with
// a console session of a password sign-in. The stale sign-in row runs on the
// in-process server with the shared test clock, with a person of the test
// OpenID provider.
func TestFirstAdministratorPath(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tofuVersion := e2e.OpenTofu().Single()
	pulumiVersion := e2e.Pulumi().Single()

	if tofuVersion == "" || pulumiVersion == "" {
		t.Fatal("the OpenTofu tool and the Pulumi tool need at least one version in their matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	pulumi, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			t.Run("local user", func(t *testing.T) {
				runFirstAdministratorLocalUser(t, e2e.NewEnv(t.Context(), t, tofu, db))
			})

			t.Run("stale sign-in", func(t *testing.T) {
				runFirstAdministratorStaleSignIn(t, e2e.NewEnvWithSignIn(t.Context(), t, pulumi, db))
			})
		})
	}
}

// runFirstAdministratorLocalUser runs the whole path of the guide with a
// local user.
func runFirstAdministratorLocalUser(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	c := localClient{env: env, browser: env.BrowserClient(t)}
	alice := signInAlice(t, c)

	// Step 2 of the guide: nothing is made yet.
	before := c.send(t, http.MethodGet, firstAdminPath, "", asAdmin())
	before.want(t, "read the state before", http.StatusOK, "")
	wantFirstAdminBody(t, "state before", before.body, `"made":false`, `"offer":null`)

	// A console session is not the bootstrap administrator: it cannot ask for a code.
	if refused := c.send(t, http.MethodPost, firstAdminPath, firstAdminOfferBody(alice.principal), asBrowser(alice.csrf)); refused.status != http.StatusForbidden {
		t.Fatalf("offer with a console session = %d, want 403: %s", refused.status, refused.body)
	}

	// Step 3: the operator asks for the code.
	offered := c.send(t, http.MethodPost, firstAdminPath, firstAdminOfferBody(alice.principal), asAdmin())
	offered.want(t, "offer", http.StatusCreated, "")

	var offer firstAdminOffer
	if err := json.Unmarshal([]byte(offered.body), &offer); err != nil ||
		offer.Principal != alice.principal || len(offer.Code) != 43 || offer.ExpiresAt == "" {
		t.Fatalf("offer = %s (%v), want the principal, a code of 43 characters and an expiry", offered.body, err)
	}

	// A wrong code spends nothing.
	c.send(t, http.MethodPost, firstAdminRedeem, `{"code":"`+strings.Repeat("A", 43)+`"}`, asBrowser(alice.csrf)).
		want(t, "redeem a wrong code", http.StatusBadRequest, "first-administrator.code-invalid")

	// An access token never gives a fresh authentication.
	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	made := c.send(t, http.MethodPost, "/api/dustpan/v1/me/tokens",
		`{"name":"first-administrator","expiresAt":"`+expires+`"}`, asBrowser(alice.csrf))
	made.want(t, "make a personal token", http.StatusCreated, "")

	var tok e2eToken
	if err := json.Unmarshal([]byte(made.body), &tok); err != nil || tok.Secret == "" {
		t.Fatalf("token = %s (%v)", made.body, err)
	}

	status, body, err := env.ServerAPIStatusAs(ctx, tok.Secret, http.MethodPost, firstAdminRedeem,
		`{"code":"`+offer.Code+`"}`)
	if err != nil || status != http.StatusForbidden || !strings.Contains(body, `"errorCode":"mfa.fresh-step-required"`) {
		t.Fatalf("redeem with an access token = %d, %v (%s); want 403 with mfa.fresh-step-required", status, err, body)
	}

	// Step 4 and 5: the person redeems the code in the console session.
	redeemed := c.send(t, http.MethodPost, firstAdminRedeem, `{"code":"`+offer.Code+`"}`, asBrowser(alice.csrf))
	redeemed.want(t, "redeem", http.StatusOK, "")

	var done firstAdminRedeemed
	if err := json.Unmarshal([]byte(redeemed.body), &done); err != nil ||
		done.Principal != alice.principal || done.Role != siteAdminRole || done.Grant == "" || done.MadeAt == "" {
		t.Fatalf("redeem = %s (%v), want the principal, the role %s, a grant ID and a time", redeemed.body, err, siteAdminRole)
	}

	// The code is spent.
	c.send(t, http.MethodPost, firstAdminRedeem, `{"code":"`+offer.Code+`"}`, asBrowser(alice.csrf)).
		want(t, "redeem again", http.StatusConflict, "first-administrator.already-made")

	checkFirstAdministratorAfter(t, c, alice, done)
}

// checkFirstAdministratorAfter checks step 6 of the guide: the state, the
// closed route, the grant and the audit entries.
func checkFirstAdministratorAfter(t *testing.T, c localClient, alice signedInPerson, done firstAdminRedeemed) {
	t.Helper()

	after := c.send(t, http.MethodGet, firstAdminPath, "", asAdmin())
	after.want(t, "read the state after", http.StatusOK, "")
	wantFirstAdminBody(t, "state after", after.body,
		`"made":true`, `"principal":"`+alice.principal+`"`, `"grant":"`+done.Grant+`"`)

	c.send(t, http.MethodPost, firstAdminPath, firstAdminOfferBody(alice.principal), asAdmin()).
		want(t, "offer after the marker", http.StatusConflict, "first-administrator.already-made")

	grants := c.send(t, http.MethodGet, "/api/dustpan/v1/me/grants", "", asBrowser(alice.csrf))
	grants.want(t, "list the grants of the person", http.StatusOK, "")
	wantFirstAdminBody(t, "grants", grants.body, siteAdminRole, done.Grant)

	audit := c.send(t, http.MethodGet, "/api/audit/entries?organization=@server", "", asAdmin())
	audit.want(t, "list the audit entries of the installation", http.StatusOK, "")

	var list struct {
		Entries []auditListItem `json:"entries"`
	}

	if err := json.Unmarshal([]byte(audit.body), &list); err != nil {
		t.Fatalf("decode the audit entries: %v", err)
	}

	var offers, redeems int

	for i := range list.Entries {
		entry := &list.Entries[i]
		if !strings.Contains(entry.Action, "first-administrator") || entry.Outcome != "success" {
			continue
		}

		if strings.Contains(entry.Action, "redeem") {
			redeems++
		} else {
			offers++
		}
	}

	if offers < 1 || redeems < 1 {
		t.Fatalf("@server entries hold %d offer and %d redeem entries of the first administrator, want one of each: %s",
			offers, redeems, audit.body)
	}

	if strings.Contains(audit.body, "\"code\"") {
		t.Fatalf("the audit entries hold a code field: %s", audit.body)
	}
}

// runFirstAdministratorStaleSignIn proves that a sign-in older than the
// window cannot redeem, and that a new sign-in can. The person is the person
// of the test OpenID provider, and the clock is the one that the server shares.
func runFirstAdministratorStaleSignIn(t *testing.T, env *e2e.Env) {
	t.Helper()

	if !env.SharesClock() {
		t.Skip("the stale sign-in row needs the in-process server with the shared test clock")
	}

	ctx := t.Context()
	clock := env.Clock(t)
	b := newBrowser(t, env)

	b.signIn(t)

	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	person := b.ownToken(t, `{"name":"first-administrator","expiresAt":"`+expires+`"}`).PrincipalID

	// The same steps as the token freshness check: touch the session so it
	// does not go idle, and pass the window.
	clock.Advance(10 * time.Minute)
	b.session(t)
	clock.Advance(6 * time.Minute)

	// The code lives 10 minutes, so the operator asks for it after the time passed.
	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, firstAdminPath, firstAdminOfferBody(person))
	if err != nil || status != http.StatusCreated {
		t.Fatalf("offer = %d, %v (%s)", status, err, body)
	}

	var offer firstAdminOffer
	if err = json.Unmarshal([]byte(body), &offer); err != nil || len(offer.Code) != 43 {
		t.Fatalf("offer = %s (%v)", body, err)
	}

	stale := b.tokenChange(t, firstAdminRedeem, `{"code":"`+offer.Code+`"}`)
	if stale.status != http.StatusForbidden || !strings.Contains(stale.body, `"errorCode":"mfa.fresh-step-required"`) {
		t.Fatalf("redeem with a stale sign-in = %d, want 403 with mfa.fresh-step-required: %s", stale.status, stale.body)
	}

	// The refusal changes nothing: a new sign-in redeems the same code.
	b.signIn(t)

	fresh := b.tokenChange(t, firstAdminRedeem, `{"code":"`+offer.Code+`"}`)
	if fresh.status != http.StatusOK || !strings.Contains(fresh.body, siteAdminRole) {
		t.Fatalf("redeem with a new sign-in = %d, want 200 with the role %s: %s", fresh.status, siteAdminRole, fresh.body)
	}
}

// firstAdminOfferBody returns the body of the offer for principal.
func firstAdminOfferBody(principal string) string {
	return `{"principal":"` + principal + `","reason":"` + firstAdminReason + `"}`
}

// wantFirstAdminBody fails the test unless body holds each part.
func wantFirstAdminBody(t *testing.T, step, body string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(body, part) {
			t.Fatalf("%s = %s, want it to hold %s", step, body, part)
		}
	}
}
