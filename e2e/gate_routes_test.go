//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// gateRoutesNamespace is the namespace that the gate route steps use.
const gateRoutesNamespace = "gateroutes"

const (
	gateRoutesGate = `{"decisionPoint":"state.write","condition":"context.operation.kind == \"lock\"",` +
		`"outcome":"deny","message":"Locks are frozen","mode":"enforce"}`
	gateRoutesLimit = `{"policy":"forbid (principal, action == OpenDustpan::Action::\"unit:write\", resource);"}`
)

// TestGateRoutes proves the gate and access limit routes of a namespace, a
// collection and a unit through the real server on each store: no CLI sets a
// gate, so the harness calls the routes. For each scope level and each kind,
// the steps make, list, read, replace and delete one object, and then send
// the requests that the server must refuse.
func TestGateRoutes(t *testing.T) {
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
			runGateRoutesOnStore(t, cli, db)
		})
	}
}

func runGateRoutesOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithRealTimeSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	gateRoutesCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + gateRoutesNamespace + `"}`, http.StatusCreated,
	})

	env.SeedCatchAllPlacement(ctx, t, gateRoutesNamespace)

	// The gates of a unit belong to the unit, so the stack of the unit scope
	// exists before the first write.
	status, body, err := env.APIStatus(ctx, http.MethodPost, "/api/stacks/"+gateRoutesNamespace+"/web",
		`{"stackName":"prod"}`)
	if err != nil || status != http.StatusOK {
		t.Fatalf("create the stack web/prod = %d, %v (%s)", status, err, body)
	}

	base := "/api/dustpan/v1/namespaces/" + gateRoutesNamespace
	scopes := map[string]string{
		"namespace":  base,
		"collection": base + "/collections/payments",
		"unit":       base + "/units/by-name/pulumi/" + url.PathEscape("web/prod"),
	}

	for level, scope := range scopes {
		gateRoutesLifecycle(ctx, t, env, gateRoutesCase{level: level, list: scope + "/gates", doc: gateRoutesGate})
		gateRoutesLifecycle(ctx, t, env, gateRoutesCase{
			level: level, list: scope + "/access-limits", doc: gateRoutesLimit,
		})
	}

	gateRoutesRefusals(ctx, t, env, base)
	gateRoutesFinal(ctx, t, env, base)
	gateRoutesTokenWrites(ctx, t, env, base)
}

// gateRoutesCall sends s and fails unless it answers s.want. It answers the
// body. A write signs in as the site administrator, because an access token
// never gives fresh authentication.
func gateRoutesCall(ctx context.Context, t *testing.T, env *e2e.Env, s principalStep) string {
	t.Helper()

	status, body := freshServerCall(ctx, t, env, s)
	if status != s.want {
		t.Fatalf("%s %s answers %d, want %d (%s)", s.method, s.path, status, s.want, body)
	}

	return body
}

// gateRoutesCase is one kind of object at one scope level: the list path and
// the request body that makes an object.
type gateRoutesCase struct {
	level, list, doc string
}

// gateRoutesLifecycle makes, lists, reads, replaces and deletes one object.
func gateRoutesLifecycle(ctx context.Context, t *testing.T, env *e2e.Env, c gateRoutesCase) {
	t.Helper()

	level, list, doc := c.level, c.list, c.doc

	item := list + "/freeze"
	step := func(method, path, body string, want int) string {
		t.Helper()

		return gateRoutesCall(ctx, t, env, principalStep{method, path, body, want})
	}

	if body := step(http.MethodGet, list, "", http.StatusOK); strings.Contains(body, `"freeze"`) {
		t.Fatalf("%s list before the write holds the object: %s", level, body)
	}

	step(http.MethodGet, item, "", http.StatusNotFound)

	body := step(http.MethodPut, item, doc, http.StatusOK)
	wantBodyHas(t, level+" set", body, `"id":"freeze"`, `"version":1`)

	wantBodyHas(t, level+" list", step(http.MethodGet, list, "", http.StatusOK), `"id":"freeze"`)
	wantBodyHas(t, level+" read", step(http.MethodGet, item, "", http.StatusOK), `"id":"freeze"`, `"version":1`)

	wantBodyHas(t, level+" replace", step(http.MethodPut, item, doc, http.StatusOK), `"version":2`)

	// A read with an access token works after the replace.
	status, _, err := env.ServerAPIStatus(ctx, http.MethodGet, item, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s read after the replace = %d, %v; want 200", level, status, err)
	}

	step(http.MethodDelete, item, "", http.StatusNoContent)
	step(http.MethodGet, item, "", http.StatusNotFound)
	step(http.MethodDelete, item, "", http.StatusNotFound)
}

