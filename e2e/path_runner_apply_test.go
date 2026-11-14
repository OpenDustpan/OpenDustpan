//go:build e2e

// This file covers the runner apply path. The persona is Sam, who reviews
// infrastructure changes. A pool named ci has one runner that joins. An apply
// job for the OpenTofu unit db plans on the runner, waits at an enforce gate
// for two approvers, and then applies the stored plan. Sam reads the log and
// the decision record of the run.
//
// User-visible end checks: the runner pool page of the docs, the job log that
// the CLI and the API print, the decision record of the gate, and the run page
// of the console.

package e2e_test

import (
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// runnerApplyTest is the name of this test, used in the names of skipped steps.
const runnerApplyTest = "TestPathRunnerApply"

// TestPathRunnerApply runs the runner apply steps on each store. Every step
// waits for a feature that is not on main yet, so each step is skipped with
// the plan items that it waits for.
func TestPathRunnerApply(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runRunnerApplyOnStore(t, db)
		})
	}
}

// runRunnerApplyOnStore runs the steps in order on one store.
func runRunnerApplyOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

	t.Run("pool_and_join", func(t *testing.T) {
		runnerApplyBlocked(t, env, "pool_and_join", "blocked: no runner joins a pool yet")
	})

	t.Run("apply_job_starts", func(t *testing.T) {
		runnerApplyBlocked(t, env, "apply_job_starts", "blocked: apply jobs do not start yet")
	})

	t.Run("plan_runs_on_runner", func(t *testing.T) {
		runnerApplyBlocked(t, env, "plan_runs_on_runner", "blocked: plans do not run on a runner yet")
	})

	t.Run("gate_waits_for_two", func(t *testing.T) {
		runnerApplyBlocked(t, env, "gate_waits_for_two",
			"blocked: enforce gates do not wait for two approvers yet")
	})

	t.Run("stored_plan_applies", func(t *testing.T) {
		runnerApplyBlocked(t, env, "stored_plan_applies", "blocked: a stored plan does not apply yet")
	})

	t.Run("log_and_decision_read", func(t *testing.T) {
		runnerApplyBlocked(t, env, "log_and_decision_read",
			"blocked: the run log and decision record are not readable yet")
	})

	t.Run("console_run_page", func(t *testing.T) {
		runnerApplyBlocked(t, env, "console_run_page", "blocked: the console run page is not built")
	})
}

// runnerApplyBlocked records that the step name does not run in this mode, and
// skips it. The reason names the missing capability.
func runnerApplyBlocked(t *testing.T, env *e2e.Env, name, reason string) {
	t.Helper()

	env.SkipScenario(t, runnerApplyTest+"/"+name, reason)
	t.Skip(reason)
}
