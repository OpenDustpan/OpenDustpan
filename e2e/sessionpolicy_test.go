//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eSessionPolicy decodes the session policy wire form.
type e2eSessionPolicy struct {
	UpdatedAt        *string `json:"updatedAt"`
	IdleSeconds      int64   `json:"idleSeconds"`
	MaxLengthSeconds int64   `json:"maxLengthSeconds"`
}

// TestSessionPolicy drives the session policy routes through the real
// binary, on each store. No CLI calls these routes, so the harness calls
// them with the bootstrap admin token, and with the tokens of an account
// administrator and a site administrator: only the site administrator can
// change the policy.
func TestSessionPolicy(t *testing.T) {
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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runSessionPolicyOnStore(t, cli, db)
		})
	}
}

// runSessionPolicyOnStore runs the session policy steps on one store.
func runSessionPolicyOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var policy e2eSessionPolicy
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/session-policy", "", http.StatusOK}, &policy)

	if policy.UpdatedAt != nil || policy.IdleSeconds != 900 || policy.MaxLengthSeconds != 43200 {
		t.Fatalf("default policy = %+v, want 900 and 43200 with no update time", policy)
	}

	for _, body := range []string{
		`{}`, `{"other":1}`, `{"idleSeconds":60}`, `{"idleSeconds":-1}`, `{"maxLengthSeconds":600}`,
		`{"maxLengthSeconds":604801}`,
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPatch, "/api/server/session-policy", body, http.StatusBadRequest,
		}, nil)
	}

	checkSessionPolicyChange(ctx, t, env)
	checkSessionPolicyRoles(ctx, t, env)
}

// checkSessionPolicyChange changes both limits with the bootstrap admin
// token, and reads the stored policy back.
func checkSessionPolicyChange(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var policy e2eSessionPolicy

	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/session-policy", `{"idleSeconds":1800,"maxLengthSeconds":28800}`,
		http.StatusOK,
	}, &policy)

	if policy.UpdatedAt == nil || policy.IdleSeconds != 1800 || policy.MaxLengthSeconds != 28800 {
		t.Fatalf("changed policy = %+v, want 1800 and 28800 with an update time", policy)
	}

	var read e2eSessionPolicy
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/session-policy", "", http.StatusOK}, &read)

	if read.UpdatedAt == nil || *read.UpdatedAt != *policy.UpdatedAt || read.IdleSeconds != 1800 ||
		read.MaxLengthSeconds != 28800 {
		t.Fatalf("read after change = %+v, want %+v", read, policy)
	}
}

// checkSessionPolicyRoles gives a person the account-administrator role and
// another the site-administrator role on the installation. Both read the
// policy. Only the site administrator changes it: the account administrator
// gets 403.
func checkSessionPolicyRoles(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	steps := []struct {
		subject, role string
		change        int
	}{
		{subject: "acct@example.com", role: "account-administrator", change: http.StatusForbidden},
		{subject: "site@example.com", role: "site-administrator", change: http.StatusOK},
	}

	for _, s := range steps {
		var person e2ePrincipal
		principalCall(ctx, t, env, principalStep{
			http.MethodPost,
			"/api/server/principals",
			`{"subject":"` + e2e.SignInSubject(s.subject) + `","kind":"person"}`,
			http.StatusCreated,
		}, &person)

		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/grants",
			`{"subject":"principal:` + person.ID + `","role":"` + s.role + `"}`, http.StatusCreated,
		}, nil)

		token := env.PersonalToken(ctx, t, person.ID, "session-policy")

		status, body, err := env.ServerAPIStatusAs(ctx, token.Secret, http.MethodGet, "/api/server/session-policy", "")
		if err != nil || status != http.StatusOK {
			t.Fatalf("%s reads the policy = %d, %v (%s); want 200", s.role, status, err, body)
		}

		// A change of the policy needs a fresh authentication: the token of the
		// person is refused, and the person changes it with a console session.
		status, body, err = env.ServerAPIStatusAs(ctx, token.Secret, http.MethodPatch, "/api/server/session-policy",
			`{"idleSeconds":900}`)
		if err != nil || status != http.StatusForbidden ||
			(s.change == http.StatusOK && !strings.Contains(body, "mfa.fresh-step-required")) {
			t.Fatalf(
				"%s changes the policy with a token = %d, %v (%s); want 403, and mfa.fresh-step-required for a role that may change it",
				s.role,
				status,
				err,
				body,
			)
		}

		status, body = env.SessionAPIStatus(ctx, t, person.ID, e2e.SessionRequest{
			Method: http.MethodPatch, Path: "/api/server/session-policy", Body: `{"idleSeconds":900}`,
		})
		if status != s.change {
			t.Fatalf("%s changes the policy with a session = %d (%s); want %d", s.role, status, body, s.change)
		}
	}
}
