//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
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

	plannerTofuWriteRefused(ctx, t, env, tofuRunner, dev)
}

// plannerTofuWriteRefused proves a planner cannot write with OpenTofu. The
// entitlement-set read answers a caller who holds a grant on a collection in
// the namespace, which the planner holds, so the CLI reaches the write. The
// write needs unit:write, which the role does not hold, so the run must exit
// non-zero with the server refusal and never with a not-found organization.
// The server-side refusal is then proved on the write itself: locking the
// workspace needs unit:write and answers 403 with access.denied for the
// planner token.
func plannerTofuWriteRefused(ctx context.Context, t *testing.T, env *e2e.Env, tofu *scopeRunner, dev *scopePerson) {
	t.Helper()

	for _, args := range [][]string{
		{"apply", "-input=false", "-auto-approve"},
		{"destroy", "-input=false", "-auto-approve"},
	} {
		res := tofu.run(ctx, "opentofu", "a", args...)
		out := res.Stdout + "\n" + res.Stderr

		if res.ExitCode == 0 {
			t.Fatalf("tofu %s as planner succeeds, want a refusal:\n%s",
				strings.Join(args, " "), res.Stdout)
		}

		// OpenTofu prints the refusal as text, without the status number.
		if !strings.Contains(out, "access.denied") && !strings.Contains(out, "403") &&
			!strings.Contains(out, "forbidden") {
			t.Fatalf("tofu %s as planner exits %d without the server refusal, want access.denied or its 403 text:\n%s",
				strings.Join(args, " "), res.ExitCode, out)
		}

		if strings.Contains(out, `organization "`+plannerNamespace+`"`) && strings.Contains(out, "not found") {
			t.Fatalf("tofu %s as planner exits %d with the organization not found, want the server refusal:\n%s",
				strings.Join(args, " "), res.ExitCode, out)
		}
	}

	wsID := plannerWorkspaceIDAsAdmin(ctx, t, env)

	status, body, err := env.TFEStatus(
		ctx,
		dev.token.Secret,
		http.MethodPost,
		"/api/v2/workspaces/"+wsID+"/actions/lock",
		`{"data":{"type":"workspaces"}}`,
	)
	if err != nil {
		t.Fatalf("lock workspace as planner: %v", err)
	}

	if status != http.StatusForbidden || !strings.Contains(body, "access.denied") {
		t.Fatalf("lock workspace as planner answers %d, want 403 with access.denied: %s", status, body)
	}
}

// plannerWorkspaceIDAsAdmin returns the ID of workspace a in the planner
// namespace, read with the administrator token.
func plannerWorkspaceIDAsAdmin(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.TFEStatusA(
		ctx,
		http.MethodGet,
		"/api/v2/organizations/"+plannerNamespace+"/workspaces/a",
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get workspace a answers %d, %v: %s", status, err, body)
	}

	var doc workspaceIDDoc

	if uerr := json.Unmarshal([]byte(body), &doc); uerr != nil || doc.Data.ID == "" {
		t.Fatalf("decode the workspace id: %v (%s)", uerr, body)
	}

	return doc.Data.ID
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
