//go:build e2e

package e2e_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	// Registers the "sqlite3" database/sql driver for the upgrade check fixtures.
	_ "github.com/ncruces/go-sqlite3/driver"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	stackName = "dev"
	// installLimit plus every step's commandTimeout must stay under the
	// go test -timeout in the Makefile, or a timeout loses every report. That
	// holds per version and store only while all runs go at once, so the
	// Makefile's -parallel must cover the whole matrix (12 runs by default: 6
	// versions on 2 stores).
	installLimit = 10 * time.Minute
	// readinessWithoutDBStep proves a stopped database turns /health/ready to
	// 503 while /health/live stays 204.
	readinessWithoutDBStep = "readiness without database"
	// healthReadyPath and healthLivePath are the probe paths (ADR 34).
	healthReadyPath = "/health/ready"
	healthLivePath  = "/health/live"
	// readyOKBody is the readiness body when every check passes.
	readyOKBody = `{"status":"ok"}`
	// readyStoreFailedBody is the readiness body when only the store fails.
	readyStoreFailedBody = `{"status":"unavailable","failed":["store"]}`
	// readyWaitTimeout bounds the wait for the readiness status to change
	// after the proxy closes or reopens. The probe itself has a 2-second
	// limit per check, so this covers several probes.
	readyWaitTimeout = 20 * time.Second
	// readyPollInterval sets how often the wait checks the probe.
	readyPollInterval = 100 * time.Millisecond
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

		// Fails for every served route that no step reached, so an
		// endpoint can never land without its e2e step again.
		checkRouteCoverage(t, reports)
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
	checkReadyWithoutDatabase(t.Context(), t, env)
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
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Stdin: "", Fail: false, Redact: false}
}

// failStep is a scenario step that expects the CLI to fail, for steps that
// prove an error message.
func failStep(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil, Stdin: "", Fail: true, Redact: false}
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

// checkReadyWithoutDatabase proves a stopped database turns /health/ready to
// 503 with the check name only while /health/live stays 204, and that
// reopening the database turns /health/ready to 200 again. The binary server
// and the in-process server both reach PostgreSQL through a TCP proxy, so
// closing the proxy stops the database for that server only. On SQLite the
// step is skipped with a report entry that says why.
func checkReadyWithoutDatabase(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	if env.Store() == e2e.StoreSQLite {
		reason := "SQLite has no separate database process to stop; the step needs PostgreSQL"
		env.SkipStep(readinessWithoutDBStep, reason)
		t.Logf("skipped %q: %s", readinessWithoutDBStep, reason)

		return
	}

	t.Run(readinessWithoutDBStep, func(t *testing.T) {
		env.Recorder.SetStep(readinessWithoutDBStep)

		if err := doReadyWithoutDatabase(ctx, t, env); err != nil {
			env.AddStep(e2e.StepResult{
				Name:     readinessWithoutDBStep,
				Command:  http.MethodGet + " " + healthReadyPath,
				Failure:  err.Error(),
				Stderr:   "",
				ExitCode: 1,
				Passed:   false,
				Skipped:  false,
			})
			t.Errorf("%s: %v", readinessWithoutDBStep, err)
		} else {
			env.AddStep(e2e.StepResult{
				Name:     readinessWithoutDBStep,
				Command:  http.MethodGet + " " + healthReadyPath,
				Failure:  "",
				Stderr:   "",
				ExitCode: 0,
				Passed:   true,
				Skipped:  false,
			})
		}
	})
}

// doReadyWithoutDatabase runs the stopped-database sequence and returns an
// error for the first check that fails. It reopens the proxy before
// returning an error after the stop, so the environment stays usable.
func doReadyWithoutDatabase(ctx context.Context, t *testing.T, env *e2e.Env) error {
	t.Helper()

	if _, _, err := waitReadyStatus(ctx, env, http.StatusOK); err != nil {
		return err
	}

	env.StopDatabase(t)

	stoppedErr := checkStoppedDatabase(ctx, env)
	if stoppedErr != nil {
		env.StartDatabase(ctx, t)

		return stoppedErr
	}

	env.StartDatabase(ctx, t)

	return checkRecoveredDatabase(ctx, env)
}

// checkStoppedDatabase proves the stopped state: /health/ready answers 503
// with the store check name and no driver text, and /health/live stays 204.
func checkStoppedDatabase(ctx context.Context, env *e2e.Env) error {
	status, body, err := waitReadyStatus(ctx, env, http.StatusServiceUnavailable)
	if err != nil {
		return err
	}

	if checkErr := checkStoppedBody(status, body); checkErr != nil {
		return checkErr
	}

	liveStatus, liveBody, liveErr := env.APIStatus(ctx, http.MethodGet, healthLivePath, "")
	if liveErr != nil {
		return fmt.Errorf("%w: GET %s: %w", errUnexpected, healthLivePath, liveErr)
	}

	if liveStatus != http.StatusNoContent {
		return fmt.Errorf(
			"%w: GET %s status %d, want %d (body %s)",
			errUnexpected,
			healthLivePath,
			liveStatus,
			http.StatusNoContent,
			liveBody,
		)
	}

	return nil
}

// checkRecoveredDatabase proves /health/ready answers 200 again after the
// proxy reopens. pgxpool discards the broken connections and dials anew
// through the proxy, so the first probes after the reopen already pass.
func checkRecoveredDatabase(ctx context.Context, env *e2e.Env) error {
	_, body, err := waitReadyStatus(ctx, env, http.StatusOK)
	if err != nil {
		return err
	}

	if strings.TrimSpace(body) != readyOKBody {
		return fmt.Errorf("%w: GET %s body %q, want %q", errUnexpected, healthReadyPath, body, readyOKBody)
	}

	return nil
}

// waitReadyStatus polls GET /health/ready until it answers want or the wait
// times out. It returns the last status and body, or an error when the probe
// never answers want.
func waitReadyStatus(ctx context.Context, env *e2e.Env, want int) (int, string, error) {
	deadline := time.Now().Add(readyWaitTimeout)

	var lastStatus int

	var lastBody string

	for time.Now().Before(deadline) {
		status, body, err := env.APIStatus(ctx, http.MethodGet, healthReadyPath, "")
		if err == nil {
			lastStatus, lastBody = status, body

			if status == want {
				return status, body, nil
			}
		} else {
			lastBody = err.Error()
		}

		timer := time.NewTimer(readyPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()

			return lastStatus, lastBody, fmt.Errorf(
				"%w: context done while waiting for %s %d: %s",
				errUnexpected,
				healthReadyPath,
				want,
				lastBody,
			)
		case <-timer.C:
		}
	}

	return lastStatus, lastBody, fmt.Errorf(
		"%w: GET %s status %d, want %d (body %s)",
		errUnexpected,
		healthReadyPath,
		lastStatus,
		want,
		lastBody,
	)
}

// checkStoppedBody proves a 503 readiness body names the store check only,
// with no driver text. The blob store stays healthy, so the body must not
// name it.
func checkStoppedBody(status int, body string) error {
	if status != http.StatusServiceUnavailable {
		return fmt.Errorf(
			"%w: GET %s status %d, want %d (body %s)",
			errUnexpected,
			healthReadyPath,
			status,
			http.StatusServiceUnavailable,
			body,
		)
	}

	trimmed := strings.TrimSpace(body)
	if !strings.Contains(trimmed, `"store"`) {
		return fmt.Errorf("%w: GET %s body %q lacks the store check name", errUnexpected, healthReadyPath, body)
	}

	if strings.Contains(trimmed, `"blob"`) {
		return fmt.Errorf("%w: GET %s body %q names the healthy blob check", errUnexpected, healthReadyPath, body)
	}

	lowered := strings.ToLower(trimmed)
	for _, leaked := range []string{"postgres", "pgx", "dial", "refused", "connect", "driver", "eof", "pool"} {
		if strings.Contains(lowered, leaked) {
			return fmt.Errorf("%w: GET %s body %q holds driver text %q", errUnexpected, healthReadyPath, body, leaked)
		}
	}

	if trimmed != readyStoreFailedBody {
		return fmt.Errorf("%w: GET %s body %q, want %q", errUnexpected, healthReadyPath, body, readyStoreFailedBody)
	}

	return nil
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
		Check:  checkRenameKeptID(ctx, env, org, renamePath, beforeID),
		Name:   "stack rename",
		Args:   []string{"stack", "rename", renamedQualified, "--stack", qualified},
		Env:    nil,
		Stdin:  "",
		Fail:   false,
		Redact: false,
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

	// ADR 28 decision 4: the history carries the update ID, the requester,
	// the checkpoint size and the restore source. The CLI still parses its
	// history on every version, and the API shows the new fields.
	env.Step(
		ctx,
		t,
		step(
			"stack history after two ups",
			historyShowsKinds("update", "import"),
			"stack",
			"history",
			"--json",
		),
	)
	checkHistoryFields(ctx, t, env, org)
	checkRunningHistoryFields(ctx, t, env, org)

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

	pulumiRestoreScenario(t, env, org)

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
			Name:   "stack init default org",
			Args:   []string{"stack", "init", "other"},
			Env:    nil,
			Stdin:  "",
			Fail:   false,
			Redact: false,
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
			Name:   "stack rm with resources without force",
			Args:   []string{"stack", "rm", "--yes", "--stack", resourced},
			Env:    nil,
			Stdin:  "",
			Fail:   true,
			Redact: false,
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

// historyAPIEntry is one entry of the history API response as the console
// reads it (ADR 28 decision 4).
type historyAPIEntry struct {
	Result          string `json:"result"`
	Kind            string `json:"kind"`
	Version         int    `json:"version"`
	StartTime       int64  `json:"startTime"`
	EndTime         int64  `json:"endTime"`
	UpdateID        string `json:"updateID"`
	RequestedBy     string `json:"requestedBy"`
	CheckpointBytes int64  `json:"checkpointBytes"`
	LeaseExpiresAt  int64  `json:"leaseExpiresAt"`
	RestoredFrom    int64  `json:"restoredFrom"`
}

// readHistory fetches the history of the renamed stack over the API with the
// admin token.
func readHistory(ctx context.Context, t *testing.T, env *e2e.Env, org string) []historyAPIEntry {
	t.Helper()

	path := fmt.Sprintf("/api/stacks/%s/%s/renamed/updates", org, e2e.Project)

	status, body, err := env.APIStatus(ctx, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s status %d, want %d (body %s)", path, status, http.StatusOK, body)
	}

	var decoded struct {
		Updates []historyAPIEntry `json:"updates"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode history %s: %v (%s)", path, err, body)
	}

	return decoded.Updates
}

// checkHistoryFields proves the history shows the requester and the
// checkpoint size: every entry names the subject, never a token, and one
// update that saved a checkpoint reports above 0.
func checkHistoryFields(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	updates := readHistory(ctx, t, env, org)

	if len(updates) == 0 {
		t.Fatal("history holds no updates, want the two ups and the import")
	}

	sized := false

	for _, u := range updates {
		if u.UpdateID == "" {
			t.Fatalf("history entry %+v holds no update ID", u)
		}

		if u.RequestedBy != e2e.Subject {
			t.Fatalf("history entry %+v requestedBy = %q, want %q", u, u.RequestedBy, e2e.Subject)
		}

		if u.CheckpointBytes > 0 {
			sized = true
		}
	}

	if !sized {
		t.Fatalf("no history entry reports a checkpoint size (%+v)", updates)
	}
}

// checkRunningHistoryFields proves the running update shows its requester and
// lease expiry with no checkpoint yet, then releases the stack.
func checkRunningHistoryFields(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")

	updates := readHistory(ctx, t, env, org)

	found := false

	for _, u := range updates {
		if u.UpdateID != held.ID {
			continue
		}

		found = true

		if u.RequestedBy != e2e.Subject {
			t.Errorf("running update requestedBy = %q, want %q", u.RequestedBy, e2e.Subject)
		}

		if u.LeaseExpiresAt <= 0 {
			t.Errorf("running update leaseExpiresAt = %d, want above 0", u.LeaseExpiresAt)
		}

		if u.CheckpointBytes != 0 {
			t.Errorf("running update checkpointBytes = %d, want 0 before any checkpoint", u.CheckpointBytes)
		}
	}

	if !found {
		t.Errorf("history holds no running update %q (%+v)", held.ID, updates)
	}

	env.EndHeldUpdate(ctx, t, held, "cancelled") //nolint:misspell // The CLI's wire value; it must not change.
}

// pulumiRestoreScenario proves the restore route (ADR 28 decision 5) on the
// renamed stack. There is no CLI command for restore, so the restore steps
// call the route with the admin token: two ups, a restore of version 1,
// export equality with version 1, up after the restore, 404 for a version
// with no checkpoint, 409 during a running update freed by cancel, and the
// restore marker in history.
func pulumiRestoreScenario(t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	ctx := t.Context()

	env.Step(ctx, t, step("up before restore 1", nil, "up", "--yes", "--non-interactive"))
	env.Step(ctx, t, step("up before restore 2", nil, "up", "--yes", "--non-interactive"))

	restoreStackVersion(t, env, org, 1, http.StatusOK)

	exported := env.Step(ctx, t, step("stack export after restore", nil, "stack", "export"))
	pinned := env.Step(
		ctx,
		t,
		step("stack export version 1 after restore", nil, "stack", "export", "--version", "1"),
	)

	if strings.TrimSpace(exported.Stdout) != strings.TrimSpace(pinned.Stdout) {
		t.Error("stack export after restoring version 1 differs from stack export --version 1")
	}

	env.Step(ctx, t, step("up after restore", nil, "up", "--yes", "--non-interactive"))

	// The CLI parses the new kind on every version in the matrix.
	env.Step(
		ctx,
		t,
		step("stack history after restore", historyShowsKinds("restore"), "stack", "history", "--json"),
	)

	// A version with no checkpoint has no state to copy.
	restoreStackVersion(t, env, org, 999999, http.StatusNotFound)

	// The version is a positive integer.
	restoreStackVersion(t, env, org, 0, http.StatusBadRequest)

	// A restore waits for the stack lock: hold the stack through the API,
	// prove the restore conflicts, cancel through the CLI in a second
	// process, then the restore works.
	env.StartHeldUpdate(ctx, t, org, e2e.Project, "renamed")
	restoreStackVersion(t, env, org, 1, http.StatusConflict)
	env.Step(ctx, t, step("cancel frees the restore", contains("has been canceled"), "cancel", "--yes"))
	restoreStackVersion(t, env, org, 1, http.StatusOK)

	checkRestoreHistory(ctx, t, env, org)
}

// restoreStackVersion restores version of the renamed stack through the API
// with the admin token and proves the status. A success answers the ID of
// the restore update; an error answers the Pulumi shape
// {"code": <status>, "message": "..."}.
func restoreStackVersion(t *testing.T, env *e2e.Env, org string, version, want int) {
	t.Helper()

	ctx := t.Context()

	path := fmt.Sprintf("/api/stacks/%s/%s/renamed/restore/%d", org, e2e.Project, version)

	status, body, err := env.APIStatus(ctx, http.MethodPost, path, "")
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}

	if status != want {
		t.Fatalf("POST %s status %d, want %d (body %s)", path, status, want, body)
	}

	if want == http.StatusOK {
		var decoded struct {
			UpdateID string `json:"updateId"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
		}

		if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded.UpdateID == "" {
			t.Fatalf("POST %s answers %q, want an update id: %v", path, body, err)
		}

		return
	}

	var decoded struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error of POST %s: %v (%s)", path, err, body)
	}

	if decoded.Code != want || decoded.Message == "" {
		t.Fatalf("POST %s error = %+v, want code %d with a message", path, decoded, want)
	}
}

