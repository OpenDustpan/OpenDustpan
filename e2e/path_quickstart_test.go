//go:build e2e

package e2e_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// quickstartNamespace is the namespace that the test makes in place of the
// namespace "acme" of the guide. The harness makes the namespace "acme" and
// puts a catch-all placement rule in it, so a stack there would not meet a
// server as `serve` leaves it. This namespace has no rule.
const quickstartNamespace = "firststack"

// quickstartGuideNamespace is the namespace in the curl commands of the guide.
const quickstartGuideNamespace = "acme"

// quickstartGuide is the path of the guide, from the e2e directory.
const quickstartGuide = "docs/guides/quickstart.md"

// quickstartWorkspace is the OpenTofu workspace of the quickstart guide.
const quickstartWorkspace = "web"

// quickstartFileMode is the mode of the files that the steps write.
const quickstartFileMode = 0o600

// TestQuickstartFirstStack runs the commands of docs/guides/quickstart.md in
// their order on a server that holds no placement rule except the one of the
// harness namespace. It proves that the first Pulumi stack is refused with an
// error that names the fix, that the collection and the rule of the guide make
// the same command work, and that the first OpenTofu workspace works.
func TestQuickstartFirstStack(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// Both CLIs must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the quickstart suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersions := e2e.Pulumi().Versions()
	tofuVersions := e2e.OpenTofu().Versions()

	if len(pulumiVersions) == 0 || len(tofuVersions) == 0 {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersions[0], err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersions[0], err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runQuickstartOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

func runQuickstartOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	// The server serves HTTPS when the env starts for OpenTofu, as the guide's
	// server does. The Pulumi CLI works over HTTPS too.
	env := e2e.NewEnv(ctx, t, tofuCLI, store)
	env.SetCLI(pulumiCLI)

	checkReady(ctx, t, env)

	// The harness made "acme" with a rule, so this namespace stands in for it.
	// "opendustpan namespace create" makes a namespace through this route of the
	// installation API.
	quickstartCall(ctx, t, env, quickstartRequest{"/api/server/namespaces", `{"name":"` + quickstartNamespace + `"}`})

	stackRef := quickstartNamespace + "/" + e2e.Project + "/dev"

	env.Step(ctx, t, step("login", contains(env.URL), "login"))

	// The bug of the quickstart: a namespace with no rule refuses the first
	// stack, and the error names the command that makes a rule.
	env.Step(ctx, t, failStep("stack init before the rule", func(r e2e.Result) error {
		for _, want := range []string{"no placement rule matches", "placement-rules", "curl"} {
			if err := outputContains(want)(r); err != nil {
				return err
			}
		}

		return nil
	}, "stack", "init", stackRef, "--no-select"))

	// The two curl commands of the guide. The test reads them from the guide, so
	// it fails when the guide loses a step or changes a route or a body.
	for _, req := range quickstartGuideRequests(t, quickstartNamespace) {
		quickstartCall(ctx, t, env, req)
	}

	env.Step(ctx, t, step("stack init after the rule", nil, "stack", "init", stackRef, "--no-select"))

	quickstartTofu(ctx, t, env, tofuCLI)
}

// quickstartCall sends one installation API request with the admin token, as
// the curl commands of the guide do, and fails unless it answers want.
func quickstartCall(ctx context.Context, t *testing.T, env *e2e.Env, c quickstartRequest) {
	t.Helper()

	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost, c.path, c.body)
	if err != nil {
		t.Fatalf("POST %s: %v", c.path, err)
	}

	if status != http.StatusCreated {
		t.Fatalf("POST %s answers %d, want %d (%s)", c.path, status, http.StatusCreated, answer)
	}
}

// quickstartRequest is one POST request of the guide.
type quickstartRequest struct {
	path string
	body string
}

var (
	// quickstartCurlURL finds the route in a curl command of the guide.
	quickstartCurlURL = regexp.MustCompile(`https://localhost:8080(/api/dustpan/v1/namespaces/\S+)`)
	// quickstartCurlBody finds the JSON body in a curl command of the guide.
	quickstartCurlBody = regexp.MustCompile(`-d '([^']*)'`)
)

