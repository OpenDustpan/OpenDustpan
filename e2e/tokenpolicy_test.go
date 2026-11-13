//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
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

// e2eAuditChange decodes one change of an audit entry.
type e2eAuditChange struct {
	Key string  `json:"key"`
	Old *string `json:"old,omitempty"`
	New string  `json:"new"`
}

// e2ePolicyAuditEntry decodes the fields of an audit entry that the policy
// check needs.
type e2ePolicyAuditEntry struct {
	Action  string           `json:"action"`
	Object  string           `json:"object"`
	Changes []e2eAuditChange `json:"changes,omitempty"`
}

// TestTokenPolicy drives the token policy routes and the inactivity
// disable (ADR 53, decisions 2, 4, 5 and 6) through the real binary, on each
// store. No CLI calls these routes, so the harness calls them with the
// bootstrap admin token. The bootstrap admin token has no stored record, so
// no sweep disables it.
func TestTokenPolicy(t *testing.T) {
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
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

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
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject("alice@example.com") + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	checkBreakGlassRefused(ctx, t, env)
	checkMaxLifetime(ctx, t, env, service)
	checkInactivityPolicy(ctx, t, env, service, person)
}

// checkBreakGlassRefused turns on the deprecated disable of the break-glass
// account: it answers 400 that says the setting is no longer supported,
// because a break-glass account is always exempt. False is accepted and
// always reads back as false.
func checkBreakGlassRefused(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body := freshServerCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"disableBreakGlass":true}`, http.StatusBadRequest,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "no longer supported") {
		t.Fatalf("break-glass disable = %d (%s); want 400 that says no longer supported", status, body)
	}

	var policy e2eTokenPolicy
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"disableBreakGlass":false}`, http.StatusOK,
	}, &policy)

	if policy.DisableBreakGlass {
		t.Fatalf("policy with the deprecated field false = %+v, want false", policy)
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

	checkPolicyAudit(ctx, t, env, map[string][2]string{"maxLifetimeSeconds": {"31536000", "3600"}})

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
// The enable of a principal that is not disabled answers 409. The disable and
// the enable of a principal answer 200.
func checkInactivityPolicy(ctx context.Context, t *testing.T, env *e2e.Env, service, person e2ePrincipal) {
	t.Helper()

	var serviceTok e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/service-tokens", `{"principalID":"` + service.ID + `","name":"idle"}`,
		http.StatusCreated,
	}, &serviceTok)

	personTok := env.PersonalToken(ctx, t, person.ID, "laptop")

	var policy e2eTokenPolicy
	principalCall(ctx, t, env, principalStep{
		http.MethodPatch, "/api/server/token-policy", `{"inactivitySeconds":3600}`, http.StatusOK,
	}, &policy)

	if policy.InactivitySeconds != 3600 || !policy.DisableTokens || !policy.DisableAccounts {
		t.Fatalf("changed policy = %+v, want an inactivity period of 3600 s with both disables on", policy)
	}

	checkPolicyAudit(ctx, t, env, map[string][2]string{"inactivitySeconds": {"7776000", "3600"}})

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

	// The disable answers 200 with the principal, a second disable answers
	// 409, and the enable then answers 200.
	disable := "/api/server/principals/" + person.ID + "/disable"
	principalCall(ctx, t, env, principalStep{http.MethodPost, disable, "", http.StatusOK}, nil)
	principalCall(ctx, t, env, principalStep{http.MethodPost, disable, "", http.StatusConflict}, nil)
	principalCall(ctx, t, env, principalStep{http.MethodPost, enable, "", http.StatusOK}, nil)

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

// checkPolicyAudit proves the latest token policy entry in @server holds
// exactly the changed keys of want with their old and new text.
func checkPolicyAudit(ctx context.Context, t *testing.T, env *e2e.Env, want map[string][2]string) {
	t.Helper()

	entries := listServerAuditEntries(ctx, t, env)

	for i := range entries {
		if entries[i].Object != "token-policy" {
			continue
		}

		checkAuditChanges(t, entries[i].Changes, want)

		return
	}

	t.Fatal("no token-policy entry in @server")
}

// listServerAuditEntries lists the audit entries of @server through the API.
func listServerAuditEntries(ctx context.Context, t *testing.T, env *e2e.Env) []e2ePolicyAuditEntry {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/audit/entries?organization=@server", "")
	if err != nil {
		t.Fatalf("list @server audit entries: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list @server audit entries answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var got struct {
		Entries []e2ePolicyAuditEntry `json:"entries"`
		Total   int64                 `json:"total"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode @server audit entries: %v (%s)", err, body)
	}

	return got.Entries
}

// checkAuditChanges proves changes hold exactly the keys of want with their
// old and new text.
func checkAuditChanges(t *testing.T, changes []e2eAuditChange, want map[string][2]string) {
	t.Helper()

	// A change from a console session also names the session.
	changes = slices.DeleteFunc(slices.Clone(changes), func(c e2eAuditChange) bool { return c.Key == "sessionId" })

	if len(changes) != len(want) {
		t.Fatalf("policy changes hold %d items %+v, want %d", len(changes), changes, len(want))
	}

	for _, c := range changes {
		pair, ok := want[c.Key]
		if !ok {
			t.Fatalf("policy change %q is not one of %v", c.Key, want)
		}

		if c.Old == nil || *c.Old != pair[0] || c.New != pair[1] {
			t.Fatalf("policy change %q = %v to %q, want %q to %q", c.Key, c.Old, c.New, pair[0], pair[1])
		}
	}
}
