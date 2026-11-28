//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// agentConnectionDocsPage is the guide that this step runs.
const agentConnectionDocsPage = "docs/guides/agent-connection.md"

// agentDocsStep is one curl command of the guide with the status that the
// guide says it answers.
type agentDocsStep struct {
	name   string
	marker string
	status string
}

// TestE2EAgentConnectionDocsPage proves that each curl command of the guide
// runs as written, in the order of the page, against the test server, and
// that each answers the status that the page names.
func TestE2EAgentConnectionDocsPage(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			runAgentConnectionDocs(t.Context(), t, env)
		})
	}
}

// runAgentConnectionDocs runs the commands of the page in order.
func runAgentConnectionDocs(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	commands := agentDocsCommands(t)

	steps := []agentDocsStep{
		{"store a key", "-X POST", "201"},
		{"rotate a key", "<the new key>", "200"},
		{"read the items", "\"$SERVER/api/agent/secrets\"", "200"},
		{"read the connection", "/api/agent/connection\"", "404"},
		{"read the formats", "/api/agent/formats", "200"},
		{"read the allowlist", "\"$SERVER/api/server/egress-allowlist\"", "200"},
		{"replace the allowlist", "models.example.com", "200"},
		{"disconnect", "-X DELETE -H \"Authorization: Bearer $TOKEN\" \"$SERVER/api/agent/connection\"", "404"},
		{"delete the item", "\"$SERVER/api/agent/secrets/itm-abc1\"", "204"},
	}

	if len(commands) != len(steps) {
		t.Fatalf("%s holds %d curl commands, want %d", agentConnectionDocsPage, len(commands), len(steps))
	}

	ref := "itm-abc1"

	for i, step := range steps {
		if !strings.Contains(commands[i], step.marker) {
			t.Fatalf("command %d (%s) of the page does not hold %q:\n%s", i+1, step.name, step.marker, commands[i])
		}

		out := runAgentDocsCommand(ctx, t, env, strings.ReplaceAll(commands[i], "itm-abc1", ref))

		answer := splitAgentDocsAnswer(t, step.name, out)
		body := answer.body

		if answer.status != step.status {
			t.Fatalf("%s: status %s, want %s: %s", step.name, answer.status, step.status, body)
		}

		if i == 0 {
			var stored struct {
				Ref string `json:"ref"`
			}

			if err := json.Unmarshal([]byte(body), &stored); err != nil || stored.Ref == "" {
				t.Fatalf("%s: the answer holds no reference: %v: %s", step.name, err, body)
			}

			ref = stored.Ref
		}
	}
}

// agentDocsCommands returns each fenced block of the page that starts with
// curl, in the order of the page.
func agentDocsCommands(t *testing.T) []string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	data, readErr := root.ReadFile(agentConnectionDocsPage)
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", agentConnectionDocsPage, readErr, closeErr)
	}

	var out []string

	parts := strings.Split(string(data), "```")
	for i := 1; i < len(parts); i += 2 {
		block := strings.TrimSpace(parts[i])
		if strings.HasPrefix(block, "curl ") {
			out = append(out, block)
		}
	}

	return out
}

// runAgentDocsCommand runs one command of the page with the address and the
// token of the test server. It asks curl to print the status after the body.
func runAgentDocsCommand(ctx context.Context, t *testing.T, env *e2e.Env, command string) string {
	t.Helper()

	script := strings.Replace(command, "curl ", "curl -s -w '\\n%{http_code}' ", 1)

	cmd := exec.CommandContext(ctx, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(),
		"TOKEN="+env.AdminToken(), "SERVER="+env.URL, "CURL_CA_BUNDLE="+env.CABundle())

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the command of the page failed: %v: %s\n%s", err, out, command)
	}

	return string(out)
}

// splitAgentDocsAnswer splits the output of a command into the body and the
// status line.
func splitAgentDocsAnswer(t *testing.T, step, out string) agentDocsAnswer {
	t.Helper()

	trimmed := strings.TrimRight(out, "\n")

	cut := strings.LastIndex(trimmed, "\n")
	if cut < 0 || len(trimmed)-cut-1 != len("200") {
		t.Fatalf("%s: the output does not end with a status line: %s", step, out)
	}

	return agentDocsAnswer{body: trimmed[:cut], status: trimmed[cut+1:]}
}

// agentDocsAnswer is the body and the status line of one answer.
type agentDocsAnswer struct {
	body   string
	status string
}