// quickstartGuideRequests reads the curl commands of the guide that call the
// namespace routes and returns their requests in the order of the guide, with
// the namespace of the guide replaced by namespace. The guide repeats the
// commands for the compose stack, so a request that appears again is kept once.
// It fails unless the guide has exactly the collection request and then the
// placement rule request.
func quickstartGuideRequests(t *testing.T, namespace string) []quickstartRequest {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository root: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the repository root: %v", closeErr)
		}
	}()

	guide, err := root.ReadFile(quickstartGuide)
	if err != nil {
		t.Fatalf("read the guide %s: %v", quickstartGuide, err)
	}

	// A curl command spans several lines. A backslash ends each line but the last.
	text := strings.ReplaceAll(string(guide), "\\\n", " ")

	var requests []quickstartRequest

	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "curl ") {
			continue
		}

		route := quickstartCurlURL.FindStringSubmatch(line)
		body := quickstartCurlBody.FindStringSubmatch(line)

		if route == nil || body == nil {
			continue
		}

		from := "/namespaces/" + quickstartGuideNamespace + "/"
		to := "/namespaces/" + namespace + "/"
		req := quickstartRequest{path: strings.Replace(route[1], from, to, 1), body: body[1]}

		if !slices.Contains(requests, req) {
			requests = append(requests, req)
		}
	}

	if len(requests) != 2 ||
		!strings.HasSuffix(requests[0].path, "/collections") ||
		!strings.HasSuffix(requests[1].path, "/placement-rules") {
		t.Fatalf("the guide %s must hold a curl command for the collection and then one for the placement rule, got %v",
			quickstartGuide, requests)
	}

	return requests
}

// quickstartTofu runs the OpenTofu part of the guide: a main.tf with a cloud
// block for the workspace web, a credentials file, tofu init and tofu apply.
func quickstartTofu(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install) {
	t.Helper()

	dir := t.TempDir()
	rc := filepath.Join(dir, "dev.tfrc")

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
`, env.Host(), quickstartNamespace, quickstartWorkspace)

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(config), quickstartFileMode); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	credentials := fmt.Sprintf("credentials %q {\n  token = %q\n}\n", env.Host(), env.TokenA())
	if err := os.WriteFile(rc, []byte(credentials), quickstartFileMode); err != nil {
		t.Fatalf("write dev.tfrc: %v", err)
	}

	for _, args := range [][]string{
		{"init", "-input=false"},
		{"apply", "-input=false", "-auto-approve"},
	} {
		res, err := env.RunAs(ctx, tofuCLI, dir, []string{"TF_CLI_CONFIG_FILE=" + rc}, args...)
		if err != nil {
			t.Fatalf("tofu %s: %v", args[0], err)
		}

		if res.ExitCode != 0 {
			t.Fatalf("tofu %s exits %d:\n%s\n%s", args[0], res.ExitCode, res.Stdout, res.Stderr)
		}
	}

	out, err := env.RunAs(ctx, tofuCLI, dir, []string{"TF_CLI_CONFIG_FILE=" + rc}, "output", "greeting")
	if err != nil || !strings.Contains(out.Stdout, "hello") {
		t.Fatalf("tofu output greeting: %v\n%s\n%s", err, out.Stdout, out.Stderr)
	}
}

// quickstartPlacement makes the collection and the placement rule of the
// guide on a server at host with the admin token and the certificate file
// cert. The compose stack uses it, because its server runs in a container.
func quickstartPlacement(ctx context.Context, t *testing.T, host, token, cert string) {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(cert))
	if err != nil {
		t.Fatalf("open the directory of the certificate %s: %v", cert, err)
	}

	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close the directory of the certificate: %v", closeErr)
		}
	})

	pem, err := root.ReadFile(filepath.Base(cert))
	if err != nil {
		t.Fatalf("read the certificate %s: %v", cert, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("the certificate %s holds no certificate", cert)
	}

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	t.Cleanup(client.CloseIdleConnections)

	for _, call := range quickstartGuideRequests(t, composeNamespace) {
		target := "https://" + host + call.path

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(call.body))
		if err != nil {
			t.Fatalf("build the request for %s: %v", call.path, err)
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", call.path, err)
		}

		status := resp.StatusCode

		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close the answer of %s: %v", call.path, err)
		}

		if status != http.StatusCreated {
			t.Fatalf("POST %s answers %d, want %d", call.path, status, http.StatusCreated)
		}
	}
}
