//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const quickstartNamespace = "quickstart"

// TestQuickstartCollectionCommands runs the documented commands in a namespace
// with no seeded collection or placement rule, then applies with OpenTofu.
func TestQuickstartCollectionCommands(t *testing.T) {
	t.Parallel()

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatal(err)
	}
	tool := e2e.OpenTofu()
	run(t, runInput{
		fetcher: sharedFetcher(t), tool: tool, label: tool.Single(),
		databases: databases, scenario: quickstartCollectionCommands,
	})
}

func quickstartCollectionCommands(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	checkReady(ctx, t, env)
	for _, args := range quickstartSetupCommands(t, env) {
		result := env.RunBinary(ctx, t, args...)
		if result.ExitCode != 0 {
			t.Fatalf("quickstart %s exits %d: %s", strings.Join(args, " "), result.ExitCode, result.Stderr)
		}
		if args[0] == "collection" && !strings.Contains(result.Stdout, "col-") {
			t.Fatal("collection create printed no stable collection ID")
		}
	}
	list := quickstartClientCommand(t, env, "collection", "list", quickstartNamespace)
	if !strings.Contains(list, quickstartNamespace+"/everything") {
		t.Fatal("collection list does not name the quickstart collection")
	}
	rules := quickstartClientCommand(t, env, "placement", "list", quickstartNamespace)
	if !strings.Contains(rules, "pulumi") || !strings.Contains(rules, "opentofu") {
		t.Fatal("placement list does not name both tools")
	}

	// The namespace create command does not seed a rule. OpenTofu can make
	// the workspace only if the documented add command made its rule.
	env.WriteTofuConfig(t, fmt.Sprintf(`terraform {
  cloud {
    hostname = %s
    organization = %s
    workspaces { name = "web" }
  }
}
output "greeting" { value = "hello" }
`, strconv.Quote(env.Host()), strconv.Quote(quickstartNamespace)))
	env.Step(ctx, t, step("quickstart tofu init", nil, "init", "-input=false", "-no-color"))
	env.Step(ctx, t, step("quickstart tofu apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("quickstart tofu output", contains("hello"), "output", "-raw", "greeting"))
	checkQuickstartCollection(t, env)

	for line := range strings.SplitSeq(strings.TrimSpace(rules), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			t.Fatalf("placement list has %d columns, want 6", len(fields))
		}
		quickstartClientCommand(t, env, "placement", "delete", quickstartNamespace, fields[1])
	}
	if got := quickstartClientCommand(t, env, "placement", "list", quickstartNamespace); got != "" {
		t.Fatal("placement delete left a rule")
	}
	checkQuickstartCollection(t, env)
}

// quickstartSetupCommands reads the one-process commands from the guide.
// Only the server URL, certificate path and isolated namespace change.
func quickstartSetupCommands(t *testing.T, env *e2e.Env) [][]string {
	t.Helper()

	path := filepath.Join(repoRoot(t), "docs", "guides", "quickstart.md")
	//nolint:gosec // The guide path is fixed under the repository, not supplied by a user.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	section, _, ok := strings.Cut(string(raw), "## Compose stack")
	if !ok {
		t.Fatal("quickstart has no compose heading")
	}
	section = strings.ReplaceAll(section, "\\\n", "")
	var commands [][]string
	for line := range strings.SplitSeq(section, "\n") {
		if !strings.HasPrefix(line, "./bin/opendustpan ") {
			continue
		}
		args := strings.Fields(strings.TrimPrefix(line, "./bin/opendustpan "))
		switch args[0] {
		case "namespace", "collection", "placement":
			commands = append(commands, quickstartCommandArgs(env, args))
		default:
		}
	}
	if len(commands) != 4 {
		t.Fatalf("quickstart has %d setup commands, want 4", len(commands))
	}
	return commands
}

func quickstartCommandArgs(env *e2e.Env, args []string) []string {
	for i, arg := range args {
		switch arg {
		case "https://localhost:8080":
			args[i] = env.URL
		case "data/cert.pem":
			args[i] = filepath.Join(env.Home(), "server.pem")
		case "acme":
			args[i] = quickstartNamespace
		case "acme/everything":
			args[i] = quickstartNamespace + "/everything"
		default:
			args[i] = strings.Trim(arg, "'")
		}
	}
	return args
}

func quickstartClientCommand(t *testing.T, env *e2e.Env, group, action string, operands ...string) string {
	t.Helper()

	args := make([]string, 0, 6+len(operands))
	args = append(args, group, action, "--server", env.URL, "--ca-file", filepath.Join(env.Home(), "server.pem"))
	result := env.RunBinary(t.Context(), t, append(args, operands...)...)
	if result.ExitCode != 0 {
		t.Fatalf("%s %s exits %d: %s", group, action, result.ExitCode, result.Stderr)
	}
	return result.Stdout
}

func checkQuickstartCollection(t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.ServerAPIStatus(t.Context(), http.MethodGet,
		"/api/dustpan/v1/namespaces/"+quickstartNamespace+"/units", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read quickstart workspaces: status %d: %v", status, err)
	}
	var doc placementUnitList
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	for _, unit := range doc.Units {
		if unit.Reference == "opentofu:"+quickstartNamespace+"/web" && unit.Collection == "everything" {
			return
		}
	}
	t.Fatal("the quickstart collection does not hold the workspace")
}
