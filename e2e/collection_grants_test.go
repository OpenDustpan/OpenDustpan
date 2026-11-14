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

// scopeNamespace is the namespace of the collection grant scope test.
const scopeNamespace = "grant-scope"

// scopeProgram is the Pulumi program of the scope test: no resources.
const scopeProgram = "name: scope\nruntime: yaml\n"

// scopePerson is one person of the scope test with its access token.
type scopePerson struct {
	name      string
	principal e2ePrincipal
	token     e2e.PersonalToken
}

// scopeRunner runs one CLI as one person of the scope test.
type scopeRunner struct {
	t      *testing.T
	env    *e2e.Env
	cli    e2e.Install
	person *scopePerson
}

// TestCollectionGrantScope proves the collection grant behavior through the
// real Pulumi CLI and the real OpenTofu CLI with a personal access token of
// a non-admin person: a writer on collection A is refused on a unit of
// collection B, a viewer reads but does not write, a revoked grant fails on
// the next request, and the access-review export of the collection names
// each grant with its holder, role, object, creator and time.
func TestCollectionGrantScope(t *testing.T) {
	t.Parallel()

	fetcher := sharedFetcher(t)

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
			runCollectionGrantScopeOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// runCollectionGrantScopeOnStore runs the scope test on one store.
func runCollectionGrantScopeOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnvWithSignIn(ctx, t, tofuCLI, store)

	checkReady(ctx, t, env)
	scopeSetup(ctx, t, env)

	writer := scopeIssuePerson(ctx, t, env, "scope-writer")
	viewer := scopeIssuePerson(ctx, t, env, "scope-viewer")

	var ops e2eGroup
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups", `{"name":"team-a-ops"}`, http.StatusCreated,
	}, &ops)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/groups/" + ops.ID + "/members",
		`{"principalID":"` + writer.principal.ID + `"}`, http.StatusCreated,
	}, nil)

	grantPath := "/api/server/namespaces/" + scopeNamespace + "/collections/team-a/grants"

	var writerGrant e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, grantPath,
		`{"subject":"group:` + ops.ID + `#member","role":"writer"}`, http.StatusCreated,
	}, &writerGrant)

	var viewerGrant e2eGrant
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, grantPath,
		`{"subject":"principal:` + viewer.principal.ID + `","role":"viewer"}`, http.StatusCreated,
	}, &viewerGrant)

	scopeSeedUnits(ctx, t, env, pulumiCLI, tofuCLI)

	checkWriterScope(t, env, pulumiCLI, tofuCLI, writer)
	checkViewerScope(t, env, pulumiCLI, tofuCLI, viewer)
	checkScopeRevocation(ctx, t,
		scopeRunnerFor(t, env, pulumiCLI, writer), scopeRunnerFor(t, env, tofuCLI, writer), writerGrant.ID)
	checkScopeReview(ctx, t, env, writerGrant, viewerGrant)
}

// scopeSetup makes the namespace, the two collections and the placement
// rules of the scope test.
func scopeSetup(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces", `{"name":"` + scopeNamespace + `"}`, http.StatusCreated,
	}, nil)

	base := "/api/dustpan/v1/namespaces/" + scopeNamespace

	for _, name := range []string{"team-a", "team-b"} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, base + "/collections", collectionBody(t, name), http.StatusCreated,
		}, nil)
	}

	for _, rule := range []string{
		`{"tool":"pulumi","project":"scope","stack":"a","collection":"team-a"}`,
		`{"tool":"pulumi","project":"scope","stack":"b","collection":"team-b"}`,
		`{"tool":"opentofu","project":"*","stack":"a","collection":"team-a"}`,
		`{"tool":"opentofu","project":"*","stack":"b","collection":"team-b"}`,
	} {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, base + "/placement-rules", rule, http.StatusCreated,
		}, nil)
	}
}

// scopeIssuePerson creates a person and issues the person its access token.
func scopeIssuePerson(ctx context.Context, t *testing.T, env *e2e.Env, name string) *scopePerson {
	t.Helper()

	p := new(scopePerson)
	p.name = name

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals",
		fmt.Sprintf(`{"subject":%q,"kind":"person"}`, e2e.SignInSubject(name+"@example.com")),
		http.StatusCreated,
	}, &p.principal)

	p.token = env.PersonalToken(ctx, t, p.principal.ID, "scope-"+name)

	return p
}

