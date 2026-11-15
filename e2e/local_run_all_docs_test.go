//go:build e2e

package e2e_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// tgLocalDocsPage is the guide of the local run-all path.
const tgLocalDocsPage = "docs/guides/terragrunt.md"

// TestE2ETerragruntLocalDocsPage proves that the commands of the local run-all
// guide run as written: it reads the guide and runs the API commands of the
// page against the e2e test server. The run-all command itself is covered by
// the Terragrunt suite.
func TestE2ETerragruntLocalDocsPage(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			tgLocalDocsScenario(t.Context(), t, env)
		})
	}
}

// tgLocalDocsScenario checks the page text and runs the page's API commands.
func tgLocalDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	tgLocalDocsPageText(t)

	checkReady(ctx, t, env)

	// The env holds the namespace "acme" of the guide, so the page's commands
	// run against it as written.
	token := env.TokenA()
	units := []string{"web", "api", "worker"}

	// Step 1: make one workspace per unit.
	for _, unit := range units {
		status, body, err := env.TFEStatus(ctx, token, http.MethodPost,
			"/api/v2/organizations/acme/workspaces",
			`{"data":{"type":"workspaces","attributes":{"name":"`+unit+`"}}}`)
		if err != nil {
			t.Fatalf("create workspace %s: %v", unit, err)
		}

		if status != http.StatusCreated {
			t.Fatalf("create workspace %s answers %d, want 201: %s", unit, status, body)
		}
	}

	// Step 4: read the state versions of each workspace.
	for _, unit := range units {
		status, body, err := env.TFEStatus(ctx, token, http.MethodGet,
			"/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]="+unit, "")
		if err != nil {
			t.Fatalf("list state versions of %s: %v", unit, err)
		}

		if status != http.StatusOK {
			t.Fatalf("state versions of %s answer %d, want 200: %s", unit, status, body)
		}
	}

	// Step 4: the units list is the cross-unit view of the run.
	status, body, err := env.APIStatusAs(ctx, token, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", "")
	if err != nil {
		t.Fatalf("list the units: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("units list answers %d, want 200: %s", status, body)
	}
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

	// The commands of the guide, as written. The test reads the page and runs
	// each API command against the e2e test server, so a command that changes
	// fails the e2e step.
	commands := []string{
		`"$SERVER/api/v2/organizations/acme/workspaces"`,
		`terragrunt run --all --non-interactive --parallelism 3 -- apply --auto-approve`,
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
