//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tgLocalNamespace holds the workspaces of the local run-all scenarios. Each
// Terragrunt unit talks to its own workspace in this namespace.
const tgLocalNamespace = "terragrunt-local"

// tgLocalUnits returns the three units of the run, each with its own
// workspace of the same name.
func tgLocalUnits() []string {
	return []string{"terragrunt-local-a", "terragrunt-local-b", "terragrunt-local-c"}
}

// TestE2ETerragruntLocalRunAllThreeWorkspaces proves that a local run-all over
// three units reaches three workspaces: each workspace holds one new state
// version and one history entry. It runs on every store. It runs the same
// per-unit applies that the Terragrunt units describe, through the pinned
// OpenTofu binary over TLS: each unit directory names its own workspace in
// its backend, as `terragrunt run-all apply` would reach it.
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

	for _, version := range e2e.OpenTofu().Versions() {
		cli, installErr := fetcher.Install(installCtx, e2e.OpenTofu(), version)
		if installErr != nil {
			t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, version, installErr)
		}

		for _, db := range databases {
			t.Run(version+"/"+string(db), func(t *testing.T) {
				t.Parallel()
				tgLocalThreeWorkspaces(t.Context(), t, cli, db)
			})
		}
	}
}

// tgLocalThreeWorkspaces runs the three-unit scenario on one store.
func tgLocalThreeWorkspaces(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
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

	tgLocalApplyAll(ctx, t, env, cli, token)

	for _, unit := range tgLocalUnits() {
		tgLocalCheckOneVersion(ctx, t, env, token, unit)
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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalRefusedUnit(t.Context(), t, cli, db)
		})
	}
}

// tgLocalRefusedUnit runs the ungranted unit scenario on one store.
func tgLocalRefusedUnit(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
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

	// The ungranted unit is refused on its state versions with the access
	// token of the run.
	status, _, err := env.TFEStatus(ctx, runner, http.MethodGet, tgLocalHistoryPath(units[2]), "")
	if err != nil {
		t.Fatalf("list state versions of the ungranted unit: %v", err)
	}

	if status == http.StatusOK {
		t.Fatal("ungranted unit lists state versions, want a refusal")
	}

	// The granted units apply with the same token, and each holds one new
	// state version and one history entry.
	granted := tgLocalRunner(ctx, t, env, cli, runner)
	for i, unit := range units[:2] {
		dir := "granted"
		if i == 1 {
			dir = "granted-second"
		}

		granted.writeUnit(t, dir, unit)
		granted.tofu(ctx, t, dir, "init", "-input=false", "-no-color")
		granted.tofu(ctx, t, dir, "apply", "-input=false", "-auto-approve", "-no-color")
	}

	for _, unit := range units[:2] {
		tgLocalCheckOneVersion(ctx, t, env, runner, unit)
	}
}

// TestE2ETerragruntLocalRunAllSharedName proves that two unit directories
// that name one workspace share its lock and its state-version chain: the
// second writer is refused with workspace.locked at parallelism 3 with no
// lock timeout, and its write lands after the first apply releases the lock.
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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalSharedName(t.Context(), t, cli, db)
		})
	}
}

// tgLocalSharedName runs the shared name scenario on one store.
func tgLocalSharedName(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
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

	runner := tgLocalRunner(ctx, t, env, cli, token)
	runner.writeUnit(t, "shared-a", unit)
	runner.writeUnit(t, "shared-b", unit)
	runner.tofu(ctx, t, "shared-a", "init", "-input=false")
	runner.tofu(ctx, t, "shared-b", "init", "-input=false")

	id := tgLocalWorkspaceID(ctx, t, env, token, unit)

	// The first unit holds the lock, as its apply would.
	if status, body, err := env.TFEStatus(ctx, token, http.MethodPost, lockRoute(id), `{"data":{}}`); err != nil ||
		status != http.StatusOK {
		t.Fatalf("lock %s answers %d: %v %s", unit, status, err, body)
	}

	tgLocalRefusedSharedApply(ctx, t, env, runner)
	tgLocalReleaseSharedLock(ctx, t, env, token, id)
	runner.tofu(ctx, t, "shared-a", "apply", "-input=false", "-auto-approve", "-no-color")
	runner.tofu(ctx, t, "shared-b", "apply", "-input=false", "-auto-approve", "-no-color")

	if got := tgLocalVersionTotal(ctx, t, env, token, unit); got != 2 {
		t.Fatalf("shared workspace holds %d state versions, want 2 in one chain", got)
	}
}

