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
// namespace holds grants inside its namespace only, and gets no token and no
// group membership.
func TestNamespacePrincipals(t *testing.T) {
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
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	const principals = "/api/server/namespaces/teamns/principals"

	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/namespaces", `{"name":"teamns"}`, http.StatusCreated}, nil)

	var lead, outsider e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"lead@example.com","kind":"person"}`, http.StatusCreated,
	}, &lead)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"outsider@example.com","kind":"person"}`,
		http.StatusCreated,
	}, &outsider)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/teamns/grants",
		`{"subject":"principal:` + lead.ID + `","role":"admin"}`, http.StatusCreated,
	}, nil)

	var leadTok, outsiderTok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/tokens", `{"principalID":"` + lead.ID + `","name":"lead"}`, http.StatusCreated,
	}, &leadTok)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/tokens", `{"principalID":"` + outsider.ID + `","name":"outsider"}`,
		http.StatusCreated,
	}, &outsiderTok)

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
		{
			http.MethodPost, "/api/server/namespaces/teamns/grants",
			`{"subject":"principal:` + bot.ID + `","role":"writer"}`, http.StatusCreated,
		},
	} {
		tokenCall(ctx, t, env, leadTok.Secret, step)
	}

	for _, step := range []principalStep{
		{http.MethodGet, principals, "", http.StatusForbidden},
		{http.MethodPost, principals, `{"subject":"ci-outsider"}`, http.StatusForbidden},
		{http.MethodDelete, one, "", http.StatusForbidden},
	} {
		tokenCall(ctx, t, env, outsiderTok.Secret, step)
	}

	checkNamespacePrincipalLimits(ctx, t, env, bot)

	tokenCall(ctx, t, env, leadTok.Secret, principalStep{http.MethodDelete, one, "", http.StatusNoContent})
	tokenCall(ctx, t, env, leadTok.Secret, principalStep{http.MethodGet, one, "", http.StatusNotFound})
}

// checkNamespacePrincipalLimits proves, with the bootstrap admin token, that
// a principal of a namespace gets no grant outside its namespace, no token
// and no group membership.
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
		{http.MethodPost, "/api/server/principals/" + bot.ID + "/tokens", `{"name":"ci"}`, http.StatusBadRequest},
		{
			http.MethodPost, "/api/server/groups/" + group.ID + "/members", `{"principalID":"` + bot.ID + `"}`,
			http.StatusBadRequest,
		},
	} {
		principalCall(ctx, t, env, step, nil)
	}
}
