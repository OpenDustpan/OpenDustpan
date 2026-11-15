//go:build e2e

package e2e_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tgLocalRootHCL is the root Terragrunt configuration of the three-unit
// fixture. The test copies it to the live directory, so the run reads the
// fixture: the test fails when the fixture is absent.
//
//go:embed testdata/terragrunt-local/terragrunt.hcl
var tgLocalRootHCL string

// tgLocalUnitHCL is the per-unit Terragrunt configuration the test writes
// next to each main.tf, so every unit finds the root configuration of the
// live directory by its root.hcl name.
const tgLocalUnitHCL = `include "root" {
  path = find_in_parent_folders("root.hcl")
}
`

// tgLocalNamespace holds the workspaces of the local run-all scenarios. Each
// Terragrunt unit talks to its own workspace in this namespace.
const tgLocalNamespace = "terragrunt-local"

// sharedNameAttempts bounds the retries of the shared name landing run: two
// units that name one workspace share its lock, so the run-all at parallelism
// 3 with no lock timeout refuses the unit that loses the lock race, and each
// retry lands the refused unit.
const sharedNameAttempts = 6

// tgLocalUnits returns the three units of the run, each with its own
// workspace of the same name.
func tgLocalUnits() []string {
	return []string{"terragrunt-local-a", "terragrunt-local-b", "terragrunt-local-c"}
}

// TestE2ETerragruntLocalRunAllThreeWorkspaces proves that a local run-all over
// three units reaches three workspaces: each workspace holds one new state
// version and one history entry. It runs on every store. It runs the real
// `terragrunt run --all apply` through the pinned Terragrunt binary over the
// fixture, with the OpenTofu binary of the fetcher as its TG_TF_PATH: each
// unit directory names its own workspace in its backend.
func TestE2ETerragruntLocalRunAllThreeWorkspaces(t *testing.T) {
	t.Parallel()

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

	tg, tgErr := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if tgErr != nil {
		t.Fatalf("install %s %s: %v", e2e.Terragrunt().Name, e2e.Terragrunt().Single(), tgErr)
	}

	for _, version := range e2e.OpenTofu().Versions() {
		cli, installErr := fetcher.Install(installCtx, e2e.OpenTofu(), version)
		if installErr != nil {
			t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, installErr)
		}

		for _, db := range databases {
			t.Run(version+"/"+string(db), func(t *testing.T) {
				t.Parallel()
				tgLocalThreeWorkspaces(t.Context(), t, cli, tg, db)
			})
		}
	}
}

// tgLocalThreeWorkspaces runs the three-unit scenario on one store.
func tgLocalThreeWorkspaces(ctx context.Context, t *testing.T, cli, tg e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	token := env.TokenA()
	for _, unit := range tgLocalUnits() {
		tgLocalCreateWorkspace(ctx, t, env, token, unit)
	}

	runner := tgLocalRunner(ctx, t, env, cli, token)
	runner.tg = tg
	live := tgLocalApplyAll(ctx, t, runner)

	for i, unit := range tgLocalUnits() {
		tgLocalCheckOneVersion(ctx, t, env, token, unit)
		runner.checkOneOutput(ctx, t, live, i, unit)
	}
}

// TestE2ETerragruntLocalRunAllLockError proves that a second writer on a
// locked workspace is refused with workspace.locked, and that the first apply
// keeps its lock.
func TestE2ETerragruntLocalRunAllLockError(t *testing.T) {
	t.Parallel()

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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalLockError(t.Context(), t, cli, db)
		})
	}
}

