//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestCallerRoutes drives the routes of the caller under /api/dustpan/v1/me
// through the real binary, on each store: the access, the grants and the
// own tokens, called with an access token of the caller. The old paths under
// /api/server/caller answer 404. No CLI calls these routes, so the harness
// calls them.
func TestCallerRoutes(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

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
			runCallerRoutesOnStore(t, cli, db)
		})
	}
}

// runCallerRoutesOnStore runs the steps of the caller routes on one store.
func runCallerRoutesOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var person e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("callerroutes") + `","kind":"person"}`, http.StatusCreated,
	}, &person)

	token := env.PersonalToken(ctx, t, person.ID, "caller-routes")

	checkCallerMeRoutes(ctx, t, env, token)
	checkCallerOldPaths(ctx, t, env)
}

// checkCallerMeRoutes calls each route of the caller with the access token
// secret of the caller, and ends the own token.
func checkCallerMeRoutes(ctx context.Context, t *testing.T, env *e2e.Env, token e2e.PersonalToken) {
	t.Helper()

	for _, path := range []string{
		"/api/dustpan/v1/me/access?object=review",
		"/api/dustpan/v1/me/access/objects?type=unit&pageSize=10",
		"/api/dustpan/v1/me/grants?pageSize=10",
	} {
		callerAccessAs(ctx, t, env, token.Secret, path)
	}

	status, body, err := env.ServerAPIStatusAs(
		ctx, token.Secret, http.MethodGet, "/api/dustpan/v1/me/tokens?pageSize=50", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("list own tokens = %d, %v (%s), want 200", status, err, body)
	}

	var list ownTokenList
	if err = json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode own tokens %q: %v", body, err)
	}

	if len(list.Tokens) != 1 || list.Tokens[0].ID != token.ID || list.NextPageToken != "" {
		t.Fatalf("own tokens = %s, want the one token %s", body, token.ID)
	}

	// An access token never gives the fresh authentication that a create
	// needs. The delete ends the token, and the next call answers 401.
	for _, step := range []principalStep{
		{http.MethodPost, "/api/dustpan/v1/me/tokens", `{"name":"refused"}`, http.StatusForbidden},
		{http.MethodDelete, "/api/dustpan/v1/me/tokens/" + token.ID, "", http.StatusNoContent},
		{http.MethodGet, "/api/dustpan/v1/me/access?object=review", "", http.StatusUnauthorized},
	} {
		status, body, err = env.ServerAPIStatusAs(ctx, token.Secret, step.method, step.path, step.body)
		if err != nil || status != step.want {
			t.Fatalf("%s %s = %d, %v (%s), want %d", step.method, step.path, status, err, body, step.want)
		}
	}
}

// ownTokenList is one page of the own tokens of the caller.
type ownTokenList struct {
	Tokens        []e2eTokenID `json:"tokens"`
	NextPageToken string       `json:"nextPageToken"`
}

// e2eTokenID is the ID of one token in a list.
type e2eTokenID struct {
	ID string `json:"id"`
}

// checkCallerOldPaths proves that the old caller paths answer 404 from the
// router, with the bootstrap admin token.
func checkCallerOldPaths(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, path := range []string{
		"/api/server/caller/access?object=review", "/api/server/caller/grants", "/api/server/caller/tokens",
	} {
		principalCall(ctx, t, env, principalStep{http.MethodGet, path, "", http.StatusNotFound}, nil)
	}
}
