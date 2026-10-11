//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

const (
	stackName = "dev"
	// installLimit plus every step's commandTimeout must stay under the
	// go test -timeout in the Makefile, or a timeout loses every report. That
	// holds per version and store only while all runs go at once, so the
	// Makefile's -parallel must cover the whole matrix (12 runs by default: 6
	// versions on 2 stores).
	installLimit = 10 * time.Minute
)

var errUnexpected = errors.New("unexpected output")

func cacheDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("E2E_CACHE_DIR"); dir != "" {
		return dir
	}

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("find user cache dir: %v", err)
	}

	return filepath.Join(base, "opendustpan-e2e")
}

// TestCLIs runs each tool's scenario against every version in its matrix.
// Reports from both tools land in one summary.
func TestCLIs(t *testing.T) {
	t.Parallel()

	reports := e2e.ReportDir()
	if err := e2e.ClearReports(reports); err != nil {
		t.Fatalf("clear old reports: %v", err)
	}

	// Runs after every parallel subtest has finished and written its report.
	t.Cleanup(func() {
		if err := e2e.WriteSummary(reports); err != nil {
			t.Errorf("write summary: %v", err)
		}
	})

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	matrix := []struct {
		scenario func(*testing.T, *e2e.Env)
		tool     e2e.Tool
	}{
		{scenario: pulumiScenario, tool: e2e.Pulumi()},
		{scenario: tofuScenario, tool: e2e.OpenTofu()},
	}

	for _, m := range matrix {
		t.Run(m.tool.Name, func(t *testing.T) {
			t.Parallel()

			for _, label := range m.tool.Versions() {
				t.Run(label, func(t *testing.T) {
					t.Parallel()

					run(t, runInput{
						fetcher:   fetcher,
						tool:      m.tool,
						label:     label,
						databases: databases,
						scenario:  m.scenario,
					})
				})
			}
		})
	}
}

// runInput groups what one CLI version needs to run on every store.
type runInput struct {
	fetcher   *e2e.Fetcher
	tool      e2e.Tool
	label     string
	databases []e2e.Store
	scenario  func(*testing.T, *e2e.Env)
}

func run(t *testing.T, in runInput) {
	t.Helper()

	tool := in.tool
	label := in.label
	databases := in.databases

	if tool.Name == e2e.ToolTofu && runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		reason := "needs Linux, not " + runtime.GOOS
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label, db, reason, true))
		}

		t.Skipf("the OpenTofu suite %s", reason)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := in.fetcher.Install(installCtx, tool, label)
	if err != nil {
		// Still report the version, so a broken download is visible in the summary.
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label, db, err.Error(), false))
		}

		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runOnStore(t, cli, db, in.scenario)
		})
	}
}

func runOnStore(t *testing.T, cli e2e.Install, store e2e.Store, scenario func(*testing.T, *e2e.Env)) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		reason := "set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite"
		writeReport(t, e2e.NotRun(cli.Tool, cli.Label, store, reason, true))
		t.Skip(reason)
	}

	// If server setup fails, NewEnv ends the test before env exists; report
	// the version as not run instead of dropping it from the summary.
	var env *e2e.Env

	t.Cleanup(func() {
		if env == nil {
			writeReport(t, e2e.NotRun(cli.Tool, cli.Label, store, "server setup failed; see the test log", false))

			return
		}

		writeReport(t, e2e.NewRun(env))
	})

	env = e2e.NewEnv(t.Context(), t, cli, store)

	scenario(t, env)
}

func writeReport(t *testing.T, r e2e.Run) {
	t.Helper()

	if err := e2e.WriteRun(e2e.ReportDir(), r); err != nil {
		t.Errorf("write report: %v", err)
	}
}

func contains(want string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, want) {
			return fmt.Errorf("%w: stdout lacks %q", errUnexpected, want)
		}

		return nil
	}
}

// tagListed checks that the tag table lists name with value: one line whose
// first two fields are the name and the value.
func tagListed(name, value string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for line := range strings.Lines(r.Stdout) {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == name && fields[1] == value {
				return nil
			}
		}

		return fmt.Errorf("%w: stdout lacks tag %q with value %q", errUnexpected, name, value)
	}
}

// tagAbsent checks that the tag table lists no line for name.
func tagAbsent(name string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for line := range strings.Lines(r.Stdout) {
			if fields := strings.Fields(line); len(fields) >= 1 && fields[0] == name {
				return fmt.Errorf("%w: stdout still lists tag %q", errUnexpected, name)
			}
		}

		return nil
	}
}

// step is a scenario step with no extra environment.
func step(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Fail: false}
}

// failStep is a scenario step that expects the CLI to fail, for steps that
// prove an error message.
func failStep(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Fail: true}
}

// outputContains reports whether stdout or stderr holds want. Failing CLI
// commands print the server error to stderr, so error steps check both.
func outputContains(want string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, want) && !strings.Contains(r.Stderr, want) {
			return fmt.Errorf("%w: output lacks %q", errUnexpected, want)
		}

		return nil
	}
}

// checkReady proves GET /health/ready answers 200 with {"status":"ok"} after
// the server starts, on every store. It sends no token: the probe is public.
func checkReady(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/health/ready", "")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET /health/ready status %d, want %d (body %s)", status, http.StatusOK, body)
	}

	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("GET /health/ready body %q, want status ok", body)
	}
}

