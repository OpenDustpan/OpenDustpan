//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// e2eToken decodes the access token wire form. Secret is set only in the
// answer of a create or a rotation.
type e2eToken struct {
	LastUsedAt  *string `json:"lastUsedAt"`
	RevokedAt   *string `json:"revokedAt"`
	ID          string  `json:"id"`
	PrincipalID string  `json:"principalID"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	ExpiresAt   string  `json:"expiresAt"`
	ReplacedBy  string  `json:"replacedBy"`
	Secret      string  `json:"secret"`
	CreatedBy   string  `json:"createdBy"`

	ExpiresInDays *int64 `json:"expiresInDays"`
}

// TestAccessTokens drives every access token route of the installation API
// (ADR 53) through the real binary, on each store, and signs in with the
// tokens that the server issues. No CLI calls these routes, so the harness
// calls them with the bootstrap admin token, which keeps working (ADR 6).
//
// A stored token authenticates a principal that is not an administrator.
// Its grants (ADR 52) decide what it can do: a route answers 403 for an
// action that it does not hold, and 401 for a token that the server refuses.
func TestAccessTokens(t *testing.T) {
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
			runTokensOnStore(t, cli, db)
		})
	}
}

// runTokensOnStore runs the access token steps on one store.
func runTokensOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var deploy, robot e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"ci-deploy","kind":"service"}`, http.StatusCreated,
	}, &deploy)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"spiffe://example.org/ci","kind":"workload"}`,
		http.StatusCreated,
	}, &robot)

	var created e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + deploy.ID + `","name":"deploy"}`,
		http.StatusCreated,
	}, &created)

	if !strings.HasPrefix(created.Secret, "odp_") || len(created.Secret) != 47 || created.Kind != "service" ||
		created.Status != "active" || created.LastUsedAt != nil {
		t.Fatalf("created token = %+v, want an active service token with an odp_ secret and no use", created)
	}

	checkTokenNeedsFreshAuth(ctx, t, env, deploy)
	checkTokenRefusals(ctx, t, env, deploy, robot)
	checkPersonalTokens(ctx, t, env)
	checkDelegatedTokens(ctx, t, env)
	checkNoLockout(ctx, t, env, created.Secret)
	checkTokenSignIn(ctx, t, env, created.Secret, http.StatusForbidden)
	checkTokenReads(ctx, t, env, deploy, created)

	rotated := checkTokenRotation(ctx, t, env, created)

	checkTokenRevoke(ctx, t, env, rotated)

	checkDeletedOwner(ctx, t, env, deploy)

	status, _, err := env.ServerAPIStatusBare(ctx, http.MethodGet, "/api/server/tokens")
	if err != nil || status != http.StatusUnauthorized {
		t.Fatalf("list without a token = %d, %v; want 401", status, err)
	}
}

// checkTokenNeedsFreshAuth proves that an access token never makes a token:
// the bootstrap admin token creates a service token and a personal token, and
// both answer 403 with mfa.fresh-step-required. The token list does not change.
func checkTokenNeedsFreshAuth(ctx context.Context, t *testing.T, env *e2e.Env, deploy e2ePrincipal) {
	t.Helper()

	_, before, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/tokens", "")
	if err != nil {
		t.Fatalf("list the tokens: %v", err)
	}

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + deploy.ID + `","name":"refused"}`, 0},
		{http.MethodPost, "/api/server/principals/" + deploy.ID + "/tokens", `{"name":"refused"}`, 0},
		{http.MethodPost, "/api/server/caller/tokens", `{"name":"refused"}`, 0},
	} {
		status, body, callErr := env.ServerAPIStatus(ctx, step.method, step.path, step.body)
		if callErr != nil || status != http.StatusForbidden ||
			!strings.Contains(body, `"errorCode":"mfa.fresh-step-required"`) {
			t.Fatalf("%s %s with the bootstrap admin token = %d, %v (%s); want 403 with mfa.fresh-step-required",
				step.method, step.path, status, callErr, body)
		}
	}

	_, after, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/tokens", "")
	if err != nil || after != before {
		t.Fatalf("token list after the refused changes = %s, %v; want the list from before: %s", after, err, before)
	}
}

// checkTokenSignIn signs in to the installation API with secret and fails
// unless the request answers want: 403 for a token that authenticates a
// principal with no grant, 401 for a token that the server refuses.
func checkTokenSignIn(ctx context.Context, t *testing.T, env *e2e.Env, secret string, want int) {
	t.Helper()

	status, body, err := env.ServerAPIStatusAs(ctx, secret, http.MethodGet, "/api/server/principals", "")
	if err != nil {
		t.Fatalf("sign in with a token: %v", err)
	}

	if status != want {
		t.Fatalf("sign in with a token answers %d, want %d (%s)", status, want, body)
	}
}

