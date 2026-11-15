//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// e2eExportRow and e2eExport decode the rows of an export.
type e2eExportRow struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

type e2eExport struct {
	Grants []e2eExportRow `json:"grants"`
}

// e2eLimitedTokens decodes a token list with the maximum lifetime.
type e2eLimitedTokens struct {
	Tokens             []e2eToken `json:"tokens"`
	MaxLifetimeSeconds int64      `json:"maxLifetimeSeconds"`
}

// e2eRotation decodes the answer of a rotation.
type e2eRotation struct {
	Token    e2eToken `json:"token"`
	Previous e2eToken `json:"previous"`
}

// TestServicePrincipalTokens drives the routes of one service principal
// through the real binary, on each store: its tokens, the grants on it and
// its export. A holder of principal:manage-tokens on the principal revokes
// its tokens with a stored token of its own, and a token of the principal
// follows the grants that the principal gets after the token.
func TestServicePrincipalTokens(t *testing.T) {
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
			runDelegationOnStore(t, cli, db)
		})
	}
}

// delegationFixture holds the records that the delegation steps create.
type delegationFixture struct {
	deploy e2ePrincipal
	other  e2ePrincipal
	ops    e2ePrincipal
	alice  e2ePrincipal
	// opsToken is a personal token of ops, which holds token-manager on
	// deploy only.
	opsToken e2e.PersonalToken
}

// runDelegationOnStore runs the delegation steps on one store.
func runDelegationOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var f delegationFixture

	for _, p := range []struct {
		dst  *e2ePrincipal
		body string
	}{
		{&f.deploy, `{"subject":"ci-deploy","kind":"service"}`},
		{&f.other, `{"subject":"ci-other","kind":"service"}`},
		{&f.ops, `{"subject":"` + e2e.SignInSubject("ops@example.com") + `","kind":"person"}`},
		{&f.alice, `{"subject":"alice@example.com","kind":"person"}`},
	} {
		principalCall(ctx, t, env,
			principalStep{http.MethodPost, "/api/server/principals", p.body, http.StatusCreated}, p.dst)
	}

	grant := checkPrincipalGrants(ctx, t, env, &f)

	f.opsToken = env.PersonalToken(ctx, t, f.ops.ID, "ops")

	checkTokenFollowsGrants(ctx, t, env, &f)
	checkHolderReads(ctx, t, env, &f)
	checkHolderRevokes(ctx, t, env, &f)

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/principals/" + f.deploy.ID + "/grants/" + grant.ID, "", http.StatusNoContent,
	}, nil)
}

// checkPrincipalGrants gives ops the role token-manager on deploy and
// checks the refusals: a grant on a person, a role with an action that is
// not a principal action, and a grant by a principal without
// server:manage-access. The export of deploy then lists ops. It returns
// the grant.
func checkPrincipalGrants(ctx context.Context, t *testing.T, env *e2e.Env, f *delegationFixture) e2eGrant {
	t.Helper()

	deployGrants := "/api/server/principals/" + f.deploy.ID + "/grants"

	for _, step := range []principalStep{
		{
			http.MethodPost, "/api/server/principals/" + f.alice.ID + "/grants",
			`{"subject":"principal:` + f.ops.ID + `","role":"token-manager"}`, http.StatusBadRequest,
		},
		{http.MethodPost, deployGrants, `{"subject":"principal:` + f.ops.ID + `","role":"viewer"}`, http.StatusBadRequest},
	} {
		principalCall(ctx, t, env, step, nil)
	}

	var grant e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, deployGrants, `{"subject":"principal:` + f.ops.ID + `","role":"token-manager"}`,
		http.StatusCreated,
	}, &grant)

	principalCall(ctx, t, env, principalStep{http.MethodGet, deployGrants, "", http.StatusOK}, nil)

	var export e2eExport

	status, body, err := env.ServerAPIStatus(
		ctx,
		http.MethodGet,
		"/api/server/principals/"+f.deploy.ID+"/access-review",
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("export of deploy = %d, %v (%s); want 200", status, err, body)
	}

	if err := json.Unmarshal([]byte(body), &export); err != nil {
		t.Fatalf("decode export: %v", err)
	}

	found := false

	for _, row := range export.Grants {
		found = found || (row.Subject == "principal:"+f.ops.ID && row.Role == "token-manager")
	}

	if !found {
		t.Fatalf("export of deploy = %s, want the grant of token-manager to ops", body)
	}

	return grant
}

// checkTokenFollowsGrants makes a token of deploy on the route of the
// principal, and proves that it follows a grant that deploy gets after it.
// A rotation on the same route issues a new token.
func checkTokenFollowsGrants(ctx context.Context, t *testing.T, env *e2e.Env, f *delegationFixture) {
	t.Helper()

	tokens := "/api/server/principals/" + f.deploy.ID + "/tokens"

	var tok e2eToken
	principalCall(ctx, t, env, principalStep{http.MethodPost, tokens, `{"name":"deploy"}`, http.StatusCreated}, &tok)

	if tok.Kind != "service" || tok.PrincipalID != f.deploy.ID {
		t.Fatalf("token = %+v, want a service token of deploy", tok)
	}

	principalCall(ctx, t, env,
		principalStep{http.MethodPost, "/api/server/namespaces", `{"name":"delegation"}`, http.StatusCreated}, nil)

	read := principalStep{http.MethodGet, "/api/server/namespaces/delegation/grants", "", http.StatusForbidden}
	tokenCall(ctx, t, env, tok.Secret, read)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/delegation/grants",
		`{"subject":"principal:` + f.deploy.ID + `","role":"admin"}`, http.StatusCreated,
	}, nil)

	read.want = http.StatusOK
	tokenCall(ctx, t, env, tok.Secret, read)

	var rot e2eRotation
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, tokens + "/" + tok.ID + "/rotate", `{}`, http.StatusCreated,
	}, &rot)

	if rot.Previous.ID != tok.ID || rot.Token.PrincipalID != f.deploy.ID {
		t.Fatalf("rotation = %+v, want a new token of deploy for %s", rot, tok.ID)
	}
}