// scopePulumiDir returns the Pulumi work directory of one stack of the scope
// test.
func scopePulumiDir(env *e2e.Env, stack string) string {
	return filepath.Join(env.Home(), "pulumi-scope-"+stack)
}

// scopeTofuDir returns the OpenTofu work directory of one workspace of the
// scope test.
func scopeTofuDir(env *e2e.Env, workspace string) string {
	return filepath.Join(env.Home(), "tofu-scope-"+workspace)
}

// scopeWritePulumi writes the Pulumi project of one stack of the scope test.
func scopeWritePulumi(t *testing.T, env *e2e.Env, stack string) {
	t.Helper()

	dir := scopePulumiDir(env, stack)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	env.WriteFile(t, filepath.Join(dir, "Pulumi.yaml"), scopeProgram)
}

// scopeWriteTofu writes the OpenTofu configuration of one workspace of the
// scope test.
func scopeWriteTofu(t *testing.T, env *e2e.Env, workspace string) {
	t.Helper()

	dir := scopeTofuDir(env, workspace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}

	config := fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`, env.Host(), scopeNamespace, workspace)

	env.WriteFile(t, filepath.Join(dir, "main.tf"), config)
}

// scopeSeedUnits creates the stacks and workspaces of both collections with
// the administrator token, so the persons below act on units that exist.
func scopeSeedUnits(ctx context.Context, t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install) {
	t.Helper()

	for _, stack := range []string{"a", "b"} {
		scopeWritePulumi(t, env, stack)

		dir := scopePulumiDir(env, stack)
		qualified := scopeNamespace + "/scope/" + stack

		res, err := env.RunAs(ctx, pulumiCLI, dir, nil, "stack", "init", qualified)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("pulumi stack init %s exits %d, %v:\n%s", qualified, res.ExitCode, err, res.Stderr)
		}

		res, err = env.RunAs(ctx, pulumiCLI, dir, nil, "up", "--yes", "--skip-preview")
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("pulumi up %s exits %d, %v:\n%s", qualified, res.ExitCode, err, res.Stderr)
		}
	}

	for _, workspace := range []string{"a", "b"} {
		scopeWriteTofu(t, env, workspace)

		dir := scopeTofuDir(env, workspace)

		res, err := env.RunAs(ctx, tofuCLI, dir, nil, "init", "-input=false")
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("tofu init %s exits %d, %v:\n%s", workspace, res.ExitCode, err, res.Stderr)
		}

		res, err = env.RunAs(ctx, tofuCLI, dir, nil, "apply", "-input=false", "-auto-approve")
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("tofu apply %s exits %d, %v:\n%s", workspace, res.ExitCode, err, res.Stderr)
		}
	}
}

// run runs one CLI as the person in the directory of the unit. Pulumi takes
// the token of the person in its environment, and OpenTofu the CLI file that
// holds it.
func (r *scopeRunner) run(ctx context.Context, tool, unit string, args ...string) e2e.Result {
	r.t.Helper()

	var dir string

	var extra []string

	switch tool {
	case "pulumi":
		dir = scopePulumiDir(r.env, unit)
		extra = []string{"PULUMI_ACCESS_TOKEN=" + r.person.token.Secret}
	default:
		dir = scopeTofuDir(r.env, unit)
		rc := filepath.Join(r.env.Home(), "tofurc-scope-"+r.person.name)

		if _, err := os.Stat(rc); err != nil {
			r.env.WriteFile(r.t, rc, fmt.Sprintf(
				"credentials %q {\n  token = %q\n}\n", r.env.Host(), r.person.token.Secret,
			))
		}

		extra = []string{"TF_CLI_CONFIG_FILE=" + rc}
	}

	res, err := r.env.RunAs(ctx, r.cli, dir, extra, args...)
	if err != nil {
		r.t.Fatalf("%s %s as %s: %v", r.cli.Tool, strings.Join(args, " "), r.person.name, err)
	}

	return res
}

// scopeRunnerFor returns the runner of one person for one CLI.
func scopeRunnerFor(t *testing.T, env *e2e.Env, cli e2e.Install, person *scopePerson) *scopeRunner {
	t.Helper()

	return &scopeRunner{t: t, env: env, cli: cli, person: person}
}

// scopeRefused fails the test unless res is a refused run: a non-zero exit
// with a 403 in the combined CLI output. A network or CLI error without the
// 403 does not pass.
func scopeRefused(t *testing.T, res e2e.Result, what string) {
	t.Helper()

	out := res.Stdout + "\n" + res.Stderr

	if res.ExitCode == 0 || !strings.Contains(out, "403") {
		t.Fatalf("%s exits %d without a 403, want a refusal:\n%s", what, res.ExitCode, out)
	}
}

// scopeHidden fails the test unless res is a hidden refusal: a non-zero
// exit with a 404 in the combined CLI output. A caller with no grant left in
// the namespace gets the same 404 as for an unknown workspace, as across
// namespaces, so the run hides whether the unit exists.
func scopeHidden(t *testing.T, res e2e.Result, what string) {
	t.Helper()

	out := res.Stdout + "\n" + res.Stderr

	if res.ExitCode == 0 || !strings.Contains(out, "404") {
		t.Fatalf("%s exits %d without a 404, want a hidden refusal:\n%s", what, res.ExitCode, out)
	}
}

// scopeAdminPulumi exports one stack with the administrator token.
func scopeAdminPulumi(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, stack string) string {
	t.Helper()

	res, err := env.RunAs(ctx, cli, scopePulumiDir(env, stack), nil, "stack", "export")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("pulumi stack export of %s as administrator exits %d, %v:\n%s",
			stack, res.ExitCode, err, res.Stderr)
	}

	return res.Stdout
}

// scopeAdminTofu pulls one workspace with the administrator token.
func scopeAdminTofu(ctx context.Context, t *testing.T, env *e2e.Env, cli e2e.Install, workspace string) string {
	t.Helper()

	res, err := env.RunAs(ctx, cli, scopeTofuDir(env, workspace), nil, "state", "pull")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("tofu state pull of %s as administrator exits %d, %v:\n%s",
			workspace, res.ExitCode, err, res.Stderr)
	}

	return res.Stdout
}

// scopeWriterHiddenNoOracle proves the no-oracle rule for the writer on
// collection team-a: `tofu init` of workspace b and of a workspace name that
// does not exist fail the same way, and the workspace read answers both
// names with the same 404 body after normalization.
func scopeWriterHiddenNoOracle(
	ctx context.Context,
	t *testing.T,
	env *e2e.Env,
	tofu *scopeRunner,
	writer *scopePerson,
) {
	t.Helper()

	const missing = "no-such-workspace"

	rc := filepath.Join(env.Home(), "tofurc-scope-"+writer.name)
	extra := []string{"TF_CLI_CONFIG_FILE=" + rc}

	runInit := func(name, dir string) e2e.Result {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}

		config := fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "greeting" {
  value = "hello"
}
`, env.Host(), scopeNamespace, name)

		env.WriteFile(t, filepath.Join(dir, "main.tf"), config)

		res, err := env.RunAs(ctx, tofu.cli, dir, extra, "init", "-input=false")
		if err != nil {
			t.Fatalf("tofu init of %s as writer: %v", name, err)
		}

		return res
	}

	dirB := filepath.Join(env.Home(), "tofu-scope-nooracle-b")
	dirMissing := filepath.Join(env.Home(), "tofu-scope-nooracle-missing")

	gotB := runInit("b", dirB)
	gotMissing := runInit(missing, dirMissing)

	if gotB.ExitCode == 0 || gotMissing.ExitCode == 0 {
		t.Fatalf(
			"tofu init exits b=%d missing=%d, want both non-zero",
			gotB.ExitCode,
			gotMissing.ExitCode,
		)
	}

	// A failed init leaves no backend, so `tofu state pull` cannot run
	// after it; the init runs are the comparison.
	normalize := func(out, dir, name string) string {
		out = strings.ReplaceAll(out, dir, "DIR")
		out = strings.ReplaceAll(out, "workspace "+name, "workspace WORKSPACE")
		out = strings.ReplaceAll(out, `"`+name+`"`, `"WORKSPACE"`)
		out = strings.ReplaceAll(out, "/"+name, "/WORKSPACE")
		return strings.ReplaceAll(out, name, "WORKSPACE")
	}

	outB := normalize(gotB.Stdout+"\n"+gotB.Stderr, dirB, "b")
	outMissing := normalize(gotMissing.Stdout+"\n"+gotMissing.Stderr, dirMissing, missing)

	if outB != outMissing {
		t.Fatalf(
			"tofu init of team-b as writer of team-a differs from a missing workspace:\n--- b ---\n%s\n--- missing ---\n%s",
			outB,
			outMissing,
		)
	}

	org := "/api/v2/organizations/" + scopeNamespace + "/workspaces/"

	statusB, bodyB, err := env.TFEStatus(ctx, writer.token.Secret, http.MethodGet, org+"b", "")
	if err != nil {
		t.Fatalf("GET workspace b as writer: %v", err)
	}

	statusMissing, bodyMissing, err := env.TFEStatus(
		ctx,
		writer.token.Secret,
		http.MethodGet,
		org+missing,
		"",
	)
	if err != nil {
		t.Fatalf("GET missing workspace as writer: %v", err)
	}

	if statusB != http.StatusNotFound || statusMissing != http.StatusNotFound {
		t.Fatalf(
			"GET workspace as writer answers b=%d missing=%d, want 404 for both",
			statusB,
			statusMissing,
		)
	}

	normalizeBody := func(body, name string) string {
		return strings.ReplaceAll(body, name, "WORKSPACE")
	}

	normB := normalizeBody(bodyB, "b")
	normMissing := normalizeBody(bodyMissing, missing)

	if normB != normMissing {
		t.Fatalf(
			"GET workspace b as writer differs from a missing workspace:\n--- b (%d) ---\n%s\n--- missing (%d) ---\n%s",
			statusB,
			bodyB,
			statusMissing,
			bodyMissing,
		)
	}
}