// pulumiScenario exercises login and stack management. A namespace is a
// Pulumi organization, so every step that names or implies an organization
// should resolve to the Namespace namespace.
func pulumiScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)
	renamedQualified := fmt.Sprintf("%s/%s/renamed", org, e2e.Project)
	renamePath := fmt.Sprintf("/api/stacks/%s/%s/%s/rename", org, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", org, e2e.Project)
	scratch := fmt.Sprintf("%s/%s/scratch", org, e2e.Project)
	resourced := fmt.Sprintf("%s/%s/resourced", org, e2e.Project)
	resourcedPath := fmt.Sprintf("/api/stacks/%s/%s/resourced", org, e2e.Project)

	for _, s := range pulumiSetupSteps(env, org, qualified, stackPath) {
		env.Step(ctx, t, s)
	}

	for _, s := range []e2e.StepSpec{
		step("preview", nil, "preview", "--non-interactive"),
		// A preview changes no resources, so it leaves no history.
		step("stack history hides previews", historyHidesPreviews, "stack", "history", "--json"),
	} {
		env.Step(ctx, t, s)
	}

	// Previews take no lock, so two at once on one stack both work.
	env.StepParallel(ctx, t, e2e.ParallelSpec{
		Name:  "concurrent previews",
		Check: nil,
		Args: [][]string{
			{"preview", "--non-interactive"},
			{"preview", "--non-interactive"},
		},
		Env:  nil,
		Envs: nil,
	})

	beforeID := stackIDBeforeRename(t, env, org)

	env.Step(ctx, t, e2e.StepSpec{
		Check: checkRenameKeptID(ctx, env, org, renamePath, beforeID),
		Name:  "stack rename",
		Args:  []string{"stack", "rename", renamedQualified, "--stack", qualified},
		Env:   nil,
		Fail:  false,
	})

	// A rename keeps the stack ID, so its secrets still decrypt.
	env.Step(ctx, t, step(
		"config get after rename",
		contains("s3cr3t"),
		"config", "get", "--stack", renamedQualified, "apiKey",
	))

	// Tags follow the stack through a rename.
	env.Step(ctx, t, step("stack tag ls after rename", tagListed("team", "e2e"), "stack", "tag", "ls"))
	env.Step(ctx, t, step("stack tag rm", nil, "stack", "tag", "rm", "team"))
	env.Step(ctx, t, step("stack tag ls after rm", tagAbsent("team"), "stack", "tag", "ls"))

	env.Step(ctx, t, step("up", checkCheckpointSaved(env, "up"), "up", "--yes", "--non-interactive"))
	exported := env.Step(ctx, t, step("stack export after up", exportHoldsReference, "stack", "export"))
	env.Step(ctx, t, step("stack export hides secrets", exportHidesSecrets, "stack", "export"))
	env.Step(ctx, t, step("stack history after up", historyShowsKinds("update"), "stack", "history", "--json"))

	// An export round-trips through import: the deployment is saved to a
	// file and imported back, which records an import update. State does not
	// change, so the next up has nothing to do.
	importPath := writeImportFile(t, exported.Stdout)
	env.Step(ctx, t, step("stack import", nil, "stack", "import", "--file", importPath))
	env.Step(
		ctx,
		t,
		step("stack history after import", historyShowsKinds("update", "import"), "stack", "history", "--json"),
	)
	env.Step(ctx, t, step("up after import", upHasNoChanges, "up", "--yes", "--non-interactive"))

	// Version 1 keeps its checkpoint after later updates, so exporting it
	// returns the first state. A version that does not exist has no state.
	env.Step(ctx, t, step("stack export version 1", exportHoldsReference, "stack", "export", "--version", "1"))
	env.Step(
		ctx,
		t,
		failStep("stack export missing version", outputContains("no state"), "stack", "export", "--version", "999999"),
	)
	env.Step(
		ctx,
		t,
		step("refresh", checkCheckpointSaved(env, "refresh"), "refresh", "--yes", "--non-interactive"),
	)
	env.Step(ctx, t, step("destroy", nil, "destroy", "--yes", "--non-interactive"))
	env.Step(
		ctx,
		t,
		step(
			"stack history after destroy",
			historyShowsKinds("update", "refresh", "destroy"),
			"stack",
			"history",
			"--json",
		),
	)

	// A second update cannot start while one runs: hold the stack lock
	// through the API, prove the CLI reports the conflict, then release it.
	// The stack is the renamed one, which up, refresh and destroy ran on.
	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	env.Step(ctx, t, failStep(
		"second up while one runs",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))
	env.Step(ctx, t, failStep(
		"stack import while one runs",
		outputContains("another update of this stack is in progress"),
		"stack", "import", "--file", importPath,
	))
	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.

	// A crashed CLI leaves its update running: hold the stack through the
	// API as the orphaned update, save the up's checkpoint with the crash
	// marker as the engine left it, prove the next up is blocked, cancel
	// through the CLI in a second process, prove the checkpoint survived
	// cancel, then up works.
	held = env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	env.SaveHeldCheckpoint(ctx, t, held, exported.Stdout)
	env.Step(ctx, t, failStep(
		"up blocked after crash",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))
	env.Step(ctx, t, step("cancel after crash", contains("has been canceled"), "cancel", "--yes"))
	env.Step(
		ctx,
		t,
		step("stack export preserves checkpoint", contains("crash-marker"), "stack", "export"),
	)
	env.Step(
		ctx,
		t,
		step("up after cancel", checkCheckpointSaved(env, "up after cancel"), "up", "--yes", "--non-interactive"),
	)
	env.Step(ctx, t, step("stack export after cancel", exportHoldsReference, "stack", "export"))

	// A ciphertext is bound to its stack: another stack cannot decrypt it.
	env.Step(ctx, t, step(
		"decrypt rejects a foreign ciphertext",
		checkForeignDecrypt(ctx, env),
		"stack", "--show-name",
	))

	// Cancel frees a running update from a second process: hold the stack
	// through the API while an update runs, cancel through the CLI, then
	// up works at once.
	env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	env.Step(ctx, t, step("cancel running update", contains("has been canceled"), "cancel", "--yes"))
	env.Step(ctx, t, step("up after concurrent cancel", nil, "up", "--yes", "--non-interactive"))

	for _, s := range pulumiTeardownSteps(scratch, resourced) {
		env.Step(ctx, t, s)
	}

	// The server cannot record resources through routes that later issues
	// own, so the test seeds a checkpoint with one managed resource.
	env.SeedResources(ctx, t, "resourced")

	for _, s := range pulumiResourcedRemoveSteps(env, resourced, resourcedPath) {
		env.Step(ctx, t, s)
	}

	// A second namespace, so the list-all step proves stacks from every
	// readable namespace. It is created late so the default-organization
	// step above still sees one namespace.
	env.CreateNamespace(ctx, t, e2e.Namespace2)

	second := fmt.Sprintf("%s/%s/%s", e2e.Namespace2, e2e.Project, stackName)
	for _, s := range []e2e.StepSpec{
		step("stack init second namespace", nil, "stack", "init", second, "--no-select"),
		step("stack ls all namespaces", bothStacksListed, "stack", "ls", "--json", "--all"),
		failStep(
			"missing subject namespace names the fix",
			outputContains("pulumi org set-default"),
			"stack", "init", e2e.Subject+"/"+e2e.Project+"/ghost",
		),
	} {
		env.Step(ctx, t, s)
	}

	consoleURLSteps(t, env)
}

// pulumiSetupSteps returns the scenario steps before the rename: login
// through a second stack init in the default organization.
func pulumiSetupSteps(env *e2e.Env, org, qualified, stackPath string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("whoami", func(r e2e.Result) error {
			if got := strings.TrimSpace(r.Stdout); got != e2e.Subject {
				return fmt.Errorf("%w: whoami printed %q, want %q", errUnexpected, got, e2e.Subject)
			}

			return nil
		}, "whoami"),
		// The namespaces a principal can use are its organizations.
		step("whoami lists organizations", contains("Organizations: "+org), "whoami", "--verbose"),
		// With one namespace, it is the default organization.
		step("org get-default", contains(org), "org", "get-default"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack ls", stackListed, "stack", "ls", "--json"),
		step("stack ls in organization", stackListed, "stack", "ls", "--json", "--organization", org),
		step("stack ls all projects", stackListed, "stack", "ls", "--json", "--all"),
		step("stack select", nil, "stack", "select", qualified),
		step("stack show-name", contains(stackName), "stack", "--show-name"),
		step("stack tag set", nil, "stack", "tag", "set", "team", "e2e"),
		step("stack tag ls", tagListed("team", "e2e"), "stack", "tag", "ls"),
		// The service backend encrypts secrets on the server, per stack.
		// The list command hides secret values unless asked to show them,
		// while config get prints the plaintext by design.
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("config get shows the secret", contains("s3cr3t"), "config", "get", "apiKey"),
		step("config hides the secret", configListHidesSecret, "config"),
		step("config shows the secret", contains("s3cr3t"), "config", "--show-secrets"),
		step("stack export", nil, "stack", "export"),
		{
			// An unqualified stack name takes the default organization.
			Check: func(e2e.Result) error {
				if !env.Requested("stack init default org", http.MethodPost, stackPath) {
					return fmt.Errorf("%w: stack was not created in namespace %q", errUnexpected, org)
				}

				return nil
			},
			Name: "stack init default org",
			Args: []string{"stack", "init", "other"},
			Env:  nil,
			Fail: false,
		},
	}
}

// pulumiTeardownSteps returns the scenario steps after the rename: a stack of
// its own, so the result does not depend on rename, removed with stack rm.
// It then creates the stack that the resourced steps remove.
func pulumiTeardownSteps(scratch, resourced string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("stack init scratch", nil, "stack", "init", scratch, "--no-select"),
		step("stack rm", nil, "stack", "rm", "--yes", "--stack", scratch),
		// A stack of its own for the delete with resources below.
		step("stack init resourced", nil, "stack", "init", resourced, "--no-select"),
	}
}

// pulumiResourcedRemoveSteps returns the steps that remove a stack with
// resources: the CLI refuses without --force and removes it with --force.
func pulumiResourcedRemoveSteps(env *e2e.Env, resourced, resourcedPath string) []e2e.StepSpec {
	return []e2e.StepSpec{
		{
			Check: func(r e2e.Result) error {
				if out := r.Stdout + r.Stderr; !strings.Contains(out, "still has resources") {
					return fmt.Errorf("%w: output lacks the refusal: %s", errUnexpected, out)
				}

				if !env.Requested("stack rm with resources without force", http.MethodDelete, resourcedPath) {
					return fmt.Errorf("%w: stack was not delete-requested", errUnexpected)
				}

				return nil
			},
			Name: "stack rm with resources without force",
			Args: []string{"stack", "rm", "--yes", "--stack", resourced},
			Env:  nil,
			Fail: true,
		},
		step("stack rm with resources with force",
			func(_ e2e.Result) error {
				if !env.Requested("stack rm with resources with force", http.MethodDelete, resourcedPath) {
					return fmt.Errorf("%w: stack was not delete-requested", errUnexpected)
				}

				return nil
			}, "stack", "rm", "--yes", "--force", "--stack", resourced),
	}
}

// stackIDBeforeRename returns the id of the dev stack, failing the test when
// the stack is missing.
func stackIDBeforeRename(t *testing.T, env *e2e.Env, org string) string {
	t.Helper()

	id, status, err := env.StackID(t.Context(), org, e2e.Project, stackName)
	if err != nil || status != http.StatusOK {
		t.Fatalf("get stack before rename: status %d, err %v", status, err)
	}

	return id
}