// checkRestoreHistory proves the history shows the restore: one entry of
// kind restore records version 1 as its source and the caller as its
// requester.
func checkRestoreHistory(ctx context.Context, t *testing.T, env *e2e.Env, org string) {
	t.Helper()

	for _, u := range readHistory(ctx, t, env, org) {
		if u.Kind != "restore" {
			continue
		}

		if u.RestoredFrom != 1 {
			t.Fatalf("restore entry %+v restoredFrom = %d, want 1", u, u.RestoredFrom)
		}

		if u.RequestedBy != e2e.Subject {
			t.Fatalf("restore entry %+v requestedBy = %q, want %q", u, u.RequestedBy, e2e.Subject)
		}

		if u.UpdateID == "" {
			t.Fatalf("restore entry %+v holds no update ID", u)
		}

		return
	}

	t.Fatal("history holds no restore entry")
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
		Env:    []string{"PULUMI_BACKEND_URL=" + apiURL, "HTTP_PROXY=" + env.URL},
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check:  contains("(" + overrideURL + ")"),
		Name:   "console url from PULUMI_CONSOLE_DOMAIN",
		Args:   []string{"login"},
		Env:    []string{"PULUMI_CONSOLE_DOMAIN=console.example.test:3000"},
		Stdin:  "",
		Fail:   false,
		Redact: false,
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
		step("apply", checkApplyReadsStateVersion(env), "apply", "-input=false", "-auto-approve", "-no-color"),
		step("output", checkTofuOutput(env), "output", "-json", "-no-color"),
		step("state pull", contains(`"greeting"`), "state", "pull"),
	} {
		env.Step(ctx, t, s)
	}

	// A second apply with a changed output makes a second state version, so
	// the list below holds two versions.
	env.WriteTofuConfig(t, tofuSecondConfig(env.Host()))
	env.Step(ctx, t, step("apply again", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	// The list route serves the two versions with the go-tfe query shape.
	tofuStateVersionListScenario(t, env)

	// A restore of the first version makes it current again: state pull
	// matches its MD5, a new apply succeeds, and a locked restore names the
	// holder.
	tofuRestoreScenario(t, env)

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

	// Import source T1 (ADR 29): local state, the http backend and
	// client-side encryption move into OpenDustpan.
	tofuImportScenario(t, env)
}

// checkApplyReadsStateVersion proves apply fetched the state version it
// created through GET /api/v2/state-versions/{id}: the CLI reads back what
// it wrote before it unlocks.
func checkApplyReadsStateVersion(env *e2e.Env) func(e2e.Result) error {
	return func(e2e.Result) error {
		entries := env.Recorder.Entries()
		for i := range entries {
			entry := &entries[i]
			if entry.Step != "apply" || entry.Method != http.MethodGet {
				continue
			}

			if strings.HasPrefix(entry.Path, "/api/v2/state-versions/") &&
				!strings.HasSuffix(entry.Path, "/download") &&
				entry.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: apply never read its state version", errUnexpected)
	}
}

// checkTofuOutput proves output printed the greeting and read the outputs
// through GET .../current-state-version-outputs.
func checkTofuOutput(env *e2e.Env) func(e2e.Result) error {
	return func(r e2e.Result) error {
		if !strings.Contains(r.Stdout, `"hello"`) {
			return fmt.Errorf("%w: stdout lacks the greeting: %s", errUnexpected, r.Stdout)
		}

		entries := env.Recorder.Entries()
		for i := range entries {
			entry := &entries[i]
			if entry.Step == "output" && entry.Method == http.MethodGet &&
				strings.HasSuffix(entry.Path, "/current-state-version-outputs") &&
				entry.Status == http.StatusOK {
				return nil
			}
		}

		return fmt.Errorf("%w: output never read the current outputs", errUnexpected)
	}
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

// importSourceConfig is a project without a backend: apply keeps local
// state. It uses the built-in terraform_data resource, so no provider is
// downloaded.
const importSourceConfig = `resource "terraform_data" "smoke" {
  input = "hello"
}

output "greeting" {
  value = "hello"
}
`

// importPassphrase is the test-only passphrase of the encrypted import
// source. The destination reads the moved state with the same block.
const importPassphrase = "correct horse battery staple"

// importCloudConfig returns the same project with a cloud block on host and
// the workspace name, so init -migrate-state copies the source state there.
func importCloudConfig(host, workspace string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

`, host, e2e.Namespace, workspace) + importSourceConfig
}

// importEncryptionBlock returns the client-side encryption block shared by
// the encrypted source and its destination.
func importEncryptionBlock() string {
	return fmt.Sprintf(`  encryption {
    key_provider "pbkdf2" "e2e" {
      passphrase = %q
    }
    method "aes_gcm" "e2e" {
      keys = key_provider.pbkdf2.e2e
    }
    state {
      method = method.aes_gcm.e2e
    }
  }
`, importPassphrase)
}

// importEncryptedSourceConfig returns the source project with client-side
// encryption and a local backend: apply writes the encrypted envelope into
// the state file.
func importEncryptedSourceConfig() string {
	return "terraform {\n" + importEncryptionBlock() + "}\n\n" + importSourceConfig
}

// importEncryptedCloudConfig returns the same encrypted project with a cloud
// block on host and the workspace name.
func importEncryptedCloudConfig(host, workspace string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
`, host, e2e.Namespace, workspace) + importEncryptionBlock() + "}\n\n" + importSourceConfig
}

// importStateDoc decodes the serial, lineage, outputs and resources of a
// pulled state.
type importStateDoc struct {
	Serial    int64          `json:"serial"`
	Lineage   string         `json:"lineage"`
	Outputs   map[string]any `json:"outputs"`
	Resources []any          `json:"resources"`
}

// decodeImportState decodes stdout as a pulled state.
func decodeImportState(stdout string) (importStateDoc, error) {
	var doc importStateDoc

	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		return importStateDoc{}, fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
	}

	return doc, nil
}

// canonicalImportState returns the outputs and resources of doc as one
// string, so a check compares state content without serial and lineage.
func canonicalImportState(doc importStateDoc) (string, error) {
	raw, err := json.Marshal(struct {
		Outputs   map[string]any `json:"outputs"`
		Resources []any          `json:"resources"`
	}{Outputs: doc.Outputs, Resources: doc.Resources})
	if err != nil {
		return "", fmt.Errorf("%w: encode state content: %w", errUnexpected, err)
	}

	return string(raw), nil
}

// writeImportConfig writes content as main.tf in dir for an import move.
func writeImportConfig(t *testing.T, dir, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("write import config: %v", err)
	}
}

// importMigrateStep returns the migrate step: plain init with input on,
// answering yes to the copy approval. A cloud block takes no migrate flags:
// -migrate-state and -force-copy are for state backends only, and the copy
// into a cloud backend is an interactive prompt instead.
func importMigrateStep(name string) e2e.StepSpec {
	return e2e.StepSpec{
		Check:  nil,
		Name:   name,
		Args:   []string{"init", "-no-color"},
		Env:    []string{"TF_INPUT=1"},
		Stdin:  "yes\n",
		Fail:   false,
		Redact: false,
	}
}

// importRecord renders the lineage and serial before and after a move for
// the report.
func importRecord(before, after importStateDoc) string {
	return fmt.Sprintf(
		"source serial=%d lineage=%s; destination serial=%d lineage=%s",
		before.Serial,
		before.Lineage,
		after.Serial,
		after.Lineage,
	)
}

// checkImportBefore records the source state and proves it has a serial and
// a lineage, then runs the extra source checks, if any.
func checkImportBefore(before *importStateDoc, extra func() error) func(e2e.Result) error {
	return func(r e2e.Result) error {
		doc, err := decodeImportState(r.Stdout)
		if err != nil {
			return err
		}

		if doc.Serial < 1 {
			return fmt.Errorf("%w: source serial is %d, want at least 1", errUnexpected, doc.Serial)
		}

		if doc.Lineage == "" {
			return fmt.Errorf("%w: source state has no lineage", errUnexpected)
		}

		*before = doc

		if extra != nil {
			return extra()
		}

		return nil
	}
}

// checkImportPreserved proves kept holds the state before: the same content
// and the same serial.
func checkImportPreserved(before, kept importStateDoc) error {
	want, err := canonicalImportState(before)
	if err != nil {
		return err
	}

	got, err := canonicalImportState(kept)
	if err != nil {
		return err
	}

	if got != want {
		return fmt.Errorf("%w: the moved state lost content", errUnexpected)
	}

	if kept.Serial != before.Serial {
		return fmt.Errorf(
			"%w: moved serial is %d, want the source serial %d",
			errUnexpected,
			kept.Serial,
			before.Serial,
		)
	}

	return nil
}

// checkImportAfter records the destination state and proves the move: the
// content and the serial match the source, and the lineage is fresh, because
// OpenTofu writes a new lineage into an empty workspace. It then proves the
// source is unchanged, if asked.
func checkImportAfter(
	before, after *importStateDoc, checkSource func() error,
) func(e2e.Result) error {
	return func(r e2e.Result) error {
		doc, err := decodeImportState(r.Stdout)
		if err != nil {
			return err
		}

		*after = doc

		if err := checkImportPreserved(*before, doc); err != nil {
			return err
		}

		if doc.Lineage == "" || doc.Lineage == before.Lineage {
			return fmt.Errorf(
				"%w: destination lineage is %q, want a fresh lineage unlike the source %q",
				errUnexpected,
				doc.Lineage,
				before.Lineage,
			)
		}

		if checkSource != nil {
			return checkSource()
		}

		return nil
	}
}

// checkImportPlan proves plan shows no changes after the move.
func checkImportPlan(r e2e.Result) error {
	if out := r.Stdout + r.Stderr; !strings.Contains(out, "No changes.") {
		return fmt.Errorf("%w: plan lacks a no-changes report: %s", errUnexpected, out)
	}

	return nil
}

// checkImportBackup proves the local source is unchanged: its backup keeps
// the state the source phase wrote, with the same serial and lineage.
func checkImportBackup(dir string, before importStateDoc) error {
	backupFile := filepath.Join(dir, "terraform.tfstate.backup")
	raw, err := os.ReadFile(backupFile) //nolint:gosec // The test reads the state file it wrote.
	if err != nil {
		return fmt.Errorf("%w: read the local backup: %w", errUnexpected, err)
	}

	kept, err := decodeImportState(string(raw))
	if err != nil {
		return err
	}

	if err := checkImportPreserved(before, kept); err != nil {
		return err
	}

	if kept.Lineage != before.Lineage {
		return fmt.Errorf(
			"%w: backup lineage is %q, want the source lineage %q",
			errUnexpected,
			kept.Lineage,
			before.Lineage,
		)
	}

	return nil
}

// checkHTTPBackendQuiet proves the http source got no write after the copy:
// the write count stays and no delete arrived. Locks do not count: the move
// locks the source where the source allows it.
func checkHTTPBackendQuiet(backend *e2e.HTTPBackend, writes, deletes int) error {
	if got := backend.Writes(); got != writes {
		return fmt.Errorf("%w: http backend holds %d writes, want %d", errUnexpected, got, writes)
	}

	if got := backend.Counts().Deletes; got != deletes {
		return fmt.Errorf("%w: http backend holds %d deletes, want %d", errUnexpected, got, deletes)
	}

	return nil
}

// checkImportEncryptedFile returns a check that proves the source state file
// holds the encrypted envelope.
func checkImportEncryptedFile(dir string) func() error {
	return func() error {
		state := filepath.Join(dir, "terraform.tfstate")
		raw, err := os.ReadFile(state) //nolint:gosec // The test reads the state file it wrote.
		if err != nil {
			return fmt.Errorf("%w: read the encrypted source: %w", errUnexpected, err)
		}

		if !strings.Contains(string(raw), "encrypted_data") {
			return fmt.Errorf("%w: the source state holds no encrypted envelope", errUnexpected)
		}

		return nil
	}
}

// importEncryptedBackupDoc decodes the plaintext envelope fields of an
// encrypted state file: the serial and the lineage travel outside the
// encrypted payload.
type importEncryptedBackupDoc struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
}

// decodeImportEnvelope decodes the plaintext envelope fields of an
// encrypted state file: the serial and the lineage travel outside the
// encrypted payload.
func decodeImportEnvelope(raw []byte) (importEncryptedBackupDoc, error) {
	var envelope importEncryptedBackupDoc

	if err := json.Unmarshal(raw, &envelope); err != nil {
		return importEncryptedBackupDoc{}, fmt.Errorf("%w: decode the encrypted backup: %w", errUnexpected, err)
	}

	return envelope, nil
}

// checkImportEnvelopePreserved proves the backup envelope keeps the source
// serial and lineage.
func checkImportEnvelopePreserved(envelope importEncryptedBackupDoc, before importStateDoc) error {
	if envelope.Serial != before.Serial {
		return fmt.Errorf(
			"%w: backup serial is %d, want the source serial %d",
			errUnexpected,
			envelope.Serial,
			before.Serial,
		)
	}

	if envelope.Lineage != before.Lineage {
		return fmt.Errorf(
			"%w: backup lineage is %q, want the source lineage %q",
			errUnexpected,
			envelope.Lineage,
			before.Lineage,
		)
	}

	return nil
}

// checkImportEncryptedBackup proves the encrypted local source is unchanged
// after the move: the backup keeps the source serial and lineage, and its
// envelope decrypts to the source content. The bytes always differ, because
// every state write re-encrypts with a fresh nonce, so the check compares
// documents, not bytes.
func checkImportEncryptedBackup(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	dir string,
	before importStateDoc,
) func() error {
	t.Helper()

	return func() error {
		backupFile := filepath.Join(dir, "terraform.tfstate.backup")
		// The test reads the state file it wrote.
		raw, err := os.ReadFile(backupFile) //nolint:gosec // Test file with a fixed name.
		if err != nil {
			return fmt.Errorf("%w: read the encrypted backup: %w", errUnexpected, err)
		}

		if !strings.Contains(string(raw), "encrypted_data") {
			return fmt.Errorf("%w: the encrypted backup holds no encrypted envelope", errUnexpected)
		}

		envelope, err := decodeImportEnvelope(raw)
		if err != nil {
			return err
		}

		if checkErr := checkImportEnvelopePreserved(envelope, before); checkErr != nil {
			return checkErr
		}

		kept, err := decryptImportBackup(ctx, t, env, raw)
		if err != nil {
			return err
		}

		if checkErr := checkImportPreserved(before, kept); checkErr != nil {
			return checkErr
		}

		if kept.Lineage != before.Lineage {
			return fmt.Errorf(
				"%w: backup lineage is %q, want the source lineage %q",
				errUnexpected,
				kept.Lineage,
				before.Lineage,
			)
		}

		return nil
	}
}