// tgLocalLockError runs the lock scenario on one store.
func tgLocalLockError(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	token := env.TokenA()
	const unit = "terragrunt-local-locked"

	tgLocalCreateWorkspace(ctx, t, env, token, unit)

	id := tgLocalWorkspaceID(ctx, t, env, token, unit)

	if status, body, err := env.TFEStatus(ctx, token, http.MethodPost, lockRoute(id), `{"data":{}}`); err != nil ||
		status != http.StatusOK {
		t.Fatalf("lock %s answers %d: %v %s", unit, status, err, body)
	}

	status, body, err := env.TFEStatus(ctx, token, http.MethodPost, lockRoute(id), `{"data":{}}`)
	if err != nil {
		t.Fatalf("lock %s again: %v", unit, err)
	}

	if status != http.StatusConflict {
		t.Fatalf("second lock of %s answers %d, want 409: %s", unit, status, body)
	}

	if got := tgLocalErrorCode(t, body); got != "workspace.locked" {
		t.Fatalf("second lock of %s error code = %q, want workspace.locked: %s", unit, got, body)
	}

	if held := tgLocalWorkspaceLock(ctx, t, env, token, id); held == "" {
		t.Fatal("first apply lost the lock after the refused second lock")
	}
}

// lockRoute returns the lock route of a workspace ID.
func lockRoute(id string) string {
	return "/api/v2/workspaces/" + id + "/actions/lock"
}

// TestE2ETerragruntLocalRunAllNotFound proves that a unit that names a
// workspace that does not exist fails with workspace.not-found and records no
// new state version.
func TestE2ETerragruntLocalRunAllNotFound(t *testing.T) {
	t.Parallel()

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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalNotFound(t.Context(), t, cli, db)
		})
	}
}

// tgLocalNotFound runs the unknown workspace scenario on one store.
func tgLocalNotFound(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	token := env.TokenA()
	const missing = "terragrunt-local-missing"

	status, body, err := env.TFEStatus(
		ctx, token, http.MethodGet, "/api/v2/organizations/"+tgLocalNamespace+"/workspaces/"+missing, "",
	)
	if err != nil {
		t.Fatalf("read the missing workspace: %v", err)
	}

	if status != http.StatusNotFound {
		t.Fatalf("read of the missing workspace answers %d, want 404: %s", status, body)
	}

	if got := tgLocalErrorCode(t, body); got != "workspace.not-found" {
		t.Fatalf("missing workspace error code = %q, want workspace.not-found: %s", got, body)
	}

	status, body, err = env.TFEStatus(ctx, token, http.MethodGet, tgLocalHistoryPath(missing), "")
	if err != nil {
		t.Fatalf("list state versions of the missing workspace: %v", err)
	}

	if status != http.StatusNotFound {
		t.Fatalf("state versions of the missing workspace answer %d, want 404: %s", status, body)
	}

	if got := tgLocalErrorCode(t, body); got != "workspace.not-found" {
		t.Fatalf("missing history error code = %q, want workspace.not-found: %s", got, body)
	}
}

// TestE2ETerragruntLocalRunAllRefusedUnit proves that the token of a run with
// no grant on one unit workspace is refused on that unit, while the granted
// units apply.
func TestE2ETerragruntLocalRunAllRefusedUnit(t *testing.T) {
	t.Parallel()

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

	tg, tgErr := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if tgErr != nil {
		t.Fatalf("install %s: %v", e2e.Terragrunt().Name, tgErr)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalRefusedUnit(t.Context(), t, cli, tg, db)
		})
	}
}