// checkWriterScope proves that the writer on collection team-a writes and
// reads the units of team-a and cannot see the units of team-b with the
// OpenTofu CLI: without namespace:read and without a grant on team-b, the
// server hides them with the same 404 as for an unknown workspace. The
// Pulumi CLI names its own refusal with a 403. Each refused write leaves the
// state unchanged.
func checkWriterScope(t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install, writer *scopePerson) {
	t.Helper()

	ctx := t.Context()

	pulumi := scopeRunnerFor(t, env, pulumiCLI, writer)
	tofu := scopeRunnerFor(t, env, tofuCLI, writer)

	if res := pulumi.run(ctx, "pulumi", "a", "stack", "export"); res.ExitCode != 0 {
		t.Fatalf("pulumi stack export of team-a as writer exits %d, want 0:\n%s", res.ExitCode, res.Stderr)
	}

	scopeRefused(t, pulumi.run(ctx, "pulumi", "b", "stack", "export"),
		"pulumi stack export of team-b as writer of team-a")

	before := scopeAdminPulumi(ctx, t, env, pulumiCLI, "b")

	scopeRefused(t, pulumi.run(ctx, "pulumi", "b", "up", "--yes", "--skip-preview"),
		"pulumi up on team-b as writer of team-a")

	if after := scopeAdminPulumi(ctx, t, env, pulumiCLI, "b"); after != before {
		t.Fatal("pulumi up on team-b as writer of team-a changed the stack")
	}

	if res := tofu.run(ctx, "opentofu", "a", "state", "pull"); res.ExitCode != 0 {
		t.Fatalf("tofu state pull of team-a as writer exits %d, want 0:\n%s", res.ExitCode, res.Stderr)
	}

	scopeWriterHiddenNoOracle(ctx, t, env, tofu, writer)

	beforeTofu := scopeAdminTofu(ctx, t, env, tofuCLI, "b")

	scopeHidden(t, tofu.run(ctx, "opentofu", "b", "apply", "-input=false", "-auto-approve"),
		"tofu apply on team-b as writer of team-a")

	if after := scopeAdminTofu(ctx, t, env, tofuCLI, "b"); after != beforeTofu {
		t.Fatal("tofu apply on team-b as writer of team-a changed the workspace")
	}
}