// checkRenameKeptID checks that the CLI renamed through renamePath, that the
// renamed stack keeps beforeID and that the old name is gone.
func checkRenameKeptID(
	ctx context.Context, env *e2e.Env, org, renamePath, beforeID string,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		if !env.Requested("stack rename", http.MethodPost, renamePath) {
			return fmt.Errorf("%w: CLI did not POST %s", errUnexpected, renamePath)
		}

		afterID, status, err := env.StackID(ctx, org, e2e.Project, "renamed")
		if err != nil {
			return fmt.Errorf("%w: get renamed stack: %w", errUnexpected, err)
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: renamed stack answers %d", errUnexpected, status)
		}

		if afterID != beforeID {
			return fmt.Errorf("%w: stack id changed from %q to %q", errUnexpected, beforeID, afterID)
		}

		oldID, status, err := env.StackID(ctx, org, e2e.Project, stackName)
		if err != nil {
			return fmt.Errorf("%w: get old stack name: %w", errUnexpected, err)
		}

		if status != http.StatusNotFound {
			return fmt.Errorf("%w: old stack name still answers %d with id %q", errUnexpected, status, oldID)
		}

		return nil
	}
}

// bothStacksListed checks that stack ls --all lists the dev stack in both
// namespaces. The CLI prints each stack as "<organization>/<stack>", so the
// organization is the part before the slash.
func bothStacksListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	seen := map[string]bool{}

	for _, s := range stacks {
		if org, _, ok := strings.Cut(s.Name, "/"); ok {
			seen[org] = true
		}
	}

	for _, want := range []string{e2e.Namespace, e2e.Namespace2} {
		if !seen[want] {
			return fmt.Errorf("%w: no stack listed in namespace %q: %s", errUnexpected, want, r.Stdout)
		}
	}

	return nil
}