// checkTokenRefusals sends creates that the server refuses: a workload, a
// missing principal, a bad body and an expiry out of range.
func checkTokenRefusals(ctx context.Context, t *testing.T, env *e2e.Env, deploy, robot e2ePrincipal) {
	t.Helper()

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	far := time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339)

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + robot.ID + `","name":"x"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/server/service-tokens", `{"principalID":"prn-none","name":"x"}`, http.StatusNotFound},
		{http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + deploy.ID + `"}`, http.StatusBadRequest},
		{
			http.MethodPost, "/api/server/service-tokens",
			`{"principalID":"` + deploy.ID + `","name":"x","expiresAt":"` + past + `"}`, http.StatusBadRequest,
		},
		{
			http.MethodPost, "/api/server/service-tokens",
			`{"principalID":"` + deploy.ID + `","name":"x","expiresAt":"` + far + `"}`, http.StatusBadRequest,
		},
		// No route makes or rotates a personal token of another person.
		{http.MethodPost, "/api/server/tokens", `{"principalID":"` + deploy.ID + `","name":"x"}`, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/server/tokens/tok-none/rotate", `{}`, http.StatusNotFound},
		{http.MethodGet, "/api/server/tokens?limit=0", "", http.StatusBadRequest},
		{http.MethodGet, "/api/server/tokens?status=all", "", http.StatusBadRequest},
		{http.MethodGet, "/api/server/tokens/tok-none", "", http.StatusNotFound},
	} {
		principalCall(ctx, t, env, step, nil)
	}

	checkTokenSignIn(ctx, t, env, "odp_"+strings.Repeat("A", 43), http.StatusUnauthorized)
}

// checkTokenReads reads the token back, alone and in the list: no answer
// holds the secret, and the sign-in recorded a last-used time.
func checkTokenReads(ctx context.Context, t *testing.T, env *e2e.Env, deploy e2ePrincipal, created e2eToken) {
	t.Helper()

	var got e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/tokens/" + created.ID, "", http.StatusOK,
	}, &got)

	if got.Secret != "" || got.LastUsedAt == nil || got.Name != "deploy" {
		t.Fatalf("read token = %+v, want no secret and a last-used time", got)
	}

	type page struct {
		NextCursor string     `json:"nextCursor"`
		Tokens     []e2eToken `json:"tokens"`
		Total      int64      `json:"total"`
	}

	var list page
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/tokens?principal=" + deploy.ID + "&limit=1", "", http.StatusOK,
	}, &list)

	if len(list.Tokens) != 1 || list.Total != 1 || list.NextCursor != "" || list.Tokens[0].Secret != "" {
		t.Fatalf("token list = %+v, want one token, a total of 1, no cursor and no secret", list)
	}

	_, raw, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/tokens", "")
	if err != nil || strings.Contains(raw, created.Secret) {
		t.Fatalf("token list holds the secret, or failed: %v", err)
	}
}

// oldTokenEndMargin is the time from the second rotation to the end time
// of its old token. It is longer than a slow request on a loaded machine, so
// the end time is never in the past when the server reads it.
const oldTokenEndMargin = 10 * time.Second

// checkTokenRotation rotates the token with no end time: both tokens sign
// in during the overlap. It answers the new token.
func checkTokenRotation(ctx context.Context, t *testing.T, env *e2e.Env, created e2eToken) e2eToken {
	t.Helper()

	var rot struct {
		Token    e2eToken `json:"token"`
		Previous e2eToken `json:"previous"`
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens/" + created.ID + "/rotate", `{}`, http.StatusCreated,
	}, &rot)

	if rot.Previous.ReplacedBy != rot.Token.ID || !strings.HasPrefix(rot.Token.Secret, "odp_") ||
		rot.Token.Secret == created.Secret || rot.Previous.Secret != "" {
		t.Fatalf("rotation = %+v, want a new secret and the old token replaced by the new one", rot)
	}

	checkTokenSignIn(ctx, t, env, created.Secret, http.StatusForbidden)
	checkTokenSignIn(ctx, t, env, rot.Token.Secret, http.StatusForbidden)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens/" + created.ID + "/rotate", `{}`, http.StatusConflict,
	}, nil)

	// An end time soon after now ends the old token of a second rotation. The
	// sign-in of the session can wait for a penalty, so it runs first.
	send := env.FreshAdminSession(ctx, t)
	endsAt := time.Now().Add(oldTokenEndMargin).UTC()

	var second struct {
		Token e2eToken `json:"token"`
	}

	status, body := send(e2e.SessionRequest{
		Method: http.MethodPost, Path: "/api/server/service-tokens/" + rot.Token.ID + "/rotate",
		Body: `{"oldTokenEndsAt":"` + endsAt.Format(time.RFC3339Nano) + `"}`,
	})
	if status != http.StatusCreated {
		t.Fatalf("second rotation answers %d, want 201 (%s)", status, body)
	}

	if err := json.Unmarshal([]byte(body), &second); err != nil {
		t.Fatalf("decode the second rotation %q: %v", body, err)
	}

	// The old token signs in until its end time.
	checkTokenSignIn(ctx, t, env, rot.Token.Secret, http.StatusForbidden)

	waitTokenRefused(ctx, t, env, rot.Token.Secret, endsAt)
	checkTokenSignIn(ctx, t, env, second.Token.Secret, http.StatusForbidden)

	return second.Token
}

