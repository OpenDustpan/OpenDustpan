//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// auditListDocsPage is the guide that this step runs.
const auditListDocsPage = "docs/guides/audit-log.md"

// TestE2EAuditListDocsPage proves that the paging loop of the audit guide
// runs as written, and that the three errors of the guide answer with their
// codes. The loop runs with the address and the token of the test server, and
// with a page of one entry, so that a log of a few entries needs more than one
// page.
func TestE2EAuditListDocsPage(t *testing.T) {
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

			auditListDocsScenario(t.Context(), t, env)
		})
	}
}

// auditListDocsScenario runs the loop and the error requests of the page.
func auditListDocsScenario(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	loop := auditListDocsLoop(t)

	env.PrepareFreshAdmin(ctx, t)

	want, err := fetchAuditEntries(ctx, env, "@server")
	if err != nil {
		t.Fatalf("list @server audit entries: %v", err)
	}

	if len(want) < 2 {
		t.Fatalf("the log of @server holds %d entries, want 2 or more to need two pages", len(want))
	}

	script := strings.NewReplacer(
		"https://dustpan.example", env.URL,
		"organization=acme", "organization=@server",
		"pageSize=200", "pageSize=1",
	).Replace(loop)

	cmd := exec.CommandContext(ctx, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "DUSTPAN_ADMIN_TOKEN="+env.AdminToken(), "CURL_CA_BUNDLE="+env.CABundle())

	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("the loop of the page failed: %v: %s", runErr, out)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(want) {
		t.Fatalf("the loop wrote %d entries, want %d: %s", len(lines), len(want), out)
	}

	for _, line := range lines {
		var entry auditListItem
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Sequence == 0 {
			t.Fatalf("a line of the loop is not an entry: %v: %s", err, line)
		}
	}

	checkAuditListDocsErrors(ctx, t, env)
}

// auditListDocsLoop returns the paging loop of the page: the shell block that
// starts with token="".
func auditListDocsLoop(t *testing.T) string {
	t.Helper()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	data, readErr := root.ReadFile(auditListDocsPage)
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", auditListDocsPage, readErr, closeErr)
	}

	_, rest, ok := strings.Cut(string(data), "```sh\ntoken=\"\"\n")
	if !ok {
		t.Fatalf("%s holds no paging loop", auditListDocsPage)
	}

	body, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatalf("the paging loop of %s has no end", auditListDocsPage)
	}

	return "token=\"\"\n" + body
}

// checkAuditListDocsErrors sends the requests of the error table of the page
// and checks the code of each answer and that the text names what to send.
func checkAuditListDocsErrors(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	cases := []struct {
		query string
		code  string
	}{
		{"organization=@server&pageSize=201", "request.page-size-invalid"},
		{"organization=@server&pageToken=not-a-token", "request.page-token-invalid"},
		{"organization=@server&page[number]=1", "request.invalid-query"},
	}

	for _, tc := range cases {
		status, body, err := env.APIStatus(ctx, http.MethodGet, "/api/audit/entries?"+tc.query, "")
		if err != nil || status != http.StatusBadRequest {
			t.Fatalf("GET ?%s answers %d, %v: %s, want 400", tc.query, status, err, body)
		}

		var answer struct {
			ErrorCode string `json:"errorCode"`
			Message   string `json:"message"`
		}

		if err := json.Unmarshal([]byte(body), &answer); err != nil || answer.ErrorCode != tc.code ||
			!strings.Contains(answer.Message, "send") {
			t.Fatalf("GET ?%s answers %q %q (%v), want %s and a text that says what to send",
				tc.query, answer.ErrorCode, answer.Message, err, tc.code)
		}
	}
}