// decryptImportBackup decrypts the encrypted backup raw with the test
// passphrase: it runs the CLI in a scratch directory that holds the backup
// as its local state beside the encrypted source project, so init and state
// pull read it through the same encryption block. It runs through RunIn, so
// it records no extra step.
func decryptImportBackup(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	raw []byte,
) (importStateDoc, error) {
	t.Helper()

	scratch := t.TempDir()

	backupCopy := filepath.Join(scratch, "terraform.tfstate")
	if err := os.WriteFile(backupCopy, raw, 0o600); err != nil { //nolint:gosec // Temp dir with a fixed name.
		return importStateDoc{}, fmt.Errorf("%w: write the backup copy: %w", errUnexpected, err)
	}

	writeImportConfig(t, scratch, importEncryptedSourceConfig())

	init, err := env.RunIn(ctx, scratch, nil, "", "init", "-input=false", "-no-color")
	if err != nil {
		return importStateDoc{}, fmt.Errorf("%w: init the decrypt project: %w", errUnexpected, err)
	}

	if init.ExitCode != 0 {
		return importStateDoc{}, fmt.Errorf(
			"%w: init the decrypt project exits %d: %s%s",
			errUnexpected,
			init.ExitCode,
			init.Stdout,
			init.Stderr,
		)
	}

	pulled, err := env.RunIn(ctx, scratch, nil, "", "state", "pull", "-no-color")
	if err != nil {
		return importStateDoc{}, fmt.Errorf("%w: pull the decrypted backup: %w", errUnexpected, err)
	}

	if pulled.ExitCode != 0 {
		return importStateDoc{}, fmt.Errorf(
			"%w: pull the decrypted backup exits %d: %s%s",
			errUnexpected,
			pulled.ExitCode,
			pulled.Stdout,
			pulled.Stderr,
		)
	}

	return decodeImportState(pulled.Stdout)
}

// tofuImportScenario proves import source T1 (ADR 29): local state and the
// http backend move into OpenDustpan with an interactive init that copies
// the source state, each with its own run and workspace, and client-side
// encryption stays.
func tofuImportScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	tofuImportLocal(ctx, t, env)
	tofuImportHTTP(ctx, t, env)
	tofuImportEncrypted(ctx, t, env)
}

// tofuImportLocal moves local state: apply with the default backend, then a
// cloud block and init, answering yes to the copy approval. The local
// backup keeps the source state.
func tofuImportLocal(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := t.TempDir()
	writeImportConfig(t, dir, importSourceConfig)

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import local init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("import local apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import local state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfig(env.Host(), "import-local"))

	env.StepIn(ctx, t, dir, importMigrateStep("import local migrate"))
	env.StepIn(ctx, t, dir, step(
		"import local state after",
		checkImportAfter(&before, &after, func() error { return checkImportBackup(dir, before) }),
		"state", "pull", "-no-color",
	))
	env.StepIn(ctx, t, dir, step("import local plan", checkImportPlan, "plan", "-input=false", "-no-color"))

	env.NoteStep(t, "import local lineage and serial", importRecord(before, after))
}

// tofuImportHTTP moves the http backend: apply against the backend, then a
// cloud block and init, answering yes to the copy approval. The backend
// counts no write after the copy.
func tofuImportHTTP(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	backend := e2e.NewHTTPBackend(t)
	dir := t.TempDir()
	writeImportConfig(t, dir, backend.Config())

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import http init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("import http apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	writesBefore := backend.Writes()
	deletesBefore := backend.Counts().Deletes

	env.StepIn(
		ctx,
		t,
		dir,
		step("import http state before", checkImportBefore(&before, nil), "state", "pull", "-no-color"),
	)

	writeImportConfig(t, dir, importCloudConfig(env.Host(), "import-http"))

	env.StepIn(ctx, t, dir, importMigrateStep("import http migrate"))
	env.StepIn(ctx, t, dir, step(
		"import http state after",
		checkImportAfter(&before, &after, func() error {
			return checkHTTPBackendQuiet(backend, writesBefore, deletesBefore)
		}),
		"state", "pull", "-no-color",
	))
	env.StepIn(ctx, t, dir, step("import http plan", checkImportPlan, "plan", "-input=false", "-no-color"))

	env.NoteStep(t, "import http lineage and serial", importRecord(before, after))
}

// tofuImportEncrypted moves client-side encrypted state: apply writes the
// encrypted envelope into the local file, plain init answers yes to the copy
// approval and moves it as it is, and the destination reads it with the same
// block. The local backup keeps the source serial, lineage and content
// behind a fresh envelope.
func tofuImportEncrypted(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := t.TempDir()
	writeImportConfig(t, dir, importEncryptedSourceConfig())

	var before, after importStateDoc

	env.StepIn(ctx, t, dir, step("import encrypted init", nil, "init", "-input=false", "-no-color"))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import encrypted apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
	)
	env.StepIn(ctx, t, dir, step(
		"import encrypted state before",
		checkImportBefore(&before, checkImportEncryptedFile(dir)),
		"state", "pull", "-no-color",
	))

	writeImportConfig(t, dir, importEncryptedCloudConfig(env.Host(), "import-encrypted"))

	env.StepIn(ctx, t, dir, importMigrateStep("import encrypted migrate"))
	env.StepIn(ctx, t, dir, step(
		"import encrypted state after",
		checkImportAfter(&before, &after, checkImportEncryptedBackup(ctx, t, env, dir, before)),
		"state", "pull", "-no-color",
	))
	env.StepIn(
		ctx,
		t,
		dir,
		step("import encrypted plan", checkImportPlan, "plan", "-input=false", "-no-color"),
	)

	env.NoteStep(t, "import encrypted lineage and serial", importRecord(before, after))

	checkImportEncryptedStored(ctx, t, env)
}

// importStateVersionData holds the data object of a current-state-version
// document.
type importStateVersionData struct {
	ID string `json:"id"`
}

// importStateVersionRef decodes a current-state-version document in tests.
type importStateVersionRef struct {
	Data importStateVersionData `json:"data"`
}

// importStateVersionID returns the id of the current-state-version document.
func importStateVersionID(body string) (string, error) {
	var current importStateVersionRef

	if err := json.Unmarshal([]byte(body), &current); err != nil {
		return "", fmt.Errorf("%w: decode current state: %w", errUnexpected, err)
	}

	if current.Data.ID == "" {
		return "", fmt.Errorf("%w: current state has no id", errUnexpected)
	}

	return current.Data.ID, nil
}

// checkImportEncryptedStored proves the moved state stays encrypted on the
// destination: the stored bytes of the encrypted workspace hold the
// encrypted envelope. Each request is a recorded API step, so the report
// holds the exchange and the bodies stay clipped there.
func checkImportEncryptedStored(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	wsID := env.WorkspaceIDByName(ctx, t, "import-encrypted")

	var versionID string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			id, err := importStateVersionID(body)
			if err != nil {
				return err
			}

			versionID = id

			return nil
		},
		Name:   "import encrypted current state",
		Method: http.MethodGet,
		Path:   "/api/v2/workspaces/" + wsID + "/current-state-version",
		Body:   "",
		Want:   http.StatusOK,
	})

	if versionID == "" {
		return
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			if !strings.Contains(body, "encrypted_data") {
				return fmt.Errorf(
					"%w: the stored state of the encrypted workspace holds no encrypted envelope",
					errUnexpected,
				)
			}

			return nil
		},
		Name:   "import encrypted stored state",
		Method: http.MethodGet,
		Path:   "/api/v2/state-versions/" + versionID + "/download",
		Body:   "",
		Want:   http.StatusOK,
	})
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
		Check:  checkPlanLockedByAnother(ctx, env),
		Name:   "plan locked by another",
		Args:   []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   true,
		Redact: false,
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
		Check:  nil,
		Name:   "force-unlock by B",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	// A plan as B now passes.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "plan after force-unlock",
		Args:   []string{"plan", "-input=false", "-no-color"},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
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

// tofuSecondConfig is the configuration of the second apply: the same cloud
// block with a changed greeting output, so the apply writes a second state
// version.
func tofuSecondConfig(host string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

output "greeting" {
  value = "hello again"
}
`
}

// stateVersionListAttributes holds the listed attributes the e2e list step
// checks: the serial for order and the added holder, force and
// restored-from attributes.
type stateVersionListAttributes struct {
	Serial       int64  `json:"serial"`
	Holder       string `json:"holder"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionListItem holds one entry of a state version list document.
type stateVersionListItem struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Attributes stateVersionListAttributes `json:"attributes"`
}

// stateVersionListPagination holds the meta.pagination object of a state
// version list document.
type stateVersionListPagination struct {
	CurrentPage int `json:"current-page"` //nolint:tagliatelle // The TFE wire name; it must not change.
	PageSize    int `json:"page-size"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalCount  int `json:"total-count"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalPages  int `json:"total-pages"`  //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionListMeta holds the meta object of a state version list
// document.
type stateVersionListMeta struct {
	Pagination stateVersionListPagination `json:"pagination"`
}

// stateVersionListDoc decodes a state version list document.
type stateVersionListDoc struct {
	Data []stateVersionListItem `json:"data"`
	Meta stateVersionListMeta   `json:"meta"`
}

// decodeStateVersionList decodes body into a state version list document.
func decodeStateVersionList(body string) (stateVersionListDoc, error) {
	var doc stateVersionListDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return stateVersionListDoc{}, fmt.Errorf("%w: decode state version list: %w", errUnexpected, err)
	}

	return doc, nil
}

// stateVersionListBase is the list target with the go-tfe request shape:
// filter[organization][name], filter[workspace][name], page[number] and
// page[size].
func stateVersionListBase() string {
	return "/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace +
		"&filter[workspace][name]=" + e2e.Workspace
}

// checkStateVersionListTwo proves the list holds two versions, newest first,
// with the holder, force and restored-from attributes on every entry.
func checkStateVersionListTwo(body string) error {
	var raw struct {
		Data []map[string]any `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return fmt.Errorf("%w: decode state version list: %w", errUnexpected, err)
	}

	if len(raw.Data) != 2 {
		return fmt.Errorf("%w: list holds %d versions, want 2: %s", errUnexpected, len(raw.Data), body)
	}

	for _, item := range raw.Data {
		attrs, ok := item["attributes"].(map[string]any)
		if !ok {
			return fmt.Errorf("%w: entry holds no attributes: %s", errUnexpected, body)
		}

		for _, key := range []string{"holder", "force", "restored-from"} {
			if _, ok := attrs[key]; !ok {
				return fmt.Errorf("%w: entry lacks attribute %q: %s", errUnexpected, key, body)
			}
		}
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		return err
	}

	if doc.Data[0].ID == doc.Data[1].ID {
		return fmt.Errorf("%w: list holds the same version twice: %s", errUnexpected, body)
	}

	if doc.Data[0].Attributes.Serial <= doc.Data[1].Attributes.Serial {
		return fmt.Errorf(
			"%w: list is not newest first: serials %d then %d",
			errUnexpected,
			doc.Data[0].Attributes.Serial,
			doc.Data[1].Attributes.Serial,
		)
	}

	if doc.Meta.Pagination.TotalCount != 2 {
		return fmt.Errorf("%w: total count is %d, want 2: %s", errUnexpected, doc.Meta.Pagination.TotalCount, body)
	}

	return nil
}

// checkStateVersionListPage proves one list page holds the want ID with the
// want pagination.
func checkStateVersionListPage(wantID string, number, size int) func(string) error {
	return func(body string) error {
		doc, err := decodeStateVersionList(body)
		if err != nil {
			return err
		}

		if len(doc.Data) != 1 || doc.Data[0].ID != wantID {
			return fmt.Errorf("%w: page holds %+v, want one entry %q", errUnexpected, doc.Data, wantID)
		}

		page := doc.Meta.Pagination
		if page.CurrentPage != number || page.PageSize != size || page.TotalCount != 2 || page.TotalPages != 2 {
			return fmt.Errorf(
				"%w: pagination %+v, want page %d of size %d with 2 entries in 2 pages",
				errUnexpected,
				page,
				number,
				size,
			)
		}

		return nil
	}
}

// tofuStateVersionListScenario proves the list route after two applies: both
// versions listed newest first with the added attributes, and page[size] and
// page[number] respected. Every request uses the go-tfe query shape.
func tofuStateVersionListScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	base := stateVersionListBase()

	var newest, oldest string

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check: func(body string) error {
			if err := checkStateVersionListTwo(body); err != nil {
				return err
			}

			doc, err := decodeStateVersionList(body)
			if err != nil {
				return err
			}

			newest, oldest = doc.Data[0].ID, doc.Data[1].ID

			return nil
		},
		Name:   "list state versions",
		Method: http.MethodGet,
		Path:   base,
		Body:   "",
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkStateVersionListPage(newest, 1, 1),
		Name:   "list state versions first page",
		Method: http.MethodGet,
		Path:   base + "&page[size]=1&page[number]=1",
		Body:   "",
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkStateVersionListPage(oldest, 2, 1),
		Name:   "list state versions second page",
		Method: http.MethodGet,
		Path:   base + "&page[size]=1&page[number]=2",
		Body:   "",
		Want:   http.StatusOK,
	})
}

// restoreResponseAttributes holds the attributes a restore response carries:
// the MD5 and size of the copied state, the forced flag and the version it
// was restored from.
type restoreResponseAttributes struct {
	MD5          string `json:"md5"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// restoreResponseData holds the data object of a restore response in tests.
type restoreResponseData struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	Attributes restoreResponseAttributes `json:"attributes"`
}

// restoreResponseMeta holds the meta object of a restore response in tests.
type restoreResponseMeta struct {
	Message string `json:"message"`
}

// restoreResponseDoc decodes a restore response: the new state version and
// the message that no infrastructure changed.
type restoreResponseDoc struct {
	Data restoreResponseData `json:"data"`
	Meta restoreResponseMeta `json:"meta"`
}

// decodeRestoreResponse decodes body into a restore response.
func decodeRestoreResponse(body string) (restoreResponseDoc, error) {
	var doc restoreResponseDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return restoreResponseDoc{}, fmt.Errorf("%w: decode restore response: %w", errUnexpected, err)
	}

	return doc, nil
}

// checkRestoreResponse proves a restore response holds a new version restored
// from wantFrom with the forced flag and the message that no infrastructure
// changed.
func checkRestoreResponse(wantFrom string) func(string) error {
	return func(body string) error {
		doc, err := decodeRestoreResponse(body)
		if err != nil {
			return err
		}

		if doc.Data.ID == "" || doc.Data.ID == wantFrom {
			return fmt.Errorf(
				"%w: restore holds id %q, want a new version from %q",
				errUnexpected,
				doc.Data.ID,
				wantFrom,
			)
		}

		if doc.Data.Type != "state-versions" {
			return fmt.Errorf("%w: restore type is %q, want state-versions", errUnexpected, doc.Data.Type)
		}

		if !doc.Data.Attributes.Force || doc.Data.Attributes.RestoredFrom != wantFrom {
			return fmt.Errorf(
				"%w: restore holds force %v restored from %q, want force true from %q",
				errUnexpected,
				doc.Data.Attributes.Force,
				doc.Data.Attributes.RestoredFrom,
				wantFrom,
			)
		}

		if msg := strings.ToLower(doc.Meta.Message); !strings.Contains(msg, "no infrastructure") {
			return fmt.Errorf(
				"%w: restore message %q lacks the words that no infrastructure changed",
				errUnexpected,
				doc.Meta.Message,
			)
		}

		return nil
	}
}

// md5Hex answers the hex MD5 of s.
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	return hex.EncodeToString(sum[:])
}

// downloadStateVersion fetches the stored bytes of a state version through
// the API, failing the test when the version is missing.
func downloadStateVersion(ctx context.Context, t *testing.T, env *e2e.Env, id string) string {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/state-versions/"+id+"/download", "")
	if err != nil {
		t.Fatalf("download state %s: %v", id, err)
	}

	if status != http.StatusOK {
		t.Fatalf("download state %s answers %d: %s", id, status, body)
	}

	return body
}

