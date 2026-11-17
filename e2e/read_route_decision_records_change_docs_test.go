//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// decisionRecordsDocsPage is the guide that this step runs.
const decisionRecordsDocsPage = "docs/guides/decision-records.md"

// TestE2EReadRouteDecisionRecordsDocsPage proves that the commands of the
// decision records guide run as written: it sets a gate that refuses a write
// with the real OpenTofu CLI, then reads the record with a personal access
// token of a principal with the gate read action.
func TestE2EReadRouteDecisionRecordsDocsPage(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), e2e.OpenTofu().Single())
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, e2e.OpenTofu().Single(), err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, tofuCLI, db)

			decisionRecordsDocsScenario(t.Context(), t, env, tofuCLI)
		})
	}
}

// decisionRecordsDocsScenario runs the page commands against env.
func decisionRecordsDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install) {
	t.Helper()

	decisionRecordsDocsPageText(t)

	checkReady(ctx, t, env)

	// A viewer reads gates and records. A maintainer writes state.
	viewer := issueDecisionRecordsPerson(ctx, t, env, "bo", "viewer")
	maintainer := issueDecisionRecordsPerson(ctx, t, env, "ann", "maintainer")

	putDecisionRecordsDenyGate(ctx, t, env)

	// The refused lock leaves one stored record.
	recordID := runDecisionRecordsRefusedLock(ctx, t, env, cli, maintainer)

	// The page read call, as written: the namespace route with the viewer
	// token reads the stored record.
	readPath := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/decision-records/" + string(recordID)
	checkDecisionRecordRead(ctx, t, env,
		decisionRecordsProbe{token: viewer.token.Secret, path: readPath, recordID: recordID})

	// The page lists the error answers. Each one answers 404 with the code
	// gate.not-found and a body that names no record ID. The maintainer holds
	// no gate read grant on the namespace.
	malformedPath := "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/decision-records/gdr-too-short"
	// The installation route has no record in this version.
	installationPath := "/api/dustpan/v1/decision-records/" + string(recordID)

	for name, probe := range map[string]decisionRecordsProbe{
		"no gate read action": {token: maintainer.token.Secret, path: readPath, recordID: recordID},
		"unauthenticated":     {token: "", path: readPath, recordID: recordID},
		"malformed ID":        {token: viewer.token.Secret, path: malformedPath, recordID: recordID},
		"installation route":  {token: viewer.token.Secret, path: installationPath, recordID: recordID},
	} {
		t.Run(name, func(t *testing.T) {
			checkDecisionRecordNotFound(ctx, t, env, probe)
		})
	}
}

// putDecisionRecordsDenyGate saves a deny gate at state.write for the lock of
// the workspace.
func putDecisionRecordsDenyGate(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	mustDecisionRecords(t, env.PutGate(ctx, t, gate.Gate{
		Scope:         decisionRecordsUnitScope(t),
		ID:            "frozen",
		Version:       0,
		DecisionPoint: gate.PointStateWrite,
		Condition:     `context.operation.kind == "lock"`,
		Unless:        "",
		Outcome:       gate.OutcomeDeny,
		Message:       "Locks are frozen for the e2e check.",
		Mode:          gate.ModeEnforce,
		OnError:       gate.OnErrorFail,
		Final:         false,
		CheckedWith:   gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:         "",
		Reads:         nil,
	}))
}

// decisionRecordsProbe is one read call of the scenario. An empty token sends
// no credential.
type decisionRecordsProbe struct {
	token    string
	path     string
	recordID gate.RecordID
}

// checkDecisionRecordRead fails unless GET path with token answers 200 with
// the stored record of want.
func checkDecisionRecordRead(ctx context.Context, t *testing.T, env *e2e.Env, probe decisionRecordsProbe) {
	t.Helper()

	token, path, want := probe.token, probe.path, probe.recordID

	status, body, err := env.ServerAPIStatusAs(ctx, token, http.MethodGet, path, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s answers %d, %v: %s, want 200", path, status, err, body)
	}

	var rec gate.Record
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		t.Fatalf("decode the record: %v: %s", err, body)
	}

	if rec.ID != want {
		t.Fatalf("record id = %q, want %q", rec.ID, want)
	}

	if !strings.Contains(body, `"secretKeys"`) {
		t.Fatalf("record body holds no secretKeys: %s", body)
	}
}

// checkDecisionRecordNotFound fails unless GET path answers 404 with the code
// gate.not-found and a body that names no record ID. An empty token sends no
// credential.
func checkDecisionRecordNotFound(ctx context.Context, t *testing.T, env *e2e.Env, probe decisionRecordsProbe) {
	t.Helper()

	token, path, recordID := probe.token, probe.path, probe.recordID

	var (
		status int
		body   string
		err    error
	)

	if token == "" {
		status, body, err = env.ServerAPIStatusBare(ctx, http.MethodGet, path)
	} else {
		status, body, err = env.ServerAPIStatusAs(ctx, token, http.MethodGet, path, "")
	}

	if err != nil || status != http.StatusNotFound || !strings.Contains(body, "gate.not-found") {
		t.Fatalf("GET %s answers %d, %v: %s, want 404 gate.not-found", path, status, err, body)
	}

	if strings.Contains(body, string(recordID)) {
		t.Fatalf("404 body names the record ID: %s", body)
	}
}

