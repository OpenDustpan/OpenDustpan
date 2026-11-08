//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestNamespacePrincipals drives the routes of the service principals of a
// namespace through the real binary, on each store. A namespace admin makes
// and manages them with a stored token of its own. A principal of a
// namespace holds grants inside its namespace only, and joins no group. A
// namespace admin manages the tokens of its principals.
func TestNamespacePrincipals(t *testing.T) {
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
			runNamespacePrincipalsOnStore(t, cli, db)
		})
	}
}

// runNamespacePrincipalsOnStore runs the steps on one store.
func runNamespacePrincipalsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	const principals = "/api/server/namespaces/teamns/principals"

	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/namespaces", `{"name":"teamns"}`, http.StatusCreated}, nil)

	var lead, outsider e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("lead@example.com") + `","kind":"person"}`,
		http.StatusCreated,
	}, &lead)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("outsider@example.com") + `","kind":"person"}`,
		http.StatusCreated,
	}, &outsider)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/teamns/grants",
		`{"subject":"principal:` + lead.ID + `","role":"admin"}`, http.StatusCreated,
	}, nil)

	leadTok := env.PersonalToken(ctx, t, lead.ID, "lead")
	outsiderTok := env.PersonalToken(ctx, t, outsider.ID, "outsider")

	var bot e2ePrincipal

	status, body, err := env.ServerAPIStatusAs(
		ctx,
		leadTok.Secret,
		http.MethodPost,
		principals,
		`{"subject":"ci-teamns"}`,
	)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create a namespace principal = %d, %v (%s); want 201", status, err, body)
	}

	if err := json.Unmarshal([]byte(body), &bot); err != nil {
		t.Fatalf("decode principal: %v", err)
	}

	if bot.Kind != "service" {
		t.Fatalf("namespace principal = %+v, want a service principal", bot)
	}

	one := principals + "/" + bot.ID

	for _, step := range []principalStep{
		{http.MethodGet, principals, "", http.StatusOK},
		{http.MethodGet, one, "", http.StatusOK},
		{http.MethodPatch, one, `{"displayName":"Team bot"}`, http.StatusOK},
		// A grant needs a console session with a recent sign-in: the stored
		// token of the admin is refused.
		{
			http.MethodPost, "/api/server/namespaces/teamns/grants",
			`{"subject":"principal:` + bot.ID + `","role":"writer"}`, http.StatusForbidden,
		},
	} {
		tokenCall(ctx, t, env, leadTok.Secret, step)
	}

	status, body = env.SessionAPIStatus(ctx, t, lead.ID, e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/namespaces/teamns/grants",
		Body: `{"subject":"principal:` + bot.ID + `","role":"writer"}`,
	})
	if status != http.StatusCreated {
		t.Fatalf("grant writer with the session of the admin = %d (%s); want 201", status, body)
	}

	for _, step := range []principalStep{
		{http.MethodGet, principals, "", http.StatusForbidden},
		{http.MethodPost, principals, `{"subject":"ci-outsider"}`, http.StatusForbidden},
		{http.MethodDelete, one, "", http.StatusForbidden},
	} {
		tokenCall(ctx, t, env, outsiderTok.Secret, step)
	}

	checkNamespacePrincipalLimits(ctx, t, env, bot)
	checkNamespacePrincipalTokens(ctx, t, env, namespaceTokenFixture{lead: leadTok, outsider: outsiderTok, bot: bot})
	checkNamespacePrincipalReview(ctx, t, env, lead, bot)

	tokenCall(ctx, t, env, leadTok.Secret, principalStep{http.MethodDelete, one, "", http.StatusNoContent})
	tokenCall(ctx, t, env, leadTok.Secret, principalStep{http.MethodGet, one, "", http.StatusNotFound})
}

// checkNamespacePrincipalLimits proves, with the bootstrap admin token, that
// a principal of a namespace gets no grant outside its namespace and no
// group membership.
func checkNamespacePrincipalLimits(ctx context.Context, t *testing.T, env *e2e.Env, bot e2ePrincipal) {
	t.Helper()

	var group e2eGroup
	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/groups", `{"name":"teamns-ops"}`, http.StatusCreated}, &group)

	for _, step := range []principalStep{
		{
			http.MethodPost, "/api/server/grants", `{"subject":"principal:` + bot.ID + `","role":"viewer"}`,
			http.StatusBadRequest,
		},
		{
			http.MethodPost, "/api/server/groups/" + group.ID + "/members", `{"principalID":"` + bot.ID + `"}`,
			http.StatusBadRequest,
		},
	} {
		principalCall(ctx, t, env, step, nil)
	}
}

