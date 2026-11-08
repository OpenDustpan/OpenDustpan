//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// refusalNamespace holds the stack and the workspace of the refusal test.
const refusalNamespace = "refuse"

// refusedProgram is the Pulumi program of the refusal test: no resources.
const refusedProgram = "name: web\nruntime: yaml\n"

// refusalPerson is one person of the refusal test with its access token.
type refusalPerson struct {
	name      string
	role      string
	principal e2ePrincipal
	token     e2e.PersonalToken
}

// TestRefusedActionsAreAudited proves that a refused action writes an audit
// entry of the outcome denied, for each tool, on each store. A maintainer
// deploys with each tool. A viewer is refused a deploy with each tool, and the
// namespace log holds one denied entry per refusal, made by each tool.
func TestRefusedActionsAreAudited(t *testing.T) {
	t.Parallel()

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiVersion := e2e.Pulumi().Single()
	tofuVersion := e2e.OpenTofu().Single()

	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("the Pulumi and OpenTofu tools each need a version in their matrix")
	}

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runRefusedActionsOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// runRefusedActionsOnStore runs the refusal test on one store.
func runRefusedActionsOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, tofuCLI, store)

	checkReady(ctx, t, env)
	env.CreateNamespace(ctx, t, refusalNamespace)

	maintainer := issueRefusalPerson(ctx, t, env, "ann", "maintainer")
	viewer := issueRefusalPerson(ctx, t, env, "bo", "viewer")

	env.WritePulumiProject(t, refusedProgram)
	runPulumiRefusal(t, env, pulumiCLI, maintainer, viewer)

	writeRefusalTofuProject(t, env)
	runTofuRefusal(t, env, tofuCLI, maintainer, viewer)
	runTofuHTTPRefusal(t, env, tofuCLI, maintainer, viewer)

	checkRefusalAudit(ctx, t, env)
}

// issueRefusalPerson creates a person with role on the refusal namespace, and
// issues the person its access token.
func issueRefusalPerson(ctx context.Context, t *testing.T, env *e2e.Env, name, role string) *refusalPerson {
	t.Helper()

	p := new(refusalPerson)
	p.name, p.role = name, role

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		fmt.Sprintf(`{"subject":%q,"kind":"person"}`, e2e.SignInSubject(name+"@example.com")), http.StatusCreated,
	}, &p.principal)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + refusalNamespace + "/grants",
		`{"subject":"principal:` + p.principal.ID + `","role":"` + role + `"}`, http.StatusCreated,
	}, nil)

	p.token = env.PersonalToken(ctx, t, p.principal.ID, "refuse-"+name)

	return p
}