// tgLocalRefusedUnit runs the ungranted unit scenario on one store.
func tgLocalRefusedUnit(ctx context.Context, t *testing.T, cli, tg e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	adminToken := env.TokenA()
	for _, unit := range tgLocalUnits() {
		tgLocalCreateWorkspace(ctx, t, env, adminToken, unit)
	}

	units := tgLocalUnits()
	runner := tgLocalIssueUnitPerson(ctx, t, env, "dee", units[:2])

	// The ungranted unit is refused with the access token of the run. The
	// token holds no read on that workspace, so the server hides it and
	// answers the 404 of a workspace that does not exist, with the
	// workspace.not-found code: it never answers 200, and a 403, a 404 of
	// another code or any other status fails this check.
	status, body, err := env.TFEStatus(ctx, runner, http.MethodGet, tgLocalHistoryPath(units[2]), "")
	if err != nil {
		t.Fatalf("list state versions of the ungranted unit: %v", err)
	}

	if status != http.StatusNotFound {
		t.Fatalf("ungranted unit answers %d, want 404: %s", status, body)
	}

	if got := tgLocalErrorCode(t, body); got != "workspace.not-found" {
		t.Fatalf("ungranted unit error code = %q, want workspace.not-found: %s", got, body)
	}

	// The granted units apply through the real `terragrunt run --all` with
	// the token of the run, with the OpenTofu binary of the fetcher as the
	// TG_TF_PATH: each granted workspace holds one new state version and one
	// history entry.
	granted := tgLocalRunner(ctx, t, env, cli, runner)
	granted.tg = tg
	live := filepath.Join(env.Home(), "tg-local-refused-live")
	tgLocalLiveRoot(t, live)

	dirs := tgLocalLiveDirs()
	for i, unit := range units[:2] {
		granted.writeLiveUnit(t, live, dirs[i], unit)
	}

	granted.runAll(ctx, t, live, "run", "--all", "--non-interactive", "--parallelism", "3",
		"--", "apply", "-input=false", "-auto-approve", "-lock-timeout=0s")

	for i, unit := range units[:2] {
		tgLocalCheckOneVersion(ctx, t, env, runner, unit)
		granted.checkOneOutput(ctx, t, live, i, unit)
	}

	// The refused workspace holds no new state version: the refusal recorded
	// nothing there. The check uses the admin token, because the token of the
	// run cannot list that workspace at all.
	if got := tgLocalVersionTotal(ctx, t, env, adminToken, units[2]); got != 0 {
		t.Fatalf("refused workspace holds %d state versions, want 0", got)
	}
}

// TestE2ETerragruntLocalRunAllSharedName proves that two unit directories
// that name one workspace share its lock and its state-version chain: the
// second writer is refused with workspace.locked at parallelism 3 with no
// lock timeout, and the refused write lands on a retry, in the one chain.
func TestE2ETerragruntLocalRunAllSharedName(t *testing.T) {
	t.Parallel()

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

	tg, tgErr := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if tgErr != nil {
		t.Fatalf("install %s: %v", e2e.Terragrunt().Name, tgErr)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalSharedName(t.Context(), t, cli, tg, db)
		})
	}
}

// tgLocalSharedName runs the shared name scenario on one store.
func tgLocalSharedName(ctx context.Context, t *testing.T, cli, tg e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	token := env.TokenA()
	const unit = "terragrunt-local-shared"

	tgLocalCreateWorkspace(ctx, t, env, token, unit)

	// Both live units name the one workspace, under the root fixture, so the
	// real `terragrunt run --all apply` reaches the same workspace twice at
	// parallelism 3 with no lock timeout.
	runner := tgLocalRunner(ctx, t, env, cli, token)
	runner.tg = tg
	live := filepath.Join(env.Home(), "tg-local-shared-live")
	tgLocalLiveRoot(t, live)
	runner.writeLiveUnit(t, live, "shared-a", unit)
	runner.writeLiveUnit(t, live, "shared-b", unit)

	id := tgLocalWorkspaceID(ctx, t, env, token, unit)

	// The first unit holds the lock, as its apply would.
	if status, body, err := env.TFEStatus(ctx, token, http.MethodPost, lockRoute(id), `{"data":{}}`); err != nil ||
		status != http.StatusOK {
		t.Fatalf("lock %s answers %d: %v %s", unit, status, err, body)
	}

	// A second lock call while the lock is held answers 409 with the
	// workspace.locked code.
	if status, body, err := env.TFEStatus(ctx, token, http.MethodPost, lockRoute(id), `{"data":{}}`); err != nil ||
		status != http.StatusConflict {
		t.Fatalf("second lock of %s answers %d, want 409: %v %s", unit, status, err, body)
	} else if got := tgLocalErrorCode(t, body); got != "workspace.locked" {
		t.Fatalf("second lock of %s error code = %q, want workspace.locked: %s", unit, got, body)
	}

	tgLocalRefusedSharedApply(ctx, t, runner, live)
	tgLocalReleaseSharedLock(ctx, t, env, token, id)

	// Both writes land in the one workspace chain, at the same parallelism 3
	// with no lock timeout. The two units still share the one workspace
	// lock, so a run lands one unit and can refuse the unit that loses the
	// lock race. Each retry is the same run-all at parallelism 3 with no
	// lock timeout: it retries the refused unit, and the run that lands the
	// last write ends the loop.
	for attempt := 1; attempt <= sharedNameAttempts; attempt++ {
		res, err := runner.runAllResult(ctx, t, live, "run", "--all", "--non-interactive", "--parallelism", "3",
			"--", "apply", "-input=false", "-auto-approve", "-lock-timeout=0s")
		if err != nil {
			t.Fatalf("terragrunt run --all attempt %d: %v", attempt, err)
		}

		if got := tgLocalVersionTotal(ctx, t, env, token, unit); got >= 2 {
			break
		}

		t.Logf("shared run attempt %d exits %d; retry the refused unit:\n%s", attempt, res.ExitCode, res.Stderr)
	}

	if got := tgLocalVersionTotal(ctx, t, env, token, unit); got != 2 {
		t.Fatalf("shared workspace holds %d state versions, want 2 in one chain", got)
	}
}