// checkNamespacePrincipalReview proves that the access-review export of a
// principal of a namespace lists the grant of the namespace admin on the
// namespace to the lead.
func checkNamespacePrincipalReview(
	ctx context.Context, t *testing.T, env *e2e.Env, lead, bot e2ePrincipal,
) {
	t.Helper()

	var export e2eExport

	status, body, err := env.ServerAPIStatus(
		ctx, http.MethodGet, "/api/server/principals/"+bot.ID+"/access-review", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("export of the namespace principal = %d, %v (%s); want 200", status, err, body)
	}

	if err := json.Unmarshal([]byte(body), &export); err != nil {
		t.Fatalf("decode export: %v", err)
	}

	for _, row := range export.Grants {
		if row.Subject == "principal:"+lead.ID && row.Role == "admin" {
			return
		}
	}

	t.Fatalf("export of the namespace principal = %s, want the grant of admin to the lead", body)
}

// namespaceTokenFixture holds the stored tokens and the bot of the token
// test.
type namespaceTokenFixture struct {
	lead     e2e.PersonalToken
	outsider e2e.PersonalToken
	bot      e2ePrincipal
}

// checkNamespacePrincipalTokens proves that a namespace admin manages the
// tokens of its principals: the bootstrap admin token makes a token of the
// bot, the stored token of the admin lists and revokes it, a rotation
// issues a new one, and the bot token acts as the bot. The same admin gets
// 403 on a principal of another namespace and on a principal of the
// installation. A create and a rotation need a sign-in, so the bootstrap
// admin token makes them; a stored token lists and revokes.
func checkNamespacePrincipalTokens(ctx context.Context, t *testing.T, env *e2e.Env, fx namespaceTokenFixture) {
	t.Helper()

	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/namespaces", `{"name":"otherns"}`, http.StatusCreated}, nil)

	var other e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"ci-install","kind":"service"}`, http.StatusCreated,
	}, &other)

	otherBotID := other.ID

	var otherNsBot e2ePrincipal
	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/namespaces/otherns/principals",
		`{"subject":"ci-otherns"}`)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create a principal of otherns = %d, %v (%s); want 201", status, err, body)
	}

	if unmarshalErr := json.Unmarshal([]byte(body), &otherNsBot); unmarshalErr != nil {
		t.Fatalf("decode principal: %v", unmarshalErr)
	}

	botTokens := "/api/server/principals/" + fx.bot.ID + "/tokens"

	var made e2eToken
	principalCall(ctx, t, env, principalStep{http.MethodPost, botTokens, `{"name":"ci"}`, http.StatusCreated}, &made)

	if made.PrincipalID != fx.bot.ID || made.Kind != "service" {
		t.Fatalf("token = %+v, want a service token of the bot", made)
	}

	tokenCall(ctx, t, env, fx.lead.Secret, principalStep{http.MethodGet, botTokens, "", http.StatusOK})
	tokenCall(ctx, t, env, fx.outsider.Secret, principalStep{http.MethodGet, botTokens, "", http.StatusForbidden})

	outsiderPost := principalStep{http.MethodPost, botTokens, `{"name":"x"}`, http.StatusForbidden}
	tokenCall(ctx, t, env, fx.outsider.Secret, outsiderPost)

	for _, path := range []string{
		"/api/server/principals/" + otherNsBot.ID + "/tokens",
		"/api/server/principals/" + otherBotID + "/tokens",
	} {
		tokenCall(ctx, t, env, fx.lead.Secret, principalStep{http.MethodGet, path, "", http.StatusForbidden})
		tokenCall(
			ctx,
			t,
			env,
			fx.lead.Secret,
			principalStep{http.MethodPost, path, `{"name":"x"}`, http.StatusForbidden},
		)
	}

	var rot e2eRotation
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, botTokens + "/" + made.ID + "/rotate", `{}`, http.StatusCreated,
	}, &rot)

	if rot.Previous.ID != made.ID || rot.Token.PrincipalID != fx.bot.ID {
		t.Fatalf("rotation = %+v, want a new token of the bot", rot)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/teamns/grants",
		`{"subject":"principal:` + fx.bot.ID + `","role":"admin"}`, http.StatusCreated,
	}, nil)

	status, body, err = env.ServerAPIStatusAs(ctx, rot.Token.Secret, http.MethodGet,
		"/api/server/namespaces/teamns/principals", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET principals with the bot token = %d, %v (%s); want 200", status, err, body)
	}

	tokenCall(ctx, t, env, fx.lead.Secret,
		principalStep{http.MethodDelete, botTokens + "/" + rot.Token.ID, "", http.StatusNoContent})
}