func stackListed(r e2e.Result) error {
	var stacks []struct {
		Name string `json:"name"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &stacks); err != nil {
		return fmt.Errorf("%w: decode stack list: %w", errUnexpected, err)
	}

	for _, s := range stacks {
		if strings.HasSuffix(s.Name, stackName) {
			return nil
		}
	}

	return fmt.Errorf("%w: stack %q not listed: %s", errUnexpected, stackName, r.Stdout)
}

// historyHidesPreviews fails when the stack history holds any update: a
// preview changes nothing, so it must leave no entry.
func historyHidesPreviews(r e2e.Result) error {
	var updates []struct {
		Kind string `json:"kind"`
	}

	if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
		return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
	}

	if len(updates) != 0 {
		return fmt.Errorf("%w: stack history holds %d updates, want none after a preview", errUnexpected, len(updates))
	}

	return nil
}

// historyShowsKinds proves that stack history lists an entry of each kind,
// such as the update, refresh and destroy the scenario ran.
func historyShowsKinds(kinds ...string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var updates []struct {
			Kind string `json:"kind"`
		}

		if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
			return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
		}

		seen := map[string]bool{}
		for _, u := range updates {
			seen[u.Kind] = true
		}

		for _, kind := range kinds {
			if !seen[kind] {
				return fmt.Errorf("%w: stack history lacks kind %q: %s", errUnexpected, kind, r.Stdout)
			}
		}

		return nil
	}
}

// writeImportFile saves an exported deployment for a later stack import
// step and returns its path.
func writeImportFile(t *testing.T, deployment string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stack.json")
	if err := os.WriteFile(path, []byte(deployment), 0o600); err != nil {
		t.Fatalf("write import file: %v", err)
	}

	return path
}

// upHasNoChanges proves an up changed nothing: the engine reported its
// resources unchanged.
func upHasNoChanges(r e2e.Result) error {
	if strings.Contains(r.Stdout, "unchanged") {
		return nil
	}

	return fmt.Errorf("%w: up lacks an unchanged report: %s", errUnexpected, r.Stdout)
}

// exportHoldsReference proves that stack export after up holds the stack
// reference resource: the checkpoint survived the round trip.
func exportHoldsReference(r e2e.Result) error {
	if !strings.Contains(r.Stdout, "pulumi:pulumi:StackReference") {
		return fmt.Errorf("%w: export lacks the stack reference resource: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// configListHidesSecret proves that config list without --show-secrets never
// prints the plaintext secret.
func configListHidesSecret(r e2e.Result) error {
	if strings.Contains(r.Stdout, "s3cr3t") {
		return fmt.Errorf("%w: config printed the plaintext secret: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// exportHidesSecrets proves that stack export keeps secrets encrypted: the
// checkpoint survives the round trip without its plaintext.
func exportHidesSecrets(r e2e.Result) error {
	if err := exportHoldsReference(r); err != nil {
		return err
	}

	if strings.Contains(r.Stdout, "s3cr3t") {
		return fmt.Errorf("%w: stack export printed the plaintext secret", errUnexpected)
	}

	return nil
}

// checkForeignDecrypt proves that a ciphertext is bound to its stack: it
// encrypts on the renamed stack, decrypts on the same stack, and proves that
// the other stack cannot decrypt the ciphertext.
func checkForeignDecrypt(ctx context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		renamed := fmt.Sprintf("/api/stacks/%s/%s/renamed", e2e.Namespace, e2e.Project)
		other := fmt.Sprintf("/api/stacks/%s/%s/other", e2e.Namespace, e2e.Project)

		status, body, err := env.APIStatus(ctx, http.MethodPost, renamed+"/encrypt", `{"plaintext":"aGk="}`)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: encrypt answers %d: %s", errUnexpected, status, body)
		}

		var encrypted struct {
			Ciphertext string `json:"ciphertext"`
		}

		if decodeErr := json.Unmarshal([]byte(body), &encrypted); decodeErr != nil || encrypted.Ciphertext == "" {
			return fmt.Errorf("%w: encrypt answers no ciphertext: %s", errUnexpected, body)
		}

		decryptBody := fmt.Sprintf(`{"ciphertext":%q}`, encrypted.Ciphertext)

		status, body, err = env.APIStatus(ctx, http.MethodPost, renamed+"/decrypt", decryptBody)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			return fmt.Errorf("%w: own decrypt answers %d: %s", errUnexpected, status, body)
		}

		status, body, err = env.APIStatus(ctx, http.MethodPost, other+"/decrypt", decryptBody)
		if err != nil {
			return err
		}

		if status != http.StatusBadRequest {
			return fmt.Errorf(
				"%w: foreign decrypt answers %d, want %d: %s",
				errUnexpected,
				status,
				http.StatusBadRequest,
				body,
			)
		}

		return nil
	}
}

// checkCheckpointSaved proves that the CLI stored its checkpoint through
// PATCH .../update/{id}/checkpoint during the named step.
func checkCheckpointSaved(env *e2e.Env, stepName string) func(e2e.Result) error {
	return func(e2e.Result) error {
		entries := env.Recorder.Entries()
		for i := range entries {
			if entries[i].Step == stepName && entries[i].Method == http.MethodPatch &&
				strings.HasSuffix(entries[i].Path, "/checkpoint") && entries[i].Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: no PATCH checkpoint answered 200 during %q", errUnexpected, stepName)
	}
}

// consoleURLSteps records how the CLI derives console links from the backend
// URL. `pulumi login` prints "(<console URL of the user>)", empty when the CLI
// cannot derive one.
func consoleURLSteps(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	const (
		apiURL = "http://api.dustpan.test"
		appURL = "http://app.dustpan.test/" + e2e.Subject
	)

	// The CLI inherits the console URL scheme from the backend URL, so the
	// override keeps http on a plain-HTTP server and https on a TLS server.
	overrideURL := "http://console.example.test:3000/" + e2e.Subject
	if strings.HasPrefix(env.URL, "https://") {
		overrideURL = "https://console.example.test:3000/" + e2e.Subject
	}

	env.Step(ctx, t, step("console url from 127.0.0.1", contains("as "+e2e.Subject+" ()\n"), "login"))
	env.Step(ctx, t, e2e.StepSpec{
		Check: contains("(" + appURL + ")"),
		Name:  "console url from api. host",
		Args:  []string{"login"},
		// The fake host does not resolve, so the CLI sends its requests through the
		// test server acting as an HTTP proxy; the server routes on the request path.
		Env:  []string{"PULUMI_BACKEND_URL=" + apiURL, "HTTP_PROXY=" + env.URL},
		Fail: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check: contains("(" + overrideURL + ")"),
		Name:  "console url from PULUMI_CONSOLE_DOMAIN",
		Args:  []string{"login"},
		Env:   []string{"PULUMI_CONSOLE_DOMAIN=console.example.test:3000"},
		Fail:  false,
	})
}

// tofuScenario drives OpenTofu's cloud block against the server: the
// organization is the Namespace namespace and the workspace is e2e.Workspace.
// The configuration has no resources and no providers, so only state and
// locking are exercised.
func tofuScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)

	for _, s := range []e2e.StepSpec{
		// Init reads the workspace by name and creates it when missing, so
		// it proves the read, create and list routes with the inventory check.
		step("init", checkTofuInit(ctx, env), "init", "-input=false", "-no-color"),
		step("workspace show", contains(e2e.Workspace), "workspace", "show"),
	} {
		env.Step(ctx, t, s)
	}

	// Lock steps run before any state write: a failed apply would hold the
	// lock, and the lock scenario needs an unlocked workspace.
	tofuLockScenario(t, env)

	for _, s := range []e2e.StepSpec{
		step("plan", nil, "plan", "-input=false", "-no-color"),
		step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		step("output", contains(`"hello"`), "output", "-json", "-no-color"),
		step("state pull", contains(`"greeting"`), "state", "pull"),
	} {
		env.Step(ctx, t, s)
	}

	// state push rejects a stale serial and another lineage, and accepts
	// -force.
	tofuStatePushScenario(t, env)

	// A crashed apply leaves the lock behind, and force-unlock recovers.
	tofuCrashScenario(t, env)

	// Destroy removes every resource and output with a new, higher serial.
	tofuDestroyScenario(t, env)

	tofuRemoteScenario(t, env)

	// The stdlib http backend is the T1 import source (ADR 29): init, apply
	// of a built-in resource and state pull pass against it.
	tofuHTTPScenario(t, env)
}

// tofuRemoteScenario drives the remote backend with a workspace prefix in its
// own directory: new, list and select run without tags, and the two
// workspaces keep separate state.
func tofuRemoteScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	env.WriteRemoteProject(t)

	for _, s := range []e2e.StepSpec{
		step("remote init", nil, "init", "-input=false", "-no-color"),
		step("workspace new dev", contains(e2e.RemoteDev), "workspace", "new", e2e.RemoteDev, "-no-color"),
		step("workspace new prod", contains(e2e.RemoteProd), "workspace", "new", e2e.RemoteProd, "-no-color"),
		step("workspace list", checkRemoteList(env), "workspace", "list", "-no-color"),
		step("workspace select dev", nil, "workspace", "select", e2e.RemoteDev, "-no-color"),
		step("workspace show dev", contains(e2e.RemoteDev), "workspace", "show", "-no-color"),
		step("remote apply dev", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		step("remote state pull dev", contains(`"greeting"`), "state", "pull", "-no-color"),
		step("workspace select prod", nil, "workspace", "select", e2e.RemoteProd, "-no-color"),
		step("remote state pull prod", statePullLacksGreeting, "state", "pull", "-no-color"),
	} {
		env.RemoteStep(ctx, t, s)
	}
}

// tofuHTTPScenario drives the stdlib http backend in its own directory: init,
// apply of a built-in resource and state pull pass, and the backend counts
// the apply write.
func tofuHTTPScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	backend := e2e.NewHTTPBackend(t)
	dir := backend.WriteProject(t)

	for _, s := range []e2e.StepSpec{
		step("http init", nil, "init", "-input=false", "-no-color"),
		step("http apply", checkHTTPWrites(backend), "apply", "-input=false", "-auto-approve", "-no-color"),
		step("http state pull", contains(`"greeting"`), "state", "pull", "-no-color"),
	} {
		env.StepIn(ctx, t, dir, s)
	}
}

// checkHTTPWrites proves the apply wrote state through the http backend.
func checkHTTPWrites(backend *e2e.HTTPBackend) func(e2e.Result) error {
	return func(e2e.Result) error {
		if got := backend.Writes(); got == 0 {
			return fmt.Errorf("%w: http backend holds no write after apply", errUnexpected)
		}

		return nil
	}
}

// checkRemoteList proves workspace list shows the short names dev and prod
// and that the CLI listed with search[name]: the request carries the prefix.
func checkRemoteList(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if err := remoteListShows(r); err != nil {
			return err
		}

		return remoteListSearched(env)
	}
}

// remoteListShows proves workspace list shows the short names dev and prod.
func remoteListShows(r e2e.Result) error {
	for _, want := range []string{e2e.RemoteDev, e2e.RemoteProd} {
		if !strings.Contains(r.Stdout, want) {
			return fmt.Errorf("%w: workspace list lacks %q: %s", errUnexpected, want, r.Stdout)
		}
	}

	return nil
}

// remoteListSearched proves the list step sent a workspace list with a search
// query.
func remoteListSearched(env *e2e.Env) error {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step != "workspace list" || entries[i].Method != http.MethodGet {
			continue
		}

		if entries[i].Path != "/api/v2/organizations/"+e2e.Namespace+"/workspaces" {
			continue
		}

		if !strings.Contains(entries[i].Query, "search") {
			return fmt.Errorf("%w: workspace list sent no search query: %q", errUnexpected, entries[i].Query)
		}

		return nil
	}

	return fmt.Errorf("%w: workspace list made no list request", errUnexpected)
}

// statePullLacksGreeting proves the state holds no resource of the other
// workspace: the pulled state lacks the greeting output.
func statePullLacksGreeting(r e2e.Result) error {
	if strings.Contains(r.Stdout, `"greeting"`) {
		return fmt.Errorf("%w: state pull shows the other workspace: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// tofuLockScenario proves workspace locks across two principals: A locks
// through the API, B cannot plan until it force-unlocks.
func tofuLockScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"
	forcePath := "/api/v2/workspaces/" + wsID + "/actions/force-unlock"

	// Principal A locks through the API. The step records 200.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by A",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	// A plan as B fails at once. go-tfe discards the 409 detail for lock, so
	// the CLI prints the generic message. The step still proves the holder:
	// the lock request the plan made answers 409 with "is locked by User"
	// and the holder of A.
	env.Step(ctx, t, e2e.StepSpec{
		Check: checkPlanLockedByAnother(ctx, env),
		Name:  "plan locked by another",
		Args:  []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:   env.TofuEnvB(),
		Fail:  true,
	})

	// A second lock by B answers 409.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B conflicts",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusConflict,
	})

	// An unlock by B answers 409: only the holder unlocks.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock by B conflicts",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusConflict,
	})

	// A force-unlock as B succeeds. The CLI sends no lock ID that the
	// server uses: the workspace ID in the path names the workspace.
	env.Step(ctx, t, e2e.StepSpec{
		Check: nil,
		Name:  "force-unlock by B",
		Args:  []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:   env.TofuEnvB(),
		Fail:  false,
	})

	// A plan as B now passes.
	env.Step(ctx, t, e2e.StepSpec{
		Check: nil,
		Name:  "plan after force-unlock",
		Args:  []string{"plan", "-input=false", "-no-color"},
		Env:   env.TofuEnvB(),
		Fail:  false,
	})

	// With the workspace locked by B, a read as A shows the holder, time
	// and reason. After the unlock the three are empty or absent. The CLI
	// steps around them prove no CLI rejects the new attributes.
	wsPath := "/api/v2/workspaces/" + wsID

	const lockReason = "e2e lock reason"

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B with reason",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{"attributes":{"reason":"` + lockReason + `"}}}`,
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceShowsLock(e2e.SubjectB, lockReason),
		Name:   "workspace shows lock holder",
		Method: http.MethodGet,
		Path:   wsPath,
		Body:   "",
		Want:   http.StatusOK,
	})
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock by B",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceHidesLock,
		Name:   "workspace hides lock holder",
		Method: http.MethodGet,
		Path:   wsPath,
		Body:   "",
		Want:   http.StatusOK,
	})

	// A force-unlock of an unlocked workspace answers 409.
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "force-unlock unlocked conflicts",
		Method: http.MethodPost,
		Path:   forcePath,
		Body:   "",
		Want:   http.StatusConflict,
	})

	// Two principals contend for one lock: both plans run at once with a
	// lock timeout, so the loser waits and retries instead of failing. Every
	// 409 lock answer during the step holds "is locked by User": a lost
	// compare-and-swap reports the held lock, or the CLI would not retry.
	env.StepParallel(ctx, t, e2e.ParallelSpec{
		Name:  "concurrent plans contend for lock",
		Check: checkContentionRetried(env, "concurrent plans contend for lock"),
		Args: [][]string{
			{"plan", "-input=false", "-no-color", "-lock-timeout=60s"},
			{"plan", "-input=false", "-no-color", "-lock-timeout=60s"},
		},
		Env: nil,
		Envs: [][]string{
			nil,
			env.TofuEnvB(),
		},
	})
}

// checkContentionRetried proves every 409 lock answer during step holds "is
// locked by User": the loser of a lock race reports the held lock, so the
// CLI waits and retries under -lock-timeout instead of failing.
func checkContentionRetried(env *e2e.Env, step string) func([]e2e.Result) error {
	return func([]e2e.Result) error {
		entries := env.Recorder.Entries()

		conflicts := 0

		for i := range entries {
			e := &entries[i]
			if e.Step != step || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				continue
			}

			conflicts++

			if !strings.Contains(e.ResponseBody, "is locked by User") {
				return fmt.Errorf(
					"%w: lock detail lacks %q: %s",
					errUnexpected,
					"is locked by User",
					e.ResponseBody,
				)
			}
		}

		return nil
	}
}

