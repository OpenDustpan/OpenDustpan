//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// ipAllowlistSource is the local address inside the list of the locked
// namespace. The default source 127.0.0.1 stays outside it.
const ipAllowlistSource = "127.0.0.2"

// TestIPAllowlists locks one namespace to one source address and proves the
// gate: a source outside the list gets 403 without the list in the body, a
// source inside it passes, another namespace is not affected, and deleting
// the rule opens the namespace again. It runs on each store with IP
// allowlists applied to the bootstrap admin token too.
func TestIPAllowlists(t *testing.T) {
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
			runIPAllowlistOnStore(t, cli, db)
		})
	}
}

// runIPAllowlistOnStore runs the IP allowlist steps on one store.
func runIPAllowlistOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithIPAllowlists(ctx, t, cli, store)

	checkReady(ctx, t, env)

	checkIPAllowlistCreateLocked(ctx, t, env)
	checkIPAllowlistEmpty(ctx, t, env)
	checkIPAllowlistSet(ctx, t, env)
	checkIPAllowlistRefusesOutside(ctx, t, env)
	checkIPAllowlistAdmitsInside(ctx, t, env)
	checkIPAllowlistLeavesOpenAlone(ctx, t, env)
	checkIPAllowlistDelete(ctx, t, env)
}

// ipAllowlistStep is one installation API request of the IP allowlist steps
// and the status it must answer.
type ipAllowlistStep struct {
	step   string
	method string
	path   string
	body   string
	want   int
}

// ipAllowlistCall sends one installation API request from the default source
// and fails unless it answers s.want. It answers the body.
func ipAllowlistCall(ctx context.Context, t *testing.T, env *e2e.Env, s ipAllowlistStep) string {
	t.Helper()

	status, resp, err := env.ServerAPIStatus(ctx, s.method, s.path, s.body)
	if err != nil {
		t.Fatalf("%s: %s %s: %v", s.step, s.method, s.path, err)
	}

	if status != s.want {
		t.Fatalf("%s: %s %s answers %d, want %d (%s)", s.step, s.method, s.path, status, s.want, resp)
	}

	return resp
}

// ipAllowlistCallFrom sends one installation API request from source and
// fails unless it answers s.want. It answers the body.
func ipAllowlistCallFrom(
	ctx context.Context, t *testing.T, env *e2e.Env, source string, s ipAllowlistStep,
) string {
	t.Helper()

	status, resp, err := env.ServerAPIStatusFrom(ctx, source, env.TokenA(), s.method, s.path, s.body)
	if err != nil {
		t.Fatalf("%s: %s %s from %s: %v", s.step, s.method, s.path, source, err)
	}

	if status != s.want {
		t.Fatalf("%s: %s %s from %s answers %d, want %d (%s)",
			s.step, s.method, s.path, source, status, s.want, resp)
	}

	return resp
}

// checkIPAllowlistCreateLocked creates the namespace that the later steps lock.
func checkIPAllowlistCreateLocked(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "create the locked namespace",
		method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"locked"}`,
		want: http.StatusCreated,
	})
}

// checkIPAllowlistEmpty reads the list of the locked namespace before any
// rule: the feature is on and the namespace has not opted in.
func checkIPAllowlistEmpty(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	body := ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the empty allowlist",
		method: http.MethodGet, path: "/api/server/namespaces/locked/ip-allowlist", body: "",
		want: http.StatusOK,
	})

	if !strings.Contains(body, `"enabled":true`) {
		t.Fatalf("read the empty allowlist: body %s has no \"enabled\":true", body)
	}

	if !strings.Contains(body, `"optedIn":false`) {
		t.Fatalf("read the empty allowlist: body %s has no \"optedIn\":false", body)
	}
}

// checkIPAllowlistSet writes the list of the locked namespace. No rule
// exists yet, so the bootstrap admin writes from the default source.
func checkIPAllowlistSet(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "set the allowlist",
		method: http.MethodPut, path: "/api/server/namespaces/locked/ip-allowlist",
		body: `{"cidrs":["127.0.0.2/32"]}`,
		want: http.StatusOK,
	})
}

// checkIPAllowlistRefusesOutside reads the locked namespace from the default
// source: the answer is 403 and never names the list.
func checkIPAllowlistRefusesOutside(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	body := ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the locked namespace from outside",
		method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
		want: http.StatusForbidden,
	})

	if strings.Contains(body, ipAllowlistSource) {
		t.Fatalf("read the locked namespace from outside: body %s leaks the list", body)
	}
}

// checkIPAllowlistAdmitsInside reads the locked namespace from a source
// inside the list.
func checkIPAllowlistAdmitsInside(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCallFrom(ctx, t, env, ipAllowlistSource, ipAllowlistStep{
		step:   "read the locked namespace from inside",
		method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistLeavesOpenAlone creates a second namespace and reads it
// from the default source: its requests still pass.
func checkIPAllowlistLeavesOpenAlone(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "create the open namespace",
		method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"open"}`,
		want: http.StatusCreated,
	})
	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the open namespace from the default source",
		method: http.MethodGet, path: "/api/server/namespaces/open/override-switches", body: "",
		want: http.StatusOK,
	})
}

// checkIPAllowlistDelete deletes the rule from a source inside the list and
// reads the locked namespace from the default source again: it passes now.
func checkIPAllowlistDelete(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	ipAllowlistCallFrom(ctx, t, env, ipAllowlistSource, ipAllowlistStep{
		step:   "delete the allowlist",
		method: http.MethodDelete, path: "/api/server/namespaces/locked/ip-allowlist", body: "",
		want: http.StatusNoContent,
	})
	ipAllowlistCall(ctx, t, env, ipAllowlistStep{
		step:   "read the unlocked namespace from the default source",
		method: http.MethodGet, path: "/api/server/namespaces/locked/override-switches", body: "",
		want: http.StatusOK,
	})
}
