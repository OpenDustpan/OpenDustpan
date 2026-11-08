//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// Names and values of the output sharing scenarios.
const (
	// shareProducer is the unit whose outputs another unit reads.
	shareProducer = "producer"
	// shareConsumer is the unit that reads the outputs of shareProducer.
	shareConsumer = "consumer"
	// shareVPC is the plain output of the producer.
	shareVPC = "vpc-0abc"
	// shareSecret is the secret output of the producer. It is a test value
	// only. A step that prints it is redacted.
	shareSecret = "s3cr3t-share"
	// terraformPackageVersion pins the Pulumi terraform package of the step
	// across tools. A pinned version needs no lookup of the latest release
	// through the GitHub API, which limits the rate of calls.
	terraformPackageVersion = "6.0.2"
	// tfeProviderVersion pins the tfe provider of the tfe_outputs steps.
	tfeProviderVersion = "0.82.0"
	// sharingRulesSkip is the reason of each step that needs the sharing
	// rules. Remove it in the slice that serves the routes of the step.
	sharingRulesSkip = "the server has no outputs route and no sharing rules yet; " +
		"these steps fail until the output sharing slices land"
	// shareFileMode is the mode of the files that the scenarios write.
	shareFileMode = 0o600
	// sharingAPI is the prefix of the tool-neutral routes of output sharing.
	sharingAPI = "/api/dustpan/v1/namespaces/"
)

// TestOutputReadPaths records which routes each CLI calls today when one unit
// reads the outputs of another unit, and fails when a CLI version calls
// another route. The read check of the sharing rules applies to these routes,
// so a new route needs a decision before it is served:
//
//   - A Pulumi stack reference reads the stack and its export, which holds the
//     whole deployment. It decrypts a secret output with the decrypt route of
//     the producer (3.200.0) or with its batch-decrypt route (3.250.0 and later).
//   - An OpenTofu terraform_remote_state data source reads the workspace by
//     name, its current state version and the download of that version, which
//     holds the whole state.
//   - The Pulumi terraform package reads an OpenTofu workspace through the same
//     three routes as terraform_remote_state.
//
// No CLI calls a route that answers the outputs only. It runs each version of
// each tool on each store under test, and writes no report.
func TestOutputReadPaths(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux.
		t.Skipf("the output read paths need Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	if len(pulumiVersions) == 0 {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	for _, label := range pulumiVersions {
		t.Run("pulumi/"+label, func(t *testing.T) {
			t.Parallel()

			cli := installShareCLI(t, fetcher, e2e.Pulumi(), label)

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					skipWithoutStore(t, db)
					pulumiReadPath(t, e2e.NewEnvWithSignIn(t.Context(), t, cli, db))
				})
			}
		})
	}

	for _, label := range e2e.OpenTofu().Versions() {
		t.Run("opentofu/"+label, func(t *testing.T) {
			t.Parallel()

			cli := installShareCLI(t, fetcher, e2e.OpenTofu(), label)
			// The step across tools runs on the single Pulumi version, which
			// is a pinned version by default.
			pulumiCLI := installShareCLI(t, fetcher, e2e.Pulumi(), e2e.Pulumi().Single())

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					skipWithoutStore(t, db)

					env := e2e.NewEnvWithSignIn(t.Context(), t, cli, db)
					tofuReadPath(t, env)
					crossToolReadPath(t, env, pulumiCLI)
				})
			}
		})
	}
}

// installShareCLI installs one version of tool and fails the test when it
// cannot.
func installShareCLI(t *testing.T, fetcher *e2e.Fetcher, tool e2e.Tool, label string) e2e.Install {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), installLimit)
	defer cancel()

	cli, err := fetcher.Install(ctx, tool, label)
	if err != nil {
		t.Fatalf("install %s %s: %v", tool.Name, label, err)
	}

	t.Logf("%s %s (checksum %s) in %s", cli.Tool, cli.Version, cli.ChecksumSource, cli.Dir)

	return cli
}

// skipWithoutStore skips the PostgreSQL runs when no database is set.
func skipWithoutStore(t *testing.T, db e2e.Store) {
	t.Helper()

	if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}
}

// shareStack returns the qualified name of the stack name in the namespace of
// the scenario.
func shareStack(name string) string {
	return e2e.Namespace + "/" + e2e.Project + "/" + name
}

// shareProducerProgram is the Pulumi program of the producer: one plain output
// and one secret output from a config secret.
func shareProducerProgram() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"config:\n" +
		"  dbPassword:\n" +
		"    type: string\n" +
		"    secret: true\n" +
		"outputs:\n" +
		"  vpcId: " + shareVPC + "\n" +
		"  dbPassword:\n" +
		"    fn::secret: ${dbPassword}\n"
}

// shareConsumerProgram is the Pulumi program of the consumer: a stack
// reference to the producer, and an output for each output that it reads.
func shareConsumerProgram() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"resources:\n" +
		"  net:\n" +
		"    type: pulumi:pulumi:StackReference\n" +
		"    properties:\n" +
		"      name: " + shareStack(shareProducer) + "\n" +
		"outputs:\n" +
		"  vpc: ${net.outputs[\"vpcId\"]}\n" +
		"  password: ${net.outputs[\"dbPassword\"]}\n"
}

// pulumiShareSetup makes the producer with its outputs and the consumer stack.
// The project file holds the consumer program at the end.
func pulumiShareSetup(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	producer := shareStack(shareProducer)

	env.WritePulumiProject(t, shareProducerProgram())
	env.Step(ctx, t, step("share init producer", nil, "stack", "init", producer, "--no-select"))
	env.Step(ctx, t, redacted(step(
		"share config producer", nil, "config", "set", "--secret", "--stack", producer, "dbPassword", shareSecret,
	)))
	env.Step(ctx, t, step("share up producer", nil, "up", "--yes", "--non-interactive", "--stack", producer))
	env.Step(ctx, t, step("share init consumer", nil, "stack", "init", shareStack(shareConsumer), "--no-select"))
	env.WritePulumiProject(t, shareConsumerProgram())
}

// pulumiReadPath reads the outputs of the producer from the consumer with a
// stack reference, in an update and in a preview, and checks the routes.
func pulumiReadPath(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	consumer := shareStack(shareConsumer)

	pulumiShareSetup(ctx, t, env)

	for _, s := range []e2e.StepSpec{
		step("share up consumer", checkPulumiReadPath(env, "share up consumer"),
			"up", "--yes", "--non-interactive", "--stack", consumer),
		step("share preview consumer", checkPulumiReadPath(env, "share preview consumer"),
			"preview", "--non-interactive", "--stack", consumer),
		redacted(step("share output consumer", containsBoth(shareVPC, shareSecret),
			"stack", "output", "--json", "--show-secrets", "--stack", consumer)),
	} {
		env.Step(ctx, t, s)
	}
}

// checkPulumiReadPath proves that the step read the producer through the
// stack read, the export and one decrypt route, and called no other route of
// the producer.
func checkPulumiReadPath(env *e2e.Env, stepName string) func(e2e.Result) error {
	return func(e2e.Result) error {
		seen := map[string]bool{}
		entries := stepEntries(env, stepName)

		for i := range entries {
			kind, err := pulumiProducerRoute(&entries[i])
			if err != nil {
				return err
			}

			seen[kind] = true
		}

		return wantSeen(seen, "export", "decrypt")
	}
}

// pulumiProducerRoute names the route of e on the producer: "stack",
// "export" or "decrypt". It answers "" for a request to another stack, and an
// error for any other route of the producer or for a status that is not 200.
func pulumiProducerRoute(e *e2e.Entry) (string, error) {
	prefix := "/api/stacks/" + shareStack(shareProducer)
	if e.Path != prefix && !strings.HasPrefix(e.Path, prefix+"/") {
		return "", nil
	}

	route := e.Method + " " + strings.TrimPrefix(e.Path, prefix)
	if e.Status != http.StatusOK {
		return "", fmt.Errorf("%w: %q on the producer answered %d", errUnexpected, route, e.Status)
	}

	switch route {
	case "GET ":
		return "stack", nil
	case "GET /export":
		return "export", nil
	case "POST /decrypt", "POST /batch-decrypt":
		return "decrypt", nil
	default:
		return "", fmt.Errorf("%w: the consumer called %q on the producer", errUnexpected, route)
	}
}

