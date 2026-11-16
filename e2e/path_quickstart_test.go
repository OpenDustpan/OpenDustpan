//go:build e2e

package e2e_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// quickstartNamespace is the namespace that the test makes in place of the
// namespace "acme" of the guide. The harness makes the namespace "acme" and
// puts a catch-all placement rule in it, so a stack there would not meet a
// server as `serve` leaves it. This namespace has no rule.
const quickstartNamespace = "firststack"

// quickstartCollection is the collection that the commands of the guide make.
const quickstartCollection = "everything"

// quickstartWorkspace is the OpenTofu workspace of the quickstart guide.
const quickstartWorkspace = "web"

// quickstartFileMode is the mode of the files that the steps write.
const quickstartFileMode = 0o600

// quickstartMaxAnswer is the most bytes that the test reads of an answer.
const quickstartMaxAnswer = 1 << 20

// TestQuickstartFirstStack runs the commands of docs/guides/quickstart.md in
// their order on a server that holds no placement rule except the one of the
// harness namespace. It proves that the first Pulumi stack is refused with an
// error that names the fix, that the collection and the rules of the guide make
// the same command work, and that the first OpenTofu workspace works.
func TestQuickstartFirstStack(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// Both CLIs must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the quickstart suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

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
	// "opendustpan namespace create" makes a namespace through this route of
	// the installation API.
	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost,
		"/api/server/namespaces", `{"name":"`+quickstartNamespace+`"}`)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create the namespace: status %d, %v (%s)", status, err, answer)
	}

	stackRef := quickstartNamespace + "/" + e2e.Project + "/dev"

	env.Step(ctx, t, step("login", contains(env.URL), "login"))

	// The bug of the quickstart: a namespace with no rule refuses the first
	// stack, and the error names the command that makes a rule.
	env.Step(ctx, t, failStep("stack init before the rule", func(r e2e.Result) error {
		for _, want := range []string{"no placement rule matches", "opendustpan placement add"} {
			if err := outputContains(want)(r); err != nil {
				return err
			}
		}

		return nil
	}, "stack", "init", stackRef, "--no-select"))

	// The commands of the guide, run as the guide shows them. The create
	// command prints the stable collection ID, and each add command resolves
	// the collection name against the collection list.
	quickstartCLI := collectionCommand(t, env)

	res := quickstartCLI("collection", "create", quickstartNamespace+"/"+quickstartCollection)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "col-") {
		t.Fatalf("collection create: exit %d:\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	for _, tool := range []string{"pulumi", "opentofu"} {
		res = quickstartCLI("placement", "add", "--tool", tool, "--project", "*",
			"--stack", "*", "--collection", quickstartCollection, quickstartNamespace)
		if res.ExitCode != 0 || !strings.Contains(res.Stdout, "plr-") {
			t.Fatalf("placement add %s: exit %d:\n%s\n%s", tool, res.ExitCode, res.Stdout, res.Stderr)
		}
	}

	env.Step(ctx, t, step("stack init after the rule", nil, "stack", "init", stackRef, "--no-select"))

	quickstartTofu(ctx, t, env, tofuCLI)
}

// quickstartCollectionID reads the collection ID from the answer of the
// collection request.
func quickstartCollectionID(t *testing.T, answer string) string {
	t.Helper()

	var created struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(answer), &created); err != nil || created.ID == "" {
		t.Fatalf("the answer of the collection request holds no collection ID: %v (%s)", err, answer)
	}

	return created.ID
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

// quickstartPlacement makes the collection and the placement rules of the
// guide on a server at host with the admin token and the certificate file
// cert. The compose stack uses it, because its server runs in a container.
// The test sends the requests that the compose commands of the guide make,
// with the collection ID in place of the collection name.
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

	collections := "https://" + host + "/api/dustpan/v1/namespaces/" + composeNamespace + "/collections"
	rules := "https://" + host + "/api/dustpan/v1/namespaces/" + composeNamespace + "/placement-rules"

	answer := quickstartPost(ctx, t, client, quickstartPostCall{
		url:   collections,
		token: token,
		body:  `{"name":"` + quickstartCollection + `"}`,
	})
	collectionID := quickstartCollectionID(t, answer)

	for _, tool := range []string{"pulumi", "opentofu"} {
		quickstartPost(ctx, t, client, quickstartPostCall{
			url:   rules,
			token: token,
			body:  fmt.Sprintf(`{"tool":%q,"project":"*","stack":"*","collectionID":%q}`, tool, collectionID),
		})
	}
}

// quickstartPostCall is one POST request of the guide: the URL, the token and
// the JSON body.
type quickstartPostCall struct {
	url   string
	token string
	body  string
}

// quickstartPost sends one POST request of the guide. It fails unless the
// server answers 201, and returns the body of the answer.
func quickstartPost(ctx context.Context, t *testing.T, client *http.Client, call quickstartPostCall) string {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, call.url, strings.NewReader(call.body))
	if err != nil {
		t.Fatalf("build the request for %s: %v", call.url, err)
	}

	req.Header.Set("Authorization", "Bearer "+call.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", call.url, err)
	}

	status := resp.StatusCode
	answer, readErr := io.ReadAll(io.LimitReader(resp.Body, quickstartMaxAnswer))

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the answer of %s: %v", call.url, err)
	}

	if readErr != nil {
		t.Fatalf("read the answer of %s: %v", call.url, readErr)
	}

	if status != http.StatusCreated {
		t.Fatalf("POST %s answers %d, want %d (%s)", call.url, status, http.StatusCreated, answer)
	}

	return string(answer)
}