// pulledOutput holds one output of a pulled state in tests.
type pulledOutput struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

// pulledRestoreState decodes the serial, lineage and outputs of a pulled
// state in tests.
type pulledRestoreState struct {
	Serial  int64                   `json:"serial"`
	Lineage string                  `json:"lineage"`
	Outputs map[string]pulledOutput `json:"outputs"`
}

// checkStatePullMatchesFirst proves a state pull holds the restored state:
// the same serial, lineage and greeting output as the first version. The CLI
// re-serializes the document, so the test compares the parsed state, not its
// bytes.
func checkStatePullMatchesFirst(firstSerial int64, firstLineage string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var got pulledRestoreState

		if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil {
			return fmt.Errorf("%w: decode pulled state: %w", errUnexpected, err)
		}

		if got.Serial != firstSerial || got.Lineage != firstLineage {
			return fmt.Errorf(
				"%w: pulled serial %d lineage %q, want serial %d lineage %q",
				errUnexpected,
				got.Serial,
				got.Lineage,
				firstSerial,
				firstLineage,
			)
		}

		greeting, ok := got.Outputs["greeting"]
		if !ok || greeting.Value != "hello" {
			return fmt.Errorf("%w: pulled state lacks the greeting hello: %s", errUnexpected, r.Stdout)
		}

		return nil
	}
}

// currentStateMD5Attributes holds the MD5 attribute of a current state
// version in tests.
type currentStateMD5Attributes struct {
	MD5 string `json:"md5"`
}

// currentStateMD5Data holds the data object of a current state version in
// tests.
type currentStateMD5Data struct {
	Attributes currentStateMD5Attributes `json:"attributes"`
}

// currentStateMD5Doc decodes a current state version in tests.
type currentStateMD5Doc struct {
	Data currentStateMD5Data `json:"data"`
}

// currentStateMD5 fetches the MD5 attribute of the workspace current state
// version, failing the test when the workspace has none.
func currentStateMD5(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) string {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID+"/current-state-version", "")
	if err != nil {
		t.Fatalf("get current state after restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("get current state after restore answers %d: %s", status, body)
	}

	var doc currentStateMD5Doc

	if decodeErr := json.Unmarshal([]byte(body), &doc); decodeErr != nil {
		t.Fatalf("decode current state after restore: %v", decodeErr)
	}

	return doc.Data.Attributes.MD5
}

// checkRestoreConflict proves a restore that another principal blocks answers
// 409 with a detail holding "is locked by User" and the holder.
func checkRestoreConflict(body string) error {
	if !strings.Contains(body, "is locked by User") || !strings.Contains(body, e2e.SubjectB) {
		return fmt.Errorf(
			"%w: restore conflict lacks %q and the holder %q: %s",
			errUnexpected,
			"is locked by User",
			e2e.SubjectB,
			body,
		)
	}

	return nil
}

// checkRestoreKeepsHistory proves a restore of an older version loses no
// version: after the restore and the apply that follows it, the list holds
// four versions, and the version that was newest before the restore is
// still among them.
func checkRestoreKeepsHistory(ctx context.Context, t *testing.T, env *e2e.Env, newest string) {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, stateVersionListBase(), "")
	if err != nil {
		t.Fatalf("list state versions after restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions after restore answers %d: %s", status, body)
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		t.Fatalf("decode state versions after restore: %v", err)
	}

	if len(doc.Data) != 4 {
		t.Fatalf("list holds %d versions after the restore and the apply, want 4", len(doc.Data))
	}

	for _, version := range doc.Data {
		if version.ID == newest {
			return
		}
	}

	t.Fatalf("list after the restore misses %s, the version that was newest before it", newest)
}

// tofuRestoreScenario proves the restore route after two applies: a restore
// of the first version makes state pull match its MD5 with the restored-from
// version and the message that no infrastructure changed, a new apply
// succeeds, a restore while another principal holds the lock answers 409 with
// the holder, and the restore works after force-unlock. A restore changes no
// other workspace: the inventory still holds one entry for the workspace.
func tofuRestoreScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, stateVersionListBase(), "")
	if err != nil {
		t.Fatalf("list state versions for restore: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions for restore answers %d: %s", status, body)
	}

	doc, err := decodeStateVersionList(body)
	if err != nil {
		t.Fatalf("decode state versions for restore: %v", err)
	}

	if len(doc.Data) != 2 {
		t.Fatalf("list holds %d versions, want 2 before the restore", len(doc.Data))
	}

	newest, oldest := doc.Data[0].ID, doc.Data[1].ID
	firstDownload := downloadStateVersion(ctx, t, env, oldest)
	firstMD5 := md5Hex(firstDownload)

	var firstState pulledRestoreState

	if decodeErr := json.Unmarshal([]byte(firstDownload), &firstState); decodeErr != nil {
		t.Fatalf("decode first state for restore: %v", decodeErr)
	}

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreResponse(oldest),
		Name:   "restore first state version",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + oldest + "/actions/restore",
		Body:   "",
		Want:   http.StatusOK,
	})

	// The new current version copies the first version: its MD5 attribute
	// equals the MD5 of the first stored bytes, and state pull holds the
	// same serial, lineage and greeting.
	if got := currentStateMD5(ctx, t, env, wsID); got != firstMD5 {
		t.Fatalf("current state MD5 is %s, want the MD5 %s of the first version", got, firstMD5)
	}

	env.Step(
		ctx,
		t,
		step(
			"state pull after restore",
			checkStatePullMatchesFirst(firstState.Serial, firstState.Lineage),
			"state",
			"pull",
		),
	)
	env.Step(ctx, t, step("apply after restore", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	checkRestoreKeepsHistory(ctx, t, env, newest)

	entries, err := env.ListInventory(ctx, e2e.Namespace)
	if err != nil {
		t.Fatalf("list inventory after restore: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("inventory holds %d entries, want one for the workspace", len(entries))
	}

	if want := "opentofu:" + e2e.Namespace + "/" + e2e.Workspace; entries[0].Reference() != want {
		t.Fatalf("inventory entry %+v, want reference %q", entries[0], want)
	}

	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:  nil,
		Name:   "lock by B for restore",
		Method: http.MethodPost,
		Path:   lockPath,
		Body:   `{"data":{}}`,
		Want:   http.StatusOK,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreConflict,
		Name:   "restore locked by another",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + newest + "/actions/restore",
		Body:   "",
		Want:   http.StatusConflict,
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock for restore",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:  checkRestoreResponse(newest),
		Name:   "restore after force-unlock",
		Method: http.MethodPost,
		Path:   "/api/v2/state-versions/" + newest + "/actions/restore",
		Body:   "",
		Want:   http.StatusOK,
	})
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
		Check:  checkPlanLockedAfterCrash(env),
		Name:   "plan fails on the crashed lock",
		Args:   []string{"plan", "-input=false", "-no-color", "-lock-timeout=0s"},
		Env:    nil,
		Stdin:  "",
		Fail:   true,
		Redact: false,
	})

	// Force-unlock as another principal releases the crashed lock.
	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock after crash",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
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

// TestPulumiServiceImport moves a stack with a secret from one service
// backend to another through the CLI alone (ADR 29 source P1). With S as the
// source and D as the destination, on every Pulumi CLI version in the matrix
// and on every store: three ups on S, a read-only dry run, a pipe of the last
// three versions into import updates on D, the checks on D, a second run that
// moves nothing and a final proof that S is unchanged. It writes an -import
// report that holds both servers.
func TestPulumiServiceImport(t *testing.T) {
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

	for _, label := range versions {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, err := fetcher.Install(installCtx, e2e.Pulumi(), label)
			if err != nil {
				t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, label, err)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					runP1ImportOnStore(t, cli, db)
				})
			}
		})
	}
}

// p1VersionCount is how many source versions the P1 import replays: the last
// three of the source history, oldest first.
const p1VersionCount = 3

// p1Snapshot holds the source export and history before the import. The
// import must leave both unchanged.
type p1Snapshot struct {
	export  string
	history string
}

// p1Fixture carries one P1 import run: the server pair and the stack names.
type p1Fixture struct {
	pair  *e2e.Pair
	dev   string
	other string
}

// runP1ImportOnStore moves a stack with a secret from the source server to
// the destination server on one store and proves every step.
func runP1ImportOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	pair := e2e.NewPair(ctx, t, cli, store)

	t.Cleanup(func() {
		writeReport(t, pair.NewPairRunAs(cli.Label+"-import"))
	})

	pair.SharePulumiHome()

	fix := &p1Fixture{
		pair:  pair,
		dev:   fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName),
		other: fmt.Sprintf("%s/%s/other", e2e.Namespace, e2e.Project),
	}

	before := fix.setupSource(ctx, t)
	since := len(pair.Src.Recorder.Entries())

	fix.dryRun(ctx, t, before)
	fix.replay(ctx, t)
	fix.checkDestination(ctx, t, before)
	fix.secondRun(ctx, t)
	fix.upOnDestination(ctx, t)
	fix.checkSourceUnchanged(ctx, t, before, since)
	fix.logoutSource(ctx, t)
}

// p1Program returns the Pulumi.yaml the P1 import moves: a stack reference
// to the other stack and a secret output from config. The checkpoint holds a
// service secret, so the import re-encrypts it on the destination. It needs
// no provider download.
func p1Program() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  apiKey:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  apiKeyOut:\n" +
		"    fn::secret: ${apiKey}\n" +
		"resources:\n" +
		"  ref:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + e2e.Namespace + "/" + e2e.Project + "/other\n"
}