// checkPlanLockedByAnother proves a plan as B fails on the held lock and
// that the lock request it made answers 409 with "is locked by User" and the
// holder of A. go-tfe maps any 409 on lock to its generic error, so the CLI
// prints "workspace already locked" without the holder; the recorded response
// body holds the detail the unit test also proves.
func checkPlanLockedByAnother(_ context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "workspace already locked") {
			return fmt.Errorf("%w: plan output lacks the lock failure: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			e := &entries[i]
			if e.Step != "plan locked by another" || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				return fmt.Errorf(
					"%w: lock during plan answers %d, want %d",
					errUnexpected,
					e.Status,
					http.StatusConflict,
				)
			}

			if !strings.Contains(e.ResponseBody, "is locked by User") ||
				!strings.Contains(e.ResponseBody, e2e.Subject) {
				return fmt.Errorf(
					"%w: lock detail lacks %q and the holder: %s",
					errUnexpected,
					"is locked by User",
					e.ResponseBody,
				)
			}

			return nil
		}

		return fmt.Errorf("%w: plan made no lock request", errUnexpected)
	}
}

// checkTofuInit proves that init got past service discovery and created the
// workspace: the CLI called discovery, ping and the entitlement set in order,
// and a direct store call sees one inventory entry for the workspace.
func checkTofuInit(ctx context.Context, env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if err := checkTofuDiscovery(env)(r); err != nil {
			return err
		}

		entries, err := env.ListInventory(ctx, e2e.Namespace)
		if err != nil {
			return err
		}

		if len(entries) != 1 {
			return fmt.Errorf("%w: inventory holds %d entries, want one for the workspace", errUnexpected, len(entries))
		}

		entry := entries[0]
		want := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace

		if entry.Reference() != want || entry.Tool != "opentofu" || entry.Name != e2e.Workspace {
			return fmt.Errorf("%w: inventory entry %+v, want reference %q", errUnexpected, entry, want)
		}

		if entry.UnitID == "" || !strings.HasPrefix(entry.UnitID, "ws-") {
			return fmt.Errorf("%w: inventory unit id %q, want the ws- workspace id", errUnexpected, entry.UnitID)
		}

		return nil
	}
}

// checkTofuDiscovery proves that init got past service discovery: the CLI
// called discovery, ping and the entitlement set in order, and each answered
// 200.
func checkTofuDiscovery(env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		want := []struct {
			method, path string
		}{
			{http.MethodGet, "/.well-known/terraform.json"},
			{http.MethodGet, "/api/v2/ping"},
			{http.MethodGet, "/api/v2/organizations/" + e2e.Namespace + "/entitlement-set"},
		}

		entries := env.Recorder.Entries()
		next := 0

		for i := range entries {
			if entries[i].Step != "init" || next >= len(want) {
				continue
			}

			if entries[i].Method != want[next].method || entries[i].Path != want[next].path {
				continue
			}

			if entries[i].Status != http.StatusOK {
				return fmt.Errorf(
					"%w: %s %s answers %d, want %d",
					errUnexpected,
					entries[i].Method,
					entries[i].Path,
					entries[i].Status,
					http.StatusOK,
				)
			}

			next++
		}

		if next != len(want) {
			return fmt.Errorf("%w: init missed a discovery call", errUnexpected)
		}

		return nil
	}
}

// tofuSlowConfig is the crash configuration: the built-in terraform_data
// resource with a local-exec provisioner that sleeps, so an apply takes time
// without a provider download. The greeting output stays, so the state after
// recovery still holds it.
func tofuSlowConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

resource "terraform_data" "slow" {
  provisioner "local-exec" {
    command = "sleep 10"
  }
}