// gateRoutesRefusals sends requests that the server must refuse, and proves
// that each one stores nothing.
func gateRoutesRefusals(ctx context.Context, t *testing.T, env *e2e.Env, base string) {
	t.Helper()

	gate := base + "/gates/refused"
	limit := base + "/access-limits/refused"

	for _, s := range []principalStep{
		{http.MethodPut, gate, strings.Replace(gateRoutesGate, "lock", "lock\\\" || context.nothing == 1 || \\\"x", 1), http.StatusUnprocessableEntity},
		{http.MethodPut, gate, strings.Replace(gateRoutesGate, "state.write", "state.read", 1), http.StatusUnprocessableEntity},
		{http.MethodPut, gate, strings.TrimSuffix(gateRoutesGate, "}") + `,"enabled":true}`, http.StatusBadRequest},
		{http.MethodPut, base + "/gates/BAD", gateRoutesGate, http.StatusUnprocessableEntity},
		{http.MethodPut, base + "/gates/builtin%2Fx", gateRoutesGate, http.StatusUnprocessableEntity},
		{http.MethodPut, limit, `{"policy":"permit (principal, action, resource);"}`, http.StatusUnprocessableEntity},
		{http.MethodPut, limit, `{"policy":"forbid ("}`, http.StatusUnprocessableEntity},
		{http.MethodPut, base + "/collections/bad%2Fname/gates/x", gateRoutesGate, http.StatusBadRequest},
	} {
		gateRoutesCall(ctx, t, env, s)
	}

	gateRoutesCall(ctx, t, env, principalStep{http.MethodGet, gate, "", http.StatusNotFound})
	gateRoutesCall(ctx, t, env, principalStep{http.MethodGet, limit, "", http.StatusNotFound})
}

// gateRoutesFinal proves that a final gate needs a reason to become weaker or
// to go, and that the version header stops a stale write.
func gateRoutesFinal(ctx context.Context, t *testing.T, env *e2e.Env, base string) {
	t.Helper()

	item := base + "/gates/hold"
	final := strings.Replace(gateRoutesGate, `"mode":"enforce"`, `"mode":"enforce","final":true`, 1)
	weaker := strings.Replace(final, "enforce", "evaluate", 1)

	gateRoutesCall(ctx, t, env, principalStep{http.MethodPut, item, final, http.StatusOK})
	gateRoutesCall(ctx, t, env, principalStep{http.MethodPut, item, weaker, http.StatusConflict})
	gateRoutesCall(ctx, t, env, principalStep{
		http.MethodPut, item, strings.Replace(weaker, `"final":true`, `"final":true,"reason":"freeze ends"`, 1),
		http.StatusOK,
	})
	gateRoutesCall(ctx, t, env, principalStep{http.MethodDelete, item, "", http.StatusConflict})
	gateRoutesCall(
		ctx,
		t,
		env,
		principalStep{http.MethodDelete, item + "?reason=freeze%20ended", "", http.StatusNoContent},
	)
}

// gateRoutesTokenWrites proves that an access token never gives fresh
// authentication: a set or a delete with the token answers 403 and changes
// nothing, and a read with the token still works.
func gateRoutesTokenWrites(ctx context.Context, t *testing.T, env *e2e.Env, base string) {
	t.Helper()

	gate := base + "/gates/seed"

	gateRoutesCall(ctx, t, env, principalStep{http.MethodPut, gate, gateRoutesGate, http.StatusOK})

	for _, s := range []principalStep{
		{http.MethodPut, gate, gateRoutesGate, 0},
		{http.MethodDelete, gate, "", 0},
		{http.MethodPut, base + "/access-limits/seed", gateRoutesLimit, 0},
		{http.MethodDelete, base + "/access-limits/seed", "", 0},
	} {
		status, body, err := env.ServerAPIStatus(ctx, s.method, s.path, s.body)
		if err != nil {
			t.Fatalf("%s %s: %v", s.method, s.path, err)
		}

		if status != http.StatusForbidden || !strings.Contains(body, "recent sign-in") {
			t.Fatalf("%s %s with a token answers %d (%s), want 403 that asks for a recent sign-in",
				s.method, s.path, status, body)
		}
	}

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, gate, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the gate with a token = %d, %v (%s); want 200", status, err, body)
	}

	gateRoutesCall(ctx, t, env, principalStep{http.MethodDelete, gate, "", http.StatusNoContent})
}
