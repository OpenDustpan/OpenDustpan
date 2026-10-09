//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"opendustpan.org/opendustpan/store/sqlite"
)

const (
	// Namespace is the namespace every Env creates; CLI stack names use it as the organization.
	Namespace = "acme"
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
}

// Env is a running server plus an isolated CLI environment.
type Env struct {
	// URL is the server's base URL: the Pulumi backend URL, or https://<host> for OpenTofu.
	URL string
	// Recorder holds every request the CLI made to the server.
	Recorder *Recorder
	cli      Install
	token    string
	home     string
	project  string
	steps    []StepResult
}

// NewEnv starts a server on a random local port, backed by a fresh SQLite
// database with the Namespace namespace, and prepares an empty home directory
// and a scratch project for cli. OpenTofu gets an HTTPS server, because its
// service discovery refuses plain HTTP, and the server's certificate is the
// only one it trusts. Everything is removed when the test ends.
func NewEnv(ctx context.Context, tb testing.TB, cli Install) *Env {
	tb.Helper()

	dir := tb.TempDir()
	token := newToken(tb)
	rec := &Recorder{step: "", entries: nil, mu: sync.Mutex{}}

	ts := httptest.NewUnstartedServer(rec.Middleware(newServer(ctx, tb, filepath.Join(dir, "dustpan.db"), token)))
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

// newServer opens a SQLite database at dbPath, creates the Namespace namespace
// and returns the server's handler. The store is closed when the test ends.
func newServer(ctx context.Context, tb testing.TB, dbPath, token string) http.Handler {
	tb.Helper()

	st, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		tb.Fatalf("open sqlite store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			tb.Errorf("close sqlite store: %v", closeErr)
		}
	})

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
		dustpan.WithAuthenticator(authn),
	)
	if err != nil {
		tb.Fatalf("new server: %v", err)
	}

	return srv.Handler()
}

// Steps returns the scenario steps run so far.
func (e *Env) Steps() []StepResult {
	return append([]StepResult(nil), e.steps...)
}

// Install returns the CLI under test.
func (e *Env) Install() Install {
	return e.cli
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
	res := Result{
		Command:  e.cli.Tool + " " + strings.Join(args, " "),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
		ExitCode: 0,
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
	// Check inspects a successful run; nil means a zero exit is enough.
	Check func(Result) error
	// Name labels the subtest, the report row and the requests the step makes.
	Name string
	// Args are the CLI arguments.
	Args []string
	// Env holds extra NAME=value pairs for this step only.
	Env []string
}

// Step runs spec as a subtest. It fails the subtest on a non-zero exit or when
// spec.Check returns an error, and records the outcome for the report. A
// failing step does not stop later steps: the goal is to see every gap.
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
		case res.ExitCode != 0:
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