// runPulumiRefusal deploys as the maintainer and checks that the viewer's
// deploy is refused.
func runPulumiRefusal(t *testing.T, env *e2e.Env, cli e2e.Install, maintainer, viewer *refusalPerson) {
	t.Helper()

	res := runRefusalCLI(t, env, cli, maintainer, "stack", "init", refusalNamespace+"/prod")
	if res.ExitCode != 0 {
		t.Fatalf("pulumi stack init as maintainer exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	res = runRefusalCLI(t, env, cli, maintainer, "up", "--yes", "--skip-preview")
	if res.ExitCode != 0 {
		t.Fatalf("pulumi up as maintainer exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	res = runRefusalCLI(t, env, cli, viewer, "up", "--yes", "--skip-preview")
	if res.ExitCode == 0 {
		t.Fatalf("pulumi up as viewer succeeds, want a refusal:\n%s", res.Stdout)
	}
}

// refusalTofuDir is the directory of the OpenTofu configuration of the refusal
// test, in the harness home directory.
func refusalTofuDir(env *e2e.Env) string {
	return filepath.Join(env.Home(), "tofu-refuse")
}

// writeRefusalTofuProject writes an OpenTofu configuration that uses the
// workspace of the refusal namespace.
func writeRefusalTofuProject(t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := refusalTofuDir(env)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	config := fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = "web"
    }
  }
}

output "greeting" {
  value = "hello"
}
`, env.Host(), refusalNamespace)

	env.WriteFile(t, filepath.Join(dir, "main.tf"), config)
}

// runTofuRefusal applies as the maintainer and checks that the viewer's apply
// is refused.
func runTofuRefusal(t *testing.T, env *e2e.Env, cli e2e.Install, maintainer, viewer *refusalPerson) {
	t.Helper()

	for _, p := range []*refusalPerson{maintainer, viewer} {
		env.WriteFile(t, filepath.Join(env.Home(), "tofurc-refuse-"+p.name), fmt.Sprintf(
			"credentials %q {\n  token = %q\n}\n", env.Host(), p.token.Secret,
		))
	}

	res := runRefusalCLI(t, env, cli, maintainer, "init", "-input=false")
	if res.ExitCode != 0 {
		t.Fatalf("tofu init as maintainer exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	res = runRefusalCLI(t, env, cli, maintainer, "apply", "-input=false", "-auto-approve")
	if res.ExitCode != 0 {
		t.Fatalf("tofu apply as maintainer exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	res = runRefusalCLI(t, env, cli, viewer, "apply", "-input=false", "-auto-approve")
	if res.ExitCode == 0 {
		t.Fatalf("tofu apply as viewer succeeds, want a refusal:\n%s", res.Stdout)
	}
}

// runRefusalCLI runs one CLI as person in its project directory. Pulumi takes
// the token of the person in its environment, and OpenTofu the CLI file that
// holds it.
func runRefusalCLI(t *testing.T, env *e2e.Env, cli e2e.Install, person *refusalPerson, args ...string) e2e.Result {
	t.Helper()

	dir, extra := env.ProjectDir(), []string{"PULUMI_ACCESS_TOKEN=" + person.token.Secret}
	if cli.Tool == e2e.ToolTofu {
		dir = refusalTofuDir(env)
		extra = []string{"TF_CLI_CONFIG_FILE=" + filepath.Join(env.Home(), "tofurc-refuse-"+person.name)}
	}

	res, err := env.RunAs(t.Context(), cli, dir, extra, args...)
	if err != nil {
		t.Fatalf("%s %s as %s: %v", cli.Tool, strings.Join(args, " "), person.name, err)
	}

	return res
}

// checkRefusalAudit proves that the namespace log holds a denied entry for
// the viewer from each tool.
func checkRefusalAudit(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	entries, err := fetchAuditEntries(ctx, env, refusalNamespace)
	if err != nil {
		t.Fatalf("list %s audit entries: %v", refusalNamespace, err)
	}

	seen := map[string]bool{}

	for i := range entries {
		if entries[i].Subject != e2e.SignInSubject("bo@example.com") || entries[i].Outcome != "denied" {
			continue
		}

		agent := strings.ToLower(entries[i].UserAgent)

		switch {
		case strings.Contains(agent, "pulumi"):
			seen[e2e.Pulumi().Name] = true
		case strings.Contains(agent, "opentofu"):
			seen[e2e.OpenTofu().Name] = true
		default:
			// Other clients do not count for this test.
		}
	}

	for _, tool := range []string{e2e.Pulumi().Name, e2e.OpenTofu().Name} {
		if !seen[tool] {
			t.Errorf("audit log of %s holds no denied entry for the viewer from %s", refusalNamespace, tool)
		}
	}
}

// runTofuHTTPRefusal runs OpenTofu against the http backend of the refusal
// namespace and checks that the viewer's apply is refused on that route table.
// The namespace log must gain one more denied entry from OpenTofu for the
// viewer, so the refusal is on record for the HTTP backend too.
func runTofuHTTPRefusal(t *testing.T, env *e2e.Env, cli e2e.Install, maintainer, viewer *refusalPerson) {
	t.Helper()

	ctx := t.Context()

	before := deniedEntriesOf(ctx, t, env, e2e.SignInSubject(viewer.name+"@example.com"))

	dir := filepath.Join(env.Home(), "tofu-refuse-http")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	address := env.URL + "/tofu/state/" + refusalNamespace + "/web"
	writeTofuHTTPConfig(t, dir, address)

	httpEnv := func(p *refusalPerson) []string {
		return []string{"TF_HTTP_USERNAME=dustpan", "TF_HTTP_PASSWORD=" + p.token.Secret}
	}

	res, err := env.RunAs(ctx, cli, dir, httpEnv(maintainer), "init", "-input=false", "-no-color")
	if err != nil {
		t.Fatalf("tofu http init: %v", err)
	}

	if res.ExitCode != 0 {
		t.Fatalf("tofu http init as maintainer exits %d:\n%s", res.ExitCode, res.Stderr)
	}

	res, err = env.RunAs(ctx, cli, dir, httpEnv(viewer), "apply", "-input=false", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("tofu http apply as viewer: %v", err)
	}

	if res.ExitCode == 0 {
		t.Fatalf("tofu http apply as viewer succeeds, want a refusal:\n%s", res.Stdout)
	}

	after := deniedEntriesOf(ctx, t, env, e2e.SignInSubject(viewer.name+"@example.com"))
	if after <= before {
		t.Fatalf("audit log holds %d denied entries for the viewer after the HTTP refusal, want more than %d",
			after, before)
	}
}

// deniedEntriesOf counts the denied entries of subject in the refusal
// namespace log. The OpenTofu HTTP backend sends its own user agent, so the
// count does not filter on the client.
func deniedEntriesOf(ctx context.Context, t *testing.T, env *e2e.Env, subject string) int {
	t.Helper()

	entries, err := fetchAuditEntries(ctx, env, refusalNamespace)
	if err != nil {
		t.Fatalf("list %s audit entries: %v", refusalNamespace, err)
	}

	count := 0

	for i := range entries {
		if entries[i].Subject == subject && entries[i].Outcome == "denied" {
			count++
		}
	}

	return count
}
