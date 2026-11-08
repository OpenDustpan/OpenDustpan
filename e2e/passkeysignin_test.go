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
	"opendustpan.org/opendustpan/internal/webauthntest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
)

// TestPasskeySignIn drives the passkey sign-in on each store. No CLI signs in
// with a passkey, so the test plays the browser with a cookie jar and a
// software authenticator. The local user has no password: a password sign-in
// fails, and the passkey signs the person in. The session makes an access
// token, and both CLIs use that token for one operation.
func TestPasskeySignIn(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersion, tofuVersion := e2e.Pulumi().Single(), e2e.OpenTofu().Single()
	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("the Pulumi tool and the OpenTofu tool need at least one version in their matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumi, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	tofu, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runPasskeySignInOnStore(t, e2e.NewEnvWithPasskeys(t.Context(), t, tofu, db), tofu, pulumi)
		})
	}
}

// passkeySignInClient is the browser of one passkey sign-in run.
type passkeySignInClient struct {
	localClient

	auth *webauthntest.Authenticator
	cred *webauthntest.Credential
}

// begin starts a sign-in ceremony and returns its challenge.
func (p passkeySignInClient) begin(t *testing.T) []byte {
	t.Helper()

	a := p.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/passkey/begin", "", asBrowser(""))
	a.want(t, "begin the passkey sign-in", http.StatusOK, "")

	if strings.Contains(a.body, `"allowCredentials":[{`) || !strings.Contains(a.body, `"userVerification":"required"`) {
		t.Fatalf("options = %s, want no named credential and user verification required", a.body)
	}

	challenge, err := webauthntest.ChallengeOf([]byte(a.body))
	if err != nil {
		t.Fatalf("challenge of %s: %v", a.body, err)
	}

	return challenge
}

// finishBody returns the body of the end of a sign-in.
func (p passkeySignInClient) finishBody(t *testing.T, c webauthntest.Ceremony, reason string) string {
	t.Helper()

	response, err := p.auth.Assert(p.cred, c)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	body, err := json.Marshal(map[string]any{"response": json.RawMessage(response), "reason": reason})
	if err != nil {
		t.Fatalf("encode the finish body: %v", err)
	}

	return string(body)
}

// finish sends a finish body.
func (p passkeySignInClient) finish(t *testing.T, body string) localAnswer {
	t.Helper()

	return p.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/passkey/finish", body, asBrowser(""))
}

// runPasskeySignInOnStore runs the steps of the passkey sign-in on the server
// of env. A server with a passkey public URL needs the OpenTofu CLI as the CLI
// of env, so the Pulumi CLI runs apart from env, with RunAs.
func runPasskeySignInOnStore(t *testing.T, env *e2e.Env, tofu, pulumi e2e.Install) {
	t.Helper()

	ctx := t.Context()
	user := makePasswordlessUser(t, env)

	auth, cred := env.SeedPasskey(ctx, t, user.PrincipalID, "maintainer")
	p := newPasskeySignInClient(t, env, auth, cred)

	checkPasskeyRefusals(t, p)

	// A good sign-in. The same body works once.
	body := p.finishBody(t, webauthntest.For(p.begin(t)), "")
	p.finish(t, body).want(t, "sign in with the passkey", http.StatusNoContent, "")
	p.finish(t, body).want(t, "replay of the response", http.StatusUnauthorized, "mfa.sign-in-failed")

	csrf := p.readSession(t, user.Subject)

	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	token := p.send(t, http.MethodPost, "/api/server/caller/tokens",
		`{"name":"laptop","expiresAt":"`+expires+`"}`, asBrowser(csrf))
	token.want(t, "make a token with the session of the passkey", http.StatusCreated, "")

	var tok e2eToken
	if err := json.Unmarshal([]byte(token.body), &tok); err != nil || tok.Secret == "" {
		t.Fatalf("token = %s (%v)", token.body, err)
	}

	whoami, err := env.RunAs(ctx, pulumi, t.TempDir(), []string{"PULUMI_ACCESS_TOKEN=" + tok.Secret}, "whoami")
	if err != nil || whoami.ExitCode != 0 || !strings.Contains(whoami.Stdout, user.Subject) {
		t.Fatalf("pulumi whoami with the token = %+v, %v; want the subject %s", whoami, err, user.Subject)
	}

	initRun, err := env.RunTofuWithToken(ctx, t, tofu, tok.Secret, "init", "-input=false")
	if err != nil || initRun.ExitCode != 0 {
		t.Fatalf("tofu init with the token = %+v, %v", initRun, err)
	}
}