// stepEntries returns the recorded requests of the step stepName.
func stepEntries(env *e2e.Env, stepName string) []e2e.Entry {
	all := env.Recorder.Entries()
	entries := make([]e2e.Entry, 0, len(all))

	for i := range all {
		if all[i].Step == stepName {
			entries = append(entries, all[i])
		}
	}

	return entries
}

// wantSeen returns an error that names the first kind in wants that seen
// does not hold.
func wantSeen(seen map[string]bool, wants ...string) error {
	for _, want := range wants {
		if !seen[want] {
			return fmt.Errorf("%w: the step made no %s read of the producer", errUnexpected, want)
		}
	}

	return nil
}

// tofuProducerConfig is the configuration of the producer workspace: one
// plain output and one sensitive output.
func tofuProducerConfig(host string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

output "vpc_id" {
  value = %q
}

output "db_password" {
  value     = %q
  sensitive = true
}
`, host, e2e.Namespace, e2e.Workspace, shareVPC, shareSecret)
}

// tofuConsumerConfig is the configuration of a consumer workspace in the
// namespace ns that reads the producer with terraform_remote_state.
func tofuConsumerConfig(host, ns string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
}

data "terraform_remote_state" "net" {
  backend = "remote"
  config = {
    hostname     = %q
    organization = %q
    workspaces = {
      name = %q
    }
  }
}

output "vpc" {
  value = data.terraform_remote_state.net.outputs.vpc_id
}

output "password" {
  value     = data.terraform_remote_state.net.outputs.db_password
  sensitive = true
}
`, host, ns, shareConsumer, host, e2e.Namespace, e2e.Workspace)
}

// tofuOutputsConfig is a configuration with no backend that reads the
// producer with the tfe_outputs data source of the tfe provider. The provider
// reads its token from TFE_TOKEN.
func tofuOutputsConfig(host string) string {
	return fmt.Sprintf(`terraform {
  required_providers {
    tfe = {
      source  = "hashicorp/tfe"
      version = %q
    }
  }
}

provider "tfe" {
  hostname = %q
}

data "tfe_outputs" "net" {
  organization = %q
  workspace    = %q
}

output "vpc" {
  value = data.tfe_outputs.net.nonsensitive_values.vpc_id
}
`, tfeProviderVersion, host, e2e.Namespace, e2e.Workspace)
}

// writeShareConfig writes content as main.tf in a new directory and returns
// the directory.
func writeShareConfig(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), shareFileMode); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	return dir
}

// tofuShareSetup applies the producer and returns the directory of a
// consumer in the namespace of the scenario.
func tofuShareSetup(ctx context.Context, t *testing.T, env *e2e.Env) string {
	t.Helper()

	env.Step(ctx, t, step("share init producer", nil, "init", "-input=false", "-no-color"))
	env.WriteTofuConfig(t, tofuProducerConfig(env.Host()))
	env.Step(ctx, t, step("share apply producer", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	return writeShareConfig(t, tofuConsumerConfig(env.Host(), e2e.Namespace))
}

// tofuReadPath reads the outputs of the producer from the consumer with
// terraform_remote_state and checks the routes.
func tofuReadPath(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	dir := tofuShareSetup(ctx, t, env)
	producerID := env.WorkspaceIDByName(ctx, t, e2e.Workspace)

	for _, s := range []e2e.StepSpec{
		step("share init consumer", nil, "init", "-input=false", "-no-color"),
		step("share apply consumer", checkTofuReadPath(env, "share apply consumer", producerID),
			"apply", "-input=false", "-auto-approve", "-no-color"),
		redacted(step("share output consumer", containsBoth(shareVPC, shareSecret),
			"output", "-json", "-no-color")),
	} {
		env.StepIn(ctx, t, dir, s)
	}
}

// checkTofuReadPath proves that the step read the producer workspace by name,
// read its current state version and downloaded a state version, and called
// no other route of the producer and no route that answers outputs only.
func checkTofuReadPath(env *e2e.Env, stepName, producerID string) func(e2e.Result) error {
	return func(e2e.Result) error {
		seen := map[string]bool{}
		entries := stepEntries(env, stepName)

		for i := range entries {
			kind, err := tofuProducerRoute(&entries[i], producerID)
			if err != nil {
				return err
			}

			seen[kind] = true
		}

		return wantSeen(seen, "workspace", "current", "download")
	}
}

// tofuProducerRoute names the read of e on the producer workspace:
// "workspace", "current" or "download". It answers "" for a request that is
// not a read of the producer, and an error for a route that answers outputs
// only or for another route of the producer.
func tofuProducerRoute(e *e2e.Entry, producerID string) (string, error) {
	byName := "/api/v2/organizations/" + e2e.Namespace + "/workspaces/" + e2e.Workspace
	prefix := "/api/v2/workspaces/" + producerID + "/"

	switch {
	case strings.Contains(e.Path, "state-version-outputs"):
		return "", fmt.Errorf("%w: the step read the outputs route %s", errUnexpected, e.Path)
	case e.Path == byName && e.Method == http.MethodGet:
		return "workspace", nil
	case strings.HasPrefix(e.Path, prefix):
		if e.Method != http.MethodGet || e.Path != prefix+"current-state-version" {
			return "", fmt.Errorf("%w: the step called %s %s on the producer", errUnexpected, e.Method, e.Path)
		}

		return "current", nil
	case strings.HasPrefix(e.Path, "/api/v2/state-versions/") && strings.HasSuffix(e.Path, "/download"):
		return "download", nil
	default:
		return "", nil
	}
}

// crossToolProgram is a Pulumi program that reads the outputs of the producer
// workspace with the remote state reference of the terraform package. The
// token is a test token of this run, and the step that runs it is redacted.
func crossToolProgram(host, token string) string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"variables:\n" +
		"  net:\n" +
		"    fn::invoke:\n" +
		"      function: terraform:state:getRemoteReference\n" +
		"      arguments:\n" +
		"        hostname: " + strconv.Quote(host) + "\n" +
		"        organization: " + e2e.Namespace + "\n" +
		"        token: " + strconv.Quote(token) + "\n" +
		"        workspaces:\n" +
		"          name: " + e2e.Workspace + "\n" +
		"      options:\n" +
		"        version: " + terraformPackageVersion + "\n" +
		"outputs:\n" +
		"  vpc: ${net.outputs[\"vpc_id\"]}\n"
}

// crossToolReadPath reads the outputs of the producer workspace from a Pulumi
// stack and checks that the terraform package used the routes of
// terraform_remote_state. It runs after tofuReadPath on the same server.
func crossToolReadPath(t *testing.T, env *e2e.Env, pulumiCLI e2e.Install) {
	t.Helper()

	ctx := t.Context()
	producerID := env.WorkspaceIDByName(ctx, t, e2e.Workspace)
	reader := shareStack("cross-tool-reader")

	env.SetCLI(pulumiCLI)
	env.WritePulumiProject(t, crossToolProgram(env.Host(), env.TokenA()))
	env.Step(ctx, t, step("share init cross-tool reader", nil, "stack", "init", reader, "--no-select"))
	env.Step(ctx, t, redacted(step("share up cross-tool reader",
		checkTofuReadPath(env, "share up cross-tool reader", producerID),
		"up", "--yes", "--non-interactive", "--stack", reader)))
	env.Step(ctx, t, step("share output cross-tool reader", contains(shareVPC),
		"stack", "output", "--json", "--stack", reader))
}

// redacted returns s with its output hidden from the log and the report.
func redacted(s e2e.StepSpec) e2e.StepSpec {
	s.Redact = true

	return s
}

// withEnv returns s with the extra NAME=value pairs for its run.
func withEnv(s e2e.StepSpec, extra ...string) e2e.StepSpec {
	s.Env = append(s.Env, extra...)

	return s
}

// containsBoth checks that stdout holds both a and b.
func containsBoth(a, b string) func(e2e.Result) error {
	return func(r e2e.Result) error {
		for _, want := range []string{a, b} {
			if !strings.Contains(r.Stdout, want) {
				return fmt.Errorf("%w: stdout lacks an expected output value", errUnexpected)
			}
		}

		return nil
	}
}

// TestOutputSharingRules drives the sharing rules, the action
// unit:read-outputs and the job token through the real CLIs. Each part names
// the behavior that it proves. It is skipped until the slices that serve the
// routes land. Each slice removes the skip of its own part.
func TestOutputSharingRules(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skipf("the sharing rules need Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}
	pulumiVersion := e2e.Pulumi().Single()
	tofuVersion := e2e.OpenTofu().Single()

	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	t.Run("setting", func(t *testing.T) {
		t.Parallel()

		databases, err := e2e.Databases()
		if err != nil {
			t.Fatalf("stores: %v", err)
		}

		for _, db := range databases {
			t.Run(string(db), func(t *testing.T) {
				t.Parallel()
				skipWithoutStore(t, db)

				// The grant of the viewer needs a fresh session of the admin.
				env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), db)
				checkOutputSharingSetting(t.Context(), t, env)
			})
		}
	})

	t.Run("pulumi", func(t *testing.T) {
		t.Parallel()

		cli := installShareCLI(t, fetcher, e2e.Pulumi(), pulumiVersion)

		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			eachShareStore(t, func(t *testing.T, db e2e.Store) {
				t.Helper()

				pulumiOutputsReader(t, e2e.NewEnvWithSignIn(t.Context(), t, cli, db))
			})
		})

		t.Run("jobs", func(t *testing.T) {
			t.Parallel()
			t.Skip(sharingRulesSkip)

			env := e2e.NewEnv(t.Context(), t, cli, e2e.StoreSQLite)
			pulumiSharingRules(t, env)
		})
	})

	t.Run("opentofu", func(t *testing.T) {
		t.Parallel()

		cli := installShareCLI(t, fetcher, e2e.OpenTofu(), tofuVersion)

		t.Run("reader", func(t *testing.T) {
			t.Parallel()
			eachShareStore(t, func(t *testing.T, db e2e.Store) {
				t.Helper()

				tofuOutputsReader(t, e2e.NewEnvWithSignIn(t.Context(), t, cli, db))
			})
		})

		t.Run("jobs", func(t *testing.T) {
			t.Parallel()
			t.Skip(sharingRulesSkip)

			env := e2e.NewEnv(t.Context(), t, cli, e2e.StoreSQLite)
			tofuSharingRules(t, env)
		})
	})
}

