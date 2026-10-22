//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	sharingRulesSkip = "the server has no unit:read-outputs action, no sharing rules and no " +
		"namespace setting for output sharing yet; these steps fail until the output sharing slices land"
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
					pulumiReadPath(t, e2e.NewEnv(t.Context(), t, cli, db))
				})
			}
		})
	}

	for _, label := range e2e.OpenTofu().Versions() {
		t.Run("opentofu/"+label, func(t *testing.T) {
			t.Parallel()

			cli := installShareCLI(t, fetcher, e2e.OpenTofu(), label)
			// The step across tools runs on the first Pulumi version of the
			// matrix, which is the latest release by default.
			pulumiCLI := installShareCLI(t, fetcher, e2e.Pulumi(), pulumiVersions[0])

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()
					skipWithoutStore(t, db)

					env := e2e.NewEnv(t.Context(), t, cli, db)
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
	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	t.Run("pulumi", func(t *testing.T) {
		t.Parallel()
		t.Skip(sharingRulesSkip)

		cli := installShareCLI(t, fetcher, e2e.Pulumi(), pulumiVersions[0])
		env := e2e.NewEnv(t.Context(), t, cli, e2e.StoreSQLite)
		pulumiSharingRules(t, env)
	})

	t.Run("opentofu", func(t *testing.T) {
		t.Parallel()
		t.Skip(sharingRulesSkip)

		cli := installShareCLI(t, fetcher, e2e.OpenTofu(), tofuVersions[0])
		env := e2e.NewEnv(t.Context(), t, cli, e2e.StoreSQLite)
		tofuSharingRules(t, env)
	})
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

	status, body, err := env.ServerAPIStatusAs(ctx, token, c.method, c.path, c.body)
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
}

// outputReader makes a service principal with a custom role that holds only
// unit:read-outputs on the unit g.producer, and answers a token of it. With
// g.writeUnit set, the principal also holds the writer role on that unit, so
// that it can run the consumer.
func outputReader(ctx context.Context, t *testing.T, env *e2e.Env, g readerGrants) string {
	t.Helper()

	ns, producer, writeUnit := g.ns, g.producer, g.writeUnit

	var principal e2ePrincipal
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals", `{"subject":"output-reader","kind":"service"}`, http.StatusCreated,
	}, &principal)

	var role struct {
		ID string `json:"id"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/roles", `{"name":"output-reader","actions":["unit:read-outputs"]}`,
		http.StatusCreated,
	}, &role)

	subject := "principal:" + principal.ID
	grants := "/api/server/namespaces/" + ns + "/unit-grants?object=" + producer
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, grants, `{"subject":"` + subject + `","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	if writeUnit != "" {
		principalCall(ctx, t, env, principalStep{
			http.MethodPost, "/api/server/namespaces/" + ns + "/unit-grants?object=" + writeUnit,
			`{"subject":"` + subject + `","role":"writer"}`, http.StatusCreated,
		}, nil)
	}

	var created struct {
		Token e2eToken `json:"token"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + principal.ID + "/tokens", `{"name":"output-reader"}`,
		http.StatusCreated,
	}, &created)

	return created.Token.Secret
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
// r.ns that names r.consumer, and answers the rule ID.
func sharingRule(ctx context.Context, t *testing.T, env *e2e.Env, r ruleTarget) string {
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

	return rule.ID
}

// checkSharingRuleRefusals proves that a rule never names a principal or a
// whole other namespace, that a second rule for the same consumer conflicts,
// and that the namespace setting is "nobody" by default.
func checkSharingRuleRefusals(ctx context.Context, t *testing.T, env *e2e.Env, producerID, consumer string) {
	t.Helper()

	rules := sharingAPI + e2e.Namespace + "/units/" + producerID + "/sharing-rules"

	body := shareAPI(ctx, t, env, shareCall{
		name: "read the namespace setting", token: "", method: http.MethodGet,
		path: sharingAPI + e2e.Namespace + "/output-sharing", body: "", want: http.StatusOK,
	})
	if !strings.Contains(body, `"newUnitDefault":"nobody"`) {
		t.Fatalf("the namespace setting %q is not nobody", body)
	}

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
//   - A person with only unit:read-outputs reads the outputs through the
//     outputs route. The export answers 403, because a stack reference reads
//     the whole deployment and needs unit:read (the limit that the console
//     states). A decrypt of a config secret of the producer answers 403.
//   - A job token reads the producer only when a sharing rule names the unit
//     of the job and the principal that started the job holds
//     unit:read-outputs. The decrypt route refuses a ciphertext that is not
//     in the outputs of the latest checkpoint.
//   - The stack read and the export that such a job token gets hold no tag
//     and no resource other than the root stack resource. The export of a
//     version and the update history refuse it.
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
	reader := outputReader(ctx, t, env, readerGrants{e2e.Namespace, "pulumi:" + shareStack(shareProducer), consumer})
	stackPath := "/api/stacks/" + shareStack(shareProducer)
	configSecret := producerConfigCiphertext(t, env)

	for _, c := range []shareCall{
		{
			"read outputs only", reader, http.MethodGet,
			sharingAPI + e2e.Namespace + "/units/" + producerID + "/outputs", "", http.StatusOK,
		},
		{"export needs read state", reader, http.MethodGet, stackPath + "/export", "", http.StatusForbidden},
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

	full := shareAPI(ctx, t, env, shareCall{
		name: "the producer reads its export", token: "", method: http.MethodGet, path: stackPath + "/export", body: "",
		want: http.StatusOK,
	})
	if n := len(deploymentOf(t, full).Resources); n < 2 {
		t.Fatalf("the export of the producer has %d resources, want the root stack resource and another", n)
	}

	reduced := deploymentOf(t, shareAPI(ctx, t, env, shareCall{
		name: "a job token reads the export", token: token, method: http.MethodGet, path: stackPath + "/export",
		body: "", want: http.StatusOK,
	}))

	if len(reduced.Resources) != 1 || reduced.Resources[0].Type != "pulumi:pulumi:Stack" {
		t.Fatalf("the export to a job token has %d resources, want the root stack resource only",
			len(reduced.Resources))
	}

	if len(reduced.Resources[0].Inputs) != 0 || len(reduced.Resources[0].Outputs) == 0 {
		t.Fatal("the root stack resource of the export to a job token has inputs or no outputs")
	}

	if len(reduced.SecretsProviders) == 0 || len(reduced.PendingOperations) != 0 {
		t.Fatal("the export to a job token has no secrets providers, or has pending operations")
	}
}

// shareResource is the part of a resource of an export that the reduced
// deployment checks read.
type shareResource struct {
	Inputs  map[string]any `json:"inputs"`
	Outputs map[string]any `json:"outputs"`
	Type    string         `json:"type"`
}

// shareDeployment is the part of an export that the reduced deployment
// checks read.
type shareDeployment struct {
	SecretsProviders  json.RawMessage   `json:"secrets_providers"` //nolint:tagliatelle // The Pulumi wire name.
	Resources         []shareResource   `json:"resources"`
	PendingOperations []json.RawMessage `json:"pending_operations"` //nolint:tagliatelle // The Pulumi wire name.
}

// deploymentOf parses the deployment of the export body. The body holds
// ciphertext, so no message prints it.
func deploymentOf(t *testing.T, body string) shareDeployment {
	t.Helper()

	var export struct {
		Deployment shareDeployment `json:"deployment"`
	}

	if err := json.Unmarshal([]byte(body), &export); err != nil {
		t.Fatalf("the export is not JSON: %v", err)
	}

	return export.Deployment
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
//   - A grant of unit:manage-sharing on the installation saves no rule whose
//     consumer is in another namespace.
func tofuSharingRules(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	dir := tofuShareSetup(ctx, t, env)
	reader := outputReader(
		ctx,
		t,
		env,
		readerGrants{e2e.Namespace, "opentofu:" + e2e.Namespace + "/" + e2e.Workspace, ""},
	)

	outputsDir := writeShareConfig(t, tofuOutputsConfig(env.Host()))
	env.StepIn(ctx, t, outputsDir, step("share init outputs", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, outputsDir, withEnv(step("tfe_outputs with read outputs", contains(shareVPC),
		"apply", "-input=false", "-auto-approve", "-no-color"), "TFE_TOKEN="+reader))

	env.StepIn(ctx, t, dir, step("share init consumer", nil, "init", "-input=false", "-no-color"))
	env.StepIn(ctx, t, dir, withEnv(failStep("terraform_remote_state of a person needs read state",
		outputContains("403"), "plan", "-input=false", "-no-color"), tofuTokenConfig(t, env.Host(), reader)))

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
	checkHiddenProducer(ctx, t, env, jobToken, producerID)
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

// checkHiddenProducer proves that a job token of another namespace with no
// rule gets the same answer for the producer as for a workspace that does
// not exist, on each route that terraform_remote_state calls.
func checkHiddenProducer(ctx context.Context, t *testing.T, env *e2e.Env, token, producerID string) {
	t.Helper()

	byName := "/api/v2/organizations/" + e2e.Namespace + "/workspaces/"

	for _, pair := range [][2]string{
		{byName + e2e.Workspace, byName + "no-such-workspace"},
		{"/api/v2/workspaces/" + producerID + "/current-state-version", "/api/v2/workspaces/ws-nosuchworkspace0/current-state-version"},
	} {
		known := shareAPI(ctx, t, env, shareCall{
			name: "a job token across namespaces reads the producer with no rule", token: token,
			method: http.MethodGet, path: pair[0], body: "", want: http.StatusNotFound,
		})
		unknown := shareAPI(ctx, t, env, shareCall{
			name: "a job token across namespaces reads an unknown workspace", token: token,
			method: http.MethodGet, path: pair[1], body: "", want: http.StatusNotFound,
		})

		if known != unknown {
			t.Fatalf("%s answers %q, and an unknown workspace answers %q; want the same body", pair[0], known, unknown)
		}
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
// unit:manage-sharing on the installation only saves no rule whose consumer
// is in another namespace.
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

	var created struct {
		Token e2eToken `json:"token"`
	}
	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/principals/" + principal.ID + "/tokens", `{"name":"sharing-operator"}`,
		http.StatusCreated,
	}, &created)

	shareAPI(ctx, t, env, shareCall{
		name: "an operator saves no rule across namespaces", token: created.Token.Secret, method: http.MethodPost,
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