// checkViewerScope proves that the viewer on collection team-a reads the
// units of team-a with each real CLI and is refused each write with a 403.
// Each refused write leaves the state unchanged.
func checkViewerScope(t *testing.T, env *e2e.Env, pulumiCLI, tofuCLI e2e.Install, viewer *scopePerson) {
	t.Helper()

	ctx := t.Context()

	pulumi := scopeRunnerFor(t, env, pulumiCLI, viewer)
	tofu := scopeRunnerFor(t, env, tofuCLI, viewer)

	if res := pulumi.run(ctx, "pulumi", "a", "stack", "export"); res.ExitCode != 0 {
		t.Fatalf("pulumi stack export of team-a as viewer exits %d, want 0:\n%s", res.ExitCode, res.Stderr)
	}

	before := scopeAdminPulumi(ctx, t, env, pulumiCLI, "a")

	scopeRefused(t, pulumi.run(ctx, "pulumi", "a", "up", "--yes", "--skip-preview"),
		"pulumi up on team-a as viewer")

	if after := scopeAdminPulumi(ctx, t, env, pulumiCLI, "a"); after != before {
		t.Fatal("pulumi up on team-a as viewer changed the stack")
	}

	if res := tofu.run(ctx, "opentofu", "a", "state", "pull"); res.ExitCode != 0 {
		t.Fatalf("tofu state pull of team-a as viewer exits %d, want 0:\n%s", res.ExitCode, res.Stderr)
	}

	beforeTofu := scopeAdminTofu(ctx, t, env, tofuCLI, "a")

	scopeRefused(t, tofu.run(ctx, "opentofu", "a", "apply", "-input=false", "-auto-approve"),
		"tofu apply on team-a as viewer")

	if after := scopeAdminTofu(ctx, t, env, tofuCLI, "a"); after != beforeTofu {
		t.Fatal("tofu apply on team-a as viewer changed the workspace")
	}
}

