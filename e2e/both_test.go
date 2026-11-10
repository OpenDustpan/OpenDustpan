//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestBothToolsSameServer runs the full OpenTofu scenario and the full
// Pulumi scenario on one server process and database: the OpenTofu steps
// run first, then the Pulumi steps run unchanged on the same server. It
// runs on every store under test and writes no report.
func TestBothToolsSameServer(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// Both CLIs must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the combined suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersion := e2e.OpenTofu().Single()

	if len(pulumiVersions) == 0 || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBothOnStore(t, fetcher, pulumiVersions, tofuVersion, db)
		})
	}
}

// runBothOnStore installs every Pulumi CLI of the matrix and one OpenTofu CLI
// and runs both full scenarios on one server. The first Pulumi CLI runs the
// full Pulumi scenario; every Pulumi CLI lists the stacks of the paging
// scenario.
func runBothOnStore(
	t *testing.T, fetcher *e2e.Fetcher, pulumiLabels []string, tofuLabel string, store e2e.Store,
) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	pulumiCLIs := make([]e2e.Install, 0, len(pulumiLabels))

	for _, label := range pulumiLabels {
		cli, err := fetcher.Install(installCtx, e2e.Pulumi(), label)
		if err != nil {
			t.Fatalf("install pulumi %s: %v", label, err)
		}

		pulumiCLIs = append(pulumiCLIs, cli)
	}

	pulumiCLI := pulumiCLIs[0]

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuLabel)
	if err != nil {
		t.Fatalf("install tofu %s: %v", tofuLabel, err)
	}

	t.Logf("pulumi %s with tofu %s on %s", pulumiCLI.Version, tofuCLI.Version, store)

	env := e2e.NewEnvWithRealTimeSignIn(t.Context(), t, tofuCLI, store)

	tofuScenario(t, env)

	env.SetCLI(pulumiCLI)

	pulumiScenario(t, env)
	pulumiStackPagingScenario(t, env, pulumiCLIs)
	env.SetCLI(pulumiCLI)

	checkServerMetrics(t, env)
	checkServerComponents(t, env)
	checkReadyWithoutDatabase(t.Context(), t, env)
}

// checkServerMetrics proves the metrics route counts both tools: GET
// /api/server/metrics with the admin token returns a request count sample
// for the pulumi and opentofu adapters, while a request without a token
// gets 401.
func checkServerMetrics(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/server/metrics", "")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("get metrics answers %d, want %d: %s", status, http.StatusOK, body)
	}

	for _, adapter := range []string{"pulumi", "opentofu"} {
		want := `dustpan_http_requests_total{adapter="` + adapter + `"`
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output misses a request count for adapter %q:\n%s", adapter, body)
		}
	}

	status, body, err = env.TFEStatus(ctx, "", http.MethodGet, "/api/server/metrics", "")
	if err != nil {
		t.Fatalf("get metrics without a token: %v", err)
	}

	if status != http.StatusUnauthorized {
		t.Fatalf("get metrics without a token answers %d, want %d: %s", status, http.StatusUnauthorized, body)
	}
}

// checkServerComponents proves the component routes: GET /api/server/components
// and GET /api/server/worker answer 200 with the admin token, which holds
// server:observe, and 401 without a token.
func checkServerComponents(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	for _, route := range []struct{ path, field string }{
		{"/api/server/components", `"components"`},
		{"/api/server/worker", `"stuck"`},
	} {
		status, body, err := env.TFEStatusA(ctx, http.MethodGet, route.path, "")
		if err != nil {
			t.Fatalf("get %s: %v", route.path, err)
		}

		if status != http.StatusOK || !strings.Contains(body, route.field) {
			t.Fatalf("get %s answers %d %q, want %d with %s", route.path, status, body, http.StatusOK, route.field)
		}

		status, body, err = env.TFEStatus(ctx, "", http.MethodGet, route.path, "")
		if err != nil {
			t.Fatalf("get %s without a token: %v", route.path, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("get %s without a token answers %d, want %d: %s",
				route.path, status, http.StatusUnauthorized, body)
		}
	}
}
