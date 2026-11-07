//go:build e2e

// This file covers the self-service path. The persona is Priya, a platform
// engineer, who publishes the template pg at version 1. Ana fills in the form
// and gets a 422 for a size that is too large. She fixes the size and submits
// the form. A runner applies the new unit into payments, and the inventory
// names pg version 1 for it.
//
// User-visible end checks: the self-service docs page, the 422 body that names
// the size field, the job and revision that the submit makes, the inventory
// row of the unit in payments, and the form page of the console.

package e2e_test

import (
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// selfServiceTest is the name of this test, used in the names of skipped steps.
const selfServiceTest = "TestPathSelfService"

// TestPathSelfService runs the self-service steps on each store. Every step
// waits for a feature that is not on main yet, so each step is skipped with
// the plan items that it waits for.
func TestPathSelfService(t *testing.T) {
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

			runSelfServiceOnStore(t, db)
		})
	}
}

// runSelfServiceOnStore runs the steps in order on one store.
func runSelfServiceOnStore(t *testing.T, store e2e.Store) {
	t.Helper()

	env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

	t.Run("publish_pg_v1", func(t *testing.T) {
		selfServiceBlocked(t, env, "publish_pg_v1", "blocked: templates cannot be published yet")
	})

	t.Run("check_returns_422", func(t *testing.T) {
		selfServiceBlocked(t, env, "check_returns_422", "blocked: the form check does not return 422 yet")
	})

	t.Run("submit_creates_revision_and_job", func(t *testing.T) {
		selfServiceBlocked(t, env, "submit_creates_revision_and_job",
			"blocked: a form submit does not create a revision and job yet")
	})

	t.Run("runner_applies", func(t *testing.T) {
		selfServiceBlocked(t, env, "runner_applies", "blocked: no runner applies a unit from a form yet")
	})

	t.Run("unit_in_payments_names_pg_v1", func(t *testing.T) {
		selfServiceBlocked(t, env, "unit_in_payments_names_pg_v1",
			"blocked: the inventory does not name the template version yet")
	})

	t.Run("console_form", func(t *testing.T) {
		selfServiceBlocked(t, env, "console_form", "blocked: the console form is not built")
	})
}

// selfServiceBlocked records that the step name does not run in this mode, and
// skips it. The reason names the missing capability.
func selfServiceBlocked(t *testing.T, env *e2e.Env, name, reason string) {
	t.Helper()

	env.SkipScenario(t, selfServiceTest+"/"+name, reason)
	t.Skip(reason)
}