// setupSource writes the secret program and builds three versions on the
// source, one up per secret value with the newest last. It saves both logins
// and returns the source export and history before the import.
func (f *p1Fixture) setupSource(ctx context.Context, t *testing.T) p1Snapshot {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst
	secrets := []string{"s3cr3t-1", "s3cr3t-2", "s3cr3t-3"}

	src.WritePulumiProject(t, p1Program())

	src.Step(ctx, t, step("login to source with token", contains(src.URL), "login"))
	src.Step(ctx, t, step("init dev on source", nil, "stack", "init", f.dev))
	src.Step(ctx, t, step("init other on source", nil, "stack", "init", f.other))
	src.Step(ctx, t, step("select dev on source", nil, "stack", "select", f.dev))

	for i, secret := range secrets {
		version := strconv.Itoa(i + 1)
		src.Step(
			ctx,
			t,
			step("config set secret "+version+" on source", nil, "config", "set", "--secret", "apiKey", secret),
		)
		src.Step(ctx, t, step("up "+version+" on source", nil, "up", "--yes", "--non-interactive"))
	}

	f.pair.SavePulumiLogin(ctx, t, src, "save login for source")
	f.pair.SavePulumiLogin(ctx, t, dst, "save login for destination")
	f.pair.UsePulumiBackend(ctx, t, src, "use source")

	exported := src.StepSavedLogin(ctx, t, step("export latest on source before import", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on source before import", p1HistoryIs("update"), "stack", "history", "--json"),
	)

	return p1Snapshot{export: exported.Stdout, history: history.Stdout}
}

// dryRun lists the stacks of the source and their versions without changing
// anything: the export and the history still match the snapshot, and no step
// sends a write request.
func (f *p1Fixture) dryRun(ctx context.Context, t *testing.T, before p1Snapshot) {
	t.Helper()

	src := f.pair.Src
	dry := []string{
		"stack ls on source",
		"select other on source",
		"history of other on source",
		"select dev on source",
		"history of dev on source",
		"export latest on source after dry run",
	}

	src.StepSavedLogin(ctx, t, step(dry[0], stackListed, "stack", "ls", "--json"))
	src.StepSavedLogin(ctx, t, step(dry[1], nil, "stack", "select", f.other))
	src.StepSavedLogin(ctx, t, step(dry[2], historyHidesPreviews, "stack", "history", "--json"))
	src.StepSavedLogin(ctx, t, step(dry[3], nil, "stack", "select", f.dev))

	history := src.StepSavedLogin(ctx, t, step(dry[4], p1HistoryIs("update"), "stack", "history", "--json"))
	exported := src.StepSavedLogin(ctx, t, step(dry[5], nil, "stack", "export"))

	if history.Stdout != before.history {
		t.Error("dry run changed the source history")
	}

	if exported.Stdout != before.export {
		t.Error("dry run changed the source export")
	}

	if writes := p1WritesInSteps(src.Recorder.Entries(), dry...); len(writes) != 0 {
		t.Errorf(
			"dry run sent %d write requests, want none (first is %s %s)",
			len(writes),
			writes[0].Method,
			writes[0].Path,
		)
	}
}

// replay pipes the last three source versions into the destination, oldest
// first, with no file: each export travels in memory into an import on
// standard input.
func (f *p1Fixture) replay(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst
	exports := make([]string, 0, 3)

	for _, version := range []string{"1", "2", "3"} {
		exported := src.StepSavedLogin(
			ctx,
			t,
			step(
				"export version "+version+" from source",
				p1ExportHoldsSecret,
				"stack",
				"export",
				"--version",
				version,
			),
		)
		exports = append(exports, exported.Stdout)
	}

	if exports[0] == exports[1] || exports[1] == exports[2] {
		t.Error("source versions share one export, want one version per secret")
	}

	f.pair.UsePulumiBackend(ctx, t, dst, "use destination")

	src.StepSavedLogin(ctx, t, step("init dev on destination", nil, "stack", "init", f.dev))
	src.StepSavedLogin(ctx, t, step("init other on destination", nil, "stack", "init", f.other))
	src.StepSavedLogin(ctx, t, step("select dev on destination", nil, "stack", "select", f.dev))
	src.StepSavedLogin(
		ctx,
		t,
		step("config set secret on destination", nil, "config", "set", "--secret", "apiKey", "s3cr3t-3"),
	)

	for i, exported := range exports {
		version := strconv.Itoa(i + 1)
		spec := step("import version "+version+" to destination", nil, "stack", "import")
		spec.Stdin = exported
		src.StepSavedLogin(ctx, t, spec)
	}
}

// checkDestination proves the replay landed: the secret output decrypts, the
// destination export matches the source latest except for the backend URL and
// the re-encrypted secrets, and the history holds three imports. The import
// time and kind replace the original update time and kind.
func (f *p1Fixture) checkDestination(ctx context.Context, t *testing.T, before p1Snapshot) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	src.StepSavedLogin(
		ctx,
		t,
		step(
			"stack output shows the secret on destination",
			contains("s3cr3t-3"),
			"stack",
			"output",
			"--show-secrets",
		),
	)

	exported := src.StepSavedLogin(ctx, t, step("export latest on destination", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination shows imports", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	normalizedSrc, srcSecrets := normalizeP1Export(t, before.export, src.URL, dst.URL)
	normalizedDst, dstSecrets := normalizeP1Export(t, exported.Stdout, src.URL, dst.URL)

	if normalizedSrc != normalizedDst {
		t.Error("destination export differs from the source export except for the backend URL and secrets")
	}

	if slices.Equal(srcSecrets, dstSecrets) {
		t.Error("destination kept the source ciphertexts, want secrets re-encrypted with its own key")
	}

	// The guide records what the replay loses: the original kind and time of
	// each version. The destination history shows kind import with the time
	// of the import.
	t.Logf("source history before the import:\n%s", before.history)
	t.Logf("destination history after the import:\n%s", history.Stdout)
}

// secondRun replays the import and moves nothing new: the destination latest
// export already matches the source latest, so no version is missing and the
// destination history and export stay identical. This is the resume rule: a
// run after a failure moves only the versions the destination still lacks.
func (f *p1Fixture) secondRun(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	latest := src.StepSavedLogin(
		ctx,
		t,
		step("export latest on destination before second run", nil, "stack", "export"),
	)
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination before second run", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	normalizedLatest, _ := normalizeP1Export(t, latest.Stdout, src.URL, dst.URL)
	exports := make([]string, 0, 3)

	for _, version := range []string{"1", "2", "3"} {
		exported := src.StepSavedLogin(
			ctx,
			t,
			step("replay version "+version+" from source", nil, "stack", "export", "--version", version),
		)
		exports = append(exports, exported.Stdout)
	}

	// Resume from the newest source version the destination already holds:
	// only newer versions still need an import. A run after a failure moves
	// the remainder; a run after a full import moves nothing.
	matched := 0

	for i, exported := range exports {
		normalized, _ := normalizeP1Export(t, exported, src.URL, dst.URL)
		if normalized == normalizedLatest {
			matched = i + 1
		}
	}

	for i := matched; i < len(exports); i++ {
		spec := step("import missing version "+strconv.Itoa(i+1)+" to destination", nil, "stack", "import")
		spec.Stdin = exports[i]
		src.StepSavedLogin(ctx, t, spec)
	}

	moved := len(exports) - matched

	t.Logf("second import run moved %d of 3 versions", moved)

	if moved != 0 {
		t.Errorf("second import run moved %d versions, want none", moved)
	}

	afterExport := src.StepSavedLogin(
		ctx,
		t,
		step("export latest on destination after second run", nil, "stack", "export"),
	)
	afterHistory := src.StepSavedLogin(
		ctx,
		t,
		step("history on destination after second run", p1HistoryIs("import"), "stack", "history", "--json"),
	)

	if afterExport.Stdout != latest.Stdout {
		t.Error("second import run changed the destination export")
	}

	if afterHistory.Stdout != history.Stdout {
		t.Error("second import run changed the destination history")
	}
}

// upOnDestination proves the moved state is current: an up changes nothing.
func (f *p1Fixture) upOnDestination(ctx context.Context, t *testing.T) {
	t.Helper()

	f.pair.Src.StepSavedLogin(
		ctx,
		t,
		step("up on destination shows no changes", upHasNoChanges, "up", "--yes", "--non-interactive"),
	)
}

// checkSourceUnchanged proves the import left the source alone: the latest
// export and the history still match the snapshot, and no write request
// reached the source past entry since. Decrypt requests are reads: the CLI
// decrypts through the source at import and encrypts through the destination.
func (f *p1Fixture) checkSourceUnchanged(
	ctx context.Context, t *testing.T, before p1Snapshot, since int,
) {
	t.Helper()

	src := f.pair.Src

	f.pair.UsePulumiBackend(ctx, t, src, "use source for final check")

	exported := src.StepSavedLogin(ctx, t, step("export latest on source after import", nil, "stack", "export"))
	history := src.StepSavedLogin(
		ctx,
		t,
		step("history on source after import", p1HistoryIs("update"), "stack", "history", "--json"),
	)

	if exported.Stdout != before.export {
		t.Error("import changed the source export")
	}

	if history.Stdout != before.history {
		t.Error("import changed the source history")
	}

	entries := src.Recorder.Entries()

	for i := since; i < len(entries); i++ {
		if entry := &entries[i]; entry.Host == src.Host() && p1IsWrite(entry.Method, entry.Path) {
			t.Errorf("import sent %s %s to the source, want no write request", entry.Method, entry.Path)

			return
		}
	}
}

// logoutSource logs out of the source and proves the destination needs no
// source login afterwards: the secret output and the config still decrypt
// through the destination alone.
func (f *p1Fixture) logoutSource(ctx context.Context, t *testing.T) {
	t.Helper()

	src := f.pair.Src
	dst := f.pair.Dst

	src.StepSavedLogin(ctx, t, step("logout of source", nil, "logout", src.URL))
	f.pair.UsePulumiBackend(ctx, t, dst, "use destination after logout")
	src.StepSavedLogin(
		ctx,
		t,
		step("stack output after source logout", contains("s3cr3t-3"), "stack", "output", "--show-secrets"),
	)
	src.StepSavedLogin(
		ctx,
		t,
		step("config get after source logout", contains("s3cr3t-3"), "config", "get", "apiKey"),
	)
}

// p1HistoryIs proves stack history holds every replayed version, each of
// kind.
func p1HistoryIs(kind string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		var updates []struct {
			Kind string `json:"kind"`
		}

		if err := json.Unmarshal([]byte(r.Stdout), &updates); err != nil {
			return fmt.Errorf("%w: decode stack history: %w", errUnexpected, err)
		}

		if len(updates) != p1VersionCount {
			return fmt.Errorf(
				"%w: stack history holds %d updates, want %d",
				errUnexpected,
				len(updates),
				p1VersionCount,
			)
		}

		for _, u := range updates {
			if u.Kind != kind {
				return fmt.Errorf("%w: stack history holds kind %q, want %q", errUnexpected, u.Kind, kind)
			}
		}

		return nil
	}
}

// p1ExportHoldsSecret proves a version export holds the stack reference and
// an encrypted secret, never its plaintext.
func p1ExportHoldsSecret(r e2e.Result) error {
	if err := exportHoldsReference(r); err != nil {
		return err
	}

	if !strings.Contains(r.Stdout, "ciphertext") {
		return fmt.Errorf("%w: export holds no encrypted secret", errUnexpected)
	}

	if strings.Contains(r.Stdout, "s3cr3t-") {
		return fmt.Errorf("%w: export printed a plaintext secret", errUnexpected)
	}

	return nil
}

// normalizeP1Export blanks the server URLs and every ciphertext of an export
// and returns the canonical form with the sorted ciphertexts. The URL names
// the server and the destination re-encrypts every secret with its own key,
// so both always differ between source and destination.
func normalizeP1Export(t *testing.T, raw string, servers ...string) (string, []string) {
	t.Helper()

	for _, server := range servers {
		raw = strings.ReplaceAll(raw, server, "")
	}

	var doc any

	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	var ciphertexts []string

	redactP1Ciphertexts(doc, &ciphertexts)

	normalized, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode stack export: %v", err)
	}

	slices.Sort(ciphertexts)

	return string(normalized), ciphertexts
}

// redactP1Ciphertexts replaces every ciphertext string under doc with "*" and
// records the originals.
func redactP1Ciphertexts(doc any, ciphertexts *[]string) {
	switch doc := doc.(type) {
	case map[string]any:
		for key, item := range doc {
			if key == "ciphertext" {
				if text, ok := item.(string); ok {
					*ciphertexts = append(*ciphertexts, text)
					doc[key] = "*"
				}

				continue
			}

			redactP1Ciphertexts(item, ciphertexts)
		}
	case []any:
		for _, item := range doc {
			redactP1Ciphertexts(item, ciphertexts)
		}
	default:
	}
}

// p1IsWrite reports whether a recorded request changes server state. The
// encrypt and decrypt routes only wrap keys, so a decrypt through the source
// during an import is not a write.
func p1IsWrite(method, path string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}

	return !strings.Contains(path, "encrypt") && !strings.Contains(path, "decrypt")
}

// p1WritesInSteps returns the write requests in entries made during steps.
func p1WritesInSteps(entries []e2e.Entry, steps ...string) []e2e.Entry {
	var writes []e2e.Entry

	for i := range entries {
		if entry := &entries[i]; slices.Contains(steps, entry.Step) && p1IsWrite(entry.Method, entry.Path) {
			writes = append(writes, *entry)
		}
	}

	return writes
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

// TestTofuImportReplay proves the T2 import (ADR 29): three applies on the
// source S, the replay script in scripts/import-tfe-state.sh (dry run first),
// three replayed versions on the destination D with the same serial, lineage
// and MD5, a normal apply on D, and a second run that moves nothing, with S
// unchanged. It runs on Linux on every OpenTofu version and store under test
// and writes a -pair report. It needs jq on PATH for the script and skips
// with a clear report when jq is absent.
func TestTofuImportReplay(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu replay suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	tool := e2e.OpenTofu()

	if skipWithoutJq(t, tool, databases) {
		t.Skip("the replay script needs jq on PATH")
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			runImportReplayLabel(t, fetcher, tool, label, databases)
		})
	}
}

// skipWithoutJq writes a skipped report for every version and store when jq
// is absent from PATH and reports whether it did. The replay script needs jq
// to read the TFE list route.
func skipWithoutJq(t *testing.T, tool e2e.Tool, databases []e2e.Store) bool {
	t.Helper()

	if _, err := exec.LookPath("jq"); err == nil {
		return false
	}

	reason := "the replay script needs jq on PATH"

	for _, label := range tool.Versions() {
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label+"-pair", db, reason, true))
		}
	}

	return true
}

// runImportReplayLabel installs one OpenTofu CLI and runs the T2 replay on
// every store under test.
func runImportReplayLabel(
	t *testing.T, fetcher *e2e.Fetcher, tool e2e.Tool, label string, databases []e2e.Store,
) {
	t.Helper()

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := fetcher.Install(installCtx, tool, label)
	if err != nil {
		for _, db := range databases {
			writeReport(t, e2e.NotRun(tool.Name, label+"-pair", db, err.Error(), false))
		}

		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runImportReplayOnStore(t, cli, db)
		})
	}
}

// replayVersion is one listed state version the replay test compares: its ID
// with the serial, lineage and MD5 the replay must keep.
type replayVersion struct {
	id      string
	serial  int64
	lineage string
	md5     string
}

// runImportReplayOnStore runs the T2 replay on one store: three applies on S,
// the script dry run and replay, the triple check on D, an apply on D, a
// second run that moves nothing, and the proof that S never changed.
func runImportReplayOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
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

	src, dst := pair.Src, pair.Dst

	src.Step(ctx, t, step("init on source", nil, "init", "-input=false", "-no-color"))

	for i, greeting := range []string{"one", "two", "three"} {
		src.WriteTofuConfig(t, tofuGreetingConfig(src.Host(), greeting))
		src.Step(
			ctx,
			t,
			step(fmt.Sprintf("source apply %d", i+1), nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		)
	}

	before := listReplayVersions(ctx, t, src)
	if len(before) != 3 {
		t.Fatalf("source holds %d versions, want 3 before the replay", len(before))
	}

	dst.Step(ctx, t, step("init on destination", nil, "init", "-input=false", "-no-color"))

	if got := listReplayVersions(ctx, t, dst); len(got) != 0 {
		t.Fatalf("destination holds %d versions, want none before the replay", len(got))
	}

	srcWS := src.WorkspaceID(ctx, t)

	args := replayArgs(pair)

	dry := runReplayScript(ctx, t, pair, "replay dry run", append(args, "--dry-run")...)

	if dry.ExitCode != 0 {
		t.Fatalf("replay dry run exits with status %d: %s%s", dry.ExitCode, dry.Stdout, dry.Stderr)
	}

	if got := listReplayVersions(ctx, t, dst); len(got) != 0 {
		t.Fatalf("destination holds %d versions, want none after the dry run", len(got))
	}

	checkDryRunReadOnly(t, pair, "replay dry run")

	replayed := runReplayScript(ctx, t, pair, "replay", args...)

	if replayed.ExitCode != 0 {
		t.Fatalf("replay exits with status %d: %s%s", replayed.ExitCode, replayed.Stdout, replayed.Stderr)
	}

	checkReplayedTriples(t, before, listReplayVersions(ctx, t, dst))
	checkSourceUnchanged(t, src, before)
	checkNoSourceWrite(t, pair, srcWS, "replay")

	dst.WriteTofuConfig(t, tofuGreetingConfig(dst.Host(), "four"))
	dst.Step(ctx, t, step("apply on destination", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	dst.Step(ctx, t, step("state pull on destination", contains(`"four"`), "state", "pull", "-no-color"))

	again := runReplayScript(ctx, t, pair, "replay again", args...)

	if again.ExitCode != 0 {
		t.Fatalf("second replay exits with status %d: %s%s", again.ExitCode, again.Stdout, again.Stderr)
	}

	if out := again.Stdout + again.Stderr; !strings.Contains(out, "moving 0") {
		t.Fatalf("second replay output lacks moving 0: %s%s", again.Stdout, again.Stderr)
	}

	if got := listReplayVersions(ctx, t, dst); len(got) != 4 {
		t.Fatalf("destination holds %d versions, want 4 after the apply and the second run", len(got))
	}

	checkSourceUnchanged(t, src, before)
	checkNoSourceWrite(t, pair, srcWS, "replay again")
}

// tofuGreetingConfig is the cloud-block configuration with one greeting
// output, so each apply with a new greeting writes a new state version.
func tofuGreetingConfig(host, greeting string) string {
	return `terraform {
  cloud {
    hostname     = ` + strconv.Quote(host) + `
    organization = ` + strconv.Quote(e2e.Namespace) + `
    workspaces {
      name = ` + strconv.Quote(e2e.Workspace) + `
    }
  }
}

output "greeting" {
  value = ` + strconv.Quote(greeting) + `
}
`
}

// replayArgs returns the replay script arguments for pair: both server URLs,
// both namespaces and workspaces, the last ten versions and the trust bundle.
func replayArgs(pair *e2e.Pair) []string {
	args := make([]string, 0, 16)

	return append(args,
		"--source-url", pair.Src.URL,
		"--dest-url", pair.Dst.URL,
		"--source-org", e2e.Namespace,
		"--source-workspace", e2e.Workspace,
		"--dest-org", e2e.Namespace,
		"--dest-workspace", e2e.Workspace,
		"--count", "10",
		"--ca-bundle", pair.Src.CABundle(),
	)
}

// replayScriptPath returns the path of scripts/import-tfe-state.sh from the
// directory of this test file.
func replayScriptPath(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find the test file for the script path")
	}

	return filepath.Join(filepath.Dir(file), "..", "scripts", "import-tfe-state.sh")
}

// runReplayScript runs the replay script with args and tokens from the pair,
// records it as name on the source side, and returns the outcome. The tokens
// travel through the environment, never as arguments, and the outcome must
// hold neither token.
func runReplayScript(
	ctx context.Context, t *testing.T, pair *e2e.Pair, name string, args ...string,
) e2e.ExternalResult {
	t.Helper()

	pair.Src.Recorder.SetStep(name)

	path := replayScriptPath(t)
	command := "import-tfe-state.sh " + strings.Join(args, " ")

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The command is the repository's own replay script.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"IMPORT_TFE_SOURCE_TOKEN="+pair.Src.TokenA(),
		"IMPORT_TFE_DEST_TOKEN="+pair.Dst.TokenA(),
	)

	err := cmd.Run()
	res := e2e.ExternalResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}

	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
			res.Stderr += "\nrun import-tfe-state.sh: " + err.Error()
		}
	}

	failure := ""

	switch {
	case res.ExitCode != 0:
		failure = fmt.Sprintf("exit code %d", res.ExitCode)
	case strings.Contains(res.Stdout+res.Stderr, pair.Src.TokenA()) ||
		strings.Contains(res.Stdout+res.Stderr, pair.Dst.TokenA()):
		failure = "output holds a token"
	default:
	}

	pair.Src.RecordExternalStep(t, name, command, res, failure)

	return res
}

// replayListAttributes holds the serial, lineage and MD5 of one listed
// state version in the replay test.
type replayListAttributes struct {
	Serial  int64  `json:"serial"`
	Lineage string `json:"lineage"`
	MD5     string `json:"md5"`
}