// tgLocalRefusedSharedApply proves that the second shared unit, at
// parallelism 3 with no lock timeout, does not wait for the held lock.
func tgLocalRefusedSharedApply(ctx context.Context, t *testing.T, runner tgLocalCLI, live string) {
	t.Helper()

	res, err := runner.runAllResult(ctx, t, live, "run", "--all", "--non-interactive", "--parallelism", "3",
		"--", "apply", "-input=false", "-auto-approve", "-lock-timeout=0s")
	if err != nil {
		t.Fatalf("terragrunt run --all of the shared units: %v", err)
	}

	if res.ExitCode == 0 {
		t.Fatalf("second shared unit applies over a held lock, want a refusal:\n%s", res.Stdout)
	}

	// The CLI prints the lock detail, not the machine code: go-tfe maps the
	// 409 to its generic error. The recorded lock call below proves the
	// workspace.locked code.
	if out := res.Stdout + res.Stderr; !strings.Contains(out, "workspace already locked") {
		t.Fatalf("second shared unit names no lock failure:\n%s\n%s", res.Stdout, res.Stderr)
	}
}

// tgLocalReleaseSharedLock releases the held lock of a shared workspace and
// proves the workspace is unlocked after the release.
func tgLocalReleaseSharedLock(ctx context.Context, t *testing.T, env *e2e.Env, token, id string) {
	t.Helper()

	unlock := "/api/v2/workspaces/" + id + "/actions/force-unlock"
	status, body, err := env.TFEStatus(ctx, token, http.MethodPost, unlock, `{"data":{}}`)
	if err != nil || status != http.StatusOK {
		t.Fatalf("release the shared lock answers %d, want 200: %v %s", status, err, body)
	}

	if held := tgLocalWorkspaceLock(ctx, t, env, token, id); held != "" {
		t.Fatalf("shared workspace stays locked after the release: %s", held)
	}
}

// TestE2ETerragruntLocalRunAllNoOrder proves that a local run-all records no
// change order: the three workspaces hold their own state versions, the
// units list is the only cross-unit view of the run, and the namespace holds
// no job.
func TestE2ETerragruntLocalRunAllNoOrder(t *testing.T) {
	t.Parallel()

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

	tg, tgErr := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if tgErr != nil {
		t.Fatalf("install %s: %v", e2e.Terragrunt().Name, tgErr)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalNoOrder(t.Context(), t, cli, tg, db)
		})
	}
}