// eachShareStore runs fn once for each store under test, as a parallel subtest
// that skips a store the run has no database for.
func eachShareStore(t *testing.T, fn func(t *testing.T, db e2e.Store)) {
	t.Helper()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, db)
			fn(t, db)
		})
	}
}

// shareCall is one request to the API of the server in a sharing step.
type shareCall struct {
	name   string
	token  string
	method string
	path   string
	body   string
	want   int
}

// shareAPI sends c and fails the test when the status is not c.want. It
// returns the body.
func shareAPI(ctx context.Context, t *testing.T, env *e2e.Env, c shareCall) string {
	t.Helper()

	token := c.token
	if token == "" {
		token = env.TokenA()
	}

	call := env.ServerAPIStatusAs
	if strings.HasPrefix(c.path, "/api/stacks/") {
		// The Pulumi Cloud-compatible API reads the scheme "token".
		call = env.APIStatusAs
	}

	status, body, err := call(ctx, token, c.method, c.path, c.body)
	if err != nil {
		t.Fatalf("%s: %v", c.name, err)
	}

	if status != c.want {
		t.Fatalf("%s: %s %s answers %d, want %d: %s", c.name, c.method, c.path, status, c.want, body)
	}

	return body
}

// readerGrants names the units of a principal that outputReader makes.
type readerGrants struct {
	// ns is the namespace of both units.
	ns string
	// producer is the unit on which the principal holds unit:read-outputs.
	producer string
	// writeUnit is a unit on which the principal holds the writer role, or
	// "" for none.
	writeUnit string
	// name is the subject of the principal and the name of its role and its
	// token. Each principal of one run has a name of its own.
	name string
}

// unitGrantQuery returns the query of the unit-grants routes for the unit
// reference ref, for example pulumi:acme/web/prod or opentofu:acme/prod: the
// tool and the name of the unit without its namespace.
func unitGrantQuery(ref string) string {
	tool, rest, _ := strings.Cut(ref, ":")
	_, unit, _ := strings.Cut(rest, "/")

	return "tool=" + url.QueryEscape(tool) + "&unit=" + url.QueryEscape(unit)
}

