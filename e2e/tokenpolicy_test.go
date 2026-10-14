//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// e2eTokenPolicy decodes the token policy wire form.
type e2eTokenPolicy struct {
	UpdatedAt          *string `json:"updatedAt"`
	MaxLifetimeSeconds int64   `json:"maxLifetimeSeconds"`
	InactivitySeconds  int64   `json:"inactivitySeconds"`
	DisableTokens      bool    `json:"disableTokens"`
	DisableAccounts    bool    `json:"disableAccounts"`
	DisableBreakGlass  bool    `json:"disableBreakGlass"`
}

// e2eSweep decodes the answer of a sweep.
type e2eSweep struct {
	Tokens     []string `json:"tokens"`
	Principals []string `json:"principals"`
}

// TestTokenPolicy drives the token policy routes and the inactivity
// disable (ADR 53, decisions 2, 4, 5 and 6) through the real binary, on each
// store. No CLI calls these routes, so the harness calls them with the
// bootstrap admin token. The bootstrap admin token has no stored record, so
// no sweep disables it.
func TestTokenPolicy(t *testing.T) {
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
			runTokenPolicyOnStore(t, cli, db)
		})
	}
}

// runTokenPolicyOnStore runs the token policy steps on one store.
func runTokenPolicyOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)

	var policy e2eTokenPolicy
	principalCall(ctx, t, env, principalStep{http.MethodGet, "/api/server/token-policy", "", http.StatusOK}, &policy)

	want := e2eTokenPolicy{
		UpdatedAt: nil, MaxLifetimeSeconds: 365 * 86400, InactivitySeconds: 90 * 86400,
		DisableTokens: true, DisableAccounts: true, DisableBreakGlass: false,
	}
	if policy != want {
		t.Fatalf("default policy = %+v, want %+v", policy, want)
	}

	for _, body := range []string{
		`{}`, `{"inactivitySeconds":-1}`, `{"inactivitySeconds":1}`, `{"maxLifetimeSeconds":0}`, `{"other":1}`,
	} {
		principalCall(
			ctx,
			t,
			env,
			principalStep{http.MethodPatch, "/api/server/token-policy", body, http.StatusBadRequest},
			nil,
		)
	}

	var service, person e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"ci-deploy","kind":"service"}`, http.StatusCreated,
	}, &service)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"alice@example.com","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	checkBreakGlassRefused(ctx, t, env)
	checkMaxLifetime(ctx, t, env, service)
	checkInactivityPolicy(ctx, t, env, service, person)
}

// checkBreakGlassRefused turns on the disable of the break-glass credential:
// it answers 400 and names ADR 57, because that credential has no stored
// record yet.
func checkBreakGlassRefused(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPatch, "/api/server/token-policy",
		`{"disableBreakGlass":true}`)
	if err != nil || status != http.StatusBadRequest || !strings.Contains(body, "ADR 57") {
		t.Fatalf("break-glass disable = %d, %v (%s); want 400 that names ADR 57", status, err, body)
	}
}

// checkMaxLifetime lowers the maximum lifetime to one hour: a create past it
// answers 400, and a create with no expiry gets one hour.
func checkMaxLifetime(ctx context.Context, t *testing.T, env *e2e.Env, service e2ePrincipal) {
	t.Helper()

	var policy e2eTokenPolicy
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"maxLifetimeSeconds":3600}`, http.StatusOK,
	}, &policy)

	if policy.MaxLifetimeSeconds != 3600 || policy.UpdatedAt == nil || policy.InactivitySeconds != 90*86400 {
		t.Fatalf("changed policy = %+v, want a maximum of 3600 s, an update time and the other defaults", policy)
	}

	later := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens",
		`{"principalID":"` + service.ID + `","name":"long","expiresAt":"` + later + `"}`, http.StatusBadRequest,
	}, nil)

	var tok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + service.ID + `","name":"short"}`,
		http.StatusCreated,
	}, &tok)

	expires, err := time.Parse(time.RFC3339Nano, tok.ExpiresAt)
	if err != nil || time.Until(expires) > time.Hour || time.Until(expires) < 50*time.Minute {
		t.Fatalf("default expiry %q (%v), want about one hour from now", tok.ExpiresAt, err)
	}
}

// checkInactivityPolicy sets the inactivity period to its minimum of one
// hour with the disable on, and runs the sweep: records that were made or
// used inside the hour stay enabled, and their tokens keep signing in. The
// e2e suite cannot make a record older than one hour, so the conformance
// suites of both stores and the service tests cover the disable itself.
// The enable of a principal that is not disabled answers 409.
func checkInactivityPolicy(ctx context.Context, t *testing.T, env *e2e.Env, service, person e2ePrincipal) {
	t.Helper()

	var serviceTok, personTok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + service.ID + `","name":"idle"}`,
		http.StatusCreated,
	}, &serviceTok)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/tokens", `{"principalID":"` + person.ID + `","name":"laptop"}`,
		http.StatusCreated,
	}, &personTok)

	var policy e2eTokenPolicy
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"inactivitySeconds":3600}`, http.StatusOK,
	}, &policy)

	if policy.InactivitySeconds != 3600 || !policy.DisableTokens || !policy.DisableAccounts {
		t.Fatalf("changed policy = %+v, want an inactivity period of 3600 s with both disables on", policy)
	}

	var swept e2eSweep
	principalCall(
		ctx,
		t,
		env,
		principalStep{http.MethodPost, "/api/server/token-policy/sweep", "", http.StatusOK},
		&swept,
	)

	for _, id := range []string{serviceTok.ID, personTok.ID} {
		if slices.Contains(swept.Tokens, id) {
			t.Fatalf("sweep = %+v, disabled the token %s that was made inside the hour", swept, id)
		}
	}

	if slices.Contains(swept.Principals, person.ID) {
		t.Fatalf("sweep = %+v, disabled the person %s that was made inside the hour", swept, person.ID)
	}

	checkTokenSignIn(ctx, t, env, serviceTok.Secret, http.StatusForbidden)
	checkTokenSignIn(ctx, t, env, personTok.Secret, http.StatusForbidden)

	enable := "/api/server/principals/" + person.ID + "/enable"
	principalCall(ctx, t, env, principalStep{http.MethodPost, enable, "", http.StatusConflict}, nil)

	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"inactivitySeconds":0}`, http.StatusOK,
	}, nil)

	var off e2eSweep
	principalCall(
		ctx,
		t,
		env,
		principalStep{http.MethodPost, "/api/server/token-policy/sweep", "", http.StatusOK},
		&off,
	)

	if len(off.Tokens) != 0 || len(off.Principals) != 0 {
		t.Fatalf("sweep with the disable off = %+v, want nothing", off)
	}
}
