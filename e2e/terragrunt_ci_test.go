//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tgCiNamespace holds the workspaces of the CI run-all scenarios.
const tgCiNamespace = tgLocalNamespace

// tgCiScenario holds what one CI scenario needs: the server, the access token
// of the job and the Terragrunt runner that sends it.
type tgCiScenario struct {
	env    *e2e.Env
	runner tgLocalCLI
	token  string
}

// TestTerragruntCiThreeUnitsApply proves that one CI job applies three units
// with an access token: each workspace holds one new state version that the
// state version list route serves to the same token.
func TestTerragruntCiThreeUnitsApply(t *testing.T) {
	t.Parallel()

	tgCiRun(t, func(t *testing.T, sc tgCiScenario) {
		t.Helper()

		ctx := t.Context()
		scope := tgLocalScope{env: sc.env, token: sc.token, namespace: tgCiNamespace}

		for _, unit := range tgLocalUnits() {
			scope.createWorkspace(ctx, t, unit)
		}

		live := tgLocalApplyAll(ctx, t, sc.runner)

		for i, unit := range tgLocalUnits() {
			status, body, err := sc.env.TFEStatus(
				ctx, sc.token, http.MethodGet, tgLocalHistoryPath(tgCiNamespace, unit), "",
			)
			if err != nil {
				t.Fatalf("list state versions of %s: %v", unit, err)
			}

			if status != http.StatusOK {
				t.Fatalf("state versions of %s answer %d, want 200: %s", unit, status, body)
			}

			scope.checkOneVersion(ctx, t, unit)
			sc.runner.checkOneOutput(ctx, t, live, i, unit)
		}
	})
}

// TestTerragruntCiOneUnitFails proves that when one of three units fails in a
// CI job, the failed workspace holds no new state version and each other
// workspace keeps its own result.
func TestTerragruntCiOneUnitFails(t *testing.T) {
	t.Parallel()

	tgCiRun(t, func(t *testing.T, sc tgCiScenario) {
		t.Helper()

		ctx := t.Context()
		scope := tgLocalScope{env: sc.env, token: sc.token, namespace: tgCiNamespace}
		units := tgLocalUnits()

		for _, unit := range units {
			scope.createWorkspace(ctx, t, unit)
		}

		live := filepath.Join(sc.env.Home(), "tg-ci-failed-live")
		tgLocalLiveRoot(t, live)

		dirs := tgLocalLiveDirs()
		for i, unit := range units {
			sc.runner.writeLiveUnit(t, live, tgCiNamespace, dirs[i], unit)
		}

		// The last unit declares a required variable that the job does not
		// set. With -input=false its plan fails before any state write, while
		// the other units have no dependency on it and apply.
		sc.env.WriteFile(t, filepath.Join(live, dirs[2], "fail.tf"),
			"variable \"ci_unset_input\" {\n  type = string\n}\n")

		res, err := sc.runner.tgResult(ctx, t, live, "run", "--all", "--non-interactive", "--parallelism", "3",
			"--", "apply", "-input=false", "-auto-approve", "-lock-timeout=0s")
		if err != nil {
			t.Fatalf("terragrunt run --all with one failing unit: %v", err)
		}

		if res.ExitCode == 0 {
			t.Fatalf("the run-all with one failing unit exits 0, want a failure:\n%s\n%s", res.Stdout, res.Stderr)
		}

		for i, unit := range units[:2] {
			scope.checkOneVersion(ctx, t, unit)
			sc.runner.checkOneOutput(ctx, t, live, i, unit)
		}

		if got := scope.versionTotal(ctx, t, units[2]); got != 0 {
			t.Fatalf("failed workspace %s holds %d state versions, want 0", units[2], got)
		}
	})
}

// TestTerragruntCiCreatesNoChangeOrder proves that a CI run-all over three
// units leaves no job and so no change order and no discovery job, while each
// workspace holds its own state version.
func TestTerragruntCiCreatesNoChangeOrder(t *testing.T) {
	t.Parallel()

	tgCiRun(t, func(t *testing.T, sc tgCiScenario) {
		t.Helper()

		ctx := t.Context()
		scope := tgLocalScope{env: sc.env, token: sc.token, namespace: tgCiNamespace}

		for _, unit := range tgLocalUnits() {
			scope.createWorkspace(ctx, t, unit)
		}

		tgLocalApplyAll(ctx, t, sc.runner)

		for _, unit := range tgLocalUnits() {
			if got := scope.versionTotal(ctx, t, unit); got != 1 {
				t.Fatalf("workspace %s holds %d state versions, want 1", unit, got)
			}
		}

		tgLocalCheckNoJobs(ctx, t, sc.env, sc.token)
	})
}

// tgCiRun starts one server per store, signs in with an access token and
// calls scenario with the Terragrunt runner of that token.
func tgCiRun(t *testing.T, scenario func(t *testing.T, sc tgCiScenario)) {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skipf("the Terragrunt suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), e2e.OpenTofu().Single())
	if err != nil {
		t.Fatalf("install %s: %v", e2e.OpenTofu().Name, err)
	}

	tg, err := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if err != nil {
		t.Fatalf("install %s: %v", e2e.Terragrunt().Name, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			ctx := t.Context()
			env := e2e.NewEnvWithSignIn(ctx, t, cli, db)

			checkReady(ctx, t, env)
			env.CreateNamespace(ctx, t, tgCiNamespace)

			token := env.TokenA()
			runner := tgLocalRunner(ctx, t, env, cli, token)
			runner.tg = tg

			scenario(t, tgCiScenario{env: env, runner: runner, token: token})
		})
	}
}