// tgLocalNoOrder runs the no change order scenario on one store.
func tgLocalNoOrder(ctx context.Context, t *testing.T, cli, tg e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, tgLocalNamespace)

	token := env.TokenA()
	for _, unit := range tgLocalUnits() {
		tgLocalCreateWorkspace(ctx, t, env, token, unit)
	}

	runner := tgLocalRunner(ctx, t, env, cli, token)
	runner.tg = tg
	live := tgLocalApplyAll(ctx, t, runner)

	units, err := tgLocalListUnits(ctx, t, env, token)
	if err != nil {
		t.Fatalf("list the units: %v", err)
	}

	seen := map[string]bool{}
	for _, name := range units {
		seen[name] = true
	}

	for i, unit := range tgLocalUnits() {
		if !seen[unit] {
			t.Fatalf("units list lacks %s: %+v", unit, units)
		}

		tgLocalCheckOneVersion(ctx, t, env, token, unit)
		runner.checkOneOutput(ctx, t, live, i, unit)
	}

	tgLocalCheckNoJobs(ctx, t, env, token)
}

// tgLocalCheckNoJobs proves that the local run records no job, and so no
// change order above jobs: the namespace job list is empty.
func tgLocalCheckNoJobs(ctx context.Context, t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	status, body, err := env.APIStatusAs(
		ctx, token, http.MethodGet, "/api/dustpan/v1/namespaces/"+tgLocalNamespace+"/jobs", "",
	)
	if err != nil {
		t.Fatalf("list the jobs: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list the jobs answers %d, want 200: %s", status, body)
	}

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the jobs list: %v", err)
	}

	jobs, ok := doc["jobs"].([]any)
	if !ok {
		t.Fatalf("jobs list has no jobs array: %s", body)
	}

	if len(jobs) != 0 {
		t.Fatalf("local run records %d jobs, want no job and no change order: %s", len(jobs), body)
	}
}

// tgLocalListUnits reads the unit names of the namespace through the
// inventory API with the access token of the run.
func tgLocalListUnits(ctx context.Context, t *testing.T, env *e2e.Env, token string) ([]string, error) {
	t.Helper()

	status, body, err := env.APIStatusAs(
		ctx, token, http.MethodGet, "/api/dustpan/v1/namespaces/"+tgLocalNamespace+"/units", "",
	)
	if err != nil {
		return nil, fmt.Errorf("list the units: %w", err)
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: list the units answers %d: %s", errUnexpected, status, body)
	}

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("decode the units list: %w", err)
	}

	raw, ok := doc["units"].([]any)
	if !ok {
		return nil, fmt.Errorf("%w: units list has no units array: %s", errUnexpected, body)
	}

	names := make([]string, 0, len(raw))

	for _, entry := range raw {
		unit, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: units list holds no unit object: %s", errUnexpected, body)
		}

		name, ok := unit["name"].(string)
		if !ok {
			return nil, fmt.Errorf("%w: a unit holds no name: %s", errUnexpected, body)
		}

		names = append(names, name)
	}

	return names, nil
}

// tgLocalHistoryPath returns the state-version history list of a workspace.
func tgLocalHistoryPath(name string) string {
	return "/api/v2/state-versions?filter[organization][name]=" + tgLocalNamespace + "&filter[workspace][name]=" + name
}

// tgLocalCreateWorkspace pre-creates one workspace, so no creation race
// occurs during the run.
func tgLocalCreateWorkspace(ctx context.Context, t *testing.T, env *e2e.Env, token, name string) {
	t.Helper()

	status, body, err := env.TFEStatus(ctx, token, http.MethodPost,
		"/api/v2/organizations/"+tgLocalNamespace+"/workspaces",
		`{"data":{"type":"workspaces","attributes":{"name":"`+name+`"}}}`)
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}

	if status != http.StatusCreated {
		t.Fatalf("create workspace %s answers %d, want 201: %s", name, status, body)
	}
}