// decisionRecordsPerson is one person of the scenario with its token.
type decisionRecordsPerson struct {
	principal e2ePrincipal
	token     e2e.PersonalToken
}

// issueDecisionRecordsPerson creates a person with role on the e2e namespace,
// and issues the person its access token.
func issueDecisionRecordsPerson(
	ctx context.Context, t *testing.T, env *e2e.Env, name, role string,
) *decisionRecordsPerson {
	t.Helper()

	p := new(decisionRecordsPerson)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		`{"subject":` + jsonString(e2e.SignInSubject(name+"@example.com")) + `,"kind":"person"}`, http.StatusCreated,
	}, &p.principal)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/grants",
		`{"subject":"principal:` + p.principal.ID + `","role":"` + role + `"}`, http.StatusCreated,
	}, nil)

	p.token = env.PersonalToken(ctx, t, p.principal.ID, "decision-records-"+name)

	return p
}

// decisionRecordsUnitScope returns the unit scope of the e2e workspace.
func decisionRecordsUnitScope(t *testing.T) gate.Scope {
	t.Helper()

	s, err := gate.UnitScope(namespace.Name(e2e.Namespace), "opentofu", e2e.Workspace)
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	return s
}

// runDecisionRecordsRefusedLock runs a tofu plan with no lock wait, which the
// deny gate refuses, and returns the ID of the decision record from the
// stored records.
func runDecisionRecordsRefusedLock(
	ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, maintainer *decisionRecordsPerson,
) gate.RecordID {
	t.Helper()

	before, err := env.StateWriteRecords(ctx, namespace.Name(e2e.Namespace))
	if err != nil {
		t.Fatalf("records before the refusal: %v", err)
	}

	writeDecisionRecordsTofuProject(t, env, maintainer)

	res, err := env.RunAs(ctx, cli, decisionRecordsTofuDir(env),
		[]string{"TF_CLI_CONFIG_FILE=" + decisionRecordsTofuRC(env, maintainer)},
		"plan", "-input=false", "-no-color", "-lock-timeout=0s")
	if err != nil {
		t.Fatalf("tofu plan: %v", err)
	}

	if res.ExitCode == 0 {
		t.Fatalf("tofu plan succeeds, want a refusal:\n%s", res.Stdout+res.Stderr)
	}

	after, err := env.StateWriteRecords(ctx, namespace.Name(e2e.Namespace))
	if err != nil {
		t.Fatalf("records after the refusal: %v", err)
	}

	if len(after) != len(before)+1 {
		t.Fatalf("records = %d (before %d), want one new record", len(after), len(before))
	}

	return after[len(after)-1].ID
}

// decisionRecordsTofuDir is the directory of the OpenTofu configuration.
func decisionRecordsTofuDir(env *e2e.Env) string {
	return env.Home() + "/tofu-decision-records"
}

// decisionRecordsTofuRC returns the CLI config file that holds the token.
func decisionRecordsTofuRC(env *e2e.Env, p *decisionRecordsPerson) string {
	return env.Home() + "/tofurc-decision-records-" + p.principal.ID
}

// writeDecisionRecordsTofuProject writes an OpenTofu configuration that uses
// the workspace of the e2e namespace.
func writeDecisionRecordsTofuProject(t *testing.T, env *e2e.Env, maintainer *decisionRecordsPerson) {
	t.Helper()

	dir := decisionRecordsTofuDir(env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	config := `terraform {
  cloud {
    hostname     = "` + env.Host() + `"
    organization = "` + e2e.Namespace + `"
    workspaces {
      name = "` + e2e.Workspace + `"
    }
  }
}

output "greeting" {
  value = "hello"
}
`

	env.WriteFile(t, dir+"/main.tf", config)
	env.WriteFile(t, decisionRecordsTofuRC(env, maintainer),
		"credentials \""+env.Host()+"\" {\n  token = \""+maintainer.token.Secret+"\"\n}\n")
}

// mustDecisionRecords fails the test on an error.
func mustDecisionRecords(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("decision records setup: %v", err)
	}
}

// decisionRecordsDocsPageText fails unless the page holds the read calls and
// the error codes that the scenario runs.
func decisionRecordsDocsPageText(t *testing.T) {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	data, readErr := root.ReadFile(decisionRecordsDocsPage)
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", decisionRecordsDocsPage, readErr, closeErr)
	}

	page := string(data)

	for _, want := range []string{
		`/api/dustpan/v1/namespaces/acme/decision-records/$RECORD`,
		`/api/dustpan/v1/decision-records/$RECORD`,
		"gate.not-found",
		"gate.unavailable",
		`installation:read-gates`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("%s does not hold %q", decisionRecordsDocsPage, want)
		}
	}
}

// jsonString quotes s for an inline JSON body.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}

	return string(b)
}