// waitTokenRefused waits until the server refuses secret with 401, for a
// token whose end time is endsAt. It waits until endsAt, then polls every 250
// milliseconds for at most 10 seconds, so the failed sign-ins stay inside the
// budget of the authentication gate.
func waitTokenRefused(ctx context.Context, t *testing.T, env *e2e.Env, secret string, endsAt time.Time) {
	t.Helper()

	wait := time.NewTimer(time.Until(endsAt))
	select {
	case <-ctx.Done():
		wait.Stop()
		t.Fatal("test ended while waiting for the end time of the old token")
	case <-wait.C:
	}

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		status, _, err := env.ServerAPIStatusAs(ctx, secret, http.MethodGet, "/api/server/principals", "")
		if err == nil && status == http.StatusUnauthorized {
			return
		}

		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("test ended while waiting for the server to refuse the old token")
		case <-timer.C:
		}
	}

	t.Fatal("the server never refused the old token after its end time")
}

// checkDeletedOwner deletes the principal deploy: it can no longer sign in
// with its tokens.
func checkDeletedOwner(ctx context.Context, t *testing.T, env *e2e.Env, deploy e2ePrincipal) {
	t.Helper()

	var other e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + deploy.ID + `","name":"other"}`,
		http.StatusCreated,
	}, &other)
	checkTokenSignIn(ctx, t, env, other.Secret, http.StatusForbidden)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/principals/" + deploy.ID, "", http.StatusNoContent,
	}, nil)
	checkTokenSignIn(ctx, t, env, other.Secret, http.StatusUnauthorized)
}

// checkTokenRevoke revokes tok: it no longer signs in, a second revoke
// answers 409, and the record stays with its revoke time.
func checkTokenRevoke(ctx context.Context, t *testing.T, env *e2e.Env, tok e2eToken) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/tokens/" + tok.ID, "", http.StatusNoContent,
	}, nil)
	principalCall(ctx, t, env, principalStep{
		http.MethodDelete, "/api/server/tokens/" + tok.ID, "", http.StatusConflict,
	}, nil)
	checkTokenSignIn(ctx, t, env, tok.Secret, http.StatusUnauthorized)

	var revoked e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/tokens/" + tok.ID, "", http.StatusOK,
	}, &revoked)

	if revoked.Status != "revoked" || revoked.RevokedAt == nil || revoked.Secret != "" {
		t.Fatalf("revoked token = %+v, want the status revoked, a revoke time and no secret", revoked)
	}
}