// outputReader makes a service principal with a custom role that holds only
// unit:read-outputs on the unit g.producer, and answers a token of it. With
// g.writeUnit set, the principal also holds the writer role on that unit, so
// that it can run the consumer.
func outputReader(ctx context.Context, t *testing.T, env *e2e.Env, g readerGrants) string {
	t.Helper()

	ns, producer, writeUnit, name := g.ns, g.producer, g.writeUnit, strconv.Quote(g.name)

	var principal e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":` + name + `,"kind":"service"}`, http.StatusCreated,
	}, &principal)

	var role struct {
		ID string `json:"id"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":` + name + `,"actions":["unit:read-outputs"]}`,
		http.StatusCreated,
	}, &role)

	subject := "principal:" + principal.ID
	grants := "/api/server/namespaces/" + ns + "/unit-grants?" + unitGrantQuery(producer)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, grants, `{"subject":"` + subject + `","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	if writeUnit != "" {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces/" + ns + "/unit-grants?" + unitGrantQuery(writeUnit),
			`{"subject":"` + subject + `","role":"writer"}`, http.StatusCreated,
		}, nil)
	}

	var created e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + principal.ID + "/tokens", `{"name":` + name + `}`,
		http.StatusCreated,
	}, &created)

	// An empty token would make shareAPI send the token of the admin.
	if created.Secret == "" {
		t.Fatalf("the token of %s has no secret", g.name)
	}

	return created.Secret
}

// jobTokenOf answers the job token of a plan job of unit in the namespace ns.
// A job token comes only from the claim of a runner phase, and the harness
// has no runner yet. The job-token slice adds the claim and removes the skip.
func jobTokenOf(t *testing.T, ns, unit string) string {
	t.Helper()
	t.Skipf("the harness has no runner claim yet, so it has no job token of a job of %s/%s", ns, unit)

	return ""
}

// ruleTarget is the producer and the consumer of a rule that sharingRule
// saves.
type ruleTarget struct {
	// ns is the namespace of the producer.
	ns string
	// producerID is the unit ID of the producer.
	producerID string
	// consumer is the reference of the consumer.
	consumer string
}

// sharingRule saves a sharing rule on the unit r.producerID of the namespace
// r.ns that names r.consumer, and fails the test when the answer has no rule
// ID.
func sharingRule(ctx context.Context, t *testing.T, env *e2e.Env, r ruleTarget) {
	t.Helper()

	ns, producerID, consumer := r.ns, r.producerID, r.consumer

	body := shareAPI(ctx, t, env, shareCall{
		name: "save a sharing rule", token: "", method: http.MethodPost,
		path: sharingAPI + ns + "/units/" + producerID + "/sharing-rules",
		body: `{"consumer":` + strconv.Quote(consumer) + `}`, want: http.StatusCreated,
	})

	var rule struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &rule); err != nil || rule.ID == "" {
		t.Fatalf("sharing rule answer %q has no id: %v", body, err)
	}
}

// checkSharingRuleRefusals proves that a rule never names a principal or a
// whole other namespace, and that a second rule for the same consumer
// conflicts.
func checkSharingRuleRefusals(ctx context.Context, t *testing.T, env *e2e.Env, producerID, consumer string) {
	t.Helper()

	rules := sharingAPI + e2e.Namespace + "/units/" + producerID + "/sharing-rules"

	for _, c := range []shareCall{
		{"a rule names a principal", "", http.MethodPost, rules, `{"consumer":"principal:p-1"}`, http.StatusBadRequest},
		{
			"a rule names a whole other namespace", "", http.MethodPost, rules,
			`{"consumer":"` + e2e.Namespace2 + `"}`, http.StatusBadRequest,
		},
		{
			"a second rule for the same consumer", "", http.MethodPost, rules,
			`{"consumer":` + strconv.Quote(consumer) + `}`, http.StatusConflict,
		},
	} {
		shareAPI(ctx, t, env, c)
	}
}

// pulumiSharingRules proves the rules for the Pulumi CLI:
//
//   - A job token reads the producer only when a sharing rule names the unit
//     of the job and the principal that started the job holds
//     unit:read-outputs. The decrypt route refuses a ciphertext that is not
//     in the outputs of the latest checkpoint.
//   - The stack read and the export that such a job token gets hold no tag
//     and no resource other than the root stack resource. The export of a
//     version and the update history refuse it.
//   - A job token of another namespace with no rule gets the answer of a
//     stack that does not exist on the stack read and on the export.
func pulumiSharingRules(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	pulumiShareSetup(ctx, t, env)
	pulumiProducerWithResource(ctx, t, env)

	producerID, _, err := env.StackID(ctx, e2e.Namespace, e2e.Project, shareProducer)
	if err != nil || producerID == "" {
		t.Fatalf("producer stack ID: %v", err)
	}

	consumer := "pulumi:" + shareStack(shareConsumer)
	// Only this test passes a job token in PULUMI_ACCESS_TOKEN, to drive the
	// CLI with it. The launcher never puts a job token in an environment
	// variable.
	jobToken := jobTokenOf(t, e2e.Namespace, consumer)

	env.Step(ctx, t, withEnv(failStep("a job token with no rule", outputContains("403"),
		"up", "--yes", "--non-interactive", "--stack", shareStack(shareConsumer)), "PULUMI_ACCESS_TOKEN="+jobToken))
	checkSharingRuleRefusals(ctx, t, env, producerID, consumer)
	sharingRule(ctx, t, env, ruleTarget{e2e.Namespace, producerID, consumer})
	env.Step(ctx, t, withEnv(step("a job token with a rule", nil,
		"up", "--yes", "--non-interactive", "--stack", shareStack(shareConsumer)), "PULUMI_ACCESS_TOKEN="+jobToken))

	stackPath := "/api/stacks/" + shareStack(shareProducer)
	configSecret := producerConfigCiphertext(t, env)

	shareAPI(ctx, t, env, shareCall{
		name: "a job token decrypts no config secret", token: jobToken, method: http.MethodPost,
		path: stackPath + "/decrypt", body: `{"ciphertext":` + strconv.Quote(configSecret) + `}`,
		want: http.StatusForbidden,
	})
	checkReducedStack(ctx, t, env, jobToken, stackPath)
	checkReducedDeployment(ctx, t, env, jobToken, stackPath)

	for _, c := range []shareCall{
		{"a job token reads no export of a version", jobToken, http.MethodGet, stackPath + "/export/1", "", http.StatusForbidden},
		{"a job token reads no update history", jobToken, http.MethodGet, stackPath + "/updates", "", http.StatusForbidden},
	} {
		shareAPI(ctx, t, env, c)
	}

	// A job of a stack of another namespace, with no rule that names it,
	// gets the answer of a stack that does not exist.
	env.CreateNamespace(ctx, t, e2e.Namespace2)
	crossJob := jobTokenOf(t, e2e.Namespace2, "pulumi:"+e2e.Namespace2+"/"+e2e.Project+"/"+shareConsumer)
	checkHiddenProducer(ctx, t, env, crossJob, pulumiHiddenPairs())
}

// pulumiShareAnchor is the Pulumi program of the anchor stack: one plain
// output and no config, so that the producer can hold a resource that needs
// no plugin.
func pulumiShareAnchor() string {
	return "name: " + e2e.Project + "\nruntime: yaml\n" +
		"outputs:\n" +
		"  anchor: anchor\n"
}

// pulumiProducerWithResource gives the state of the producer a resource
// other than the root stack resource, a stack reference to an anchor stack,
// so that a check of the reduced deployment has a resource to leave out. The
// project file holds the consumer program at the end.
func pulumiProducerWithResource(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	anchor := shareStack("anchor")

	env.WritePulumiProject(t, pulumiShareAnchor())
	env.Step(ctx, t, step("share init anchor", nil, "stack", "init", anchor, "--no-select"))
	env.Step(ctx, t, step("share up anchor", nil, "up", "--yes", "--non-interactive", "--stack", anchor))
	env.WritePulumiProject(t, shareProducerProgram()+
		"resources:\n"+
		"  anchor:\n"+
		"    type: pulumi:pulumi:StackReference\n"+
		"    properties:\n"+
		"      name: "+anchor+"\n")
	env.Step(ctx, t, step("share up producer with a resource", nil,
		"up", "--yes", "--non-interactive", "--stack", shareStack(shareProducer)))
	env.WritePulumiProject(t, shareConsumerProgram())
}

// checkReducedStack proves that the stack read of a job token of another
// stack holds no tag of the producer.
func checkReducedStack(ctx context.Context, t *testing.T, env *e2e.Env, token, stackPath string) {
	t.Helper()

	body := shareAPI(ctx, t, env, shareCall{
		name: "a job token reads the stack", token: token, method: http.MethodGet, path: stackPath, body: "",
		want: http.StatusOK,
	})

	var answer struct {
		Tags         map[string]string `json:"tags"`
		ActiveUpdate string            `json:"activeUpdate"`
	}

	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("the stack answer to a job token is not JSON: %v", err)
	}

	if len(answer.Tags) != 0 || answer.ActiveUpdate != "" {
		t.Fatalf("the stack answer to a job token has %d tags and active update %q, want none",
			len(answer.Tags), answer.ActiveUpdate)
	}
}

// checkReducedDeployment proves that the export that a job token of another
// stack gets holds the root stack resource with its outputs and the secrets
// providers, and no other resource, no inputs and no pending operation. The
// body holds the ciphertext of a secret output, so no message prints it.
func checkReducedDeployment(ctx context.Context, t *testing.T, env *e2e.Env, token, stackPath string) {
	t.Helper()

	full := exportOf(t, shareAPI(ctx, t, env, shareCall{
		name: "the producer reads its export", token: "", method: http.MethodGet, path: stackPath + "/export", body: "",
		want: http.StatusOK,
	}))
	if n := len(full.Deployment.Resources); n < 2 {
		t.Fatalf("the export of the producer has %d resources, want the root stack resource and another", n)
	}

	reduced := exportOf(t, shareAPI(ctx, t, env, shareCall{
		name: "a job token reads the export", token: token, method: http.MethodGet, path: stackPath + "/export",
		body: "", want: http.StatusOK,
	}))

	if reduced.Version != 3 {
		t.Fatalf("the export to a job token has version %d, want 3", reduced.Version)
	}

	if len(reduced.Deployment.Resources) != 1 {
		t.Fatalf("the export to a job token has %d resources, want the root stack resource only",
			len(reduced.Deployment.Resources))
	}

	root := resourceOf(t, reduced.Deployment.Resources[0])

	if root.Type != "pulumi:pulumi:Stack" {
		t.Fatalf("the export to a job token has a resource of type %q, want the root stack resource", root.Type)
	}

	if len(root.Inputs) != 0 || len(root.Outputs) == 0 {
		t.Fatal("the root stack resource of the export to a job token has inputs or no outputs")
	}

	if len(reduced.Deployment.SecretsProviders) == 0 || len(reduced.Deployment.PendingOperations) != 0 {
		t.Fatal("the export to a job token has no secrets providers, or has pending operations")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(reduced.Deployment.Resources[0], &fields); err != nil {
		t.Fatalf("the root stack resource of the export to a job token is not JSON: %v", err)
	}

	custom, ok := fields["custom"]
	if !ok || string(custom) != "false" {
		t.Fatal("the root stack resource of the export to a job token has no explicit custom false")
	}
}

// shareResource is the part of a resource of an export that the reduced
// deployment checks read.
type shareResource struct {
	Inputs  map[string]any `json:"inputs"`
	Outputs map[string]any `json:"outputs"`
	Type    string         `json:"type"`
}

// resourceOf parses one resource of an export.
func resourceOf(t *testing.T, raw json.RawMessage) shareResource {
	t.Helper()

	var resource shareResource
	if err := json.Unmarshal(raw, &resource); err != nil {
		t.Fatalf("a resource of the export is not JSON: %v", err)
	}

	return resource
}

// shareDeployment is the part of an export that the reduced deployment
// checks read.
type shareDeployment struct {
	SecretsProviders  json.RawMessage   `json:"secrets_providers"` //nolint:tagliatelle // The Pulumi wire name.
	Resources         []json.RawMessage `json:"resources"`
	PendingOperations []json.RawMessage `json:"pending_operations"` //nolint:tagliatelle // The Pulumi wire name.
}

// shareExport is the envelope of an export: the schema version and the
// deployment that it holds.
type shareExport struct {
	Deployment shareDeployment `json:"deployment"`
	Version    int             `json:"version"`
}

// exportOf parses the export body. The body holds ciphertext, so no message
// prints it.
func exportOf(t *testing.T, body string) shareExport {
	t.Helper()

	var export shareExport
	if err := json.Unmarshal([]byte(body), &export); err != nil {
		t.Fatalf("the export is not JSON: %v", err)
	}

	return export
}

// producerConfigCiphertext answers the ciphertext of the config secret of the
// producer from its stack configuration file. The service secrets provider
// encrypts it with the key of the producer, as the secret output.
func producerConfigCiphertext(t *testing.T, env *e2e.Env) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(env.ProjectDir(), "Pulumi."+shareProducer+".yaml"))
	if err != nil {
		t.Fatalf("read the producer config: %v", err)
	}

	for line := range strings.Lines(string(raw)) {
		if _, value, ok := strings.Cut(line, "secure: "); ok {
			return strings.TrimSpace(value)
		}
	}

	t.Fatal("the producer config has no secure value")

	return ""
}

// tofuSharingRules proves the rules for OpenTofu:
//
//   - A person with only unit:read-outputs reads the outputs with tfe_outputs,
//     and terraform_remote_state fails, because the download holds the whole
//     state and needs unit:read.
//   - A job token of a consumer in another namespace reads the producer only
//     when a sharing rule of the producer names that consumer. With no rule,
//     each read answers 404, as for a workspace that does not exist.
//   - The download that such a job token gets holds the outputs and no
//     resource, and a state version that is not current is refused.
//   - A grant of unit:manage-sharing on the installation saves a rule whose
//     consumer is in the namespace, and no rule whose consumer is in another
//     namespace.
//   - A read of outputs only of a producer whose state the client encrypted
//     fails closed: 404 across namespaces and 409 in the namespace.
func tofuSharingRules(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	dir := tofuShareSetup(ctx, t, env)
	reader := outputReader(
		ctx,
		t,
		env,
		readerGrants{e2e.Namespace, "opentofu:" + e2e.Namespace + "/" + e2e.Workspace, "", "output-reader"},
	)

	outputsDir := writeShareConfig(t, tofuOutputsConfig(env.Host()))
	env.StepIn(ctx, t, outputsDir, step("share init outputs", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, outputsDir, withEnv(step("tfe_outputs with read outputs", contains(shareVPC),
		"apply", "-input=false", "-auto-approve", "-no-color"), "TFE_TOKEN="+reader))

	env.StepIn(ctx, t, dir, step("share init consumer", nil, "init", "-input=false", "-no-color"))

	env.CreateNamespace(ctx, t, e2e.Namespace2)

	crossDir := writeShareConfig(t, tofuConsumerConfig(env.Host(), e2e.Namespace2))
	crossConsumer := "opentofu:" + e2e.Namespace2 + "/" + shareConsumer

	env.StepIn(ctx, t, crossDir, step("share init cross-namespace consumer", nil, "init", "-input=false", "-no-color"))

	producerID := env.WorkspaceIDByName(ctx, t, e2e.Workspace)
	oldVersion := currentStateVersion(ctx, t, env, "", producerID)

	env.WriteTofuConfig(t, tofuProducerConfig(env.Host())+"\nresource \"terraform_data\" \"anchor\" {}\n")
	env.Step(ctx, t, step("share apply producer with a resource", nil,
		"apply", "-input=false", "-auto-approve", "-no-color"))

	if oldVersion == currentStateVersion(ctx, t, env, "", producerID) {
		t.Fatal("the apply with a resource made no new state version of the producer")
	}

	// Only this test gives a job token to the CLI in its configuration, to
	// drive the CLI with it. The launcher never puts a job token in an
	// environment variable.
	jobToken := jobTokenOf(t, e2e.Namespace2, crossConsumer)

	env.StepIn(ctx, t, crossDir, withEnv(failStep("a job token across namespaces with no rule",
		outputContains("404"), "plan", "-input=false", "-no-color"), tofuTokenConfig(t, env.Host(), jobToken)))
	checkHiddenProducer(ctx, t, env, jobToken, tofuHiddenPairs(ctx, t, env, producerID))
	checkOperatorSavesNoCrossRule(ctx, t, env, producerID, crossConsumer)
	sharingRule(ctx, t, env, ruleTarget{e2e.Namespace, producerID, crossConsumer})
	env.StepIn(ctx, t, crossDir, withEnv(step("a job token across namespaces with a rule",
		contains(shareVPC), "plan", "-input=false", "-no-color"), tofuTokenConfig(t, env.Host(), jobToken)))
	checkReducedState(ctx, t, env, jobToken, producerID)
	shareAPI(ctx, t, env, shareCall{
		name: "a job token across namespaces downloads no old state version", token: jobToken,
		method: http.MethodGet, path: "/api/v2/state-versions/" + oldVersion + "/download", body: "",
		want: http.StatusNotFound,
	})
	checkEncryptedProducer(ctx, t, env, encryptedTarget{crossConsumer: crossConsumer, crossJob: jobToken})
}

// shareEncrypted is the name of the workspace whose state the client encrypts.
const shareEncrypted = "encrypted-producer"

// tofuEncryptedProducerConfig is the configuration of a producer workspace
// whose state the client encrypts, with the outputs of tofuProducerConfig.
func tofuEncryptedProducerConfig(host string) string {
	return fmt.Sprintf(`terraform {
  cloud {
    hostname     = %q
    organization = %q
    workspaces {
      name = %q
    }
  }
`, host, e2e.Namespace, shareEncrypted) + importEncryptionBlock() + fmt.Sprintf(`}