// replayListItem decodes one entry of a state version list in the replay
// test: its ID with the serial, lineage and MD5 the replay must keep.
type replayListItem struct {
	ID         string               `json:"id"`
	Attributes replayListAttributes `json:"attributes"`
}

// listReplayVersions returns the state versions of the environment workspace,
// newest first, through the TFE list route.
func listReplayVersions(ctx context.Context, t *testing.T, env *e2e.Env) []replayVersion {
	t.Helper()

	target := "/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace +
		"&filter[workspace][name]=" + e2e.Workspace + "&page[size]=100"

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, target, "")
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("list state versions answers %d: %s", status, body)
	}

	var doc struct {
		Data []replayListItem `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode state version list: %v", err)
	}

	out := make([]replayVersion, 0, len(doc.Data))
	for _, item := range doc.Data {
		out = append(out, replayVersion{
			id:      item.ID,
			serial:  item.Attributes.Serial,
			lineage: item.Attributes.Lineage,
			md5:     item.Attributes.MD5,
		})
	}

	return out
}

// checkReplayedTriples proves D holds every source version with the same
// serial, lineage and MD5: the replay kept the three values of each version.
func checkReplayedTriples(t *testing.T, want, got []replayVersion) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("destination holds %d versions, want %d", len(got), len(want))
	}

	bySerial := func(a, b replayVersion) int {
		switch {
		case a.serial != b.serial:
			return cmp.Compare(a.serial, b.serial)
		case a.lineage != b.lineage:
			return strings.Compare(a.lineage, b.lineage)
		default:
			return strings.Compare(a.md5, b.md5)
		}
	}

	slices.SortFunc(want, bySerial)
	slices.SortFunc(got, bySerial)

	for i := range want {
		if got[i].serial != want[i].serial || got[i].lineage != want[i].lineage || got[i].md5 != want[i].md5 {
			t.Fatalf(
				"destination version %+v, want serial %d lineage %q md5 %q",
				got[i],
				want[i].serial,
				want[i].lineage,
				want[i].md5,
			)
		}
	}
}

// checkSourceUnchanged proves the source still holds the same versions: the
// replay never writes to S.
func checkSourceUnchanged(t *testing.T, src *e2e.Env, want []replayVersion) {
	t.Helper()

	got := listReplayVersions(t.Context(), t, src)

	if len(got) != len(want) {
		t.Fatalf("source holds %d versions, want %d: the replay changed S", len(got), len(want))
	}

	ids := map[string]bool{}
	for _, v := range want {
		ids[v.id] = true
	}

	for _, v := range got {
		if !ids[v.id] {
			t.Fatalf("source holds new version %q: the replay changed S", v.id)
		}
	}
}

// checkDryRunReadOnly proves every request of a dry-run step is a GET: the
// dry run only reads.
func checkDryRunReadOnly(t *testing.T, pair *e2e.Pair, step string) {
	t.Helper()

	entries := pair.Src.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]
		if entry.Step != step {
			continue
		}

		if entry.Method != http.MethodGet {
			t.Fatalf("dry run step %q holds a write: %s %s", step, entry.Method, entry.Path)
		}
	}
}

// checkNoSourceWrite proves the recorder shows no write to the source
// workspace during step: no non-GET entry names the source workspace ID. The
// destination writes of a real run name its own workspace instead.
func checkNoSourceWrite(t *testing.T, pair *e2e.Pair, srcWS, step string) {
	t.Helper()

	entries := pair.Src.Recorder.Entries()

	for i := range entries {
		entry := &entries[i]
		if entry.Step != step {
			continue
		}

		if entry.Method == http.MethodGet {
			continue
		}

		if strings.Contains(entry.Path, srcWS) {
			t.Fatalf("replay step %q writes to the source: %s %s", step, entry.Method, entry.Path)
		}
	}
}

// TestUpgradeCheck proves `opendustpan upgrade check` with RunBinary: on a
// fresh, an old, an up-to-date and a newer database, on SQLite and on
// PostgreSQL, the verdict and the exit code match and the command writes
// nothing. It runs only with E2E_SERVER=binary, on every store under test.
func TestUpgradeCheck(t *testing.T) {
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
			runUpgradeCheckOnStore(t, cli, db)
		})
	}
}

// runUpgradeCheckOnStore runs the upgrade check cases on one store: a fresh,
// an old, an up-to-date and a newer database through RunBinary.
func runUpgradeCheckOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	switch store {
	case e2e.StoreSQLite:
		checkUpgradeSQLite(ctx, t, env)
	case e2e.StorePostgres:
		checkUpgradePostgres(ctx, t, env)
	default:
		t.Fatalf("unknown store: %s", store)
	}
}

// wantUpgradeResult holds the expected outcome of one upgrade check run:
// the verdict, the exit code and whether pending migrations are listed.
type wantUpgradeResult struct {
	verdict  string
	exitCode int
	pending  bool
}

// checkUpgradeResult proves res exits with the expected code, holds the
// expected verdict and names the binary version, both schema versions and
// the pending state.
func checkUpgradeResult(t *testing.T, res e2e.BinaryResult, want wantUpgradeResult) {
	t.Helper()

	if res.ExitCode != want.exitCode {
		t.Fatalf(
			"upgrade check exits with status %d, want %d:\n%s%s",
			res.ExitCode,
			want.exitCode,
			res.Stdout,
			res.Stderr,
		)
	}

	for _, line := range []string{
		"binary version: ",
		"database schema version: ",
		"binary schema version: ",
		"verdict: " + want.verdict,
	} {
		if !strings.Contains(res.Stdout, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s%s", line, res.Stdout, res.Stderr)
		}
	}

	if want.pending {
		checkUpgradePending(t, res)
	} else {
		checkUpgradeNoPending(t, res)
	}
}

// checkUpgradePending proves res lists pending migrations with the backup
// advice.
func checkUpgradePending(t *testing.T, res e2e.BinaryResult) {
	t.Helper()

	for _, line := range []string{
		"pending migrations:",
		".sql",
		"Back up the database before the upgrade.",
		"No downgrade path exists.",
	} {
		if !strings.Contains(res.Stdout, line) {
			t.Fatalf("upgrade check output lacks %q:\n%s%s", line, res.Stdout, res.Stderr)
		}
	}
}

// checkUpgradeNoPending proves res holds `pending migrations: none`.
func checkUpgradeNoPending(t *testing.T, res e2e.BinaryResult) {
	t.Helper()

	if !strings.Contains(res.Stdout, "pending migrations: none") {
		t.Fatalf("upgrade check output lacks `pending migrations: none`:\n%s%s", res.Stdout, res.Stderr)
	}
}

// parseUpgradeVersions reads the database and binary schema versions from
// upgrade check output.
//
//nolint:nonamedreturns // The names keep the database and binary versions in order at callers.
func parseUpgradeVersions(t *testing.T, out string) (database, binary int64) {
	t.Helper()

	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(line, "database schema version: "); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				t.Fatalf("parse database schema version %q: %v", rest, err)
			}

			database = v
		}

		if rest, ok := strings.CutPrefix(line, "binary schema version: "); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				t.Fatalf("parse binary schema version %q: %v", rest, err)
			}

			binary = v
		}
	}

	if database < 0 || binary <= 0 {
		t.Fatalf("schema versions are missing:\n%s", out)
	}

	return database, binary
}

// oldFixtureMaxVersion is the newest version in the fixed old-database
// fixture. The fixture stays below the binary version, so the check reports
// pending migrations. Bump it only with the fixture name and assertions.
const oldFixtureMaxVersion = 8

// checkUpgradeSQLite runs the four upgrade check cases on SQLite files: a
// fresh file, a file with versions 1 through oldFixtureMaxVersion, a file the
// binary migrated and a migrated file with one extra version row.
func checkUpgradeSQLite(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	t.Run("fresh", func(t *testing.T) {
		t.Parallel()
		checkUpgradeFreshSQLite(ctx, t, env)
	})

	t.Run("old", func(t *testing.T) {
		t.Parallel()
		checkUpgradeOldSQLite(ctx, t, env)
	})

	t.Run("up to date", func(t *testing.T) {
		t.Parallel()
		checkUpgradeCurrentSQLite(ctx, t, env)
	})

	t.Run("newer", func(t *testing.T) {
		t.Parallel()
		checkUpgradeNewerSQLite(ctx, t, env)
	})
}

// checkUpgradeFreshSQLite proves upgrade check on an empty file: it exits 0
// with `upgrade possible` and leaves the file unchanged.
func checkUpgradeFreshSQLite(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty database: %v", err)
	}

	before := readSQLiteFile(t, path)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", "sqlite:"+path)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(out, "database schema version: 0") {
		t.Fatalf("fresh output lacks database schema version 0:\n%s", out)
	}

	checkSQLiteUnchanged(t, path, before)
}

// checkUpgradeOldSQLite proves upgrade check on versions 1 through
// oldFixtureMaxVersion: it exits 0 with `upgrade possible` and leaves the
// file unchanged.
func checkUpgradeOldSQLite(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	seedOldSQLite(ctx, t, path)

	before := readSQLiteFile(t, path)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", "sqlite:"+path)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(
		out,
		"database schema version: "+strconv.FormatInt(oldFixtureMaxVersion, 10),
	) {
		t.Fatalf("old output lacks database schema version %d:\n%s", oldFixtureMaxVersion, out)
	}

	checkSQLiteUnchanged(t, path, before)
}

// checkUpgradeCurrentSQLite proves upgrade check on a migrated file: it exits
// 0 with `up to date` and leaves the file unchanged.
func checkUpgradeCurrentSQLite(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	migrateSQLiteViaBinary(ctx, t, env, path)

	before := readSQLiteFile(t, path)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", "sqlite:"+path)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "up to date", exitCode: 0, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database != binary {
		t.Fatalf("schema versions differ: database %d, binary %d:\n%s", database, binary, res.Stdout)
	}

	checkSQLiteUnchanged(t, path, before)
}

// checkUpgradeNewerSQLite proves upgrade check on a migrated file with one
// extra version row: it exits 1 with `cannot start` and leaves the file
// unchanged.
func checkUpgradeNewerSQLite(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	migrateSQLiteViaBinary(ctx, t, env, path)
	insertFutureSQLite(ctx, t, path)

	before := readSQLiteFile(t, path)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", "sqlite:"+path)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "cannot start", exitCode: 1, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database <= binary {
		t.Fatalf("newer database %d is not above binary %d:\n%s", database, binary, res.Stdout)
	}

	checkSQLiteUnchanged(t, path, before)
}

// seedOldSQLite creates a SQLite database at path with schema versions 1
// through oldFixtureMaxVersion recorded, so upgrade check sees an old database.
func seedOldSQLite(ctx context.Context, t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty database: %v", err)
	}

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	const createTable = `CREATE TABLE schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := conn.ExecContext(ctx, createTable); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	for v := int64(1); v <= oldFixtureMaxVersion; v++ {
		if _, err := conn.ExecContext(ctx, record, v, fmt.Sprintf("%04d_old.sql", v)); err != nil {
			t.Fatalf("record version %d: %v", v, err)
		}
	}
}

// migrateSQLiteViaBinary migrates the SQLite file at path by creating a
// namespace through the binary, so upgrade check later sees an up-to-date
// database.
func migrateSQLiteViaBinary(ctx context.Context, t *testing.T, env *e2e.Env, path string) {
	t.Helper()

	created := env.RunBinary(ctx, t, "namespace", "create", "--database", "sqlite:"+path, e2e.Namespace)
	if created.ExitCode != 0 {
		t.Fatalf(
			"namespace create exits with status %d:\n%s%s",
			created.ExitCode,
			created.Stdout,
			created.Stderr,
		)
	}
}

// insertFutureSQLite records one version above the newest in path, so upgrade
// check sees a newer database.
func insertFutureSQLite(ctx context.Context, t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	var newest int64

	row := conn.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`)
	if scanErr := row.Scan(&newest); scanErr != nil {
		t.Fatalf("read newest version: %v", scanErr)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`

	future := newest + 1

	if _, execErr := conn.ExecContext(ctx, record, future, fmt.Sprintf("%04d_future.sql", future)); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}
}

// readSQLiteFile returns the raw bytes of the database file at path.
func readSQLiteFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // The test reads the database file it created.
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}

	return data
}

// checkSQLiteUnchanged proves upgrade check wrote nothing: the database file
// at path is byte-identical to before.
func checkSQLiteUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()

	if after := readSQLiteFile(t, path); !bytes.Equal(after, before) {
		t.Fatal("upgrade check changed the database file, want it unchanged")
	}
}

// checkUpgradePostgres runs the four upgrade check cases on PostgreSQL
// schemas: an empty schema, versions 1 through oldFixtureMaxVersion, a schema
// the binary migrated and a migrated schema with one extra version row.
func checkUpgradePostgres(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	t.Run("fresh", func(t *testing.T) {
		t.Parallel()
		checkUpgradeFreshPostgres(ctx, t, env)
	})

	t.Run("old", func(t *testing.T) {
		t.Parallel()
		checkUpgradeOldPostgres(ctx, t, env)
	})

	t.Run("up to date", func(t *testing.T) {
		t.Parallel()
		checkUpgradeCurrentPostgres(ctx, t, env)
	})

	t.Run("newer", func(t *testing.T) {
		t.Parallel()
		checkUpgradeNewerPostgres(ctx, t, env)
	})
}

// checkUpgradeFreshPostgres proves upgrade check on an empty schema: it exits
// 0 with `upgrade possible` and creates no table.
func checkUpgradeFreshPostgres(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schemaURL := upgradePostgresSchema(ctx, t)
	before := snapshotPostgres(ctx, t, schemaURL)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", schemaURL)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(out, "database schema version: 0") {
		t.Fatalf("fresh output lacks database schema version 0:\n%s", out)
	}

	after := snapshotPostgres(ctx, t, schemaURL)
	checkPostgresUnchanged(t, before, after)

	if slices.Contains(after.tables, "schema_migrations") {
		t.Fatal("upgrade check created schema_migrations, want no such table")
	}
}

// checkUpgradeOldPostgres proves upgrade check on versions 1 through
// oldFixtureMaxVersion: it exits 0 with `upgrade possible` and changes no
// table or version row.
func checkUpgradeOldPostgres(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schemaURL := upgradePostgresSchema(ctx, t)
	seedOldPostgres(ctx, t, schemaURL)

	before := snapshotPostgres(ctx, t, schemaURL)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", schemaURL)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "upgrade possible", exitCode: 0, pending: true})

	if out := res.Stdout; !strings.Contains(
		out,
		"database schema version: "+strconv.FormatInt(oldFixtureMaxVersion, 10),
	) {
		t.Fatalf("old output lacks database schema version %d:\n%s", oldFixtureMaxVersion, out)
	}

	checkPostgresUnchanged(t, before, snapshotPostgres(ctx, t, schemaURL))
}

// checkUpgradeCurrentPostgres proves upgrade check on a migrated schema: it
// exits 0 with `up to date` and changes no table or version row.
func checkUpgradeCurrentPostgres(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schemaURL := upgradePostgresSchema(ctx, t)
	migratePostgresViaBinary(ctx, t, env, schemaURL)

	before := snapshotPostgres(ctx, t, schemaURL)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", schemaURL)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "up to date", exitCode: 0, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database != binary {
		t.Fatalf("schema versions differ: database %d, binary %d:\n%s", database, binary, res.Stdout)
	}

	checkPostgresUnchanged(t, before, snapshotPostgres(ctx, t, schemaURL))
}

