//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

const (
	guidePage    = "docs/guides/private-registry.md"
	guideHeading = "## Publish a module"
)

// guideShBlock matches one sh code block of the guide.
var guideShBlock = regexp.MustCompile("(?s)```sh\n(.*?)```")

// guideCurl is one curl command of the guide, parsed but not changed.
type guideCurl struct {
	method, url, data, archive, token string
	header                            map[string]string
}

// TestPrivateModuleGuideCommands reads the sh blocks of the section "Publish a
// module" of the private registry guide, and sends the request of each curl
// command to the test server. The test changes only the host, the namespace,
// the token, the Idempotency-Key and the archive. A guide command that changes,
// or that the test does not know, fails the test.
func TestPrivateModuleGuideCommands(t *testing.T) {
	t.Parallel()

	tool := e2e.OpenTofu()

	if runtime.GOOS != "linux" {
		t.Skipf("the OpenTofu suite needs Linux, not %s", runtime.GOOS)
	}

	section := guideSection(t)

	var commands []guideCurl

	for _, block := range guideShBlock.FindAllStringSubmatch(section, -1) {
		for line := range strings.SplitSeq(strings.ReplaceAll(block[1], "\\\n", " "), "\n") {
			if strings.TrimSpace(line) != "" {
				commands = append(commands, parseGuideCurl(t, line))
			}
		}
	}

	if len(commands) != 4 {
		t.Fatalf("the section %q holds %d curl commands, want 4", guideHeading, len(commands))
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, label := range tool.Versions() {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
			defer cancel()

			cli, installErr := fetcher.Install(installCtx, tool, label)
			if installErr != nil {
				t.Fatalf("install %s %s: %v", tool.Name, label, installErr)
			}

			for _, db := range databases {
				t.Run(string(db), func(t *testing.T) {
					t.Parallel()

					if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
						t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
					}

					runGuideCommands(t, e2e.NewEnv(t.Context(), t, cli, db), commands)
				})
			}
		})
	}
}

// runGuideCommands sends each command and checks the answer. The upload answer
// gives the token that the publish command names.
func runGuideCommands(t *testing.T, env *e2e.Env, commands []guideCurl) {
	t.Helper()

	upload := ""

	for _, c := range commands {
		sub := strings.NewReplacer(
			"https://dustpan.example.com", "", "/namespaces/acme/", "/namespaces/"+e2e.Namespace+"/",
			"<token>", upload, "$(uuidgen)", e2e.NewIdempotencyKey(), "$DUSTPAN_TOKEN", env.TokenA(),
		).Replace

		body, url := sub(c.data), sub(c.url)
		if strings.ContainsAny(url+body, "$<>") || strings.Contains(url, "example.com") {
			t.Fatalf("the command %q holds a value that the test does not replace: %q %q", c.url, url, body)
		}

		if c.archive != "" {
			body = string(moduleTarGz(t, "from the guide"))
		}

		if got := sub(c.header["Authorization"]); got != "token "+env.TokenA() {
			t.Fatalf("authorization header of %q = %q, want the token scheme", c.url, got)
		}

		resp, err := env.ServerAPIStatusKeyed(t.Context(), e2e.TypedRequest{
			Token: env.TokenA(), Method: c.method, Path: url, ContentType: sub(c.header["Content-Type"]),
			Body: body, IfMatch: "",
		}, sub(c.header["Idempotency-Key"]))
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, url, err)
		}

		want := http.StatusOK
		if strings.HasSuffix(url, "/uploads") {
			want = http.StatusCreated
		}

		if resp.Status != want || (c.method == http.MethodGet && !strings.Contains(resp.Body, `"vpc"`)) {
			t.Fatalf("%s %s = %d %s, want %d", c.method, url, resp.Status, resp.Body, want)
		}

		if strings.HasSuffix(url, "/uploads") {
			var doc struct {
				Upload string `json:"upload"`
			}

			if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil || doc.Upload == "" {
				t.Fatalf("upload answer %q: %v", resp.Body, err)
			}

			upload = doc.Upload
		}
	}
}

// guideSection returns the section of the guide, up to the next section.
func guideSection(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile("../" + guidePage)
	if err != nil {
		t.Fatalf("read %s: %v", guidePage, err)
	}

	text := string(raw)

	start := strings.Index(text, "\n"+guideHeading+"\n")
	if start < 0 {
		t.Fatalf("%s has no section %q", guidePage, guideHeading)
	}

	text = text[start+1:]
	if end := strings.Index(text[1:], "\n## "); end >= 0 {
		text = text[:end+1]
	}

	return text
}

// parseGuideCurl splits one curl command of the guide. It knows only the
// options that the guide uses, and it fails on any other.
func parseGuideCurl(t *testing.T, line string) guideCurl {
	t.Helper()

	words, err := splitShell(line)
	if err != nil || len(words) < 2 || words[0] != "curl" {
		t.Fatalf("the guide line %q is not a curl command: %v", line, err)
	}

	c := guideCurl{method: http.MethodGet, header: map[string]string{}}

	for i := 1; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "https://") {
			c.url = w

			continue
		}

		if i+1 >= len(words) {
			t.Fatalf("the guide option %q of %q has no value", w, line)
		}

		i++

		switch v := words[i]; w {
		case "-X":
			c.method = v
		case "-H":
			k, val, _ := strings.Cut(v, ": ")
			c.header[k] = val
		case "-d":
			c.data = v
		case "--data-binary":
			c.archive = strings.TrimPrefix(v, "@")
		default:
			t.Fatalf("the guide option %q of %q is not known to the test", w, line)
		}
	}

	if c.url == "" {
		t.Fatalf("the guide command %q has no address", line)
	}

	return c
}

// splitShell splits a line into words with single and double quotes.
func splitShell(line string) ([]string, error) {
	var (
		words []string
		cur   strings.Builder
		quote rune
		open  bool
	)

	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, open = r, true
		case r == ' ':
			if cur.Len() > 0 || open {
				words = append(words, cur.String())
				cur.Reset()

				open = false
			}
		default:
			cur.WriteRune(r)
		}
	}

	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote %q", quote)
	}

	if cur.Len() > 0 || open {
		words = append(words, cur.String())
	}

	return words, nil
}
