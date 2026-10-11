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

// pulumiScenario exercises login and stack management. A namespace is a
// Pulumi organization, so every step that names or implies an organization
// should resolve to the Namespace namespace.
func pulumiScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)
	renamedQualified := fmt.Sprintf("%s/%s/renamed", org, e2e.Project)
	renamePath := fmt.Sprintf("/api/stacks/%s/%s/%s/rename", org, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", org, e2e.Project)
	scratch := fmt.Sprintf("%s/%s/scratch", org, e2e.Project)

	for _, s := range pulumiSetupSteps(env, org, qualified, stackPath) {
		env.Step(ctx, t, s)
	}

	beforeID := stackIDBeforeRename(t, env, org)

	env.Step(ctx, t, e2e.StepSpec{
		Check: checkRenameKeptID(ctx, env, org, renamePath, beforeID),
		Name:  "stack rename",
		Args:  []string{"stack", "rename", renamedQualified, "--stack", qualified},
		Env:   nil,
		Fail:  false,
	})

	// Tags follow the stack through a rename.
	env.Step(ctx, t, step("stack tag ls after rename", tagListed("team", "e2e"), "stack", "tag", "ls"))
	env.Step(ctx, t, step("stack tag rm", nil, "stack", "tag", "rm", "team"))
	env.Step(ctx, t, step("stack tag ls after rm", tagAbsent("team"), "stack", "tag", "ls"))

	for _, s := range pulumiTeardownSteps(scratch) {
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
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("stack export", nil, "stack", "export"),
		step("preview", nil, "preview", "--non-interactive"),
		step("up", nil, "up", "--yes", "--non-interactive"),
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
// its own, so the result does not depend on rename.
func pulumiTeardownSteps(scratch string) []e2e.StepSpec {
	return []e2e.StepSpec{
		step("stack init scratch", nil, "stack", "init", scratch, "--no-select"),
		step("stack rm", nil, "stack", "rm", "--yes", "--stack", scratch),
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

	overrideURL := "http://console.example.test:3000/" + e2e.Subject

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

	for _, s := range []e2e.StepSpec{
		step("init", nil, "init", "-input=false", "-no-color"),
		step("workspace show", contains(e2e.Workspace), "workspace", "show"),
		step("plan", nil, "plan", "-input=false", "-no-color"),
		step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
		step("output", contains(`"hello"`), "output", "-json", "-no-color"),
		step("state pull", contains(`"greeting"`), "state", "pull"),
	} {
		env.Step(ctx, t, s)
	}
}