// checkUpgradeNewerPostgres proves upgrade check on a migrated schema with
// one extra version row: it exits 1 with `cannot start` and changes no table
// or version row.
func checkUpgradeNewerPostgres(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schemaURL := upgradePostgresSchema(ctx, t)
	migratePostgresViaBinary(ctx, t, env, schemaURL)
	insertFuturePostgres(ctx, t, schemaURL)

	before := snapshotPostgres(ctx, t, schemaURL)
	res := env.RunBinary(ctx, t, "upgrade", "check", "--database", schemaURL)
	checkUpgradeResult(t, res, wantUpgradeResult{verdict: "cannot start", exitCode: 1, pending: false})

	database, binary := parseUpgradeVersions(t, res.Stdout)
	if database <= binary {
		t.Fatalf("newer database %d is not above binary %d:\n%s", database, binary, res.Stdout)
	}

	checkPostgresUnchanged(t, before, snapshotPostgres(ctx, t, schemaURL))
}

// upgradePostgresSchema creates a fresh schema and returns the base URL with
// the search path set to it. The schema is dropped when the test ends.
func upgradePostgresSchema(ctx context.Context, t *testing.T) string {
	t.Helper()

	base := e2e.PostgresURL()
	if base == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate schema name: %v", err)
	}

	schema := "e2e_" + hex.EncodeToString(raw)

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()

		dropConn, dropErr := pgx.Connect(dropCtx, base)
		if dropErr != nil {
			t.Errorf("connect to postgres: %v", dropErr)

			return
		}

		defer func() {
			if closeErr := dropConn.Close(dropCtx); closeErr != nil {
				t.Errorf("close postgres connection: %v", closeErr)
			}
		}()

		dropStmt := "DROP SCHEMA " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
		if _, dropExecErr := dropConn.Exec(dropCtx, dropStmt); dropExecErr != nil {
			t.Errorf("drop schema: %v", dropExecErr)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse postgres URL: %v", err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}

// postgresSnapshot holds the table list and schema_migrations rows of one
// PostgreSQL schema, so the test proves upgrade check wrote nothing.
type postgresSnapshot struct {
	tables   []string
	versions []string
}

// snapshotPostgres reads the table names and the schema_migrations rows of
// schemaURL. versions is nil when the table is absent.
func snapshotPostgres(ctx context.Context, t *testing.T, schemaURL string) postgresSnapshot {
	t.Helper()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			rows.Close()
			t.Fatalf("read table name: %v", scanErr)
		}

		tables = append(tables, name)
	}

	rows.Close()

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read tables: %v", rowsErr)
	}

	if !slices.Contains(tables, "schema_migrations") {
		return postgresSnapshot{tables: tables, versions: nil}
	}

	versionRows, err := conn.Query(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema versions: %v", err)
	}

	var versions []string

	for versionRows.Next() {
		var version int64

		var name string

		if scanErr := versionRows.Scan(&version, &name); scanErr != nil {
			versionRows.Close()
			t.Fatalf("read schema version: %v", scanErr)
		}

		versions = append(versions, strconv.FormatInt(version, 10)+":"+name)
	}

	versionRows.Close()

	if rowsErr := versionRows.Err(); rowsErr != nil {
		t.Fatalf("read schema versions: %v", rowsErr)
	}

	return postgresSnapshot{tables: tables, versions: versions}
}

// checkPostgresUnchanged proves upgrade check wrote nothing: the table list
// and the schema_migrations rows match the snapshot taken before the call.
func checkPostgresUnchanged(t *testing.T, before, after postgresSnapshot) {
	t.Helper()

	if !slices.Equal(after.tables, before.tables) {
		t.Fatalf("tables after check = %v, want %v", after.tables, before.tables)
	}

	if !slices.Equal(after.versions, before.versions) {
		t.Fatalf("versions after check = %v, want %v", after.versions, before.versions)
	}
}

// seedOldPostgres records schema versions 1 through oldFixtureMaxVersion in
// schemaURL, so upgrade check sees an old database.
func seedOldPostgres(ctx context.Context, t *testing.T, schemaURL string) {
	t.Helper()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	const createTable = `CREATE TABLE schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(ctx, createTable); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const recordOld = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for v := int64(1); v <= oldFixtureMaxVersion; v++ {
		name := fmt.Sprintf("%04d_old.sql", v)

		_, execErr := conn.Exec(ctx, recordOld, v, name)
		if execErr != nil {
			t.Fatalf("record version %d: %v", v, execErr)
		}
	}
}

// migratePostgresViaBinary migrates the schema at schemaURL by creating a
// namespace through the binary, so upgrade check later sees an up-to-date
// database.
func migratePostgresViaBinary(ctx context.Context, t *testing.T, env *e2e.Env, schemaURL string) {
	t.Helper()

	created := env.RunBinary(ctx, t, "namespace", "create", "--database", schemaURL, e2e.Namespace)
	if created.ExitCode != 0 {
		t.Fatalf(
			"namespace create exits with status %d:\n%s%s",
			created.ExitCode,
			created.Stdout,
			created.Stderr,
		)
	}
}

// TestBackupCheck proves `opendustpan backup check` with RunBinary: it takes
// a backup into a target directory, the check exits 0 with one verdict, then
// one deleted blob file makes the check exit 1 naming the key. It runs only
// with E2E_SERVER=binary, on every store under test.
func TestBackupCheck(t *testing.T) {
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
			runBackupCheckOnStore(t, cli, db)
		})
	}
}

// runBackupCheckOnStore runs the backup check steps on one store: a backup
// into a target directory, a check with exit 0, a deleted blob file, and a
// check with exit 1 naming the key.
func runBackupCheckOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	const id = "bkp-e2e-check"

	targetDir := writeE2EBackup(t, id)
	blobKey := "blobs/tofu/ws1/sv1/a1"

	complete := env.RunBinary(ctx, t, "backup", "check", "--backup", id, "--backup-target", "dir:"+targetDir)
	if complete.ExitCode != 0 {
		t.Fatalf(
			"backup check exits with status %d, want 0:\n%s%s",
			complete.ExitCode,
			complete.Stdout,
			complete.Stderr,
		)
	}

	if !strings.Contains(complete.Stdout, "backup "+id+" is complete") {
		t.Fatalf("backup check output lacks the verdict:\n%s%s", complete.Stdout, complete.Stderr)
	}

	if err := os.Remove(filepath.Join(targetDir, filepath.FromSlash(blobKey))); err != nil {
		t.Fatalf("delete blob file: %v", err)
	}

	broken := env.RunBinary(ctx, t, "backup", "check", "--backup", id, "--backup-target", "dir:"+targetDir)
	if broken.ExitCode != 1 {
		t.Fatalf(
			"backup check after the delete exits with status %d, want 1:\n%s%s",
			broken.ExitCode,
			broken.Stdout,
			broken.Stderr,
		)
	}

	if out := broken.Stdout + broken.Stderr; !strings.Contains(out, blobKey) {
		t.Fatalf("backup check output lacks the key %q:\n%s", blobKey, out)
	}
}

// writeE2EBackup writes a complete backup with id into a new target directory:
// the SQL part, one blob and the manifest last. It returns the target directory.
func writeE2EBackup(t *testing.T, id string) string {
	t.Helper()

	dir := t.TempDir()

	target, err := blobfs.Open(dir)
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	db := []byte("e2e backup database part")
	content := []byte(`{"state":1}`)
	blobKey := "tofu/ws1/sv1/a1"
	md5Sum := md5.Sum(content) //nolint:gosec // The test computes the MD5 the manifest records.
	shaSum := sha256.Sum256(db)

	manifest := backup.Manifest{
		CreatedAt:   time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		ID:          backup.ID(id),
		Dialect:     backup.DialectSQLite,
		HomeRegions: []string{"e2e"},
		WrapperIDs:  nil,
		RowCounts:   nil,
		Blobs: []backup.ManifestBlob{
			{Key: blobKey, Size: int64(len(content)), MD5: hex.EncodeToString(md5Sum[:])},
		},
		Version:       "e2e",
		SQLSHA256:     hex.EncodeToString(shaSum[:]),
		Format:        backup.FormatVersion,
		SchemaVersion: 1,
		SQLSize:       int64(len(db)),
	}

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	ctx := t.Context()

	for key, body := range map[string][]byte{
		"backups/" + id + "/db":            db,
		"blobs/" + blobKey:                 content,
		"backups/" + id + "/manifest.json": data,
	} {
		parsed, err := blob.ParseKey(key)
		if err != nil {
			t.Fatalf("parse key %q: %v", key, err)
		}

		if err := target.Put(ctx, parsed, bytes.NewReader(body)); err != nil {
			t.Fatalf("put %q: %v", key, err)
		}
	}

	if err := target.Close(); err != nil {
		t.Fatalf("close backup target: %v", err)
	}

	return dir
}

// insertFuturePostgres records one version above the newest in schemaURL, so
// upgrade check sees a newer database.
func insertFuturePostgres(ctx context.Context, t *testing.T, schemaURL string) {
	t.Helper()

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	var newest int64
	if err := conn.QueryRow(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&newest); err != nil {
		t.Fatalf("read newest version: %v", err)
	}

	future := newest + 1
	name := fmt.Sprintf("%04d_future.sql", future)

	const recordFuture = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	_, execErr := conn.Exec(ctx, recordFuture, future, name)
	if execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}
}

// backupPadBytes grows the database with one padded checkpoint, so a backup
// holds still running while the test starts another one or stops the server.
// The pad stays below the default 32 MiB checkpoint limit, and no test hook
// is needed: the data set itself holds the backup.
const backupPadBytes = 24 << 20

// backupPollTimeout bounds a backup poll: a test backup ends well before it,
// even with the padded checkpoint.
const backupPollTimeout = 2 * time.Minute

// backupPollInterval sets how often a backup poll reads the row.
const backupPollInterval = 100 * time.Millisecond

// TestBackups proves the installation backup routes (ADR 30 decision 9): the
// Pulumi CLI writes state, POST starts a backup that polls to succeeded with
// a manifest holding the Pulumi data, a second start while one runs answers
// 409, calls without a token answer 401, and the list holds the backups
// newest first. It runs on every store under test with the first Pulumi
// version of the matrix. The restart part needs the real server binary: it
// stops the server during a running backup and sees the row failed after the
// start. Without E2E_SERVER=binary the test proves the routes and skips only
// the restart.
func TestBackups(t *testing.T) {
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
			runBackupsOnStore(t, cli, db)
		})
	}
}

// runBackupsOnStore runs the backup steps on one store: CLI writes, a backup
// to succeeded with the manifest checks, the 409 and 401 checks, the list
// check, and the restart check with the real binary.
func runBackupsOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	checkReady(ctx, t, env)
	writeBackupData(ctx, t, env)

	id := startBackupAndConflict(ctx, t, env)
	doc := pollBackupState(ctx, t, env, id, "succeeded")
	checkBackupManifest(t, store, doc)
	checkBackupList(ctx, t, env, id)
	checkBackupUnauthorized(ctx, t, env, id)

	if !e2e.UseBinaryServer() {
		t.Log("skipping the binary parts without E2E_SERVER=binary")
		return
	}

	checkBackupCommand(ctx, t, env)
	checkBackupRestartFailed(ctx, t, env, id)
}

// writeBackupData writes Pulumi state with the CLI and grows the database
// with one padded checkpoint, so a backup holds still running while the test
// starts another one or stops the server.
func writeBackupData(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack init other", nil, "stack", "init", "other"),
		step("stack select", nil, "stack", "select", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("up", nil, "up", "--yes", "--non-interactive"),
	} {
		env.Step(ctx, t, s)
	}

	exported := env.Step(ctx, t, step("stack export", exportHoldsReference, "stack", "export"))

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, stackName)
	env.SaveHeldPaddedCheckpoint(ctx, t, held, exported.Stdout, map[string]string{
		"e2ePad": strings.Repeat("x", backupPadBytes),
	})
	env.EndHeldUpdate(ctx, t, held, "succeeded")
}

// backupDoc decodes one backup in tests.
type backupDoc struct {
	ID        string            `json:"id"`
	State     string            `json:"state"`
	StartedAt string            `json:"startedAt"`
	EndedAt   string            `json:"endedAt"`
	Target    string            `json:"target"`
	Error     string            `json:"error"`
	Manifest  backupManifestDoc `json:"manifest"`
}

// backupManifestDoc holds the manifest fields the backup steps check.
type backupManifestDoc struct {
	ID            string   `json:"id"`
	Format        int      `json:"format"`
	Dialect       string   `json:"dialect"`
	HomeRegions   []string `json:"homeRegions"`
	WrapperIDs    []string `json:"wrapperIds"`
	SQLSize       int64    `json:"sqlSize"`
	SQLSHA256     string   `json:"sqlSha256"`
	SchemaVersion int      `json:"schemaVersion"`
}

// startBackupAndConflict starts a backup and proves a second start while it
// runs answers 409 with the one JSON error shape. The padded checkpoint holds
// the first backup running, so no test hook is needed. It returns the ID of
// the running backup.
func startBackupAndConflict(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("POST backups: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST backups answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &started); decodeErr != nil || started.ID == "" {
		t.Fatalf("POST backups answers %q, want an id: %v", body, decodeErr)
	}

	if !strings.HasPrefix(started.ID, "bkp-") {
		t.Fatalf("backup id = %q, want the bkp- prefix", started.ID)
	}

	status, body, err = env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("second POST backups: %v", err)
	}

	if status != http.StatusConflict {
		t.Fatalf("second POST backups answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	var conflict struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if decodeErr := json.Unmarshal([]byte(body), &conflict); decodeErr != nil {
		t.Fatalf("decode 409 of POST backups: %v (%s)", decodeErr, body)
	}

	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Message, "already running") {
		t.Fatalf("second POST backups error = %+v, want code 409 naming the running backup", conflict)
	}

	return started.ID
}

// pollBackupState polls one backup until it reaches want, failing the test
// when it reaches another terminal state or the deadline passes first.
func pollBackupState(ctx context.Context, t *testing.T, env *e2e.Env, id, want string) backupDoc {
	t.Helper()

	deadline, cancel := context.WithTimeout(ctx, backupPollTimeout)
	defer cancel()

	timer := time.NewTimer(backupPollInterval)
	defer timer.Stop()

	for {
		status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups/"+id, "")
		if err != nil {
			t.Fatalf("GET backup %s: %v", id, err)
		}

		if status != http.StatusOK {
			t.Fatalf("GET backup %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
		}

		var doc backupDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode backup %s %q: %v", id, body, err)
		}

		switch doc.State {
		case want:
			return doc
		case "running":
		default:
			t.Fatalf("backup %s reads %q with error %q, want %q", id, doc.State, doc.Error, want)
		}

		select {
		case <-deadline.Done():
			t.Fatalf("backup %s stays running, want %q", id, want)
		case <-timer.C:
			timer.Reset(backupPollInterval)
		}
	}
}

// checkBackupManifest proves the manifest of a succeeded backup holds the
// Pulumi data: the store dialect, the padded SQL part, the wrapper of the
// config secret and the home region.
func checkBackupManifest(t *testing.T, store e2e.Store, doc backupDoc) {
	t.Helper()

	manifest := doc.Manifest

	if manifest.ID != doc.ID {
		t.Fatalf("manifest id = %q, want backup %q", manifest.ID, doc.ID)
	}

	if manifest.Format != 1 {
		t.Fatalf("manifest format = %d, want 1", manifest.Format)
	}

	if manifest.Dialect != string(store) {
		t.Fatalf("manifest dialect = %q, want %q", manifest.Dialect, store)
	}

	if manifest.SQLSize <= backupPadBytes {
		t.Fatalf("manifest SQL size = %d, want above the %d pad bytes", manifest.SQLSize, backupPadBytes)
	}

	if manifest.SQLSHA256 == "" {
		t.Fatal("manifest holds no SQL SHA-256")
	}

	if manifest.SchemaVersion <= 0 {
		t.Fatalf("manifest schema version = %d, want above 0", manifest.SchemaVersion)
	}

	if len(manifest.WrapperIDs) == 0 {
		t.Fatal("manifest holds no wrapper ID, want the key of the config secret")
	}

	if len(manifest.HomeRegions) == 0 {
		t.Fatal("manifest holds no home region")
	}
}

// checkBackupList proves GET lists the backups newest first and holds the
// backup the test started.
func checkBackupList(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("GET backups: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET backups answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc struct {
		Backups []backupDoc `json:"backups"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode backup list %q: %v", body, err)
	}

	found := false

	for i := range doc.Backups {
		entry := &doc.Backups[i]
		if entry.ID == id {
			found = true

			if entry.State != "succeeded" {
				t.Fatalf("listed backup %s reads %q, want succeeded", id, entry.State)
			}
		}
	}

	if !found {
		t.Fatalf("backup list holds %v, want backup %q", doc.Backups, id)
	}

	checkBackupListOrder(t, doc.Backups)
}