output "vpc_id" {
  value = %q
}

output "db_password" {
  value     = %q
  sensitive = true
}
`, shareVPC, shareSecret)
}

// encryptedTarget is the consumer of another namespace and its job token that
// checkEncryptedProducer uses.
type encryptedTarget struct {
	crossConsumer string
	crossJob      string
}

// checkEncryptedProducer proves that a read of outputs only of a producer whose
// state the client encrypted fails closed, also with a rule and every grant:
//
//   - A job token of another namespace gets 404 with the body of a workspace
//     that does not exist, on each route that it calls.
//   - A job token of the producer namespace gets 409, with a message that
//     names the client encryption. A person with unit:read-outputs and not
//     unit:read gets the same answer, which tofuOutputsReader proves.
//   - The administrator, who holds unit:read, still reads the outputs.
func checkEncryptedProducer(ctx context.Context, t *testing.T, env *e2e.Env, target encryptedTarget) {
	t.Helper()

	dir := writeShareConfig(t, tofuEncryptedProducerConfig(env.Host()))
	env.StepIn(ctx, t, dir, step("share init client-encrypted producer", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("share apply client-encrypted producer", nil,
		"apply", "-input=false", "-auto-approve", "-no-color"))

	encryptedID := env.WorkspaceIDByName(ctx, t, shareEncrypted)
	sameConsumer := "opentofu:" + e2e.Namespace + "/" + shareConsumer

	sharingRule(ctx, t, env, ruleTarget{e2e.Namespace, encryptedID, target.crossConsumer})
	sharingRule(ctx, t, env, ruleTarget{e2e.Namespace, encryptedID, sameConsumer})

	byName := "/api/v2/organizations/" + e2e.Namespace + "/workspaces/"
	workspaces := "/api/v2/workspaces/"
	unknownID := "ws-nosuchworkspace0"
	version := currentStateVersion(ctx, t, env, "", encryptedID)
	outputs := workspaces + encryptedID + "/current-state-version-outputs"

	checkHiddenProducer(ctx, t, env, target.crossJob, []hiddenPair{
		{byName + shareEncrypted, byName + "no-such-workspace"},
		{workspaces + encryptedID + "/current-state-version", workspaces + unknownID + "/current-state-version"},
		{"/api/v2/state-versions/" + version + "/download", "/api/v2/state-versions/sv-nosuchversion000/download"},
		{outputs, workspaces + unknownID + "/current-state-version-outputs"},
	})

	sameJob := jobTokenOf(t, e2e.Namespace, sameConsumer)

	for _, c := range []shareCall{
		{"a job token of the namespace reads no outputs of a client-encrypted state", sameJob, http.MethodGet, outputs, "", http.StatusConflict},
		{
			"a job token of the namespace downloads no client-encrypted state", sameJob, http.MethodGet,
			"/api/v2/state-versions/" + version + "/download", "", http.StatusConflict,
		},
	} {
		if err := checkEncryptedByClient(shareAPI(ctx, t, env, c)); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}

	shareAPI(ctx, t, env, shareCall{
		name: "the administrator reads the outputs of a client-encrypted state", token: "", method: http.MethodGet,
		path: outputs, body: "", want: http.StatusOK,
	})
}

// currentStateVersion answers the ID of the current state version of the
// workspace workspaceID, read with token, or with the token of the
// administrator when token is "".
func currentStateVersion(ctx context.Context, t *testing.T, env *e2e.Env, token, workspaceID string) string {
	t.Helper()

	body := shareAPI(ctx, t, env, shareCall{
		name: "read the current state version", token: token, method: http.MethodGet,
		path: "/api/v2/workspaces/" + workspaceID + "/current-state-version", body: "", want: http.StatusOK,
	})

	var answer struct {
		Data shareResourceID `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &answer); err != nil || answer.Data.ID == "" {
		t.Fatalf("the current state version answer has no id: %v", err)
	}

	return answer.Data.ID
}

