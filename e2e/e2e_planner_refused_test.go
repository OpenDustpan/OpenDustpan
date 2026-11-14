//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestPlannerWriteRefused proves a planner cannot write: up, destroy and
// apply are refused with 403 and access.denied.
func TestPlannerWriteRefused(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiVersion := e2e.Pulumi().Single()
	tofuVersion := e2e.OpenTofu().Single()

	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("the Pulumi and OpenTofu tools each need a version in their matrix")
	}

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPlannerRefusedOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// TestPlannerSecretRevealRefused proves a planner cannot reveal a secret:
// the reveal route on
// /api/stacks/{}/{}/{}/state/{}/reveal answers 403 with access.denied.
func TestPlannerSecretRevealRefused(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiVersion := e2e.Pulumi().Single()

	if pulumiVersion == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPlannerRevealRefusedOnStore(t, pulumiCLI, db)
		})
	}
}

// runPlannerRefusedOnStore runs the refused-write test on one store.
func runPlannerRefusedOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, tofuCLI, store)

	checkReady(ctx, t, env)
	plannerSetup(ctx, t, env)
	plannerSeedUnits(ctx, t, env, pulumiCLI, tofuCLI)

	dev := scopeIssuePerson(ctx, t, env, "planner-refused")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + plannerNamespace + "/collections/team/grants",
		`{"subject":"principal:` + dev.principal.ID + `","role":"planner"}`, http.StatusCreated,
	}, nil)

	pulumiRunner := &scopeRunner{t: t, env: env, cli: pulumiCLI, person: dev}
	tofuRunner := &scopeRunner{t: t, env: env, cli: tofuCLI, person: dev}

	scopeRefused(t, pulumiRunner.run(ctx, "pulumi", "a", "up", "--yes", "--skip-preview"), "pulumi up as planner")

	scopeRefused(
		t,
		pulumiRunner.run(ctx, "pulumi", "a", "destroy", "--yes", "--skip-preview"),
		"pulumi destroy as planner",
	)

	scopeRefused(
		t,
		tofuRunner.run(ctx, "opentofu", "a", "apply", "-input=false", "-auto-approve"),
		"tofu apply as planner",
	)

	scopeRefused(
		t,
		tofuRunner.run(ctx, "opentofu", "a", "destroy", "-input=false", "-auto-approve"),
		"tofu destroy as planner",
	)
}

// runPlannerRevealRefusedOnStore runs the refused-reveal test on one store.
func runPlannerRevealRefusedOnStore(t *testing.T, pulumiCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, pulumiCLI, store)

	checkReady(ctx, t, env)
	plannerSetup(ctx, t, env)

	scopeWritePulumi(t, env, "a")

	dir := scopePulumiDir(env, "a")
	qualified := plannerNamespace + "/scope/a"

	res, err := env.RunAs(ctx, pulumiCLI, dir, nil, "stack", "init", qualified)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("pulumi stack init %s exits %d, %v:\n%s", qualified, res.ExitCode, err, res.Stderr)
	}

	res, err = env.RunAs(ctx, pulumiCLI, dir, nil, "up", "--yes", "--skip-preview")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("pulumi up %s exits %d, %v:\n%s", qualified, res.ExitCode, err, res.Stderr)
	}

	dev := scopeIssuePerson(ctx, t, env, "planner-reveal")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + plannerNamespace + "/collections/team/grants",
		`{"subject":"principal:` + dev.principal.ID + `","role":"planner"}`, http.StatusCreated,
	}, nil)

	status, body, err := env.APIStatusAs(
		ctx,
		dev.token.Secret,
		http.MethodPost,
		fmt.Sprintf("/api/stacks/%s/scope/a/state/1/reveal", plannerNamespace),
		`{"path":"greeting"}`,
	)
	if err != nil {
		t.Fatalf("reveal as planner: %v", err)
	}

	if status != http.StatusForbidden || !strings.Contains(body, "access.denied") {
		t.Fatalf("reveal as planner answers %d, want 403 with access.denied: %s", status, body)
	}
}