// checkBackupListOrder proves the listed backups run newest first by start
// time.
func checkBackupListOrder(t *testing.T, backups []backupDoc) {
	t.Helper()

	seen := time.Time{}

	for i := range backups {
		at, err := time.Parse(time.RFC3339Nano, backups[i].StartedAt)
		if err != nil {
			t.Fatalf("backup %s startedAt %q does not parse: %v", backups[i].ID, backups[i].StartedAt, err)
		}

		if !seen.IsZero() && at.After(seen) {
			t.Fatalf("backup list is not newest first at %q", backups[i].ID)
		}

		seen = at
	}
}

// checkBackupUnauthorized proves the backup routes answer 401 without a
// token, with the one JSON error shape.
func checkBackupUnauthorized(ctx context.Context, t *testing.T, env *e2e.Env, id string) {
	t.Helper()

	for _, target := range []struct {
		method, path string
	}{
		{http.MethodPost, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups/" + id},
	} {
		status, body, err := env.ServerAPIStatusBare(ctx, target.method, target.path)
		if err != nil {
			t.Fatalf("%s %s without a token: %v", target.method, target.path, err)
		}

		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token answers %d, want %d (%s)",
				target.method, target.path, status, http.StatusUnauthorized, body)
		}

		var doc struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode 401 of %s %s: %v (%s)", target.method, target.path, err, body)
		}

		if doc.Code != http.StatusUnauthorized || doc.Message == "" {
			t.Fatalf("%s %s error = %+v, want code 401 with a message", target.method, target.path, doc)
		}
	}
}

// checkBackupCommand runs `opendustpan backup` through RunBinary against
// the running server (ADR 30 decision 10): the command starts a backup,
// waits, and exits with status 0. The output names the backup ID, the
// succeeded state and the key warning that a backup cannot decrypt secrets
// without DUSTPAN_KEY_ENCRYPTION_KEY. The command trusts the server through
// --ca-file with the same certificate bundle the CLIs trust through
// SSL_CERT_FILE, and it reads the token from DUSTPAN_ADMIN_TOKEN in the
// RunBinary environment. It needs E2E_SERVER=binary.
func checkBackupCommand(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	res := env.RunBinary(ctx, t, "backup", "--server", env.URL, "--ca-file", env.CABundle())
	if res.ExitCode != 0 {
		t.Fatalf("opendustpan backup exits with status %d: %s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	for _, want := range []string{"bkp-", "succeeded", "DUSTPAN_KEY_ENCRYPTION_KEY", "plain text"} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("opendustpan backup output lacks %q: %q", want, res.Stdout)
		}
	}

	t.Logf("opendustpan backup: %s", strings.TrimSpace(res.Stdout))
}

// checkBackupRestartFailed stops the server during a running backup, starts
// it, and proves the row reads failed. The padded checkpoint holds the backup
// running, so the stop lands mid-backup with no test hook. It also proves the
// earlier backup survives the restart still succeeded.
func checkBackupRestartFailed(ctx context.Context, t *testing.T, env *e2e.Env, firstID string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodPost, "/api/server/backups", "")
	if err != nil {
		t.Fatalf("POST backups before the restart: %v", err)
	}

	if status != http.StatusAccepted {
		t.Fatalf("POST backups before the restart answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var started struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil || started.ID == "" {
		t.Fatalf("POST backups before the restart answers %q, want an id: %v", body, err)
	}

	env.StopServer(ctx, t)
	env.StartServer(ctx, t)

	doc := pollBackupState(ctx, t, env, started.ID, "failed")

	if doc.Error == "" {
		t.Fatalf("backup %s reads failed with no error text", started.ID)
	}

	t.Logf("backup %s failed after the restart: %q", started.ID, doc.Error)

	if again := pollBackupState(ctx, t, env, firstID, "succeeded"); again.Manifest.SQLSize <= backupPadBytes {
		t.Fatalf("backup %s after the restart holds SQL size %d, want above the pad bytes",
			firstID, again.Manifest.SQLSize)
	}
}

// restoreBackupID is the backup the restore test takes and loads.
const restoreBackupID = "bkp-e2e-restore"

// restoreRegion is the region the restore test passes. It matches the region
// of the harness environment, so no region warning appears.
const restoreRegion = "e2e"

// restoreSecret is the Pulumi config secret the test writes before the
// backup and reads back after the restore.
const restoreSecret = "s3cr3t"

// TestBackupRestore proves `opendustpan restore` with RunBinary: while the
// server runs, the test writes a Pulumi secret and OpenTofu state, takes a
// backup, stops the server, restores the backup into an empty destination
// with the same key, starts the server on the restored data, and checks that
// the Pulumi secret decrypts and the OpenTofu state reads. A second restore
// into the loaded destination exits 1. It runs only with E2E_SERVER=binary,
// on SQLite and on PostgreSQL with an empty schema.
func TestBackupRestore(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the restore test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersions[0])
	if err != nil {
		t.Fatalf("install pulumi %s: %v", pulumiVersions[0], err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersions[0])
	if err != nil {
		t.Fatalf("install tofu %s: %v", tofuVersions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runBackupRestoreOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// runBackupRestoreOnStore writes state with both CLIs, backs it up, stops
// the server, restores into an empty destination, restarts on the restored
// data and checks both tools read the same state back.
func runBackupRestoreOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, pulumiCLI, store)

	writeRestoreState(ctx, t, env, tofuCLI)
	targetDir := takeRestoreBackup(ctx, t, env)

	env.StopServer(ctx, t)
	wipeRestoreDestination(ctx, t, env, store)
	runRestoreBinary(ctx, t, env, targetDir)
	env.StartServer(ctx, t)

	checkRestoredState(ctx, t, env, pulumiCLI, tofuCLI)
	checkRestoreRefusesLoaded(ctx, t, env, targetDir)
}

// writeRestoreState writes a Pulumi secret with an up and OpenTofu state
// with an apply: a small part of the backup scenario, enough to prove that
// secrets decrypt and state reads after the restore.
func writeRestoreState(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install) {
	t.Helper()

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init", nil, "stack", "init", qualified))
	// The scratch program reads stack other, so up needs it to exist.
	env.Step(ctx, t, step("stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("config set secret", nil, "config", "set", "--secret", "apiKey", restoreSecret))
	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))
	env.Step(ctx, t, step("config get", contains(restoreSecret), "config", "get", "apiKey"))

	env.SetCLI(tofuCLI)

	env.Step(ctx, t, step("init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("state pull", contains(`"greeting"`), "state", "pull"))
}

// takeRestoreBackup takes backup restoreBackupID of the running server into
// a target directory with the backup service and returns the directory. The
// backup reads the live database in one read transaction, so the server
// keeps running while it copies.
func takeRestoreBackup(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	targetDir := filepath.Join(t.TempDir(), "target")

	switch env.Store() {
	case e2e.StoreSQLite:
		takeSQLiteBackup(ctx, t, env, targetDir)
	case e2e.StorePostgres:
		takePostgresBackup(ctx, t, env, targetDir)
	default:
		t.Fatalf("unknown store: %s", env.Store())
	}

	return targetDir
}

// takeSQLiteBackup takes backup restoreBackupID of the SQLite database the
// environment serves into targetDir.
func takeSQLiteBackup(ctx context.Context, t *testing.T, env *e2e.Env, targetDir string) {
	t.Helper()

	dbURL := env.DatabaseURL(t)

	path, ok := strings.CutPrefix(dbURL, "sqlite:")
	if !ok {
		t.Fatalf("database URL %q is not a SQLite URL", dbURL)
	}

	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}

	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close source store: %v", closeErr)
		}
	}()

	runBackupService(ctx, t, env, backupServiceInput{exporter: st, registry: st, targetDir: targetDir})
}

// takePostgresBackup takes backup restoreBackupID of the PostgreSQL schema
// the environment serves into targetDir.
func takePostgresBackup(ctx context.Context, t *testing.T, env *e2e.Env, targetDir string) {
	t.Helper()

	st, err := postgres.Open(ctx, env.DatabaseURL(t))
	if err != nil {
		t.Fatalf("open source store: %v", err)
	}

	defer st.Close()

	runBackupService(ctx, t, env, backupServiceInput{exporter: st, registry: st, targetDir: targetDir})
}

// backupServiceInput bundles the backup service inputs: the store as
// exporter and registry with the target directory.
type backupServiceInput struct {
	exporter  backup.Exporter
	registry  backup.Registry
	targetDir string
}

// runBackupService runs the backup service over source blobs and store into
// targetDir and proves the backup succeeded.
func runBackupService(ctx context.Context, t *testing.T, env *e2e.Env, in backupServiceInput) {
	t.Helper()

	blobs, err := blobfs.Open(env.BlobDir(t))
	if err != nil {
		t.Fatalf("open source blobs: %v", err)
	}

	defer func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close source blobs: %v", closeErr)
		}
	}()

	target, err := blobfs.Open(in.targetDir)
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	defer func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	}()

	svc, err := backup.NewService(backup.Config{
		Exporter:    in.exporter,
		Registry:    in.registry,
		Blobs:       blobs,
		Target:      target,
		TargetName:  "dir:" + in.targetDir,
		Now:         nil,
		Version:     "",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("backup service: %v", err)
	}

	record, err := svc.Run(ctx, backup.ID(restoreBackupID))
	if err != nil {
		t.Fatalf("run backup: %v", err)
	}

	if record.State != backup.StateSucceeded {
		t.Fatalf("backup state = %q, want %q", record.State, backup.StateSucceeded)
	}
}

// wipeRestoreDestination empties the destination the restore loads into: the
// SQLite file with its blob directory, or the PostgreSQL schema recreated
// empty, so the restore migrates it and finds no rows. The server is stopped.
func wipeRestoreDestination(ctx context.Context, t *testing.T, env *e2e.Env, store e2e.Store) {
	t.Helper()

	switch store {
	case e2e.StoreSQLite:
		wipeSQLiteDestination(t, env)
	case e2e.StorePostgres:
		wipePostgresDestination(ctx, t, env)
	default:
		t.Fatalf("unknown store: %s", store)
	}
}

// wipeSQLiteDestination removes the SQLite file with its journals and the
// blob directory. The restore recreates both.
func wipeSQLiteDestination(t *testing.T, env *e2e.Env) {
	t.Helper()

	dbURL := env.DatabaseURL(t)

	path, ok := strings.CutPrefix(dbURL, "sqlite:")
	if !ok {
		t.Fatalf("database URL %q is not a SQLite URL", dbURL)
	}

	for _, file := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", file, err)
		}
	}

	if err := os.RemoveAll(env.BlobDir(t)); err != nil {
		t.Fatalf("remove blob directory: %v", err)
	}
}

// wipePostgresDestination drops the PostgreSQL schema the environment serves
// and recreates it empty, and removes the blob directory. The restore
// migrates the empty schema and finds no rows and no blobs: a copied blob
// that the destination already holds is an error.
func wipePostgresDestination(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	schema := restoreSchema(t, env.DatabaseURL(t))

	conn, err := pgx.Connect(ctx, e2e.PostgresURL())
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close postgres connection: %v", closeErr)
		}
	}()

	name := pgx.Identifier{schema}.Sanitize()

	if _, err := conn.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}

	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := os.RemoveAll(env.BlobDir(t)); err != nil {
		t.Fatalf("remove blob directory: %v", err)
	}
}

// restoreSchema returns the search_path schema of schemaURL: the isolated
// schema the environment serves.
func restoreSchema(t *testing.T, schemaURL string) string {
	t.Helper()

	parsed, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}

	if schema := parsed.Query().Get("search_path"); schema != "" {
		return schema
	}

	t.Fatal("database URL holds no search_path schema")

	return ""
}

// restoreBinaryArgs returns the restore arguments the test runs: the backup
// from targetDir into the database and blob directory of the environment.
func restoreBinaryArgs(t *testing.T, env *e2e.Env, targetDir string) []string {
	t.Helper()

	return []string{
		"restore",
		"--from", "dir:" + targetDir,
		"--backup", restoreBackupID,
		"--database", env.DatabaseURL(t),
		"--blob-dir", env.BlobDir(t),
		"--region", restoreRegion,
	}
}

// runRestoreBinary restores the backup from targetDir into the empty
// destination with the same key and proves the steps with the row counts.
func runRestoreBinary(ctx context.Context, t *testing.T, env *e2e.Env, targetDir string) {
	t.Helper()

	res := env.RunBinary(ctx, t, restoreBinaryArgs(t, env, targetDir)...)

	if res.ExitCode != 0 {
		t.Fatalf("restore exits with status %d:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	for _, want := range []string{
		"restored backup " + restoreBackupID,
		"table namespaces: 1 rows",
		"table state_versions: 1 rows",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Fatalf("restore output lacks %q:\n%s%s", want, res.Stdout, res.Stderr)
		}
	}
}

// checkRestoredState proves the Pulumi secret decrypts and the OpenTofu
// state reads on the restored data.
func checkRestoredState(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	pulumiCLI, tofuCLI e2e.Install,
) {
	t.Helper()

	env.SetCLI(pulumiCLI)

	env.Step(ctx, t, step("config get after restore", contains(restoreSecret), "config", "get", "apiKey"))
	env.Step(ctx, t, step("stack export after restore", exportHidesSecrets, "stack", "export"))

	env.SetCLI(tofuCLI)

	env.Step(ctx, t, step("state pull after restore", contains(`"greeting"`), "state", "pull"))
}

// checkRestoreRefusesLoaded proves a second restore into the loaded
// destination exits 1 and names the refusal.
func checkRestoreRefusesLoaded(ctx context.Context, t *testing.T, env *e2e.Env, targetDir string) {
	t.Helper()

	res := env.RunBinary(ctx, t, restoreBinaryArgs(t, env, targetDir)...)

	if res.ExitCode != 1 {
		t.Fatalf("restore into a loaded destination exits with status %d, want 1:\n%s%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}

	if out := res.Stdout + res.Stderr; !strings.Contains(out, "not empty") {
		t.Fatalf("restore refusal lacks %q:\n%s", "not empty", out)
	}
}
