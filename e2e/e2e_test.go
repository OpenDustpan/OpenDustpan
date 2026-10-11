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

// step is a scenario step with no extra environment.
func step(name string, check func(e2e.Result) error, args ...string) e2e.StepSpec {
	return e2e.StepSpec{Check: check, Name: name, Args: args, Env: nil}
}

// pulumiScenario exercises login and stack management. A namespace is a
// Pulumi organization, so every step that names or implies an organization
// should resolve to the Namespace namespace.
func pulumiScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)
	stackPath := fmt.Sprintf("/api/stacks/%s/%s", org, e2e.Project)
	scratch := fmt.Sprintf("%s/%s/scratch", org, e2e.Project)

	for _, s := range []e2e.StepSpec{
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
		},
		step("stack rename", nil, "stack", "rename", org+"/"+e2e.Project+"/renamed", "--stack", qualified),
		// A stack of its own, so the result does not depend on rename.
		step("stack init scratch", nil, "stack", "init", scratch, "--no-select"),
		step("stack rm", nil, "stack", "rm", "--yes", "--stack", scratch),
	} {
		env.Step(ctx, t, s)
	}

	consoleURLSteps(t, env)
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
		Env: []string{"PULUMI_BACKEND_URL=" + apiURL, "HTTP_PROXY=" + env.URL},
	})
	env.Step(ctx, t, e2e.StepSpec{
		Check: contains("(" + overrideURL + ")"),
		Name:  "console url from PULUMI_CONSOLE_DOMAIN",
		Args:  []string{"login"},
		Env:   []string{"PULUMI_CONSOLE_DOMAIN=console.example.test:3000"},
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
