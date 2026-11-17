//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// cliCollectionsNamespace is the namespace that the test makes in place of the
// namespace "acme" of the quickstart guide. The harness makes its own
// namespace with a catch-all placement rule, so a stack there would not meet a
// server as `serve` leaves it. This namespace has no rule.
const cliCollectionsNamespace = "quickcli"

// cliCollectionsCollection is the collection that the quickstart commands make.
const cliCollectionsCollection = "everything"

// cliCollectionsWorkspace is the OpenTofu workspace that the apply places.
const cliCollectionsWorkspace = "web"

// TestQuickstartCollectionCommands runs the collection and placement commands
// of the quickstart guide against a server that holds no placement rule for
// the namespace, with the admin token. It proves that the commands make the
// collection and the rule for each tool, that a rule with an unknown
// collection name makes no rule, and that a real OpenTofu apply places its
// workspace in the collection.
func TestQuickstartCollectionCommands(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// The OpenTofu CLI must trust the test server's certificate, and Go
		// honors SSL_CERT_FILE only on Linux.
		t.Skipf("the quickstart suite needs Linux, not %s", runtime.GOOS)
	}

	fetcher := sharedFetcher(t)

	tofuVersions := e2e.OpenTofu().Versions()

	if len(tofuVersions) == 0 {
		t.Fatal("OpenTofu needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersions[0])
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersions[0], err)
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runCollectionCommandsOnStore(t, tofuCLI, db)
		})
	}
}

func runCollectionCommandsOnStore(t *testing.T, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	ctx := t.Context()

	env := e2e.NewEnv(ctx, t, tofuCLI, store)

	checkReady(ctx, t, env)

	ns := cliCollectionsNamespace

	// The harness made its own namespace with a rule, so this test makes a
	// namespace of its own, as the guide makes "acme" with `namespace create`.
	status, answer, err := env.ServerAPIStatus(ctx, http.MethodPost,
		"/api/server/namespaces", `{"name":"`+ns+`"}`)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create the namespace: status %d, %v (%s)", status, err, answer)
	}

	cli := collectionCommand(t, env)

	cliCollectionsCreateAndList(t, cli, ns)
	ruleID := cliCollectionsAddRules(t, cli, ns)
	quickcliTofuApply(ctx, t, env, tofuCLI)
	cliCollectionsCheckPlacement(ctx, t, env, ns)
	cliCollectionsDeleteRule(t, cli, ns, ruleID)
}