// tgLocalWorkspaceID reads the ID of a workspace by name.
func tgLocalWorkspaceID(ctx context.Context, t *testing.T, env *e2e.Env, token, name string) string {
	t.Helper()

	status, body, err := env.TFEStatus(ctx, token, http.MethodGet,
		"/api/v2/organizations/"+tgLocalNamespace+"/workspaces/"+name, "")
	if err != nil {
		t.Fatalf("read workspace %s: %v", name, err)
	}

	if status != http.StatusOK {
		t.Fatalf("read workspace %s answers %d, want 200: %s", name, status, body)
	}

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode workspace %s: %v", name, err)
	}

	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("workspace %s document has no data: %s", name, body)
	}

	id, ok := data["id"].(string)
	if !ok || id == "" {
		t.Fatalf("workspace %s document has no ID: %s", name, body)
	}

	return id
}

// tgLocalWorkspaceLock reads the lock ID that a workspace document shows, or
// the empty string when the workspace is unlocked.
func tgLocalWorkspaceLock(ctx context.Context, t *testing.T, env *e2e.Env, token, id string) string {
	t.Helper()

	status, body, err := env.TFEStatus(ctx, token, http.MethodGet, "/api/v2/workspaces/"+id, "")
	if err != nil {
		t.Fatalf("read workspace %s: %v", id, err)
	}

	if status != http.StatusOK {
		t.Fatalf("read workspace %s answers %d, want 200: %s", id, status, body)
	}

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode workspace %s: %v", id, err)
	}

	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("workspace %s document has no data: %s", id, body)
	}

	attrs, ok := data["attributes"].(map[string]any)
	if !ok {
		return ""
	}

	held, ok := attrs["lock-id"].(string)
	if !ok {
		return ""
	}

	return held
}

// tgLocalErrorCode reads the machine code of the first error of a JSON:API
// error document.
func tgLocalErrorCode(t *testing.T, body string) string {
	t.Helper()

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode error document %q: %v", body, err)
	}

	errs, ok := doc["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("error document %q holds no error", body)
	}

	first, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("error document %q holds no error object", body)
	}

	code, ok := first["code"].(string)
	if !ok {
		t.Fatalf("error document %q holds no code", body)
	}

	return code
}

// tgLocalDataLen counts the data members of a JSON:API list document.
func tgLocalDataLen(t *testing.T, body string) int {
	t.Helper()

	var doc map[string]any

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode list document: %v", err)
	}

	data, ok := doc["data"].([]any)
	if !ok {
		t.Fatalf("list document has no data array: %s", body)
	}

	return len(data)
}

