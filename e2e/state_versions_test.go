//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestStateVersions proves with the real CLIs that each state write sets the
// current state version of the unit: a Pulumi checkpoint and an OpenTofu
// finalize each make the written version current, and a person reads the state
// through the routes of the server.
//
// It writes no report: its run would overwrite the report of TestCLIs for the
// same version and store.
func TestStateVersions(t *testing.T) {
	t.Parallel()

	t.Run("Pulumi checkpoint", func(t *testing.T) {
		t.Parallel()

		cli := installOneVersion(t, e2e.Pulumi())

		for _, store := range databasesUnderTest(t) {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()
				runPulumiExportAfterUp(t, cli, store)
			})
		}
	})

	t.Run("OpenTofu finalize", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS != "linux" {
			// OpenTofu must trust the test server's certificate, and Go
			// honors SSL_CERT_FILE only on Linux.
			t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
		}

		cli := installOneVersion(t, e2e.OpenTofu())

		for _, store := range databasesUnderTest(t) {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()
				runTofuCurrentVersionAfterApplies(t, cli, store)
			})
		}
	})
}

// installOneVersion installs the first pinned version of tool and returns it.
// The steps use one version per tool, so they download one CLI per tool.
func installOneVersion(t *testing.T, tool e2e.Tool) e2e.Install {
	t.Helper()

	version := tool.Single()
	if version == "" {
		t.Fatalf("the %s tool needs at least one version in its matrix", tool.Name)
	}

	ctx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := sharedFetcher(t).Install(ctx, tool, version)
	if err != nil {
		t.Fatalf("install %s %s: %v", tool.Name, version, err)
	}

	t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

	return cli
}

// databasesUnderTest returns the stores of the run.
func databasesUnderTest(t *testing.T) []e2e.Store {
	t.Helper()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	return databases
}

// skipWithoutPostgres skips a store that the run does not configure.
func skipWithoutPostgres(t *testing.T, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}
}

// stateVersionReader makes a person that holds the writer role in the e2e
// namespace, and returns a personal access token of that person. The writer
// role holds unit:read and unit:write on the units of the namespace, so the
// person reads the state of a unit and restores a state version.
func stateVersionReader(ctx context.Context, t *testing.T, env *e2e.Env, subject string) string {
	t.Helper()

	var person e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"` + e2e.SignInSubject(subject) + `","kind":"person"}`,
		http.StatusCreated,
	}, &person)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost,
		"/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + person.ID + `","role":"writer"}`,
		http.StatusCreated,
	}, nil)

	return env.PersonalToken(ctx, t, person.ID, "state-versions").Secret
}

// runPulumiExportAfterUp runs a stack update with the real Pulumi CLI, then
// reads the state of the stack through the export route with a personal
// access token of a person. The route answers the checkpoint that the update
// saved, so the read proves that the state write committed its state.
func runPulumiExportAfterUp(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	skipWithoutPostgres(t, store)

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	qualified := fmt.Sprintf("%s/%s/%s", e2e.Namespace, e2e.Project, stackName)

	env.Step(ctx, t, step("login", contains(env.URL), "login"))
	env.Step(ctx, t, step("stack init", nil, "stack", "init", qualified))
	env.Step(ctx, t, step("stack init other", nil, "stack", "init", "other"))
	env.Step(ctx, t, step("stack select", nil, "stack", "select", qualified))
	env.Step(ctx, t, step("up", nil, "up", "--yes", "--non-interactive"))

	exported := env.Step(ctx, t, step("stack export", nil, "stack", "export")).Stdout

	if exported == "" {
		t.Fatal("the stack export of the CLI is empty after the up")
	}

	token := stateVersionReader(ctx, t, env, "state-version-reader")
	path := "/api/stacks/" + e2e.Namespace + "/" + e2e.Project + "/" + stackName + "/export"

	status, body, err := env.APIStatusAs(ctx, token, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", path, status, http.StatusOK, body)
	}

	if canonicalExport(t, body) != canonicalExport(t, exported) {
		t.Fatalf("GET %s does not answer the checkpoint that the up saved:\n%s", path, body)
	}
}

// stateVersionReadDocument is the part of a state version document that the
// steps read.
type stateVersionReadDocument struct {
	Data stateVersionReadData `json:"data"`
}

// stateVersionReadData is the data object of a state version document.
type stateVersionReadData struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Attributes stateVersionReadAttributes `json:"attributes"`
}

// stateVersionReadAttributes holds the attributes of a state version document
// that the steps read: the status, the serial and the MD5.
type stateVersionReadAttributes struct {
	Status string `json:"status"`
	Serial int64  `json:"serial"`
	MD5    string `json:"md5"`
}

// runTofuCurrentVersionAfterApplies runs two applies with the real OpenTofu
// CLI, then reads the current state version of the workspace with a personal
// access token of a person. Each apply sets the current version, so the
// second version is a new one with a higher serial.
func runTofuCurrentVersionAfterApplies(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	skipWithoutPostgres(t, store)

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, cli, store)

	checkReady(ctx, t, env)

	env.Step(ctx, t, step("init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	wsID := env.WorkspaceID(ctx, t)
	token := stateVersionReader(ctx, t, env, "state-version-reader")
	path := "/api/v2/workspaces/" + wsID + "/current-state-version"

	first := readCurrentStateVersion(t, env, token, path)
	checkCurrentStateVersion(t, "after the first apply", first)

	env.WriteTofuConfig(t, tofuSecondConfig(env.Host()))
	env.Step(ctx, t, step("apply again", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	second := readCurrentStateVersion(t, env, token, path)
	checkCurrentStateVersion(t, "after the second apply", second)

	if second.Data.ID == first.Data.ID {
		t.Fatalf("the current state version %s did not change after the second apply", second.Data.ID)
	}

	if second.Data.Attributes.Serial <= first.Data.Attributes.Serial {
		t.Fatalf("the serial after the second apply = %d, want above %d",
			second.Data.Attributes.Serial, first.Data.Attributes.Serial)
	}
}

// readCurrentStateVersion reads the current state version of a workspace as
// token and decodes the document.
func readCurrentStateVersion(
	t *testing.T, env *e2e.Env, token, path string,
) stateVersionReadDocument {
	t.Helper()

	status, body, err := env.TFEStatus(t.Context(), token, http.MethodGet, path, "")
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET %s answers %d, want %d (%s)", path, status, http.StatusOK, body)
	}

	var doc stateVersionReadDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode GET %s %q: %v", path, body, err)
	}

	return doc
}

// checkCurrentStateVersion fails t unless doc is a finalized state version of
// the workspace with a serial and an MD5. when names the step in the message.
func checkCurrentStateVersion(t *testing.T, when string, doc stateVersionReadDocument) {
	t.Helper()

	if doc.Data.Type != "state-versions" || doc.Data.ID == "" {
		t.Fatalf("the current state version %s = %+v, want a state version with an ID", when, doc.Data)
	}

	if doc.Data.Attributes.Status != "finalized" {
		t.Fatalf("the status of the current state version %s = %q, want %q",
			when, doc.Data.Attributes.Status, "finalized")
	}

	if doc.Data.Attributes.Serial < 1 || doc.Data.Attributes.MD5 == "" {
		t.Fatalf("the current state version %s = %+v, want a serial and an MD5",
			when, doc.Data.Attributes)
	}
}