// passwordlessUser is a local user that has no password.
type passwordlessUser struct {
	PrincipalID string `json:"principal"`
	Subject     string `json:"subject"`
}

// makePasswordlessUser makes the namespace and the local user pat, who has no
// password, and checks that the password sign-in refuses pat.
func makePasswordlessUser(t *testing.T, env *e2e.Env) passwordlessUser {
	t.Helper()

	status, answer, err := env.ServerAPIStatus(t.Context(), http.MethodPost, "/api/server/namespaces",
		`{"name":"`+e2e.Namespace+`"}`)
	if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
		t.Fatalf("make the namespace = %d, %v: %s", status, err, answer)
	}

	admin := localClient{env: env, browser: env.BrowserClient(t)}

	created := admin.send(t, http.MethodPost, "/api/dustpan/v1/local-users",
		`{"userName":"pat","displayName":"Pat Example","reason":"e2e"}`, asAdmin())
	created.want(t, "create the local user", http.StatusCreated, "")

	var user passwordlessUser
	if err := json.Unmarshal([]byte(created.body), &user); err != nil || user.PrincipalID == "" {
		t.Fatalf("created = %s (%v)", created.body, err)
	}

	// The user has no password, so the password sign-in fails.
	admin.send(t, http.MethodPost, "/api/dustpan/v1/sign-in/password",
		`{"userName":"pat","password":"`+e2ePassword+`"}`, asBrowser("")).
		want(t, "password sign-in of a user with no password", http.StatusUnauthorized, "local-user.sign-in-failed")

	return user
}

// newPasskeySignInClient returns a new browser with the authenticator and the
// credential.
func newPasskeySignInClient(
	t *testing.T, env *e2e.Env, auth *webauthntest.Authenticator, cred *webauthntest.Credential,
) passkeySignInClient {
	t.Helper()

	var p passkeySignInClient

	p.env, p.browser, p.auth, p.cred = env, env.BrowserClient(t), auth, cred

	return p
}

// checkPasskeyRefusals sends hostile responses to the real server. Each gets
// the one answer of a failed sign-in and no session.
func checkPasskeyRefusals(t *testing.T, p passkeySignInClient) {
	t.Helper()

	for _, tc := range []struct {
		name   string
		change func(c webauthntest.Ceremony) webauthntest.Ceremony
	}{
		{"origin of another site", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithOrigin("https://evil.example")
		}},
		{"no user verification", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithFlags(webauthntest.FlagUserPresent)
		}},
		{"user handle of another person", func(c webauthntest.Ceremony) webauthntest.Ceremony {
			return c.WithUserHandle([]byte("prn-other"))
		}},
	} {
		body := p.finishBody(t, tc.change(webauthntest.For(p.begin(t))), "")
		p.finish(t, body).want(t, tc.name, http.StatusUnauthorized, "mfa.sign-in-failed")
		p.send(t, http.MethodGet, "/api/server/session", "", asBrowser("")).
			want(t, "session after "+tc.name, http.StatusUnauthorized, "auth.unauthenticated")
	}
}

// TestBreakGlassPasskey drives the sign-in of a break-glass account with a
// passkey on each store. The account needs a reason. The sign-in raises the
// alert of a break-glass use to the administrators, in the transaction of the
// session, and the audit entry of the session holds the reason.
func TestBreakGlassPasskey(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

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

			runBreakGlassPasskeyOnStore(t, e2e.NewEnvWithPasskeys(t.Context(), t, tofu, db))
		})
	}
}