// cliCollectionsCreateAndList runs the collection create and list commands of
// the guide. The create command prints the stable collection ID.
func cliCollectionsCreateAndList(t *testing.T, cli func(args ...string) e2e.BinaryResult, ns string) {
	t.Helper()

	res := cli("collection", "create", ns+"/"+cliCollectionsCollection)
	if res.ExitCode != 0 {
		t.Fatalf("collection create exits %d:\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "col-") {
		t.Fatalf("collection create prints no stable collection ID:\n%s", res.Stdout)
	}

	res = cli("collection", "list", ns)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "\t"+ns+"/"+cliCollectionsCollection+"\n") {
		t.Fatalf("collection list: exit %d, stdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// cliCollectionsAddRules refuses a rule with an unknown collection name, adds
// the two rules of the guide, one for each tool, and returns the ID of the
// first rule of the list.
func cliCollectionsAddRules(t *testing.T, cli func(args ...string) e2e.BinaryResult, ns string) string {
	t.Helper()

	// A rule with an unknown collection name makes no rule and names the
	// command that lists the collections.
	res := cli("placement", "add", "--tool", "opentofu", "--project", "*",
		"--stack", "*", "--collection", "absent", ns)
	if res.ExitCode == 0 {
		t.Fatalf("placement add with an unknown collection exits 0:\n%s", res.Stdout)
	}

	if !strings.Contains(res.Stderr, "collection does not exist") ||
		!strings.Contains(res.Stderr, "opendustpan collection list") {
		t.Fatalf("placement add with an unknown collection:\n%s\n%s", res.Stdout, res.Stderr)
	}

	// The two rules of the guide, one for each tool.
	for _, tool := range []string{"pulumi", "opentofu"} {
		res = cli("placement", "add", "--tool", tool, "--project", "*",
			"--stack", "*", "--collection", cliCollectionsCollection, ns)
		if res.ExitCode != 0 || !strings.Contains(res.Stdout, "plr-") {
			t.Fatalf("placement add %s: exit %d:\n%s\n%s", tool, res.ExitCode, res.Stdout, res.Stderr)
		}
	}

	res = cli("placement", "list", ns)
	if res.ExitCode != 0 || strings.Count(res.Stdout, "plr-") != 2 {
		t.Fatalf("placement list: exit %d, stdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	// Each line of the list prints position, rule ID, tool, project pattern,
	// stack pattern and collection ID.
	first := strings.TrimSpace(strings.SplitN(res.Stdout, "\n", 2)[0])
	fields := strings.SplitN(first, "\t", 3)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "plr-") {
		t.Fatalf("placement list prints no rule ID on its first line:\n%s", res.Stdout)
	}

	return fields[1]
}

// cliCollectionsCheckPlacement proves that the apply placed the workspace of
// the test in the collection.
func cliCollectionsCheckPlacement(ctx context.Context, t *testing.T, env *e2e.Env, ns string) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(ctx, http.MethodGet,
		"/api/dustpan/v1/namespaces/"+ns+"/units", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list the units: status %d, %v (%s)", status, err, body)
	}

	if !strings.Contains(body, `"name":"`+cliCollectionsWorkspace+`"`) ||
		!strings.Contains(body, `"collection":"`+cliCollectionsCollection+`"`) {
		t.Fatalf("the units list does not place %s in %s: %s", cliCollectionsWorkspace,
			cliCollectionsCollection, body)
	}
}

// cliCollectionsDeleteRule removes the rule by its ID and proves that the
// list no longer shows it. The workspace that the rule placed keeps its
// collection.
func cliCollectionsDeleteRule(
	t *testing.T, cli func(args ...string) e2e.BinaryResult, ns, ruleID string,
) {
	t.Helper()

	res := cli("placement", "delete", ns, ruleID)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "deleted placement rule "+ruleID) {
		t.Fatalf("placement delete: exit %d:\n%s\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	res = cli("placement", "list", ns)
	if res.ExitCode != 0 || strings.Contains(res.Stdout, ruleID) || strings.Count(res.Stdout, "plr-") != 1 {
		t.Fatalf("placement list after the delete: exit %d, stdout:\n%s\nstderr:\n%s",
			res.ExitCode, res.Stdout, res.Stderr)
	}
}

// collectionCommand returns a run of one opendustpan command against the
// server of env. The command reads the admin token from DUSTPAN_ADMIN_TOKEN
// in the child environment and trusts the server through --ca-file.
// The flags go after the two words of the command path and before the
// operands, where the flag set of the command reads them.
func collectionCommand(t *testing.T, env *e2e.Env) func(args ...string) e2e.BinaryResult {
	t.Helper()

	return func(args ...string) e2e.BinaryResult {
		t.Helper()

		withFlags := append([]string{}, args[:2]...)
		withFlags = append(withFlags, "--server", env.URL, "--ca-file", env.CABundle())
		withFlags = append(withFlags, args[2:]...)

		return env.RunBinaryWithEnv(t.Context(), t,
			[]string{"DUSTPAN_ADMIN_TOKEN=" + env.TokenA()}, withFlags...)
	}
}

// quickcliTofuApply runs the OpenTofu part of the quickstart for the
// namespace of the test: a main.tf with a cloud block for the workspace web,
// a credentials file, tofu init and tofu apply.
func quickcliTofuApply(ctx context.Context, t *testing.T, env *e2e.Env, tofuCLI e2e.Install) {
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
`, env.Host(), cliCollectionsNamespace, cliCollectionsWorkspace)

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