// shareResourceID is the ID of a JSON:API resource.
type shareResourceID struct {
	ID string `json:"id"`
}

// hiddenPair is a route of the producer and the same route of a unit that
// does not exist.
type hiddenPair struct {
	known   string
	unknown string
}

// checkHiddenProducer proves that a job token of another namespace with no
// rule gets the same answer for the producer as for a unit that does not
// exist, on each route of pairs: the same status (404) and the same body.
func checkHiddenProducer(ctx context.Context, t *testing.T, env *e2e.Env, token string, pairs []hiddenPair) {
	t.Helper()

	for _, pair := range pairs {
		known := shareAPI(ctx, t, env, shareCall{
			name: "a job token across namespaces reads the producer with no rule", token: token,
			method: http.MethodGet, path: pair.known, body: "", want: http.StatusNotFound,
		})
		unknown := shareAPI(ctx, t, env, shareCall{
			name: "a job token across namespaces reads a unit that does not exist", token: token,
			method: http.MethodGet, path: pair.unknown, body: "", want: http.StatusNotFound,
		})

		if known != unknown {
			t.Fatalf("%s answers %q, and a unit that does not exist answers %q; want the same body",
				pair.known, known, unknown)
		}
	}
}

// tofuHiddenPairs are the routes of the OpenTofu producer that a job token
// of another namespace calls: the routes of terraform_remote_state, and the
// outputs route.
func tofuHiddenPairs(ctx context.Context, t *testing.T, env *e2e.Env, producerID string) []hiddenPair {
	t.Helper()

	byName := "/api/v2/organizations/" + e2e.Namespace + "/workspaces/"
	workspaces := "/api/v2/workspaces/"
	unknownID := "ws-nosuchworkspace0"
	version := currentStateVersion(ctx, t, env, "", producerID)

	return []hiddenPair{
		{byName + e2e.Workspace, byName + "no-such-workspace"},
		{workspaces + producerID + "/current-state-version", workspaces + unknownID + "/current-state-version"},
		{
			"/api/v2/state-versions/" + version + "/download",
			"/api/v2/state-versions/sv-nosuchversion000/download",
		},
		{
			workspaces + producerID + "/current-state-version-outputs",
			workspaces + unknownID + "/current-state-version-outputs",
		},
	}
}

// pulumiHiddenPairs are the routes of the Pulumi producer that a stack
// reference of another namespace calls.
func pulumiHiddenPairs() []hiddenPair {
	known := "/api/stacks/" + shareStack(shareProducer)
	unknown := "/api/stacks/" + shareStack("no-such-stack")

	return []hiddenPair{
		{known, unknown},
		{known + "/export", unknown + "/export"},
	}
}

// checkReducedState proves that the download that a job token of another
// unit gets holds the outputs and no resource.
func checkReducedState(ctx context.Context, t *testing.T, env *e2e.Env, token, producerID string) {
	t.Helper()

	full := stateOf(t, shareAPI(ctx, t, env, shareCall{
		name: "the producer downloads its state", token: "", method: http.MethodGet,
		path: "/api/v2/state-versions/" + currentStateVersion(ctx, t, env, "", producerID) + "/download", body: "",
		want: http.StatusOK,
	}))
	if len(full.Resources) == 0 {
		t.Fatal("the state of the producer has no resource, so the check of the reduced state proves nothing")
	}

	reduced := stateOf(t, shareAPI(ctx, t, env, shareCall{
		name: "a job token downloads the current state version", token: token, method: http.MethodGet,
		path: "/api/v2/state-versions/" + currentStateVersion(ctx, t, env, token, producerID) + "/download",
		body: "", want: http.StatusOK,
	}))

	if reduced.Resources == nil || len(reduced.Resources) != 0 || len(reduced.Outputs) == 0 {
		t.Fatalf("the state to a job token has %d resources and %d outputs, want resources: [] and the outputs",
			len(reduced.Resources), len(reduced.Outputs))
	}
}

// shareState is the part of an OpenTofu state that the reduced state checks
// read.
type shareState struct {
	Outputs   map[string]json.RawMessage `json:"outputs"`
	Resources []json.RawMessage          `json:"resources"`
}

// stateOf parses an OpenTofu state. The body can hold a sensitive output, so
// no message prints it.
func stateOf(t *testing.T, body string) shareState {
	t.Helper()

	var state shareState
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		t.Fatalf("the state is not JSON: %v", err)
	}

	return state
}

// checkOperatorSavesNoCrossRule proves that a principal with a grant of
// unit:manage-sharing on the installation only saves a rule whose consumer is
// in the producer namespace, and no rule whose consumer is in another
// namespace.
func checkOperatorSavesNoCrossRule(ctx context.Context, t *testing.T, env *e2e.Env, producerID, consumer string) {
	t.Helper()

	var principal e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"sharing-operator","kind":"service"}`,
		http.StatusCreated,
	}, &principal)

	var role struct {
		ID string `json:"id"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"sharing-operator","actions":["unit:manage-sharing"]}`,
		http.StatusCreated,
	}, &role)
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:` + principal.ID + `","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	var created e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + principal.ID + "/tokens", `{"name":"sharing-operator"}`,
		http.StatusCreated,
	}, &created)

	// An empty token would make shareAPI send the token of the admin.
	if created.Secret == "" {
		t.Fatalf("the token of %s has no secret", "sharing-operator")
	}

	// The positive control: the same grant saves a rule whose consumer is in
	// the producer namespace, so the refusal below is for the other
	// namespace only. The test removes the rule, so the next steps see no
	// rule of the operator.
	ruleBody := shareAPI(ctx, t, env, shareCall{
		name: "an operator saves a rule in the namespace", token: created.Secret, method: http.MethodPost,
		path: sharingAPI + e2e.Namespace + "/units/" + producerID + "/sharing-rules",
		body: `{"consumer":` + strconv.Quote(e2e.Namespace) + `}`, want: http.StatusCreated,
	})

	var rule struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(ruleBody), &rule); err != nil || rule.ID == "" {
		t.Fatalf("the rule of the operator has no id: %v", err)
	}

	shareAPI(ctx, t, env, shareCall{
		name: "an operator removes the rule in the namespace", token: created.Secret,
		method: http.MethodDelete,
		path:   sharingAPI + e2e.Namespace + "/units/" + producerID + "/sharing-rules/" + rule.ID,
		body:   "", want: http.StatusNoContent,
	})
	shareAPI(ctx, t, env, shareCall{
		name: "an operator saves no rule across namespaces", token: created.Secret, method: http.MethodPost,
		path: sharingAPI + e2e.Namespace + "/units/" + producerID + "/sharing-rules",
		body: `{"consumer":` + strconv.Quote(consumer) + `}`, want: http.StatusForbidden,
	})
}

// tofuTokenConfig writes a CLI configuration of OpenTofu that signs in to
// host with token, in a new directory with mode 0600, and returns the
// TF_CLI_CONFIG_FILE pair for one step. The token is a test token of this run.
func tofuTokenConfig(t *testing.T, host, token string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tofurc")
	content := fmt.Sprintf("credentials %q {\n  token = %q\n}\n", host, token)

	if err := os.WriteFile(path, []byte(content), shareFileMode); err != nil {
		t.Fatalf("write the CLI configuration: %v", err)
	}

	return "TF_CLI_CONFIG_FILE=" + path
}

// namespaceViewer makes a service principal that holds the viewer role on the
// namespace ns, and answers a token of it. The viewer role holds
// namespace:read and unit:read-outputs, and not unit:manage-sharing.
func namespaceViewer(ctx context.Context, t *testing.T, env *e2e.Env, ns, name string) string {
	t.Helper()

	quoted := strconv.Quote(name)

	var principal e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":` + quoted + `,"kind":"service"}`, http.StatusCreated,
	}, &principal)

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + ns + "/grants",
		`{"subject":"principal:` + principal.ID + `","role":"viewer"}`, http.StatusCreated,
	}, nil)

	var created e2eToken
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + principal.ID + "/tokens", `{"name":` + quoted + `}`,
		http.StatusCreated,
	}, &created)

	// An empty token would make shareAPI send the token of the admin.
	if created.Secret == "" {
		t.Fatalf("the token of %s has no secret", name)
	}

	return created.Secret
}