// tgLocalVersionTotal counts the finalized state versions of a workspace.
func tgLocalVersionTotal(ctx context.Context, t *testing.T, env *e2e.Env, token, name string) int {
	t.Helper()

	status, body, err := env.TFEStatus(ctx, token, http.MethodGet, tgLocalHistoryPath(name), "")
	if err != nil {
		t.Fatalf("list state versions of %s: %v", name, err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions of %s answers %d, want 200: %s", name, status, body)
	}

	return tgLocalDataLen(t, body)
}

// tgLocalCheckOneVersion proves that a workspace holds one state version and
// one history entry: the current version reads back, and the history list
// holds one entry.
func tgLocalCheckOneVersion(ctx context.Context, t *testing.T, env *e2e.Env, token, name string) {
	t.Helper()

	id := tgLocalWorkspaceID(ctx, t, env, token, name)

	status, body, err := env.TFEStatus(
		ctx, token, http.MethodGet, "/api/v2/workspaces/"+id+"/current-state-version", "",
	)
	if err != nil {
		t.Fatalf("read the current state version of %s: %v", name, err)
	}

	if status != http.StatusOK {
		t.Fatalf("current state version of %s answers %d, want 200: %s", name, status, body)
	}

	if got := tgLocalVersionTotal(ctx, t, env, token, name); got != 1 {
		t.Fatalf("workspace %s holds %d state versions, want 1 new state version and 1 history entry", name, got)
	}
}

// tgLocalLiveDirs returns the three live unit directories of tgLocalApplyAll.
func tgLocalLiveDirs() []string {
	return []string{"unit-a", "unit-b", "unit-c"}
}

// tgLocalCLI runs the Terragrunt and OpenTofu CLIs in one live directory with
// the access token of the run.
type tgLocalCLI struct {
	env   *e2e.Env
	cli   e2e.Install
	tg    e2e.Install
	creds string
}

// checkOneOutput proves that the state of one live unit names that unit
// alone: its output holds its own unit directory and no other unit, so no
// state version holds resources from another unit.
func (c tgLocalCLI) checkOneOutput(
	ctx context.Context,
	t *testing.T,
	live string,
	i int,
	unit string,
) {
	t.Helper()

	dirs := tgLocalLiveDirs()
	dir := filepath.Join(live, dirs[i])
	// The credentials file holds the token of the run, written by the runner.
	extra := []string{"TF_CLI_CONFIG_FILE=" + c.creds}

	// Terragrunt runs OpenTofu in its own cache directory, so the unit
	// directory holds no initialized backend: init here points the read at
	// the workspace, and output reads the state of the run.
	res, err := c.env.RunAs(ctx, c.cli, dir, extra, "init", "-input=false", "-no-color")
	if err != nil {
		t.Fatalf("tofu init in %s: %v", dirs[i], err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("tofu init in %s exits %d:\n%s\n%s", dirs[i], res.ExitCode, res.Stdout, res.Stderr)
	}

	res, err = c.env.RunAs(ctx, c.cli, dir, extra, "output", "-json", "-no-color")
	if err != nil {
		t.Fatalf("tofu output in %s: %v", dirs[i], err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("tofu output in %s exits %d:\n%s\n%s", dirs[i], res.ExitCode, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, `"`+dirs[i]+`"`) {
		t.Fatalf("state output of %s names no %s:\n%s", unit, dirs[i], res.Stdout)
	}

	for j, other := range dirs {
		if j != i && strings.Contains(res.Stdout, `"`+other+`"`) {
			t.Fatalf("state output of %s names %s too:\n%s", unit, other, res.Stdout)
		}
	}
}

// tgLocalRunner returns the CLI runner of one scenario.
func tgLocalRunner(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, token string) tgLocalCLI {
	t.Helper()

	_ = ctx

	path := filepath.Join(env.Home(), "tofurc-tg-local")
	runner := tgLocalCLI{env: env, cli: cli, tg: e2e.NoCLI(), creds: path}
	runner.useToken(t, token)

	return runner
}

// useToken points the credentials file of the runner at token.
func (c tgLocalCLI) useToken(t *testing.T, token string) {
	t.Helper()

	creds := "credentials " + fmt.Sprintf("%q", c.env.Host()) + " {\n"
	creds += "  token = " + fmt.Sprintf("%q", token) + "\n}\n"
	c.env.WriteFile(t, c.creds, creds)
}

// runAllResult runs `terragrunt run --all` with args in the live directory.
// returns what it printed. A non-zero exit is reported in the Result, not as
// an error.
func (c tgLocalCLI) runAllResult(ctx context.Context, t *testing.T, live string, args ...string) (e2e.Result, error) {
	t.Helper()

	if c.tg.Binary() == "" {
		t.Fatal("run-all needs the pinned Terragrunt binary, not a direct tofu call")
	}

	extra := []string{"TF_CLI_CONFIG_FILE=" + c.creds, "TG_TF_PATH=" + c.cli.Binary()}

	return c.env.RunAs(ctx, c.tg, live, extra, args...)
}

// runAll runs `terragrunt run --all` with args in the live directory, with the
// OpenTofu binary of the fetcher as its TG_TF_PATH, and fails the test when
// the run fails. No test calls tofu directly in place of Terragrunt.
func (c tgLocalCLI) runAll(ctx context.Context, t *testing.T, live string, args ...string) {
	t.Helper()

	res, err := c.runAllResult(ctx, t, live, args...)
	if err != nil {
		t.Fatalf("terragrunt run --all %s: %v", strings.Join(args, " "), err)
	}

	if res.ExitCode != 0 {
		t.Fatalf(
			"terragrunt run --all %s exits %d:\n%s\n%s",
			strings.Join(args, " "),
			res.ExitCode,
			res.Stdout,
			res.Stderr,
		)
	}
}

// tgLocalLiveRoot writes the root Terragrunt configuration of the fixture to
// the live directory as root.hcl, so the run reads the fixture without
// running the root as a unit of its own.
func tgLocalLiveRoot(t *testing.T, live string) {
	t.Helper()

	if tgLocalRootHCL == "" {
		t.Fatal("root fixture terragrunt.hcl is empty")
	}

	if err := os.MkdirAll(live, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", live, err)
	}

	if err := os.WriteFile(filepath.Join(live, "root.hcl"), []byte(tgLocalRootHCL), 0o600); err != nil {
		t.Fatalf("write the live root.hcl: %v", err)
	}
}

// writeLiveUnit writes one unit directory under the live directory with a
// backend that names the workspace and the per-unit Terragrunt configuration
// of the fixture.
func (c tgLocalCLI) writeLiveUnit(t *testing.T, live, dir, workspace string) {
	t.Helper()

	unitDir := filepath.Join(live, dir)
	if err := os.MkdirAll(unitDir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", unitDir, err)
	}
	config := "terraform {\n  cloud {\n    hostname     = " + fmt.Sprintf("%q", c.env.Host()) + "\n" +
		"    organization = " + fmt.Sprintf("%q", tgLocalNamespace) + "\n" +
		"    workspaces {\n      name = " + fmt.Sprintf("%q", workspace) + "\n    }\n  }\n}\n\n" +
		"output \"unit\" {\n  value = " + fmt.Sprintf("%q", dir) + "\n}\n"
	c.env.WriteFile(t, filepath.Join(unitDir, "main.tf"), config)
	c.env.WriteFile(t, filepath.Join(unitDir, "terragrunt.hcl"), tgLocalUnitHCL)
}

// tgLocalApplyAll writes the three unit directories under one live directory,
// each with a backend that names its own workspace, and applies every unit
// with the real `terragrunt run --all apply` at parallelism 3 with no lock
// timeout. It returns the live directory.
func tgLocalApplyAll(ctx context.Context, t *testing.T, runner tgLocalCLI) string {
	t.Helper()

	live := filepath.Join(runner.env.Home(), "tg-local-live")
	tgLocalLiveRoot(t, live)

	dirs := tgLocalLiveDirs()

	for i, unit := range tgLocalUnits() {
		runner.writeLiveUnit(t, live, dirs[i], unit)
	}

	runner.runAll(ctx, t, live, "run", "--all", "--non-interactive", "--parallelism", "3",
		"--", "apply", "-input=false", "-auto-approve", "-lock-timeout=0s")

	return live
}

// tgLocalIssueUnitPerson creates a person with a writer grant on each named
// unit workspace only, and returns the access token of the person.
func tgLocalIssueUnitPerson(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	name string,
	units []string,
) string {
	t.Helper()

	var principal e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		fmt.Sprintf(`{"subject":%q,"kind":"person"}`, e2e.SignInSubject(name+"@example.com")), http.StatusCreated,
	}, &principal)

	for _, unit := range units {
		grantPath := "/api/server/namespaces/" + tgLocalNamespace + "/unit-grants?tool=opentofu&unit=" + unit
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, grantPath,
			`{"subject":"principal:` + principal.ID + `","role":"writer"}`, http.StatusCreated,
		}, nil)
	}

	// Every tofu command reads the organization settings, so the namespace
	// must exist for the run token before its first tofu run. The run holds
	// a custom role with namespace:read only: no built-in role reads the
	// namespace without reading every unit, and the ungranted unit must
	// stay refused.
	var role e2eRole

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles",
		`{"name":"tg-local-runner","actions":["namespace:read"]}`, http.StatusCreated,
	}, &role)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + tgLocalNamespace + "/grants",
		`{"subject":"principal:` + principal.ID + `","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	return env.PersonalToken(ctx, t, principal.ID, "tg-local-"+name).Secret
}