// runBreakGlassPasskeyOnStore runs the steps of the break-glass passkey
// sign-in on the server of env.
func runBreakGlassPasskeyOnStore(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	// A second account keeps the rule of the last enabled account out of the way.
	ids := env.SeedBreakGlass(ctx, t, "ana", "ben")
	auth, cred := env.SeedPasskey(ctx, t, ids[0], "")
	p := newPasskeySignInClient(t, env, auth, cred)

	// No reason: the sign-in is refused, with no session and no alert.
	missing := p.finishBody(t, webauthntest.For(p.begin(t)), "")
	p.finish(t, missing).want(t, "no reason", http.StatusBadRequest, "mfa.break-glass-reason-required")
	p.send(t, http.MethodGet, "/api/server/session", "", asBrowser("")).
		want(t, "session after a refused break-glass sign-in", http.StatusUnauthorized, "auth.unauthenticated")

	if _, notes := env.ServerRecords(ctx, t); len(breakGlassAlerts(notes)) != 0 {
		t.Fatalf("alerts after a refused sign-in = %+v, want none", notes)
	}

	checkBreakGlassLimitSetting(t, env)

	const reason = "the identity provider is down"

	body := p.finishBody(t, webauthntest.For(p.begin(t)), reason)
	p.finish(t, body).want(t, "break-glass sign-in", http.StatusNoContent, "")
	p.readSession(t, "break-glass:ana")

	entries, notes := env.ServerRecords(ctx, t)

	alerts := breakGlassAlerts(notes)
	if len(alerts) != 1 || alerts[0].Severity != notify.SeverityCritical || len(alerts[0].Recipients) != 1 ||
		!alerts[0].Recipients[0].Admins || alerts[0].Attributes["principal"] != ids[0] ||
		alerts[0].Attributes["session"] == "" || !strings.Contains(alerts[0].Summary, reason) {
		t.Fatalf("alerts = %+v, want one critical alert of the use of %s for the administrators", alerts, ids[0])
	}

	if !hasBreakGlassEntry(entries, reason) {
		t.Fatalf("no sign-in entry of break-glass:ana with the reason %q in %d entries", reason, len(entries))
	}
}

// checkBreakGlassLimitSetting reads the settings resource and checks that it
// shows the effective limit of a break-glass session: 1 hour, from the default.
func checkBreakGlassLimitSetting(t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(t.Context(), http.MethodGet, settingsRoute, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the settings = %d, %v: %s", status, err, body)
	}

	var doc settingsDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the settings: %v", err)
	}

	for i := range doc.Settings {
		if doc.Settings[i].Key == "session.breakGlassMaxLength" {
			if doc.Settings[i].Value != "1h0m0s" || doc.Settings[i].Source != "default" {
				t.Fatalf("break-glass session limit = %+v, want 1h0m0s from the default", doc.Settings[i])
			}

			return
		}
	}

	t.Fatal("the settings resource has no session.breakGlassMaxLength")
}

// breakGlassAlerts returns the alerts of a break-glass use in notes.
func breakGlassAlerts(notes []notify.Notification) []notify.Notification {
	var out []notify.Notification

	for i := range notes {
		if notes[i].Type == notify.BreakGlassUsed {
			out = append(out, notes[i])
		}
	}

	return out
}

// hasBreakGlassEntry reports whether entries holds the successful sign-in of
// the break-glass account ana with the reason.
func hasBreakGlassEntry(entries []audit.Entry, reason string) bool {
	for i := range entries {
		if entries[i].Action == "session:sign-in" && entries[i].Outcome == audit.OutcomeSuccess &&
			entries[i].Subject == "break-glass:ana" && entries[i].Reason == reason {
			return true
		}
	}

	return false
}