// checkScopeRevocation proves that the writer token is refused with a 403 on
// the next request of each tool after its grant ends: it revokes the grant
// through DELETE /api/server/grants/{id} and retries the read with the same
// token.
func checkScopeRevocation(
	ctx context.Context, t *testing.T, pulumi, tofu *scopeRunner, grantID string,
) {
	t.Helper()

	principalCall(ctx, t, pulumi.env, principalStep{
		http.MethodDelete, "/api/server/grants/" + grantID, "", http.StatusNoContent,
	}, nil)

	scopeRefused(t, pulumi.run(ctx, "pulumi", "a", "stack", "export"),
		"pulumi stack export after the revoke")

	// The revoked writer keeps no grant in the namespace, so OpenTofu hides
	// the workspace with the same 404 as for an unknown one; the run still
	// fails on the next request.
	scopeHidden(t, tofu.run(ctx, "opentofu", "a", "state", "pull"),
		"tofu state pull after the revoke")
}

// checkScopeReview proves that the access-review export of collection team-a
// names each grant with its holder, role, object, creator and time, and that
// every row belongs to the collection: the collection itself, one of its
// units, the namespace or the installation.
func checkScopeReview(
	ctx context.Context, t *testing.T, env *e2e.Env, writerGrant, viewerGrant e2eGrant,
) {
	t.Helper()

	var doc e2eReview

	principalCall(ctx, t, env, principalStep{
		http.MethodGet,
		"/api/server/namespaces/" + scopeNamespace + "/collections/team-a/access-review",
		"",
		http.StatusOK,
	}, &doc)

	if len(doc.Grants) == 0 {
		t.Fatal("the access-review export of team-a holds no grant")
	}

	allowed := map[string]bool{
		scopeNamespace + "/team-a":              true,
		"pulumi:" + scopeNamespace + "/scope/a": true,
		"opentofu:" + scopeNamespace + "/a":     true,
		scopeNamespace:                          true,
		"@server":                               true,
	}

	seen := map[string]bool{}

	for i := range doc.Grants {
		row := &doc.Grants[i]

		if row.Holder == "" || row.Role == "" || row.Object == "" {
			t.Fatalf("the review row %+v misses the holder, role or object", row)
		}

		if row.GrantedBy == "" || row.GrantedAtMs == nil {
			t.Fatalf("the review row %+v misses the creator or time", row)
		}

		if !allowed[row.Object] {
			t.Fatalf("the review row %+v names an object outside collection team-a", row)
		}

		seen[row.GrantID] = true
	}

	for _, id := range []string{writerGrant.ID, viewerGrant.ID} {
		if !seen[id] {
			t.Fatalf("the access-review export of team-a misses the grant %s", id)
		}
	}
}
