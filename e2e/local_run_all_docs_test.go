//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tgLocalDocsPage is the guide of the local run-all path.
const tgLocalDocsPage = "docs/guides/terragrunt.md"

// tgLocalDocsUnits returns the three unit workspaces of the guide.
func tgLocalDocsUnits() []string {
	return []string{"web", "api", "worker"}
}

// tgLocalDocsRunAllArgs is the argument list of the run-all command block of
// the guide, as written. The docs step runs this exact command and checks the
// page for it, so a change to the command in the page fails the step.
func tgLocalDocsRunAllArgs() []string {
	return []string{"run", "--all", "--non-interactive", "--parallelism", "3", "--", "apply", "--auto-approve"}
}

// tgLocalDocsBinaries holds the pinned binaries that the page's run-all
// command needs: OpenTofu as the TG_TF_PATH and Terragrunt as the runner. The
// command needs Linux, so the other platforms hold the zero Install of a test
// that drives the API only.
type tgLocalDocsBinaries struct {
	cli e2e.Install
	tg  e2e.Install
}

// TestE2ETerragruntLocalDocsPage proves that the commands of the local run-all
// guide run as written: it reads the guide, runs the API commands of the page
// against the e2e test server and runs the page's run-all command over the
// guide's three units. The run-all command needs the real Terragrunt and
// OpenTofu binaries over TLS, so it runs on Linux, where the CLIs trust the
// server certificate through SSL_CERT_FILE.
func TestE2ETerragruntLocalDocsPage(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	bin := tgLocalDocsCLIs(t)

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, bin.cli, store)

			tgLocalDocsScenario(t.Context(), t, env, bin)
		})
	}
}

// tgLocalDocsCLIs installs the OpenTofu and Terragrunt binaries that the
// page's run-all command needs, or the zero Install on a platform where that
// command does not run.
func tgLocalDocsCLIs(t *testing.T) tgLocalDocsBinaries {
	t.Helper()

	if runtime.GOOS != "linux" {
		return tgLocalDocsBinaries{cli: e2e.NoCLI(), tg: e2e.NoCLI()}
	}

	fetcher := sharedFetcher(t)

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.OpenTofu(), e2e.OpenTofu().Single())
	if err != nil {
		t.Fatalf("install %s: %v", e2e.OpenTofu().Name, err)
	}

	tg, err := fetcher.Install(installCtx, e2e.Terragrunt(), e2e.Terragrunt().Single())
	if err != nil {
		t.Fatalf("install %s: %v", e2e.Terragrunt().Name, err)
	}

	return tgLocalDocsBinaries{cli: cli, tg: tg}
}

// tgLocalDocsScenario checks the page text, runs the page's API commands and
// runs the page's run-all command over the guide's three units.
func tgLocalDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env, bin tgLocalDocsBinaries) {
	t.Helper()

	tgLocalDocsPageText(t)

	checkReady(ctx, t, env)

	// The env holds the namespace "acme" of the guide, so the page's commands
	// run against it as written.
	token := env.TokenA()
	scope := tgLocalScope{env: env, token: token, namespace: e2e.Namespace}
	units := tgLocalDocsUnits()

	// Step 1: make one workspace per unit.
	for _, unit := range units {
		scope.createWorkspace(ctx, t, unit)
	}

	// Step 3: run the apply over all units, from the live directory, with the
	// exact command of the page. It reports whether the run happened; the
	// command needs Linux.
	ran := tgLocalDocsRunAll(ctx, t, env, bin)

	// Step 4: read the state versions of each workspace. Each workspace holds
	// one new state version after the run.
	for _, unit := range units {
		status, body, err := env.TFEStatus(ctx, token, http.MethodGet,
			tgLocalHistoryPath(e2e.Namespace, unit), "")
		if err != nil {
			t.Fatalf("list state versions of %s: %v", unit, err)
		}

		if status != http.StatusOK {
			t.Fatalf("state versions of %s answer %d, want 200: %s", unit, status, body)
		}

		if ran {
			scope.checkOneVersion(ctx, t, unit)
		}
	}

	// Step 4: the units list is the cross-unit view of the run.
	status, body, err := env.APIStatusAs(
		ctx, token, http.MethodGet, "/api/dustpan/v1/namespaces/"+e2e.Namespace+"/units", "",
	)
	if err != nil {
		t.Fatalf("list the units: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("units list answers %d, want 200: %s", status, body)
	}
}

// tgLocalDocsRunAll runs the page's run-all command over the guide's three
// units and proves each workspace holds one new state version. The command
// needs the real Terragrunt and OpenTofu binaries over TLS, so it runs on
// Linux only and reports false elsewhere.
func tgLocalDocsRunAll(ctx context.Context, t *testing.T, env *e2e.Env, bin tgLocalDocsBinaries) bool {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Log("the page's run-all command needs Linux; the page text and the API commands ran")

		return false
	}

	runner := tgLocalRunner(ctx, t, env, bin.cli, env.TokenA())
	runner.tg = bin.tg

	live := filepath.Join(env.Home(), "tg-local-docs-live")
	tgLocalLiveRoot(t, live)

	for _, unit := range tgLocalDocsUnits() {
		runner.writeLiveUnit(t, live, e2e.Namespace, unit, unit)
	}

	runner.runAll(ctx, t, live, tgLocalDocsRunAllArgs()...)

	return true
}

// tgLocalDocsPageText proves that the guide holds each command of the run and
// that the links between the pages resolve.
func tgLocalDocsPageText(t *testing.T) {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	page, pageErr := root.ReadFile(tgLocalDocsPage)
	quickstart, quickErr := root.ReadFile("docs/guides/quickstart.md")

	if closeErr := root.Close(); pageErr != nil || quickErr != nil || closeErr != nil {
		t.Fatalf("read the docs pages: %v %v %v", pageErr, quickErr, closeErr)
	}

	// The commands of the guide, as written. The run-all command is built from
	// the arguments the step runs, so the page and the run cannot drift. The
	// test reads the page and runs each API command against the e2e test
	// server, so a command that changes fails the e2e step.
	commands := []string{
		`"$SERVER/api/v2/organizations/acme/workspaces"`,
		"terragrunt " + strings.Join(tgLocalDocsRunAllArgs(), " "),
		`"$SERVER/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=$unit"`,
		`"$SERVER/api/dustpan/v1/namespaces/acme/units"`,
	}

	for _, command := range commands {
		if !strings.Contains(string(page), command) {
			t.Errorf("%s lacks the command %q", tgLocalDocsPage, command)
		}
	}

	if !strings.Contains(string(page), "../explanation/profiles-and-blueprints.md") {
		t.Errorf("%s does not link the explanation page", tgLocalDocsPage)
	}

	if !strings.Contains(string(quickstart), "terragrunt.md") {
		t.Errorf("docs/guides/quickstart.md does not link %s", tgLocalDocsPage)
	}
}
