//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// plannerNamespace is the namespace of the planner role test.
const plannerNamespace = "planner-role"

// TestPlannerReadsStateWithBothCLIs proves a planner reads masked state
// with both CLIs: the masked resource list
// GET /api/v2/state-versions/{state_version_id}/resources answers 200 with
// unit:read, while the raw download
// GET /api/v2/state-versions/{state_version_id}/download answers 403 with
// access.denied because it needs unit:read-secrets. The Pulumi stack export
// keeps ciphertext with unit:read, so it answers 200.
func TestPlannerReadsStateWithBothCLIs(t *testing.T) {
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
			runPlannerReadsOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// runPlannerReadsOnStore runs the planner read test on one store.
func runPlannerReadsOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, tofuCLI, store)

	checkReady(ctx, t, env)
	plannerSetup(ctx, t, env)
	plannerSeedUnits(ctx, t, env, pulumiCLI, tofuCLI)

	dev := scopeIssuePerson(ctx, t, env, "planner-dev")

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + plannerNamespace + "/collections/team/grants",
		`{"subject":"principal:` + dev.principal.ID + `","role":"planner"}`, http.StatusCreated,
	}, nil)

	checkPlannerPulumiExport(ctx, t, env, pulumiCLI, dev)
	checkPlannerTofuMasked(ctx, t, env, dev)
}

// plannerSetup makes the namespace, the collection and the placement rules
// of the planner test.
func plannerSetup(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + plannerNamespace + `"}`, http.StatusCreated,
	}, nil)

	base := "/api/dustpan/v1/namespaces/" + plannerNamespace

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, base + "/collections", collectionBody(t, "team"), http.StatusCreated,
	}, nil)

	// A rule names its collection by ID, read after the create above.
	team := placementCollectionID(ctx, t, env, plannerNamespace, "team")

	for _, rule := range []string{
		fmt.Sprintf(`{"tool":"pulumi","project":"scope","stack":"a","collectionID":%q}`, team),
		fmt.Sprintf(`{"tool":"opentofu","project":"*","stack":"a","collectionID":%q}`, team),
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, base + "/placement-rules", rule, http.StatusCreated,
		}, nil)
	}
}

// plannerWriteTofu writes the OpenTofu configuration of the planner
// workspace: one output, no resources.
func plannerWriteTofu(t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := scopeTofuDir(env, "a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	config := fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`, env.Host(), plannerNamespace, "a")

	env.WriteFile(t, filepath.Join(dir, "main.tf"), config)
}

// plannerSeedUnits creates one stack and one workspace with the
// administrator token, so the planner below acts on units that exist.
func plannerSeedUnits(ctx context.Context, t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install) {
	t.Helper()

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

	plannerWriteTofu(t, env)

	tofuDir := scopeTofuDir(env, "a")

	res, err = env.RunAs(ctx, tofuCLI, tofuDir, nil, "init", "-input=false")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("tofu init a exits %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}

	res, err = env.RunAs(ctx, tofuCLI, tofuDir, nil, "apply", "-input=false", "-auto-approve")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("tofu apply a exits %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}
}

// checkPlannerPulumiExport proves the planner stack export keeps working:
// `pulumi stack export` needs only unit:read, and the checkpoint keeps
// ciphertext, so secrets stay masked.
func checkPlannerPulumiExport(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	pulumiCLI e2e.Install,
	dev *scopePerson,
) {
	t.Helper()

	extra := []string{"PULUMI_ACCESS_TOKEN=" + dev.token.Secret}

	res, err := env.RunAs(ctx, pulumiCLI, scopePulumiDir(env, "a"), extra, "stack", "export")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("pulumi stack export as planner exits %d, %v:\n%s", res.ExitCode, err, res.Stderr)
	}

	status, body, err := env.APIStatusAs(
		ctx,
		dev.token.Secret,
		http.MethodGet,
		fmt.Sprintf("/api/stacks/%s/scope/a/state/latest/outputs", plannerNamespace),
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("stack outputs as planner answers %d, %v: %s", status, err, body)
	}
}

// checkPlannerTofuMasked proves the masked OpenTofu read answers 200 for a
// planner while the raw download answers 403 with access.denied.
func checkPlannerTofuMasked(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	dev *scopePerson,
) {
	t.Helper()

	versionID := plannerCurrentVersionID(ctx, t, env, dev.token.Secret)

	status, body, err := env.TFEStatus(
		ctx, dev.token.Secret, http.MethodGet, "/api/v2/state-versions/"+versionID+"/resources", "",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("masked resources as planner answers %d, %v: %s", status, err, body)
	}

	status, body, err = env.TFEStatus(
		ctx, dev.token.Secret, http.MethodGet, "/api/v2/state-versions/"+versionID+"/download", "",
	)
	if err != nil {
		t.Fatalf("download as planner: %v", err)
	}

	if status != http.StatusForbidden || !strings.Contains(body, "access.denied") {
		t.Fatalf("download as planner answers %d, want 403 with access.denied: %s", status, body)
	}
}

// plannerCurrentVersionID returns the current state version ID of workspace
// a as the planner sees it, through the planner token. It looks the workspace
// up in the planner namespace: the shared WorkspaceIDByName helper reads the
// e2e namespace instead, so a planner test must not use it.
func plannerCurrentVersionID(ctx context.Context, t *testing.T, env *e2e.Env, token string) string {
	t.Helper()

	status, body, err := env.TFEStatus(
		ctx,
		token,
		http.MethodGet,
		"/api/v2/organizations/"+plannerNamespace+"/workspaces/a",
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get workspace a as planner answers %d, %v: %s", status, err, body)
	}

	var doc workspaceIDDoc

	if uerr := json.Unmarshal([]byte(body), &doc); uerr != nil || doc.Data.ID == "" {
		t.Fatalf("decode the workspace id as planner: %v (%s)", uerr, body)
	}

	wsID := doc.Data.ID

	status, body, err = env.TFEStatus(
		ctx,
		token,
		http.MethodGet,
		"/api/v2/workspaces/"+wsID+"/current-state-version",
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("current state version as planner answers %d, %v: %s", status, err, body)
	}

	versionID, err := importStateVersionID(body)
	if err != nil {
		t.Fatalf("decode the current state version as planner: %v", err)
	}

	return versionID
}