// tgLocalRefusedSharedApply proves that the second shared unit, at
// parallelism 3 with no lock timeout, does not wait for the held lock.
func tgLocalRefusedSharedApply(ctx context.Context, t *testing.T, env *e2e.Env, runner tgLocalCLI) {
	t.Helper()

	res, err := env.RunIn(ctx, filepath.Join(env.Home(), "tg-local-shared-b"),
		[]string{"TF_CLI_CONFIG_FILE=" + runner.creds}, "",
		"apply", "-input=false", "-auto-approve", "-no-color", "-parallelism=3", "-lock-timeout=0s")
	if err != nil {
		t.Fatalf("tofu apply of the second shared unit: %v", err)
	}

	if res.ExitCode == 0 {
		t.Fatalf("second shared unit applies over a held lock, want a refusal:\n%s", res.Stdout)
	}

	if !strings.Contains(res.Stderr, "workspace.locked") && !strings.Contains(res.Stdout, "workspace.locked") {
		t.Fatalf("second shared unit names no workspace.locked error:\n%s\n%s", res.Stdout, res.Stderr)
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

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			tgLocalNoOrder(t.Context(), t, cli, db)
		})
	}
}

// tgLocalNoOrder runs the no change order scenario on one store.
func tgLocalNoOrder(ctx context.Context, t *testing.T, cli e2e.Install, store e2e.Store) {
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

	tgLocalApplyAll(ctx, t, env, cli, token)

	units, err := tgLocalListUnits(ctx, t, env, token)
	if err != nil {
		t.Fatalf("list the units: %v", err)
	}

	seen := map[string]bool{}
	for _, name := range units {
		seen[name] = true
	}

	for _, unit := range tgLocalUnits() {
		if !seen[unit] {
			t.Fatalf("units list lacks %s: %+v", unit, units)
		}

		tgLocalCheckOneVersion(ctx, t, env, token, unit)
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

// tgLocalCLI runs the OpenTofu CLI in one unit directory with the access
// token of the run.
type tgLocalCLI struct {
	env   *e2e.Env
	cli   e2e.Install
	creds string
}

// tgLocalRunner returns the CLI runner of one scenario.
func tgLocalRunner(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, token string) tgLocalCLI {
	t.Helper()

	_ = ctx

	path := filepath.Join(env.Home(), "tofurc-tg-local")
	creds := "credentials " + fmt.Sprintf("%q", env.Host()) + " {\n"
	creds += "  token = " + fmt.Sprintf("%q", token) + "\n}\n"
	env.WriteFile(t, path, creds)

	return tgLocalCLI{env: env, cli: cli, creds: path}
}

// writeUnit writes one unit directory with a backend that names the
// workspace.
func (c tgLocalCLI) writeUnit(t *testing.T, dir, workspace string) {
	t.Helper()

	unitDir := filepath.Join(c.env.Home(), "tg-local-"+dir)
	config := "terraform {\n  cloud {\n    hostname     = " + fmt.Sprintf("%q", c.env.Host()) + "\n" +
		"    organization = " + fmt.Sprintf("%q", tgLocalNamespace) + "\n" +
		"    workspaces {\n      name = " + fmt.Sprintf("%q", workspace) + "\n    }\n  }\n}\n\n" +
		"output \"unit\" {\n  value = " + fmt.Sprintf("%q", dir) + "\n}\n"
	c.env.WriteFile(t, filepath.Join(unitDir, "main.tf"), config)
}

// tofu runs one OpenTofu command in one unit directory, and fails the test
// when the command fails.
func (c tgLocalCLI) tofu(ctx context.Context, t *testing.T, dir string, args ...string) {
	t.Helper()

	res, err := c.env.RunAs(ctx, c.cli, filepath.Join(c.env.Home(), "tg-local-"+dir),
		[]string{"TF_CLI_CONFIG_FILE=" + c.creds}, args...)
	if err != nil {
		t.Fatalf("tofu %s in %s: %v", strings.Join(args, " "), dir, err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("tofu %s in %s exits %d:\n%s\n%s", strings.Join(args, " "), dir, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// tgLocalApplyAll writes the three unit directories, each with a backend
// that names its own workspace, and applies every unit through the pinned
// OpenTofu binary: the same backend calls that `terragrunt run-all apply`
// makes.
func tgLocalApplyAll(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, token string) {
	t.Helper()

	runner := tgLocalRunner(ctx, t, env, cli, token)
	dirs := []string{"unit-a", "unit-b", "unit-c"}

	for i, unit := range tgLocalUnits() {
		runner.writeUnit(t, dirs[i], unit)
	}

	for _, dir := range dirs {
		runner.tofu(ctx, t, dir, "init", "-input=false", "-no-color")
		runner.tofu(ctx, t, dir, "apply", "-input=false", "-auto-approve", "-no-color")
	}
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

	return env.PersonalToken(ctx, t, principal.ID, "tg-local-"+name).Secret
}