// checkPersonalTokens signs a person in twice, and makes a personal token
// with each session, and drives the own-token routes. Every authenticated
// person holds token:manage-own. The bootstrap admin token has no person
// record, so it owns no personal token (403). The person lists its own
// tokens with a token and revokes one, and it cannot create or rotate one
// with a token: that needs a sign-in (403).
func checkPersonalTokens(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var person e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("bob@example.com") + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	personal := env.PersonalToken(ctx, t, person.ID, "laptop")
	spare := env.PersonalToken(ctx, t, person.ID, "laptop")

	var read e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodGet, "/api/server/tokens/" + personal.ID, "", http.StatusOK,
	}, &read)

	if read.Kind != "personal" || read.PrincipalID != person.ID ||
		read.CreatedBy != e2e.SignInSubject("bob@example.com") ||
		read.ExpiresInDays == nil {
		t.Fatalf("personal token = %+v, want a personal token of the person, made by the person, with days left", read)
	}

	// The service-token route answers the opaque 403 for a token of another
	// kind, so a caller cannot learn whether the token exists.
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens/" + personal.ID + "/rotate", `{}`, http.StatusForbidden,
	}, nil)

	own := "/api/server/caller/tokens"
	for _, step := range []principalStep{
		{http.MethodGet, own, "", http.StatusForbidden},
		{http.MethodPost, own, `{"name":"mine"}`, http.StatusForbidden},
	} {
		principalCallWithToken(ctx, t, env, step, nil)
	}

	for _, step := range []struct {
		method, path, body, total string
		want                      int
	}{
		{http.MethodGet, own + "?status=active", "", `"total":2`, http.StatusOK},
		{http.MethodPost, own, `{"name":"mine"}`, "", http.StatusForbidden},
		{http.MethodPost, own + "/" + personal.ID + "/rotate", `{}`, "", http.StatusForbidden},
		{http.MethodDelete, own + "/" + spare.ID, "", "", http.StatusNoContent},
		{http.MethodGet, own + "?status=ended", "", `"total":1`, http.StatusOK},
	} {
		status, body, err := env.ServerAPIStatusAs(ctx, personal.Secret, step.method, step.path, step.body)
		if err != nil || status != step.want {
			t.Fatalf("%s %s with the person's token = %d, %v (%s); want %d",
				step.method, step.path, status, err, body, step.want)
		}

		if !strings.Contains(body, step.total) {
			t.Fatalf("%s %s = %s, want %s", step.method, step.path, body, step.total)
		}
	}
}

// checkNoLockout spends the failure budget of the address of the harness
// with 35 unknown secret-shaped tokens. The misses over the budget wait a
// penalty and answer 401, and still the bootstrap admin token and a valid
// stored token pass: an attacker behind a shared address locks out nobody.
func checkNoLockout(ctx context.Context, t *testing.T, env *e2e.Env, valid string) {
	t.Helper()

	for i := range 35 {
		bogus := fmt.Sprintf("odp_%043d", i)

		status, _, err := env.ServerAPIStatusAs(ctx, bogus, http.MethodGet, "/api/server/principals", "")
		if err != nil || status != http.StatusUnauthorized {
			t.Fatalf("unknown token %d = %d, %v; want 401", i, status, err)
		}
	}

	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/principals", "", http.StatusOK}, nil)
	checkTokenSignIn(ctx, t, env, valid, http.StatusForbidden)
}

// checkDelegatedTokens gives a person that is not an administrator the role
// account-administrator, which holds server:manage-tokens and
// server:manage-service-tokens, and a personal token. With that token, it
// cannot make or rotate any token: a token comes from a sign-in (ADR 53,
// decision 10), and a token that could make tokens would outlive a revoke.
// The bootstrap admin token is not a stored token, so it still can.
func checkDelegatedTokens(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	var delegate, plain e2ePrincipal

	for _, p := range []struct {
		dst  *e2ePrincipal
		body string
	}{
		{&delegate, `{"subject":"` + e2e.SignInSubject("delegate@example.com") + `","kind":"person"}`},
		{&plain, `{"subject":"ci-plain","kind":"service"}`},
	} {
		principalCall(
			ctx,
			t,
			env,
			principalStep{http.MethodPost, "/api/server/principals", p.body, http.StatusCreated},
			p.dst,
		)
	}

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/grants",
		`{"subject":"principal:` + delegate.ID + `","role":"account-administrator"}`,
		http.StatusCreated,
	}, nil)

	own := env.PersonalToken(ctx, t, delegate.ID, "delegate")

	var plainTok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + plain.ID + `","name":"plain"}`,
		http.StatusCreated,
	}, &plainTok)

	for _, step := range []principalStep{
		{http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + plain.ID + `","name":"plain"}`, http.StatusForbidden},
		{http.MethodPost, "/api/server/service-tokens/" + plainTok.ID + "/rotate", `{}`, http.StatusForbidden},
	} {
		status, body, err := env.ServerAPIStatusAs(ctx, own.Secret, step.method, step.path, step.body)
		if err != nil || status != step.want {
			t.Fatalf("%s %s with the token of the delegate = %d, %v (%s); want %d",
				step.method, step.path, status, err, body, step.want)
		}
	}

	// The token of the delegate still reads the tokens: only a create and
	// a rotation need a sign-in.
	checkTokenSignIn(ctx, t, env, own.Secret, http.StatusOK)

	// Without a stored token, the same create succeeds.
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + plain.ID + `","name":"admin"}`,
		http.StatusCreated,
	}, nil)
}