// checkHolderReads proves what the token of ops reads: the tokens of
// deploy with the maximum lifetime, the principals it manages, and its own
// token list with the maximum lifetime. The tokens of other answer 403.
func checkHolderReads(ctx context.Context, t *testing.T, env *e2e.Env, f *delegationFixture) {
	t.Helper()

	reads := []struct {
		out  any
		path string
		want int
	}{
		{
			&e2eLimitedTokens{Tokens: nil, MaxLifetimeSeconds: 0},
			"/api/server/principals/" + f.deploy.ID + "/tokens",
			http.StatusOK,
		},
		{nil, "/api/server/principals/" + f.other.ID + "/tokens", http.StatusForbidden},
		{&e2eLimitedTokens{Tokens: nil, MaxLifetimeSeconds: 0}, "/api/server/caller/tokens", http.StatusOK},
	}

	for _, read := range reads {
		status, body, err := env.ServerAPIStatusAs(ctx, f.opsToken.Secret, http.MethodGet, read.path, "")
		if err != nil || status != read.want {
			t.Fatalf("GET %s with the token of ops = %d, %v (%s); want %d", read.path, status, err, body, read.want)
		}

		list, isList := read.out.(*e2eLimitedTokens)
		if !isList {
			continue
		}

		if err := json.Unmarshal([]byte(body), list); err != nil || list.MaxLifetimeSeconds <= 0 {
			t.Fatalf("GET %s = %s, %v; want a list with the maximum lifetime", read.path, body, err)
		}
	}

	checkHolderPrincipals(ctx, t, env, f)
}

// checkHolderPrincipals proves that the token of ops lists deploy, and only
// deploy, as the principals it manages.
func checkHolderPrincipals(ctx context.Context, t *testing.T, env *e2e.Env, f *delegationFixture) {
	t.Helper()

	var managed e2eAccessObjects

	status, body, err := env.ServerAPIStatusAs(ctx, f.opsToken.Secret, http.MethodGet,
		"/api/server/caller/access?type=principal", "")
	if err != nil || status != http.StatusOK || json.Unmarshal([]byte(body), &managed) != nil {
		t.Fatalf("principals that ops manages = %d, %v (%s); want 200", status, err, body)
	}

	if managed.All || len(managed.Objects) != 1 || managed.Objects[0].Object != "principal:"+f.deploy.ID {
		t.Fatalf("principals that ops manages = %s, want only deploy", body)
	}
}

// checkHolderRevokes proves what the token of ops can do: it revokes the
// tokens of deploy on both routes, it cannot make a token (a token comes
// from a sign-in), and it cannot touch the tokens of other, or give
// token-manager without server:manage-access.
func checkHolderRevokes(ctx context.Context, t *testing.T, env *e2e.Env, f *delegationFixture) {
	t.Helper()

	tokens := "/api/server/principals/" + f.deploy.ID + "/tokens"

	var first, second, foreign e2eToken
	principalCall(ctx, t, env, principalStep{http.MethodPost, tokens, `{"name":"first"}`, http.StatusCreated}, &first)
	principalCall(ctx, t, env, principalStep{http.MethodPost, tokens, `{"name":"second"}`, http.StatusCreated}, &second)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + f.other.ID + `","name":"other"}`,
		http.StatusCreated,
	}, &foreign)

	// A token of another principal is not on the route of deploy.
	principalCall(
		ctx,
		t,
		env,
		principalStep{http.MethodDelete, tokens + "/" + foreign.ID, "", http.StatusNotFound},
		nil,
	)

	for _, step := range []principalStep{
		{http.MethodPost, tokens, `{"name":"more"}`, http.StatusForbidden},
		{http.MethodDelete, tokens + "/" + first.ID, "", http.StatusNoContent},
		{http.MethodDelete, "/api/server/tokens/" + second.ID, "", http.StatusNoContent},
		{http.MethodDelete, "/api/server/tokens/" + foreign.ID, "", http.StatusForbidden},
		{http.MethodDelete, "/api/server/principals/" + f.other.ID + "/tokens/" + foreign.ID, "", http.StatusForbidden},
		{
			http.MethodPost, "/api/server/principals/" + f.deploy.ID + "/grants",
			`{"subject":"principal:` + f.alice.ID + `","role":"token-manager"}`, http.StatusForbidden,
		},
	} {
		tokenCall(ctx, t, env, f.opsToken.Secret, step)
	}
}

// tokenCall sends step with the stored token secret and fails unless it
// answers step.want.
func tokenCall(ctx context.Context, t *testing.T, env *e2e.Env, secret string, step principalStep) {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, secret, step.method, step.path, step.body)
	if err != nil || status != step.want {
		t.Fatalf("%s %s with a stored token = %d, %v (%s); want %d",
			step.method, step.path, status, err, body, step.want)
	}
}