// wantNewUnitDefault fails the test unless body is the setting with the
// value want.
func wantNewUnitDefault(t *testing.T, body, want string) {
	t.Helper()

	var setting struct {
		NewUnitDefault string `json:"newUnitDefault"`
	}

	if err := json.Unmarshal([]byte(body), &setting); err != nil {
		t.Fatalf("the setting answer %q is not JSON: %v", body, err)
	}

	if setting.NewUnitDefault != want {
		t.Fatalf("the setting is %q, want %q", setting.NewUnitDefault, want)
	}
}

// checkOutputSharingSetting proves the output sharing setting of a namespace
// through the API:
//
//   - A new namespace reads "nobody": no job of another unit reads the
//     outputs of a new unit until a person saves a rule.
//   - A viewer reads the setting, and cannot change it: unit:manage-sharing
//     is an action of the admin role only.
//   - A value that is not "nobody" or "namespace" answers 400.
//   - The admin changes the setting, the read answers the new value, and the
//     audit log of the namespace holds the change and the refusal.
func checkOutputSharingSetting(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	path := sharingAPI + e2e.Namespace + "/output-sharing"
	viewer := namespaceViewer(ctx, t, env, e2e.Namespace, "sharing-viewer")

	body := shareAPI(ctx, t, env, shareCall{
		name: "the admin reads the setting of a new namespace", token: "", method: http.MethodGet, path: path,
		body: "", want: http.StatusOK,
	})
	wantNewUnitDefault(t, body, "nobody")

	body = shareAPI(ctx, t, env, shareCall{
		name: "a viewer reads the setting", token: viewer, method: http.MethodGet, path: path, body: "",
		want: http.StatusOK,
	})
	wantNewUnitDefault(t, body, "nobody")

	for _, c := range []shareCall{
		{
			"a viewer cannot change the setting", viewer, http.MethodPut, path, `{"newUnitDefault":"namespace"}`,
			http.StatusForbidden,
		},
		{"a value that is not a value of the setting", "", http.MethodPut, path, `{"newUnitDefault":"everyone"}`, http.StatusBadRequest},
		{"a body with no value", "", http.MethodPut, path, `{}`, http.StatusBadRequest},
		{"an unknown namespace", "", http.MethodGet, sharingAPI + "absent/output-sharing", "", http.StatusNotFound},
	} {
		shareAPI(ctx, t, env, c)
	}

	body = shareAPI(ctx, t, env, shareCall{
		name: "the setting stays after the refusals", token: "", method: http.MethodGet, path: path, body: "",
		want: http.StatusOK,
	})
	wantNewUnitDefault(t, body, "nobody")

	body = shareAPI(ctx, t, env, shareCall{
		name: "the admin changes the setting", token: "", method: http.MethodPut, path: path,
		body: `{"newUnitDefault":"namespace"}`, want: http.StatusOK,
	})
	wantNewUnitDefault(t, body, "namespace")

	body = shareAPI(ctx, t, env, shareCall{
		name: "the read answers the new value", token: viewer, method: http.MethodGet, path: path, body: "",
		want: http.StatusOK,
	})
	wantNewUnitDefault(t, body, "namespace")

	checkSettingAudit(ctx, t, env)
}

// checkSettingAudit proves that the audit log of the namespace holds one
// change of the setting by the admin and one refusal of the viewer.
func checkSettingAudit(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/audit/entries?organization="+e2e.Namespace, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the audit log: status %d, %v: %s", status, err, body)
	}

	var listing struct {
		Entries []auditListItem `json:"entries"`
	}

	if err := json.Unmarshal([]byte(body), &listing); err != nil {
		t.Fatalf("the audit answer is not JSON: %v", err)
	}

	outcomes := map[string]int{}

	for i := range listing.Entries {
		if e := &listing.Entries[i]; e.Action == "unit:manage-sharing" && e.Object == e2e.Namespace {
			outcomes[e.Outcome]++
		}
	}

	if outcomes["success"] != 1 || outcomes["denied"] != 1 {
		t.Fatalf("the audit log holds the setting entries %v, want one success and one denied", outcomes)
	}
}

// neutralOutputs is the answer of the tool-neutral outputs route.
type neutralOutputs struct {
	Tool    string          `json:"tool"`
	UnitID  string          `json:"unitId"`  //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	StateID string          `json:"stateId"` //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	Outputs []neutralOutput `json:"outputs"`
}

// neutralOutput is one output of neutralOutputs.
type neutralOutput struct {
	Name   string          `json:"name"`
	Secret bool            `json:"secret"`
	Value  json.RawMessage `json:"value"`
}

// neutralWant is what checkNeutralOutputs expects: the tool and the unit ID,
// the plain outputs with their JSON values, and the names of the secret outputs.
type neutralWant struct {
	tool    string
	unitID  string
	plain   map[string]string
	secrets []string
}

// checkNeutralOutputs proves the answer of the outputs route for a unit: the
// tool, the unit ID, a state ID, the plain outputs with their values, and each
// secret output with its mark and no value. The answer never holds the secret
// value of the scenario.
func checkNeutralOutputs(t *testing.T, body string, want neutralWant) {
	t.Helper()

	var doc neutralOutputs
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode the outputs answer: %v: %s", err, body)
	}

	if doc.Tool != want.tool || doc.UnitID != want.unitID || doc.StateID == "" {
		t.Fatalf("outputs answer = %s, want the tool %q, the unit %q and a state ID", body, want.tool, want.unitID)
	}

	if len(doc.Outputs) != len(want.plain)+len(want.secrets) {
		t.Fatalf(
			"outputs answer holds %d outputs, want %d: %s",
			len(doc.Outputs),
			len(want.plain)+len(want.secrets),
			body,
		)
	}

	for i := range doc.Outputs {
		checkNeutralOutput(t, want, &doc.Outputs[i])
	}

	if strings.Contains(body, shareSecret) {
		t.Fatalf("the outputs answer holds a secret value: %s", body)
	}

	// The checkpoint of the Pulumi CLI holds a secret as a ciphertext, and
	// the state of OpenTofu holds none. Neither shape reaches a reader of
	// outputs only.
	for _, marker := range []string{"ciphertext", "4dabf18193072939515e3"} {
		if strings.Contains(body, marker) {
			t.Fatalf("the outputs answer holds ciphertext (%q): %s", marker, body)
		}
	}
}

// checkNeutralOutput proves one output of the answer.
func checkNeutralOutput(t *testing.T, want neutralWant, output *neutralOutput) {
	t.Helper()

	value, isPlain := want.plain[output.Name]
	if isPlain {
		if output.Secret || string(output.Value) != value {
			t.Errorf("plain output %q = %+v, want the value %s", output.Name, *output, value)
		}

		return
	}

	if !slices.Contains(want.secrets, output.Name) || !output.Secret || len(output.Value) != 0 {
		t.Errorf("output %q = %+v, want a secret with no value", output.Name, *output)
	}
}

