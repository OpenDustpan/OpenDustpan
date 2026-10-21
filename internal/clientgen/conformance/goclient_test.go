package conformance_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/conformance"
	"opendustpan.org/opendustpan/internal/clientgen/conformance/goclient"
)

// envRun turns on the tests that build the server and run it. The make target
// clients-conformance sets it. Without it, the tests skip, because they build
// two programs and need a few minutes.
const envRun = "DUSTPAN_CLIENTGEN_CONFORMANCE"

// goOperations are the operations that the Go client covers in this slice:
// five reads and one write, across the caller, namespace and server areas.
func goOperations() []string {
	return []string{
		"getCapabilities", "getGitHostApp", "getNotificationSink", "getPreferences",
		"listNotificationSinks", "putGitHostApp",
	}
}

// requiredCases are the cases that must pass for the first slice: a success, a
// 404 with an error code, a 403, a conflict on If-Match and two answers that
// fail validation.
func requiredCases() []string {
	return []string{
		"operations/every-operation-is-called",
		"errors/error-shape-is-decoded",
		"errors/forbidden-error-is-decoded",
		"concurrency/stale-if-match-gives-412",
		"errors/response-with-wrong-type-is-rejected",
		"errors/response-without-required-field-is-rejected",
		"errors/response-with-null-required-is-rejected",
	}
}

// TestGoClientConformance generates the Go client from the description of a
// real dev server, builds its adapter, and runs the suite through JSON-RPC on
// standard input and output.
func TestGoClientConformance(t *testing.T) {
	t.Parallel()

	if os.Getenv(envRun) == "" {
		t.Skipf("set %s=1 or run make clients-conformance", envRun)
	}

	// t.Context ends just before the cleanups run, and the cleanups stop the
	// processes that run on ctx. So ctx keeps the values of t.Context and ends
	// in the last cleanup.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Minute)

	t.Cleanup(cancel)

	reportResults(t, conformance.Run(ctx, startSession(ctx, t), conformance.Cases(), 0))
}

// startSession builds and starts the server, the proxy and the adapter.
func startSession(ctx context.Context, t *testing.T) conformance.Env {
	t.Helper()

	work := t.TempDir()
	binary := buildServer(ctx, t, work)

	dataDir := filepath.Join(work, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("make the data directory: %v", err)
	}

	server, err := conformance.StartDevServer(ctx, conformance.DevOptions{Binary: binary, DataDir: dataDir})
	if err != nil {
		t.Fatalf("start the server: %v", err)
	}

	t.Cleanup(func() { closeAll(t, "server", server.Close) })

	// The generator reads the description that the server serves, as a team
	// that downloads it does.
	description, err := server.Description(ctx)
	if err != nil {
		t.Fatalf("get the description: %v", err)
	}

	built, err := goclient.Build(ctx, goclient.Options{
		Description: description, Operations: goOperations(), ServerRelease: server.Release(),
		Dir: filepath.Join(work, "client"),
	})
	if err != nil {
		t.Fatalf("build the Go client: %v", err)
	}

	api, err := clientgen.NewParser(goOperations()...).Parse(description)
	if err != nil {
		t.Fatalf("parse the description: %v", err)
	}

	proxy, err := conformance.NewProxy(
		ctx,
		conformance.ProxyOptions{Upstream: server.URL(), Operations: api.Operations},
	)
	if err != nil {
		t.Fatalf("start the proxy: %v", err)
	}

	t.Cleanup(func() { closeAll(t, "proxy", proxy.Close) })

	adapter, err := conformance.StartAdapter(ctx, conformance.ProcessOptions{
		Path: built.Adapter, Args: nil, Env: conformance.Environ(nil), CallTimeout: 0,
	})
	if err != nil {
		t.Fatalf("start the adapter: %v", err)
	}

	t.Cleanup(func() { closeAll(t, "adapter", adapter.Close) })

	params := initParams()
	params.BaseURL, params.Token = proxy.URL(), server.Token()

	info, err := adapter.Initialize(ctx, params)
	if err != nil {
		t.Fatalf("initialize the adapter: %v", err)
	}

	env := env(proxy)
	env.Server, env.Adapter, env.Transport = server, adapter, "net/http"
	env.Operations, env.Info = goOperations(), info

	return env
}

// closeAll stops one part of the session, and reports the error.
func closeAll(t *testing.T, what string, stop func() error) {
	t.Helper()

	if err := stop(); err != nil {
		t.Errorf("stop the %s: %v", what, err)
	}
}

// reportResults makes one subtest for each case, and checks that the cases of
// the first slice ran and passed.
func reportResults(t *testing.T, results []conformance.Result) {
	t.Helper()

	passed := make(map[string]bool)
	notImplemented := 0

	for _, r := range results {
		name := string(r.Group) + "/" + r.Name
		passed[name] = r.Status == conformance.StatusPassed

		if r.Status == conformance.StatusNotImplemented {
			notImplemented++
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			switch r.Status {
			case conformance.StatusPassed:
			case conformance.StatusNotImplemented:
				t.Skip("the case has no body yet")
			case conformance.StatusNotApplicable:
				t.Skipf("not applicable: %v", r.Err)
			case conformance.StatusFailed:
				t.Errorf("%v", r.Err)
			default:
				t.Errorf("unknown status %d", int(r.Status))
			}
		})
	}

	for _, name := range requiredCases() {
		if !passed[name] {
			t.Errorf("the required case %s did not pass", name)
		}
	}

	t.Logf("%d cases, %d with no body yet", len(results), notImplemented)
}

// buildServer builds the opendustpan binary from this module.
func buildServer(ctx context.Context, t *testing.T, work string) string {
	t.Helper()

	binary := filepath.Join(work, "opendustpan")

	args := []string{"build", "-o", binary, "./cmd/opendustpan"}
	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec // G204: go comes from PATH; the arguments are fixed.
	cmd.Dir = filepath.Join("..", "..", "..")
	cmd.Env = goclient.Environ()

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	if err := cmd.Run(); err != nil {
		t.Fatalf("build the server: %v\n%s", err, out.String())
	}

	return binary
}