output "greeting" {
  value = "hello"
}
`
}

// tofuCrashScenario proves recovery after a crashed apply: the harness kills
// an apply at its lock request, the workspace stays locked, a plan fails on
// the lock, force-unlock releases it, the next apply passes and no pending
// version is current.
func tofuCrashScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)

	env.WriteTofuConfig(t, tofuSlowConfig(env.Host()))

	// The harness kills the apply once the recorder shows its lock request,
	// then reaps it, so no process stays behind.
	env.KillAtRequest(ctx, t, e2e.KillSpec{
		Name:       "crashed apply",
		Args:       []string{"apply", "-input=false", "-auto-approve", "-no-color"},
		Env:        nil,
		Method:     http.MethodPost,
		PathSuffix: "/actions/lock",
	})

	// The killed apply never unlocked: the workspace is still locked.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceLocked,
		Name:   "workspace still locked",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID,
		Body:   "",
		Want:   http.StatusOK,
	})

	// A plan now fails on the held lock: the CLI prints the lock failure and
	// the lock request it made answers 409 with "is locked by User".
	env.Step(ctx, t, e2e.StepSpec{
		Check: checkPlanLockedAfterCrash(env),
		Name:  "plan fails on the crashed lock",
		Args:  []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:   nil,
		Fail:  true,
	})

	// Force-unlock as another principal releases the crashed lock.
	env.Step(ctx, t, e2e.StepSpec{
		Check: nil,
		Name:  "force-unlock after crash",
		Args:  []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:   env.TofuEnvB(),
		Fail:  false,
	})

	// The next apply passes and the state holds the slow resource.
	env.Step(ctx, t, step("apply after crash", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("state pull after crash", checkStateHasSlowResource, "state", "pull"))

	// No pending version is current: the current state version is finalized.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkCurrentFinalized,
		Name:   "current state is finalized",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID + "/current-state-version",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// tofuDestroyScenario proves destroy: it locks, uploads a new state version
// with a higher serial and unlocks, leaving no resources and no outputs.
func tofuDestroyScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	before := currentSerial(ctx, t, env, wsID)

	env.Step(
		ctx,
		t,
		step("destroy", checkDestroyUploadsNewSerial(ctx, env, wsID, before), "destroy", "-auto-approve", "-no-color"),
	)

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkWorkspaceUnlocked,
		Name:   "workspace unlocked after destroy",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.Step(ctx, t, step("state pull after destroy", checkStateDestroyed, "state", "pull"))
	env.Step(ctx, t, step("output after destroy", checkOutputDestroyed, "output", "-json", "-no-color"))
}

// workspaceLockedAttributes holds the locked flag of a workspace document.
type workspaceLockedAttributes struct {
	Locked bool `json:"locked"`
}

// workspaceLockedData holds the data object of a workspace document.
type workspaceLockedData struct {
	Attributes workspaceLockedAttributes `json:"attributes"`
}

// workspaceLockedDoc decodes a workspace document in tests.
type workspaceLockedDoc struct {
	Data workspaceLockedData `json:"data"`
}

// statePullResource is one resource of a pulled state.
type statePullResource struct {
	Type string `json:"type"`
}

// statePullDoc decodes a pulled state in tests.
type statePullDoc struct {
	Outputs   map[string]any      `json:"outputs"`
	Resources []statePullResource `json:"resources"`
}

// currentStateAttributes holds the status and serial of a state version
// document.
type currentStateAttributes struct {
	Status string `json:"status"`
	Serial int64  `json:"serial"`
}

// currentStateData holds the data object of a state version document.
type currentStateData struct {
	Attributes currentStateAttributes `json:"attributes"`
}

// currentStateDoc decodes a state version document in tests.
type currentStateDoc struct {
	Data currentStateData `json:"data"`
}

// decodeWorkspaceLocked returns the locked flag of a workspace document.
func decodeWorkspaceLocked(body string) (bool, error) {
	var doc workspaceLockedDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return false, fmt.Errorf("%w: decode workspace: %w", errUnexpected, err)
	}

	return doc.Data.Attributes.Locked, nil
}

// checkWorkspaceLocked proves a workspace document holds locked true.
func checkWorkspaceLocked(body string) error {
	locked, err := decodeWorkspaceLocked(body)
	if err != nil {
		return err
	}

	if !locked {
		return fmt.Errorf("%w: locked is false, want true", errUnexpected)
	}

	return nil
}

// checkWorkspaceUnlocked proves a workspace document holds locked false.
func checkWorkspaceUnlocked(body string) error {
	locked, err := decodeWorkspaceLocked(body)
	if err != nil {
		return err
	}

	if locked {
		return fmt.Errorf("%w: locked is true, want false", errUnexpected)
	}

	return nil
}

// workspaceLockAttributes holds the lock view of a workspace document.
type workspaceLockAttributes struct {
	Locked     bool   `json:"locked"`
	LockHolder string `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
	LockedAt   string `json:"locked-at"`   //nolint:tagliatelle // The TFE wire name; it must not change.
	LockReason string `json:"lock-reason"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceLockData holds the data object of a workspace document.
type workspaceLockData struct {
	Attributes workspaceLockAttributes `json:"attributes"`
}

// workspaceLockDoc decodes a workspace document in tests.
type workspaceLockDoc struct {
	Data workspaceLockData `json:"data"`
}

// decodeWorkspaceLock returns the lock view of a workspace document. A
// missing attribute decodes as empty, so absent and empty both read as "".
func decodeWorkspaceLock(body string) (workspaceLockAttributes, error) {
	var doc workspaceLockDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return workspaceLockAttributes{}, fmt.Errorf("%w: decode workspace: %w", errUnexpected, err)
	}

	return doc.Data.Attributes, nil
}

// checkWorkspaceShowsLock proves a workspace document shows the lock of
// holder with reason: locked is true, the holder and reason match, and the
// time parses as RFC 3339.
func checkWorkspaceShowsLock(holder, reason string) func(string) error {
	return func(body string) error {
		attrs, err := decodeWorkspaceLock(body)
		if err != nil {
			return err
		}

		if !attrs.Locked {
			return fmt.Errorf("%w: locked is false, want true", errUnexpected)
		}

		if attrs.LockHolder != holder {
			return fmt.Errorf("%w: lock-holder is %q, want %q", errUnexpected, attrs.LockHolder, holder)
		}

		if attrs.LockReason != reason {
			return fmt.Errorf("%w: lock-reason is %q, want %q", errUnexpected, attrs.LockReason, reason)
		}

		if attrs.LockedAt == "" {
			return fmt.Errorf("%w: locked-at is empty", errUnexpected)
		}

		if _, err := time.Parse(time.RFC3339, attrs.LockedAt); err != nil {
			return fmt.Errorf("%w: locked-at %q is not RFC 3339: %w", errUnexpected, attrs.LockedAt, err)
		}

		return nil
	}
}

// checkWorkspaceHidesLock proves a workspace document shows no lock: locked
// is false and the holder, time and reason are empty or absent.
func checkWorkspaceHidesLock(body string) error {
	attrs, err := decodeWorkspaceLock(body)
	if err != nil {
		return err
	}

	if attrs.Locked {
		return fmt.Errorf("%w: locked is true, want false", errUnexpected)
	}

	if attrs.LockHolder != "" {
		return fmt.Errorf("%w: lock-holder is %q, want empty", errUnexpected, attrs.LockHolder)
	}

	if attrs.LockedAt != "" {
		return fmt.Errorf("%w: locked-at is %q, want empty", errUnexpected, attrs.LockedAt)
	}

	if attrs.LockReason != "" {
		return fmt.Errorf("%w: lock-reason is %q, want empty", errUnexpected, attrs.LockReason)
	}

	return nil
}

// checkPlanLockedAfterCrash proves a plan fails on the crashed lock: the CLI
// prints the lock failure and its lock request answers 409 with the holder.
func checkPlanLockedAfterCrash(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if out := r.Stdout + r.Stderr; !strings.Contains(out, "locked") {
			return fmt.Errorf("%w: plan output lacks the lock failure: %s", errUnexpected, out)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			e := &entries[i]
			if e.Step != "plan fails on the crashed lock" || e.Method != http.MethodPost ||
				!strings.HasSuffix(e.Path, "/actions/lock") {
				continue
			}

			if e.Status != http.StatusConflict {
				return fmt.Errorf(
					"%w: lock during plan answers %d, want %d",
					errUnexpected,
					e.Status,
					http.StatusConflict,
				)
			}

			if !strings.Contains(e.ResponseBody, "is locked by User") {
				return fmt.Errorf("%w: lock detail lacks %q: %s", errUnexpected, "is locked by User", e.ResponseBody)
			}

			return nil
		}

		return fmt.Errorf("%w: plan made no lock request", errUnexpected)
	}
}

// checkStateHasSlowResource proves the state after recovery holds the slow
// resource and the greeting output.
func checkStateHasSlowResource(r e2e.Result) error {
	var doc statePullDoc

	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return fmt.Errorf("%w: decode state: %w", errUnexpected, err)
	}

	slow := false

	for _, res := range doc.Resources {
		if res.Type == "terraform_data" {
			slow = true
		}
	}

	if !slow {
		return fmt.Errorf("%w: state holds no terraform_data resource: %s", errUnexpected, r.Stdout)
	}

	if _, ok := doc.Outputs["greeting"]; !ok {
		return fmt.Errorf("%w: state holds no greeting output: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// checkCurrentFinalized proves the current state version is finalized: no
// pending version is current.
func checkCurrentFinalized(body string) error {
	var doc currentStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	if doc.Data.Attributes.Status != "finalized" {
		return fmt.Errorf("%w: current state is %q, want finalized", errUnexpected, doc.Data.Attributes.Status)
	}

	return nil
}

// fetchCurrentSerial returns the serial of the workspace's current state
// version.
func fetchCurrentSerial(ctx context.Context, env *e2e.Env, wsID string) (int64, error) {
	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID+"/current-state-version", "")
	if err != nil {
		return 0, err
	}

	if status != http.StatusOK {
		return 0, fmt.Errorf("%w: current state answers %d: %s", errUnexpected, status, body)
	}

	var doc currentStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return 0, fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	return doc.Data.Attributes.Serial, nil
}

// currentSerial returns the serial of the workspace's current state version,
// failing the test when the workspace has none.
func currentSerial(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) int64 {
	t.Helper()

	serial, err := fetchCurrentSerial(ctx, env, wsID)
	if err != nil {
		t.Fatalf("get current state: %v", err)
	}

	return serial
}

// destroyLocked reports whether the destroy step locked the workspace.
func destroyLocked(env *e2e.Env) bool {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step == "destroy" && entries[i].Method == http.MethodPost &&
			strings.HasSuffix(entries[i].Path, "/actions/lock") && entries[i].Status == http.StatusOK {
			return true
		}
	}

	return false
}

// destroyCreatedState reports whether the destroy step created a state
// version.
func destroyCreatedState(env *e2e.Env) bool {
	entries := env.Recorder.Entries()

	for i := range entries {
		if entries[i].Step == "destroy" && entries[i].Method == http.MethodPost &&
			strings.HasSuffix(entries[i].Path, "/state-versions") && entries[i].Status == http.StatusCreated {
			return true
		}
	}

	return false
}

// checkDestroyUploadsNewSerial proves destroy uploaded a new state version:
// the current serial is higher than before, and the destroy step locked and
// created a state version.
func checkDestroyUploadsNewSerial(
	ctx context.Context, env *e2e.Env, wsID string, before int64,
) func(e2e.Result) error {
	return func(e2e.Result) error {
		after, err := fetchCurrentSerial(ctx, env, wsID)
		if err != nil {
			return err
		}

		if after <= before {
			return fmt.Errorf("%w: serial %d after destroy, want higher than %d", errUnexpected, after, before)
		}

		if !destroyLocked(env) {
			return fmt.Errorf("%w: destroy made no lock request", errUnexpected)
		}

		if !destroyCreatedState(env) {
			return fmt.Errorf("%w: destroy created no state version", errUnexpected)
		}

		return nil
	}
}

// checkStateDestroyed proves destroy emptied the state: no resources and no
// outputs remain.
func checkStateDestroyed(r e2e.Result) error {
	var doc statePullDoc

	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		return fmt.Errorf("%w: decode state: %w", errUnexpected, err)
	}

	if len(doc.Resources) != 0 {
		return fmt.Errorf("%w: state still holds %d resources: %s", errUnexpected, len(doc.Resources), r.Stdout)
	}

	if len(doc.Outputs) != 0 {
		return fmt.Errorf("%w: state still holds outputs: %s", errUnexpected, r.Stdout)
	}

	return nil
}

// checkOutputDestroyed proves destroy removed every output: output -json
// prints an empty object.
func checkOutputDestroyed(r e2e.Result) error {
	trimmed := strings.TrimSpace(r.Stdout)
	if trimmed == "" {
		trimmed = strings.TrimSpace(r.Stderr)
	}

	var outputs map[string]any

	if err := json.Unmarshal([]byte(trimmed), &outputs); err != nil {
		return fmt.Errorf("%w: decode output: %w (%s)", errUnexpected, err, r.Stdout)
	}

	if len(outputs) != 0 {
		return fmt.Errorf("%w: output still holds %d entries: %s", errUnexpected, len(outputs), r.Stdout)
	}

	return nil
}

// staleSerialDetail is the server detail a stale push shows: the CLI prints
// the JSON:API error detail go-tfe returns, which holds this sentinel text.
const staleSerialDetail = "state serial is not newer than the current state"

// lineageMismatchDetail is the server detail a push of another lineage
// shows, through the same path as staleSerialDetail.
const lineageMismatchDetail = "state lineage differs from the current state"

// clientImportStale and clientOverwriteSame are the CLI-side refusals of a
// push without -force: a lower serial cannot import over a newer state, an
// equal serial cannot overwrite a different state, and another lineage
// cannot import over an unrelated state (which shares the import wording).
const (
	clientImportStale   = "cannot import state"
	clientOverwriteSame = "cannot overwrite existing state"
)

// tofuStatePushScenario proves stale-serial rejection and force push: a push
// of a copy of the current state with its own serial fails, direct API
// creates with a stale serial and another lineage answer 422, a force push
// of the same file succeeds and a later pull shows it, and a push of another
// lineage fails without -force.
func tofuStatePushScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	pulled := env.Step(ctx, t, step("state pull before push", contains(`"greeting"`), "state", "pull"))

	var current pulledStateDoc

	if err := json.Unmarshal([]byte(pulled.Stdout), &current); err != nil {
		t.Fatalf("decode pulled state: %v", err)
	}

	if current.Lineage == "" {
		t.Fatal("pulled state has no lineage")
	}

	// The file carries the current serial with a marker output added. A
	// lower serial would not survive the force push: the CLI writes file
	// serial plus one, which would still be stale. The current serial fails
	// without -force (the CLI stops the import itself, and the server would
	// answer 422 for a serial that is not newer) and succeeds with -force.
	stalePath := writePushFile(t, "stale.tfstate", pushedState(t, pulled.Stdout, current.Serial, current.Lineage))

	env.Step(
		ctx,
		t,
		failStep(
			"state push stale",
			checkPushRejected(env, "state push stale", staleSerialDetail),
			"state",
			"push",
			stalePath,
		),
	)
	logPushSide(t, env, "state push stale")

	// The CLI may stop a push itself before it reaches the server, so the
	// harness also calls the create route directly: a lower serial and
	// another lineage each answer 422 with the JSON:API error the failure
	// steps prove when the server is the side that rejects. The workspace
	// must be held for the creates to reach the checks.
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock for stale check",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkDirectRejected(staleSerialDetail),
		Name:   "stale serial rejected direct",
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + wsID + "/state-versions",
		Body: fmt.Sprintf(
			`{"data":{"type":"state-versions","attributes":{"serial":%d,"lineage":%q,"md5":%q}}}`,
			max(current.Serial-1, 0),
			current.Lineage,
			"d41d8cd98f00b204e9800998ecf8427e",
		),
		Want: http.StatusUnprocessableEntity,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkDirectRejected(lineageMismatchDetail),
		Name:   "other lineage rejected direct",
		Method: http.MethodPost,
		Path:   "/api/v2/workspaces/" + wsID + "/state-versions",
		Body: fmt.Sprintf(
			`{"data":{"type":"state-versions","attributes":{"serial":%d,"lineage":%q,"md5":%q}}}`,
			current.Serial+100,
			"00000000-0000-0000-0000-000000000000",
			"d41d8cd98f00b204e9800998ecf8427e",
		),
		Want: http.StatusUnprocessableEntity,
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "unlock after stale check",
		Method: http.MethodPost,
		Path:   unlockPath,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.Step(ctx, t, step("state push force", nil, "state", "push", "-force", stalePath))
	env.Step(
		ctx,
		t,
		step("state pull after force", checkPushedState(current.Serial, current.Lineage), "state", "pull"),
	)

	otherPath := writePushFile(
		t,
		"other.tfstate",
		editedState(t, pulled.Stdout, current.Serial+100, "00000000-0000-0000-0000-000000000000"),
	)

	env.Step(
		ctx,
		t,
		failStep(
			"state push other lineage",
			checkPushRejected(env, "state push other lineage", lineageMismatchDetail),
			"state",
			"push",
			otherPath,
		),
	)
	logPushSide(t, env, "state push other lineage")
}

// pulledStateDoc decodes the serial and lineage of a pulled state document.
type pulledStateDoc struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
}

// editedState returns pulled with its serial and lineage replaced.
func editedState(t *testing.T, pulled string, serial int64, lineage string) []byte {
	t.Helper()

	var fields map[string]json.RawMessage

	if err := json.Unmarshal([]byte(pulled), &fields); err != nil {
		t.Fatalf("decode pulled state: %v", err)
	}

	rawSerial, err := json.Marshal(serial)
	if err != nil {
		t.Fatalf("encode serial %d: %v", serial, err)
	}

	rawLineage, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("encode lineage: %v", err)
	}

	fields["serial"], fields["lineage"] = rawSerial, rawLineage

	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode edited state: %v", err)
	}

	return out
}

// pushedState returns pulled with its serial and lineage replaced and a
// marker output added, so the pull after a force push proves the pushed
// state landed whatever serial the CLI sent.
func pushedState(t *testing.T, pulled string, serial int64, lineage string) []byte {
	t.Helper()

	doc := editedState(t, pulled, serial, lineage)

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(doc, &fields); err != nil {
		t.Fatalf("decode edited state: %v", err)
	}

	var outputs map[string]json.RawMessage

	if raw, ok := fields["outputs"]; ok {
		if err := json.Unmarshal(raw, &outputs); err != nil {
			t.Fatalf("decode state outputs: %v", err)
		}
	}

	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}

	marker, err := json.Marshal(map[string]string{"value": "forced", "type": "string"})
	if err != nil {
		t.Fatalf("encode marker output: %v", err)
	}

	outputs["pushed"] = marker

	rawOutputs, err := json.Marshal(outputs)
	if err != nil {
		t.Fatalf("encode state outputs: %v", err)
	}

	fields["outputs"] = rawOutputs

	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode pushed state: %v", err)
	}

	return out
}

// writePushFile saves doc for a state push step and returns its path.
func writePushFile(t *testing.T, name string, doc []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

// checkPushRejected proves the push failed and which side rejected it. When
// the create reached the server, the CLI output holds the server detail
// go-tfe printed and the create answers 422 with the JSON:API error
// document holding it. Otherwise the CLI stopped the push itself, and the
// output holds its own message.
func checkPushRejected(env *e2e.Env, step, serverDetail string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		out := r.Stdout + r.Stderr

		if !pushReachedServer(env, step) {
			return checkClientRefusal(step, out)
		}

		return checkServerRefusal(env, step, out, serverDetail)
	}
}

// checkClientRefusal proves the CLI stopped the push itself with its own
// import refusal.
func checkClientRefusal(step, out string) error {
	if strings.Contains(out, clientImportStale) || strings.Contains(out, clientOverwriteSame) {
		return nil
	}

	return fmt.Errorf(
		"%w: %s output lacks %q or %q: %s",
		errUnexpected,
		step,
		clientImportStale,
		clientOverwriteSame,
		out,
	)
}

// checkServerRefusal proves the CLI output holds the server detail and the
// create the step made answers 422 with the JSON:API error document holding
// it.
func checkServerRefusal(env *e2e.Env, step, out, serverDetail string) error {
	if !strings.Contains(out, serverDetail) {
		return fmt.Errorf("%w: %s output lacks %q: %s", errUnexpected, step, serverDetail, out)
	}

	entries := env.Recorder.Entries()
	for i := range entries {
		e := &entries[i]
		if e.Step != step || e.Method != http.MethodPost || !strings.HasSuffix(e.Path, "/state-versions") {
			continue
		}

		return checkCreateError(step, e, serverDetail)
	}

	return fmt.Errorf("%w: %s reached no state-versions route", errUnexpected, step)
}

// checkCreateError proves the recorded create answers 422 with the JSON:API
// error document holding the detail.
func checkCreateError(step string, e *e2e.Entry, serverDetail string) error {
	if e.Status != http.StatusUnprocessableEntity {
		return fmt.Errorf(
			"%w: POST state-versions during %q answers %d, want %d: %s",
			errUnexpected,
			step,
			e.Status,
			http.StatusUnprocessableEntity,
			e.ResponseBody,
		)
	}

	if !strings.Contains(e.ResponseBody, `"errors"`) ||
		!strings.Contains(e.ResponseBody, `"422"`) ||
		!strings.Contains(e.ResponseBody, serverDetail) {
		return fmt.Errorf(
			"%w: POST state-versions during %q lacks the 422 detail %q: %s",
			errUnexpected,
			step,
			serverDetail,
			e.ResponseBody,
		)
	}

	return nil
}

// pushReachedServer reports whether the CLI sent a state version create
// during step: a create means the server rejected the push, none means the
// CLI stopped it itself.
func pushReachedServer(env *e2e.Env, step string) bool {
	entries := env.Recorder.Entries()
	for i := range entries {
		if e := &entries[i]; e.Step == step && e.Method == http.MethodPost &&
			strings.HasSuffix(e.Path, "/state-versions") {
			return true
		}
	}

	return false
}

// checkDirectRejected proves the direct create answered the JSON:API error
// with the detail.
func checkDirectRejected(wantDetail string) func(string) error {
	return func(body string) error {
		if !strings.Contains(body, `"errors"`) ||
			!strings.Contains(body, `"422"`) ||
			!strings.Contains(body, wantDetail) {
			return fmt.Errorf("%w: direct create lacks the 422 detail %q: %s", errUnexpected, wantDetail, body)
		}

		return nil
	}
}

// logPushSide records which side rejected step's push: a create request
// during the step means the server answered, otherwise the CLI stopped the
// push itself.
func logPushSide(t *testing.T, env *e2e.Env, step string) {
	t.Helper()

	entries := env.Recorder.Entries()
	for i := range entries {
		e := &entries[i]
		if e.Step == step && e.Method == http.MethodPost && strings.HasSuffix(e.Path, "/state-versions") {
			t.Logf("%s rejected server-side: POST %s answers %d", step, e.Path, e.Status)

			return
		}
	}

	t.Logf("%s rejected client-side: the CLI stopped the push itself", step)
}

// checkPushedState proves the pull after a force push shows the pushed
// state: its lineage, its marker output, and the pushed serial with the
// increment of one the CLI writes on top.
func checkPushedState(serial int64, lineage string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var got pulledStateDoc

		if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
			return fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
		}

		if got.Lineage != lineage || (got.Serial != serial && got.Serial != serial+1) {
			return fmt.Errorf(
				"%w: pulled serial %d lineage %q, want %d or %d of %q",
				errUnexpected,
				got.Serial,
				got.Lineage,
				serial,
				serial+1,
				lineage,
			)
		}

		if !strings.Contains(r.Stdout, `"pushed"`) {
			return fmt.Errorf("%w: pulled state lacks the pushed marker output: %s", errUnexpected, r.Stdout)
		}

		return nil
	}
}

// TestBinaryRunAndRestart proves the binary helpers: RunBinary runs any
// subcommand against the database of the environment, and StopServer and
// StartServer stop and start the same server on the same port with the same
// state. It runs only with E2E_SERVER=binary, on every store under test.
func TestBinaryRunAndRestart(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBinaryRestartOnStore(t, cli, db)
		})
	}
}

// runBinaryRestartOnStore runs the binary restart steps on one store: a
// namespace create and an unknown subcommand through RunBinary, one Pulumi
// stack before the stop, and the same stack read with the same login after
// the start.
func runBinaryRestartOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkRunBinary(ctx, t, env)
	writeRestartStack(ctx, t, env)
	checkServerRestart(ctx, t, env)
	readRestartStack(ctx, t, env)
}

// checkRunBinary proves RunBinary against the database of the environment: a
// namespace create gives exit code 0 and an unknown subcommand gives a
// non-zero exit with text on stderr.
func checkRunBinary(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	created := env.RunBinary(ctx, t, "namespace", "create", "--database", env.DatabaseURL(t), e2e.Namespace2)
	if created.ExitCode != 0 {
		t.Fatalf("namespace create exits with status %d: %s%s", created.ExitCode, created.Stdout, created.Stderr)
	}

	if blobDir := env.BlobDir(t); blobDir == "" {
		t.Fatal("blob directory is empty")
	}

	unknown := env.RunBinary(ctx, t, "no-such-command")
	if unknown.ExitCode == 0 {
		t.Fatal("unknown subcommand exits with status 0")
	}

	if !strings.Contains(unknown.Stderr, "usage") {
		t.Fatalf("unknown subcommand stderr lacks usage: %q", unknown.Stderr)
	}
}

// writeRestartStack writes one Pulumi stack before the stop, so the restart
// check proves the same state survives.
func writeRestartStack(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
	} {
		env.Step(ctx, t, s)
	}
}

// healthStatus returns the status of GET /health/live through the environment.
func healthStatus(ctx context.Context, env *e2e.Env) (int, string, error) {
	return env.APIStatus(ctx, http.MethodGet, "/health/live", "")
}

// checkServerRestart proves the stop and the start: after StopServer
// GET /health/live fails, after StartServer it answers 204 again.
func checkServerRestart(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.StopServer(ctx, t)

	status, body, err := healthStatus(ctx, env)
	if err == nil && status == http.StatusNoContent {
		t.Fatal("GET /health/live answers 204 after the stop")
	}

	t.Logf("GET /health/live after the stop: status %d, err %v, body %q", status, err, body)

	env.StartServer(ctx, t)

	status, body, err = healthStatus(ctx, env)
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("GET /health/live after the start: status %d, err %v, body %q", status, err, body)
	}
}

// readRestartStack proves the CLI reads the same stack with the same login
// after the start.
func readRestartStack(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	for _, s := range []e2e.StepSpec{
		step("config get after restart", contains("s3cr3t"), "config", "get", "apiKey"),
		step("stack ls after restart", stackListed, "stack", "ls", "--json"),
	} {
		env.Step(ctx, t, s)
	}
}

// TestPairTwoLogins proves two servers in one test with two saved Pulumi
// logins in one home: a stack exists on the source S, login works for S and
// for the destination D, and both list through the saved logins with no
// access token in the environment. It runs on every store under test and
// writes a -pair report that holds both servers.
func TestPairTwoLogins(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.Pulumi().Versions()
	if len(versions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runPairLoginsOnStore(t, cli, db)
		})
	}
}

// runPairLoginsOnStore runs the two-login smoke test on one store.
func runPairLoginsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	pair.SharePulumiHome()

	src := pair.Src
	dst := pair.Dst
	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	src.Step(ctx, t, step("login to source with token", contains(src.URL), "login"))
	src.Step(ctx, t, step("init on source", nil, "stack", "init", qualified))

	pair.SavePulumiLogin(ctx, t, src, "save login for source")
	pair.SavePulumiLogin(ctx, t, dst, "save login for destination")

	pair.UsePulumiBackend(ctx, t, src, "use source")
	src.StepSavedLogin(ctx, t, step("list on source", stackListed, "stack", "ls", "--json"))

	pair.UsePulumiBackend(ctx, t, dst, "use destination")
	src.StepSavedLogin(ctx, t, step("init on destination", nil, "stack", "init", qualified))
	src.StepSavedLogin(ctx, t, step("list on destination", stackListed, "stack", "ls", "--json"))

	checkPairReport(t, pair)
}

// checkPairReport proves the report records both servers: one step command
// holds the source URL and another holds the destination URL.
func checkPairReport(t *testing.T, pair *e2e.Pair) {
	t.Helper()

	run := pair.NewPairRun()
	seenSrc, seenDst := false, false

	for _, s := range run.Steps {
		if strings.Contains(s.Command, pair.Src.URL) {
			seenSrc = true
		}

		if strings.Contains(s.Command, pair.Dst.URL) {
			seenDst = true
		}
	}

	if !seenSrc || !seenDst {
		t.Fatalf("pair report holds source %v and destination %v, want both", seenSrc, seenDst)
	}

	if len(run.Requests) == 0 {
		t.Fatal("pair report holds no requests")
	}
}

// TestTofuPairHosts proves one OpenTofu client addresses both servers in one
// test: after the trust bundle and the merged credentials, init works in the
// source project and in the destination project. It runs on Linux on every
// store under test and writes a -pair report.
func TestTofuPairHosts(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu pair suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	versions := e2e.OpenTofu().Versions()
	if len(versions) == 0 {
		t.Fatal("the OpenTofu tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), versions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, versions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runTofuPairOnStore(t, cli, db)
		})
	}
}

// runTofuPairOnStore runs the two-host OpenTofu test on one store.
func runTofuPairOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRun())
	})

	pair.MergeTofuPair(t)

	pair.Src.Step(ctx, t, step("init on source host", nil, "init", "-input=false", "-no-color"))
	pair.Dst.Step(ctx, t, step("init on destination host", nil, "init", "-input=false", "-no-color"))

	checkTofuPairReport(ctx, t, pair)
}

// checkTofuPairReport proves one client addressed both hosts and the report
// records both: each server holds one workspace in its inventory and the
// steps hold the init on each host.
func checkTofuPairReport(ctx context.Context, t *testing.T, pair *e2e.Pair) {
	t.Helper()

	for _, env := range []*e2e.Env{pair.Src, pair.Dst} {
		entries, err := env.ListInventory(ctx, e2e.Namespace)
		if err != nil {
			t.Fatalf("list inventory: %v", err)
		}

		if len(entries) != 1 {
			t.Fatalf("inventory holds %d entries, want one workspace", len(entries))
		}
	}

	run := pair.NewPairRun()
	seenSrc, seenDst := false, false

	for _, s := range run.Steps {
		switch s.Name {
		case "init on source host":
			seenSrc = true
		case "init on destination host":
			seenDst = true
		default:
		}
	}

	if !seenSrc || !seenDst {
		t.Fatalf("pair report holds source %v and destination %v, want both", seenSrc, seenDst)
	}

	if len(run.Requests) == 0 {
		t.Fatal("pair report holds no requests")
	}
}
