//go:build e2e

// This file covers the self-service path. The persona is Priya, a platform
// engineer, who publishes the Blueprint pg at version 1. Ana fills in the
// form and gets a 422 for a size that is too large. She fixes the size and
// submits the form. A runner applies the new unit into payments, and the
// inventory names pg version 1 for it.
//
// The publish step runs now. The other five steps wait for routes and pages
// that main does not have.
//
// User-visible end checks: the self-service docs page, the 422 body that names
// the size field, the job and revision that the submit makes, the inventory
// row of the unit in payments, and the form page of the console.

package e2e_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// selfServiceTest is the name of this test, used in the names of skipped steps.
const selfServiceTest = "TestPathSelfService"

// TestPathSelfService runs the self-service steps on each store. The publish
// step runs against the Blueprint routes of the server. No form route exists
// yet, so each of the other five steps is skipped with the reason that names
// what it waits for.
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
		publishPGV1(t, env)
	})

	t.Run("check_returns_422", func(t *testing.T) {
		selfServiceBlocked(t, env, "check_returns_422",
			"blocked: the form check route does not exist on main")
	})

	t.Run("submit_creates_revision_and_job", func(t *testing.T) {
		selfServiceBlocked(t, env, "submit_creates_revision_and_job",
			"blocked: the form submit route does not exist on main")
	})

	t.Run("runner_applies", func(t *testing.T) {
		selfServiceBlocked(t, env, "runner_applies",
			"blocked: a form submit makes no job on main, so no runner applies a unit from a form")
	})

	t.Run("unit_in_payments_names_pg_v1", func(t *testing.T) {
		selfServiceBlocked(t, env, "unit_in_payments_names_pg_v1",
			"blocked: the inventory row of a unit names no Blueprint version on main")
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

// pgDraft is the draft of the Blueprint pg that Priya publishes.
const pgDraft = `{
	"schemaVersion": 1, "name": "pg", "title": "PostgreSQL", "tool": "opentofu",
	"program": {"module": {"source": "registry.example.com/platform/database/aws", "version": "1.2.3"}},
	"unitName": "'databases/' + request.name",
	"inputs": [
		{"name": "size", "type": "integer", "maps": "size", "validate": [
			{"rule": "value >= 1", "message": "Give at least 1."},
			{"rule": "value <= 10", "message": "Give at most 10."}]}
	],
	"delivery": {"homes": ["opendustpan"], "default": "opendustpan"}
}`

// pgBase is the route prefix of the Blueprint pg in the e2e namespace.
const pgBase = blueprintListPath + "/pg"

// publishPGV1 writes the draft of pg, publishes draft version 1 and reads
// version 1 back.
func publishPGV1(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: blueprintAgent,
		Name:      "write the draft of pg",
		Method:    http.MethodPut, Path: pgBase + "/draft", Body: pgDraft,
		Want: http.StatusOK, Scheme: "token", Check: blueprintAnswer(`"version":1`),
	}, map[string]string{"If-Match": `"0"`, "Content-Type": "application/json"})

	env.APIStepWithHeaders(ctx, t, e2e.APIStepSpec{
		UserAgent: blueprintAgent,
		Name:      "publish draft version 1 of pg",
		Method:    http.MethodPost, Path: pgBase + "/versions", Body: `{"draftVersion": 1}`,
		Want: http.StatusCreated, Scheme: "token", Check: blueprintAnswer(`"draftVersion":1`),
	}, map[string]string{"Content-Type": "application/json"})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: blueprintAgent,
		Name:      "read version 1 of pg",
		Method:    http.MethodGet, Path: pgBase + "/versions/1", Body: "",
		Want: http.StatusOK, Scheme: "token", Check: blueprintAnswer(`"draftVersion":1`),
	})
}