// pulumiOutputsReader proves the person who reads outputs only, for the
// Pulumi CLI:
//
//   - The outputs route answers 200 with the plain output and the secret output
//     as a mark with no value.
//   - The export answers 403, because a stack reference reads the whole
//     deployment and needs unit:read. A decrypt of a config secret of the
//     producer answers 403.
//   - A stack reference of the CLI, run with that person's token, fails with 403.
//   - A person with unit:read-outputs on another unit gets the 404 of an
//     unknown unit on the outputs route.
func pulumiOutputsReader(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	pulumiShareSetup(ctx, t, env)

	producerID, _, err := env.StackID(ctx, e2e.Namespace, e2e.Project, shareProducer)
	if err != nil || producerID == "" {
		t.Fatalf("producer stack ID: %v", err)
	}

	consumer := "pulumi:" + shareStack(shareConsumer)
	reader := outputReader(ctx, t, env, readerGrants{
		e2e.Namespace, "pulumi:" + shareStack(shareProducer), consumer, "output-reader",
	})
	stackPath := "/api/stacks/" + shareStack(shareProducer)
	outputsPath := sharingAPI + e2e.Namespace + "/units/" + producerID + "/outputs"

	body := shareAPI(
		ctx,
		t,
		env,
		shareCall{"read outputs only", reader, http.MethodGet, outputsPath, "", http.StatusOK},
	)
	checkNeutralOutputs(t, body, neutralWant{
		tool: "pulumi", unitID: producerID, plain: map[string]string{"vpcId": `"` + shareVPC + `"`},
		secrets: []string{"dbPassword"},
	})

	adminBody := shareAPI(
		ctx,
		t,
		env,
		shareCall{"the administrator reads outputs", "", http.MethodGet, outputsPath, "", http.StatusOK},
	)
	checkNeutralOutputs(t, adminBody, neutralWant{
		tool: "pulumi", unitID: producerID, plain: map[string]string{"vpcId": `"` + shareVPC + `"`},
		secrets: []string{"dbPassword"},
	})

	configSecret := producerConfigCiphertext(t, env)

	for _, c := range []shareCall{
		{"export needs read state", reader, http.MethodGet, stackPath + "/export", "", http.StatusForbidden},
		{"stack read needs read state", reader, http.MethodGet, stackPath, "", http.StatusForbidden},
		{
			"no decrypt of a config secret", reader, http.MethodPost, stackPath + "/decrypt",
			`{"ciphertext":` + strconv.Quote(configSecret) + `}`, http.StatusForbidden,
		},
	} {
		shareAPI(ctx, t, env, c)
	}

	env.Step(ctx, t, withEnv(failStep("a stack reference of a person needs read state",
		outputContains("403"), "up", "--yes", "--non-interactive", "--stack", shareStack(shareConsumer)),
		"PULUMI_ACCESS_TOKEN="+reader))

	// The reader holds unit:read-outputs on the consumer unit too, but a
	// person with that action on the consumer only sees no producer.
	other := outputReader(ctx, t, env, readerGrants{
		e2e.Namespace, consumer, "", "other-reader",
	})
	shareAPI(ctx, t, env, shareCall{
		"outputs of a unit with no grant answer as an unknown unit", other, http.MethodGet, outputsPath, "",
		http.StatusNotFound,
	})
	shareAPI(ctx, t, env, shareCall{
		"outputs of a unit that does not exist", reader, http.MethodGet,
		sharingAPI + e2e.Namespace + "/units/stk-nosuchstack0000/outputs", "", http.StatusNotFound,
	})
}

// tofuOutputsReader proves the person who reads outputs only, for OpenTofu:
//
//   - The outputs route and the TFE routes of outputs answer 200. The list
//     shows a sensitive output as null, and the route of one sensitive output
//     answers 403 without unit:read-secrets.
//   - An output of an old state version answers 403: it needs unit:read.
//   - The current state version, its download and the state version list
//     answer 403. The data source terraform_remote_state of that person fails
//     before it reads them: the person holds no namespace:read, so the read of
//     the organization answers 404 (the limit that the console states).
//   - A state that the client encrypted answers 409 to that person on both
//     outputs routes, and the administrator still reads it.
func tofuOutputsReader(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	dir := tofuShareSetup(ctx, t, env)
	producerID := env.WorkspaceIDByName(ctx, t, e2e.Workspace)
	reader := outputReader(ctx, t, env, readerGrants{
		e2e.Namespace, "opentofu:" + e2e.Namespace + "/" + e2e.Workspace, "", "output-reader",
	})
	oldVersion := currentStateVersion(ctx, t, env, "", producerID)

	env.WriteTofuConfig(t, tofuProducerConfig(env.Host())+"\nresource \"terraform_data\" \"anchor\" {}\n")
	env.Step(ctx, t, step("share apply producer again", nil, "apply", "-input=false", "-auto-approve", "-no-color"))

	version := currentStateVersion(ctx, t, env, "", producerID)
	if version == oldVersion {
		t.Fatal("the second apply made no new state version of the producer")
	}

	workspaces := "/api/v2/workspaces/" + producerID

	body := shareAPI(ctx, t, env, shareCall{
		"read outputs only",
		reader,
		http.MethodGet,
		sharingAPI + e2e.Namespace + "/units/" + producerID + "/outputs",
		"",
		http.StatusOK,
	})
	checkNeutralOutputs(t, body, neutralWant{
		tool: "opentofu", unitID: producerID, plain: map[string]string{"vpc_id": `"` + shareVPC + `"`},
		secrets: []string{"db_password"},
	})

	list := shareAPI(ctx, t, env, shareCall{
		"the list of outputs", reader, http.MethodGet, workspaces + "/current-state-version-outputs", "", http.StatusOK,
	})
	if !strings.Contains(list, shareVPC) || strings.Contains(list, shareSecret) {
		t.Fatalf("the list of outputs must hold the plain value and no secret: %s", list)
	}

	for _, c := range []shareCall{
		{"a plain output", reader, http.MethodGet, "/api/v2/state-version-outputs/" + version + ".vpc_id", "", http.StatusOK},
		{
			"a sensitive output needs read secrets", reader, http.MethodGet,
			"/api/v2/state-version-outputs/" + version + ".db_password", "", http.StatusForbidden,
		},
		{
			"an output of an old state version needs read state", reader, http.MethodGet,
			"/api/v2/state-version-outputs/" + oldVersion + ".vpc_id", "", http.StatusForbidden,
		},
		{"the current state version needs read state", reader, http.MethodGet, workspaces + "/current-state-version", "", http.StatusForbidden},
		{"a download needs read state", reader, http.MethodGet, "/api/v2/state-versions/" + version + "/download", "", http.StatusForbidden},
		{
			"the state version list needs read state", reader, http.MethodGet,
			"/api/v2/state-versions?filter[organization][name]=" + e2e.Namespace + "&filter[workspace][name]=" + e2e.Workspace,
			"", http.StatusForbidden,
		},
		{
			"the administrator reads a sensitive output", "", http.MethodGet,
			"/api/v2/state-version-outputs/" + version + ".db_password", "", http.StatusOK,
		},
	} {
		shareAPI(ctx, t, env, c)
	}

	env.StepIn(ctx, t, dir, step("share init consumer", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, withEnv(failStep("terraform_remote_state of a person needs read state",
		outputContains("not found"), "plan", "-input=false", "-no-color"), tofuTokenConfig(t, env.Host(), reader)))

	checkEncryptedReader(ctx, t, env)
}

// checkEncryptedReader proves that a person with unit:read-outputs and not
// unit:read gets 409 from both outputs routes for a workspace whose state the
// client encrypted, with a message that names the client encryption, and that
// the administrator reads the outputs.
func checkEncryptedReader(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	dir := writeShareConfig(t, tofuEncryptedProducerConfig(env.Host()))
	env.StepIn(ctx, t, dir, step("share init client-encrypted producer", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, step("share apply client-encrypted producer", nil,
		"apply", "-input=false", "-auto-approve", "-no-color"))

	encryptedID := env.WorkspaceIDByName(ctx, t, shareEncrypted)
	reader := outputReader(ctx, t, env, readerGrants{
		e2e.Namespace, "opentofu:" + e2e.Namespace + "/" + shareEncrypted, "", "encrypted-output-reader",
	})

	for _, c := range []shareCall{
		{
			"a person with read outputs only reads no client-encrypted state", reader, http.MethodGet,
			"/api/v2/workspaces/" + encryptedID + "/current-state-version-outputs", "", http.StatusConflict,
		},
		{
			"the outputs route refuses a client-encrypted state too", reader, http.MethodGet,
			sharingAPI + e2e.Namespace + "/units/" + encryptedID + "/outputs", "", http.StatusConflict,
		},
	} {
		if !strings.Contains(shareAPI(ctx, t, env, c), "client") {
			t.Fatalf("%s: the 409 does not name the client encryption", c.name)
		}
	}

	shareAPI(ctx, t, env, shareCall{
		"the administrator reads the outputs of a client-encrypted state", "", http.MethodGet,
		"/api/v2/workspaces/" + encryptedID + "/current-state-version-outputs", "", http.StatusOK,
	})

	if !strings.Contains(shareAPI(ctx, t, env, shareCall{
		"the outputs route refuses a client-encrypted state also for the administrator", "", http.MethodGet,
		sharingAPI + e2e.Namespace + "/units/" + encryptedID + "/outputs", "", http.StatusConflict,
	}), "unit.client-encrypted") {
		t.Fatal("the 409 of the outputs route for the administrator has no code unit.client-encrypted")
	}
}
