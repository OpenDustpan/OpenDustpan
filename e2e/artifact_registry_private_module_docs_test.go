//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
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

// errGuideModuleMissing means that the answer of a guide command does not name the module.
var errGuideModuleMissing = errors.New("the answer does not name the module vpc")

// guideShBlock matches one sh code block of the guide.
var guideShBlock = regexp.MustCompile("(?s)```sh\n(.*?)```")

// guideWord matches one word of a command: a double-quoted word, a
// single-quoted word or a bare word.
var guideWord = regexp.MustCompile(`"([^"]*)"|'([^']*)'|(\S+)`)

// guideCurl is one curl command of the guide, parsed but not changed.
type guideCurl struct {
	method, url, data, archive string
	header                     map[string]string
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

	commands := guideCommands(t)
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
					runGuideOnStore(t, cli, db, commands)
				})
			}
		})
	}
}

// runGuideOnStore starts a server on the store and sends each command.
func runGuideOnStore(t *testing.T, cli e2e.Install, db e2e.Store, commands []guideCurl) {
	t.Helper()

	if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	env := e2e.NewEnv(t.Context(), t, cli, db)
	upload := ""

	for _, c := range commands {
		upload = sendGuideCommand(t, env, c, upload)
	}
}

// guideTokenVariable returns the shell variable that the guide puts after the
// scheme word of the Authorization header of c. The test reads it from the
// command, so that no literal here looks like a server setting.
func guideTokenVariable(t *testing.T, c guideCurl) string {
	t.Helper()

	tokenVar, hasScheme := strings.CutPrefix(c.header["Authorization"], "token ")
	if !hasScheme || !strings.HasPrefix(tokenVar, "$") {
		t.Fatalf("the authorization header of %q is not the token scheme with a variable", c.url)
	}

	return tokenVar
}

// sendGuideCommand sends the request of c and checks the answer. It returns
// the upload token that the answer holds, or upload when it holds none.
func sendGuideCommand(t *testing.T, env *e2e.Env, c guideCurl, upload string) string {
	t.Helper()

	tokenVar := guideTokenVariable(t, c)

	sub := strings.NewReplacer(
		"https://dustpan.example.com", "", "/namespaces/acme/", "/namespaces/"+e2e.Namespace+"/",
		"<token>", upload, "$(uuidgen)", e2e.NewIdempotencyKey(), tokenVar, env.TokenA(),
	).Replace

	body, path := sub(c.data), sub(c.url)
	if strings.ContainsAny(path+body, "$<>") || strings.Contains(path, "example.com") {
		t.Fatalf("the command %q holds a value that the test does not replace: %q %q", c.url, path, body)
	}

	if c.archive != "" {
		body = string(moduleTarGz(t, "from the guide"))
	}

	headers := make(map[string]string, len(c.header))
	for name, value := range c.header {
		headers[name] = sub(value)
	}

	if got := headers["Authorization"]; got != "token "+env.TokenA() {
		t.Fatalf("the authorization header of %q is not the token scheme", c.url)
	}

	// Send the headers of the guide as written: the credential scheme, the
	// media type and the Idempotency-Key come from the command.
	isUpload := strings.HasSuffix(path, "/uploads")
	want := http.StatusOK

	if isUpload {
		want = http.StatusCreated
	}

	var answer string

	env.APIStepWithHeaders(t.Context(), t, e2e.APIStepSpec{
		Name:      c.method + " " + path,
		Method:    c.method,
		Path:      path,
		Body:      body,
		Want:      want,
		UserAgent: "",
		Scheme:    "token",
		Check: func(got string) error {
			answer = got

			if !isUpload && c.method == http.MethodGet && !strings.Contains(got, `"vpc"`) {
				return fmt.Errorf("answer %q: %w", got, errGuideModuleMissing)
			}

			return nil
		},
	}, headers)

	if !isUpload {
		return upload
	}

	return uploadToken(t, answer)
}

// uploadToken returns the upload token of an upload answer.
func uploadToken(t *testing.T, body string) string {
	t.Helper()

	var doc struct {
		Upload string `json:"upload"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Upload == "" {
		t.Fatalf("upload answer %q: %v", body, err)
	}

	return doc.Upload
}

// guideCommands returns the curl commands of the sh blocks of the section.
func guideCommands(t *testing.T) []guideCurl {
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

	var commands []guideCurl

	for _, block := range guideShBlock.FindAllStringSubmatch(text, -1) {
		for line := range strings.SplitSeq(strings.ReplaceAll(block[1], "\\\n", " "), "\n") {
			if strings.TrimSpace(line) != "" {
				commands = append(commands, parseGuideCurl(t, line))
			}
		}
	}

	if len(commands) != 4 {
		t.Fatalf("the section %q holds %d curl commands, want 4", guideHeading, len(commands))
	}

	return commands
}

// parseGuideCurl splits one curl command of the guide. It knows only the
// options that the guide uses, and it fails on any other.
func parseGuideCurl(t *testing.T, line string) guideCurl {
	t.Helper()

	words := make([]string, 0, 12)

	for _, m := range guideWord.FindAllStringSubmatch(line, -1) {
		words = append(words, m[1]+m[2]+m[3])
	}

	if len(words) < 2 || words[0] != "curl" || strings.ContainsAny(line, "`") {
		t.Fatalf("the guide line %q is not a curl command", line)
	}

	c := guideCurl{method: http.MethodGet, url: "", data: "", archive: "", header: map[string]string{}}

	for i := 1; i < len(words); i++ {
		if strings.HasPrefix(words[i], "https://") {
			c.url = words[i]

			continue
		}

		if i+1 >= len(words) {
			t.Fatalf("the guide option %q of %q has no value", words[i], line)
		}

		if !c.setOption(words[i], words[i+1]) {
			t.Fatalf("the guide option %q of %q is not known to the test", words[i], line)
		}

		i++
	}

	if c.url == "" {
		t.Fatalf("the guide command %q has no address", line)
	}

	return c
}

// setOption stores one curl option and reports whether the test knows it.
func (c *guideCurl) setOption(option, value string) bool {
	switch option {
	case "-X":
		c.method = value
	case "-H":
		k, v, ok := strings.Cut(value, ": ")
		if !ok || k == "" {
			return false
		}

		c.header[k] = v
	case "-d":
		c.data = value
	case "--data-binary":
		c.archive = strings.TrimPrefix(value, "@")
	default:
		return false
	}

	return true
}

// TestGuideCurlSetOption checks that the parser refuses a -H value without
// the ": " separator, so that no request goes out without its header.
func TestGuideCurlSetOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, option, value string
		want                bool
	}{
		{"header with separator", "-H", "Content-Type: application/json", true},
		{"header without separator", "-H", "Content-Type application/json", false},
		{"header with colon and no space", "-H", "Content-Type:application/json", false},
		{"header with empty name", "-H", ": application/json", false},
		{"unknown option", "--data-raw", "x", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := guideCurl{method: http.MethodGet, url: "", data: "", archive: "", header: map[string]string{}}
			if got := c.setOption(tt.option, tt.value); got != tt.want {
				t.Fatalf("setOption(%q, %q) = %v, want %v", tt.option, tt.value, got, tt.want)
			}
		})
	}
}
