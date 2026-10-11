//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/sqlite"
)

const (
	// Namespace is the namespace every Env creates; CLI stack names use it as the organization.
	Namespace = "acme"
	// Namespace2 is a second namespace a scenario creates to prove that
	// listings span namespaces. NewEnv does not create it, so steps that
	// need one readable namespace run first.
	Namespace2 = "beta"
	// Project is the project name written to the scratch Pulumi.yaml.
	Project = "e2e-project"
	// Workspace is the workspace name written to the scratch main.tf.
	Workspace = "e2e-workspace"
	// Subject is the principal the access token authenticates as.
	Subject = "admin"

	region         = "e2e"
	tokenBytes     = 24
	commandTimeout = 2 * time.Minute
	waitDelay      = 5 * time.Second
	maxShown       = 2000
	baseEnvEntries = 8
	pulumiProject  = "name: " + Project + "\nruntime: yaml\n"
	// tofuConfig uses the cloud block, OpenTofu's client for the TFE-compatible
	// remote API, and declares no providers so nothing is downloaded.
	tofuConfig = `terraform {
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
`
	tofuCLIConfig = "credentials %q {\n  token = %q\n}\n"
)

// Result is the outcome of one CLI invocation.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	Duration time.Duration
	ExitCode int
}

// StepResult is one scenario step as shown in the report.
type StepResult struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Failure  string `json:"failure,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exitCode"`
	Passed   bool   `json:"passed"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// Env is a running server plus an isolated CLI environment.
type Env struct {
	// URL is the server's base URL: the Pulumi backend URL, or https://<host> for OpenTofu.
	URL string
	// Recorder holds every request the CLI made to the server.
	Recorder *Recorder
	cli      Install
	store    Store
	server   serverStore
	token    string
	home     string
	project  string
	steps    []StepResult
}

// serverStore is every persistence port the server needs.
type serverStore interface {
	namespace.Store
	stack.Store
	stack.UpdateStore
	stack.CheckpointStore
	stack.EventStore
}

// NewEnv starts a server on a random local port, backed by a fresh database
// with the Namespace namespace, and prepares an empty home directory and a
// scratch project for cli. SQLite uses a temporary file; PostgreSQL uses an
// isolated schema that is dropped when the test ends. OpenTofu gets an HTTPS
// server, because its service discovery refuses plain HTTP, and the server's
// certificate is the only one it trusts. Everything is removed when the test ends.
func NewEnv(ctx context.Context, tb testing.TB, cli Install, store Store) *Env {
	tb.Helper()

	dir := tb.TempDir()
	token := newToken(tb)
	rec := &Recorder{step: "", entries: nil, arrived: 0, mu: sync.Mutex{}}

	handler, st := newServer(ctx, tb, store, filepath.Join(dir, "dustpan.db"), token)

	ts := httptest.NewUnstartedServer(rec.Middleware(handler))
	if cli.Tool == ToolTofu {
		ts.StartTLS()
	} else {
		ts.Start()
	}

	tb.Cleanup(ts.Close)

	env := &Env{
		URL:      ts.URL,
		Recorder: rec,
		cli:      cli,
		store:    store,
		server:   st,
		token:    token,
		home:     filepath.Join(dir, "home"),
		project:  filepath.Join(dir, "project"),
		steps:    nil,
	}

	for _, d := range []string{env.home, env.project} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			tb.Fatalf("create %s: %v", d, err)
		}
	}

	if cli.Tool == ToolTofu {
		env.writeTofuFiles(tb, ts)
	} else {
		env.write(tb, filepath.Join(env.project, "Pulumi.yaml"), pulumiProject)
	}

	return env
}

// Host is the server's host:port, as OpenTofu names it.
func (e *Env) Host() string {
	return strings.TrimPrefix(strings.TrimPrefix(e.URL, "https://"), "http://")
}

func newToken(tb testing.TB) string {
	tb.Helper()

	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatalf("generate token: %v", err)
	}

	return hex.EncodeToString(raw)
}

// newServer opens store (a fresh SQLite file at dbPath, or an isolated
// PostgreSQL schema), creates the Namespace namespace and returns the server's
// handler and store. The store and its schema are closed and removed when the
// test ends.
// the environment needs the interface.
//
//nolint:ireturn // SQLite and PostgreSQL stores share no concrete type;
func newServer(
	ctx context.Context, tb testing.TB, store Store, dbPath, token string,
) (http.Handler, serverStore) {
	tb.Helper()

	var st serverStore

	switch store {
	case StoreSQLite:
		sqliteStore, err := sqlite.Open(ctx, dbPath)
		if err != nil {
			tb.Fatalf("open sqlite store: %v", err)
		}

		tb.Cleanup(func() {
			if closeErr := sqliteStore.Close(); closeErr != nil {
				tb.Errorf("close sqlite store: %v", closeErr)
			}
		})

		st = sqliteStore
	case StorePostgres:
		st = openPostgresStore(ctx, tb, PostgresURL())
	default:
		tb.Fatalf("unknown store: %s", store)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: region})
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	if _, err = namespaces.Create(ctx, Namespace); err != nil {
		tb.Fatalf("create namespace: %v", err)
	}

	authn, err := identity.NewStaticToken(Subject, token)
	if err != nil {
		tb.Fatalf("authenticator: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithAuthenticator(authn),
	)
	if err != nil {
		tb.Fatalf("new server: %v", err)
	}

	return srv.Handler(), st
}

// seedSchemaVersion is the deployment format version SeedResources saves.
// The server never interprets the deployment, so any version 1 or higher
// passes validation.
const seedSchemaVersion = 3

// seedDeployment is the checkpoint SeedResources saves: the automatic stack
// resource and one managed resource, so the stack counts as having resources.
const seedDeployment = `{"manifest":{},"resources":[` +
	`{"urn":"urn:pulumi:stack::e2e-project::pulumi:pulumi:Stack::e2e-project-resourced",` +
	`"type":"pulumi:pulumi:Stack"},` +
	`{"urn":"urn:pulumi:stack::e2e-project::custom:resource:Type::res1",` +
	`"type":"custom:resource:Type","custom":true,"id":"res1-id"}]}`

// SeedResources records a finished update with one managed resource as the
// latest checkpoint of the stack named stackName in Project. It exists so
// delete tests can set up a stack with resources without driving a full
// update through routes that later issues own.
func (e *Env) SeedResources(ctx context.Context, tb testing.TB, stackName string) {
	tb.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: e.server, Now: nil, Region: region})
	if err != nil {
		tb.Fatalf("namespace service: %v", err)
	}

	ref, err := stack.ParseRef(Namespace, Project, stackName)
	if err != nil {
		tb.Fatalf("parse ref: %v", err)
	}

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            e.server,
		Checkpoints:        e.server,
		Events:             e.server,
		Namespaces:         namespaces,
		Now:                nil,
		MaxCheckpointBytes: 0,
	})
	if err != nil {
		tb.Fatalf("updates service: %v", err)
	}

	u, err := updates.Create(ctx, ref, stack.Plan{Kind: stack.KindUpdate, DryRun: false})
	if err != nil {
		tb.Fatalf("create update: %v", err)
	}

	started, err := updates.Start(ctx, ref, u.ID)
	if err != nil {
		tb.Fatalf("start update: %v", err)
	}

	err = updates.SaveCheckpoint(ctx, ref, u.ID, started.Token, stack.CheckpointInput{
		Features:      nil,
		Deployment:    []byte(seedDeployment),
		SchemaVersion: seedSchemaVersion,
		Sequence:      0,
		Invalid:       false,
	})
	if err != nil {
		tb.Fatalf("save checkpoint: %v", err)
	}

	if _, err := updates.Complete(ctx, ref, u.ID, started.Token, stack.StatusSucceeded); err != nil {
		tb.Fatalf("complete update: %v", err)
	}
}

// CreateNamespace creates a namespace homed in the test region, for steps
// that need more than the Namespace namespace NewEnv creates.
func (e *Env) CreateNamespace(ctx context.Context, tb testing.TB, name string) {
	tb.Helper()

	nsName, err := namespace.ParseName(name)
	if err != nil {
		tb.Fatalf("parse namespace %q: %v", name, err)
	}

	err = e.server.CreateNamespace(ctx, namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            nsName,
		HomeRegion:      region,
		FailoverVersion: 1,
	})
	if err != nil {
		tb.Fatalf("create namespace %q: %v", name, err)
	}
}

// Steps returns the scenario steps run so far.
func (e *Env) Steps() []StepResult {
	return append([]StepResult(nil), e.steps...)
}

// Install returns the CLI under test.
func (e *Env) Install() Install {
	return e.cli
}

// Store returns the database backend this environment runs against.
func (e *Env) Store() Store {
	return e.store
}

// Requested reports whether the CLI sent method path during step.
func (e *Env) Requested(step, method, path string) bool {
	entries := e.Recorder.Entries()

	for i := range entries {
		if r := &entries[i]; r.Step == step && r.Method == method && r.Path == path {
			return true
		}
	}

	return false
}

// errNoStackID reports a stack response without an id field.
var errNoStackID = errors.New("stack has no id")

// StackID fetches the stack at ns/project/name over the Pulumi CLI API
// and returns its id field and the response status. It authenticates with the
// environment's token. A missing stack returns status 404 with no error.
func (e *Env) StackID(
	ctx context.Context, ns, project, name string,
) (string, int, error) {
	target := e.URL + "/api/stacks/" + ns + "/" + project + "/" + name

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("build stack request: %w", err)
	}

	req.Header.Set("Authorization", "token "+e.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("get stack %s/%s/%s: %w", ns, project, name, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}

	var got struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return "", 0, fmt.Errorf("decode stack %s/%s/%s: %w", ns, project, name, err)
	}

	if got.ID == "" {
		return "", 0, fmt.Errorf("%w: %s/%s/%s", errNoStackID, ns, project, name)
	}

	return got.ID, resp.StatusCode, nil
}

// Run executes the CLI with args and returns what it printed. extra holds
// NAME=value pairs added to the environment. A non-zero exit is reported in the
// Result, not as an error.
func (e *Env) Run(ctx context.Context, extra []string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer

	//nolint:gosec // The binary is the CLI this suite downloaded and checksum-verified.
	cmd := exec.CommandContext(ctx, e.cli.Binary(), args...)
	cmd.Dir = e.project
	cmd.Env = e.cliEnv(extra)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	start := time.Now()
	err := cmd.Run()
	timedOut := ctx.Err()
	res := Result{
		Command:  e.cli.Tool + " " + strings.Join(args, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
		ExitCode: 0,
	}

	if timedOut != nil {
		// The kill shows up as an exit error; name the real cause instead.
		return res, fmt.Errorf("%s did not finish within %s: %w", res.Command, commandTimeout, timedOut)
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.ExitCode = exitErr.ExitCode()

		return res, nil
	}

	if err != nil {
		return res, fmt.Errorf("run %s: %w", res.Command, err)
	}

	return res, nil
}

// StepSpec describes one scenario step.
type StepSpec struct {
	// Check inspects the run; nil means the exit code is enough.
	Check func(Result) error
	// Name labels the subtest, the report row and the requests the step makes.
	Name string
	// Args are the CLI arguments.
	Args []string
	// Env holds extra NAME=value pairs for this step only.
	Env []string
	// Fail expects a non-zero exit. Check then inspects the failed run,
	// for steps that prove an error message.
	Fail bool
}

// ParallelSpec describes one scenario step that runs several CLI invocations
// at the same time, for operations that must not block each other.
type ParallelSpec struct {
	// Check inspects every successful run; nil means zero exits are enough.
	Check func([]Result) error
	// Name labels the subtest, the report row and the requests the runs make.
	Name string
	// Args holds one CLI argument list per invocation.
	Args [][]string
	// Env holds extra NAME=value pairs for every invocation of this step.
	Env []string
}

// StepParallel runs every argument list in spec at the same time and records
// one step. It fails the subtest on a non-zero exit or when spec.Check
// returns an error. A failing step does not stop later steps.
func (e *Env) StepParallel(ctx context.Context, t *testing.T, spec ParallelSpec) []Result {
	t.Helper()

	res := make([]Result, len(spec.Args))

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		type outcome struct {
			res Result
			err error
		}

		outs := make([]outcome, len(spec.Args))

		var wg sync.WaitGroup

		for i, args := range spec.Args {
			wg.Go(func() {
				outs[i].res, outs[i].err = e.Run(ctx, spec.Env, args...)
			})
		}

		wg.Wait()

		var failure string

		for i := range outs {
			res[i] = outs[i].res

			t.Logf("%s (%s)\nstdout:\n%s\nstderr:\n%s", res[i].Command,
				res[i].Duration.Round(time.Millisecond), clip(res[i].Stdout), clip(res[i].Stderr))

			switch {
			case failure != "":
			case outs[i].err != nil:
				failure = outs[i].err.Error()
			case res[i].ExitCode != 0:
				failure = fmt.Sprintf("exit code %d", res[i].ExitCode)
			default:
			}
		}

		if failure == "" && spec.Check != nil {
			if checkErr := spec.Check(res); checkErr != nil {
				failure = checkErr.Error()
			}
		}

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  parallelCommand(spec.Args),
			Failure:  failure,
			Stderr:   clip(parallelStderr(res)),
			ExitCode: parallelExit(res, failure),
			Passed:   failure == "",
			Skipped:  false,
		})

		if failure != "" {
			t.Errorf("%s: %s", spec.Name, failure)
		}
	})

	return res
}

// parallelCommand joins several argument lists into one report command.
func parallelCommand(argSets [][]string) string {
	parts := make([]string, 0, len(argSets))
	for _, args := range argSets {
		parts = append(parts, strings.Join(args, " "))
	}

	return strings.Join(parts, " & ")
}

// parallelStderr joins the stderr of parallel runs for the report.
func parallelStderr(res []Result) string {
	parts := make([]string, 0, len(res))
	for _, r := range res {
		parts = append(parts, r.Stderr)
	}

	return strings.Join(parts, "\n")
}

// parallelExit reports the first non-zero exit, or zero when failure names a
// transport error rather than an exit.
func parallelExit(res []Result, failure string) int {
	for _, r := range res {
		if r.ExitCode != 0 {
			return r.ExitCode
		}
	}

	if failure != "" {
		return 1
	}

	return 0
}

// Step runs spec as a subtest. It fails the subtest on an unexpected exit
// (non-zero, or zero when spec.Fail expects a failure) or when spec.Check
// returns an error, and records the outcome for the report. A failing step
// does not stop later steps: the goal is to see every gap.
func (e *Env) Step(ctx context.Context, t *testing.T, spec StepSpec) Result {
	t.Helper()

	var res Result

	t.Run(spec.Name, func(t *testing.T) {
		e.Recorder.SetStep(spec.Name)

		var failure string

		var err error

		res, err = e.Run(ctx, spec.Env, spec.Args...)

		switch {
		case err != nil:
			failure = err.Error()
		case spec.Fail && res.ExitCode == 0:
			failure = "expected a non-zero exit"
		case !spec.Fail && res.ExitCode != 0:
			failure = fmt.Sprintf("exit code %d", res.ExitCode)
		case spec.Check != nil:
			if checkErr := spec.Check(res); checkErr != nil {
				failure = checkErr.Error()
			}
		default:
		}

		e.steps = append(e.steps, StepResult{
			Name:     spec.Name,
			Command:  res.Command,
			Failure:  failure,
			Stderr:   clip(res.Stderr),
			ExitCode: res.ExitCode,
			Passed:   failure == "",
			Skipped:  false,
		})

		t.Logf("%s (%s)\nstdout:\n%s\nstderr:\n%s", res.Command, res.Duration.Round(time.Millisecond),
			clip(res.Stdout), clip(res.Stderr))

		if failure != "" {
			t.Errorf("%s: %s", res.Command, failure)
		}
	})

	return res
}

// writeTofuFiles writes main.tf, a CLI configuration holding the token, and
// the server certificate.
func (e *Env) writeTofuFiles(tb testing.TB, ts *httptest.Server) {
	tb.Helper()

	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: ts.Certificate().Raw})
	e.write(tb, filepath.Join(e.home, "server.pem"), string(cert))
	e.write(tb, filepath.Join(e.home, "tofurc"), fmt.Sprintf(tofuCLIConfig, e.Host(), e.token))
	e.write(tb, filepath.Join(e.project, "main.tf"), fmt.Sprintf(tofuConfig, e.Host(), Namespace, Workspace))
}

func (e *Env) write(tb testing.TB, name, content string) {
	tb.Helper()

	if err := os.WriteFile(name, []byte(content), fileMode); err != nil {
		tb.Fatalf("write %s: %v", name, err)
	}
}

// cliEnv builds the CLI environment from scratch, so nothing from the
// developer's own Pulumi or OpenTofu setup leaks into a run.
func (e *Env) cliEnv(extra []string) []string {
	env := make([]string, 0, baseEnvEntries+len(extra))
	env = append(env,
		"PATH="+e.cli.Dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+e.home,
		"NO_COLOR=1",
		"NO_PROXY=127.0.0.1,localhost",
	)

	if e.cli.Tool == ToolTofu {
		env = append(env,
			"TF_CLI_CONFIG_FILE="+filepath.Join(e.home, "tofurc"),
			// Go reads SSL_CERT_FILE on Linux only; see the OpenTofu test.
			"SSL_CERT_FILE="+filepath.Join(e.home, "server.pem"),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
		)
	} else {
		env = append(env,
			"PULUMI_HOME="+filepath.Join(e.home, ".pulumi"),
			"PULUMI_BACKEND_URL="+e.URL,
			"PULUMI_ACCESS_TOKEN="+e.token,
			// The update check is asynchronous and would make the request log vary between runs.
			"PULUMI_SKIP_UPDATE_CHECK=true",
		)
	}

	// Later entries win, so extra can override anything above.
	return append(env, extra...)
}

// clip shortens s for logs and reports.
func clip(s string) string {
	if len(s) <= maxShown {
		return s
	}

	return s[:maxShown] + "…"
}
