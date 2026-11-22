package cli_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

const (
	collectionsPath  = "/api/dustpan/v1/namespaces/acme/collections"
	testCollectionID = "col-0123456789abcdefghij"
	testRuleID       = "plr-0123456789abcdefghij"
	collectionAnswer = `{"id":"` + testCollectionID + `","namespace":"acme","name":"prod"}`
	collectionList   = `{"collections":[` + collectionAnswer + `]}`
)

type commandAnswer struct {
	method string
	path   string
	body   string
	status int
	answer string
}

type commandResult struct {
	Output string
	Error  string
}

// commandRuns answers the run function of each command under test.
func commandRuns() map[string]cli.RunFunc {
	return map[string]cli.RunFunc{
		"collection create": cli.CollectionCreate,
		"collection list":   cli.CollectionList,
		"placement add":     cli.PlacementAdd,
		"placement list":    cli.PlacementList,
		"placement delete":  cli.PlacementDelete,
		"runner pools list": cli.RunnerPoolsList,
	}
}

// setTokenEnv sets the admin token variables of the process for one token
// source.
func setTokenEnv(t *testing.T, source string) {
	t.Helper()
	switch source {
	case "variable":
		t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token,second-token")
	case "file":
		t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", writeTokenFile(t, "test-token\nsecond-token\n", 0o600))
	case "public file":
		t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", writeTokenFile(t, "test-token\n", 0o644))
	case "line break":
		t.Setenv("DUSTPAN_ADMIN_TOKEN", "test\ntoken")
	case "":
		// The command must refuse a run without a token, so no variable
		// holds one.
	default:
		t.Fatal("unknown token source")
	}
}

// writeTokenFile writes content to a new file with mode and returns its path.
// The mode of the case travels in a variable, as the admin token tests of the
// binary write their fixtures.
func writeTokenFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "token")

	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}

	// WriteFile applies the umask; set the mode the case names.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}

	return path
}

// runCommand runs one command in the process with the token source set and
// answers what it printed and the text of its error.
func runCommand(t *testing.T, command string, args []string, tokenSource string) commandResult {
	t.Helper()

	setTokenEnv(t, tokenSource)

	run, ok := commandRuns()[command]
	if !ok {
		t.Fatal("unknown test command")
	}

	var stdout, stderr bytes.Buffer
	err := run(t.Context(), args, &stdout, &stderr)
	result := commandResult{Output: stdout.String(), Error: stderr.String()}

	if err != nil {
		result.Error = err.Error()
	}

	return result
}

func serveCollectionCommands(t *testing.T, answers []commandAnswer) *httptest.Server {
	t.Helper()

	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if count >= len(answers) {
			t.Error("unexpected request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		want := answers[count]
		count++
		checkCollectionRequest(t, r, want)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(want.status)
		_, writeErr := io.WriteString(w, want.answer)
		_ = writeErr // The client of an oversized answer closes early on purpose; the test asserts the refusal.
	}))
	t.Cleanup(func() {
		srv.Close()
		mu.Lock()
		defer mu.Unlock()
		if count != len(answers) {
			t.Errorf("got %d requests, want %d", count, len(answers))
		}
	})
	return srv
}

func checkCollectionRequest(t *testing.T, r *http.Request, want commandAnswer) {
	t.Helper()
	if r.Method != want.method || r.URL.EscapedPath() != want.path || r.URL.RawQuery != "" {
		t.Errorf("request = %s %s, want %s %s", r.Method, r.URL, want.method, want.path)
	}
	if r.Header.Get("Authorization") != "Bearer test-token" {
		t.Error("request did not use the first admin token")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	if string(body) != want.body {
		t.Errorf("body = %s, want %s", body, want.body)
	}
	if want.body != "" && r.Header.Get("Content-Type") != "application/json" {
		t.Error("request has no JSON content type")
	}
}

func checkCollectionResult(t *testing.T, got commandResult, output, errorText string) {
	t.Helper()
	if got.Output != output {
		t.Errorf("output = %q, want %q", got.Output, output)
	}
	if errorText == "" && got.Error != "" || errorText != "" && !strings.Contains(got.Error, errorText) {
		t.Errorf("error = %q, want %q", got.Error, errorText)
	}
}

func TestCollectionCommands(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	cases := []struct {
		name    string
		command string
		operand string
		answer  commandAnswer
		output  string
	}{
		{
			name: "create", command: "collection create", operand: "acme/prod",
			answer: commandAnswer{"POST", collectionsPath, `{"name":"prod"}`, 201, collectionAnswer},
			output: "created collection acme/prod\t" + testCollectionID + "\n",
		},
		{
			name: "list", command: "collection list", operand: "acme",
			answer: commandAnswer{"GET", collectionsPath, "", 200, collectionList},
			output: testCollectionID + "\tacme/prod\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, source := range []string{"variable", "file", ""} {
				t.Run("token "+source, func(t *testing.T) {
					t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
					t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
					answers := []commandAnswer{tc.answer}
					wantOut, wantErr := tc.output, ""
					if source == "" {
						answers, wantOut, wantErr = nil, "", "no admin token is set"
					}
					srv := serveCollectionCommands(t, answers)
					got := runCommand(t, tc.command, []string{"--server", srv.URL, tc.operand}, source)
					checkCollectionResult(t, got, wantOut, wantErr)
				})
			}
		})
	}
}

func TestCollectionCreateRejectsBadOperands(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, operand := range []string{
		"acme", "/prod", "acme/", "acme/prod/extra", "../prod", "acme/../prod",
		"acme/prod\n", "acme/ prod", "acme%2fother/prod",
	} {
		t.Run(operand, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			srv := serveCollectionCommands(t, nil)
			got := runCommand(t, "collection create", []string{"--server", srv.URL, operand}, "variable")
			checkCollectionResult(t, got, "", "opendustpan collection create")
		})
	}
}

func TestCollectionCommandsRejectBadAnswers(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, tc := range []struct {
		name    string
		status  int
		answer  string
		wantErr string
	}{
		{
			"server error code", 409, `{"message":"collection exists","errorCode":"collection.already-exists"}`,
			"collection.already-exists",
		},
		{"missing ID", 201, `{"namespace":"acme","name":"prod"}`, "no stable collection ID"},
		{"bad JSON", 201, `{`, "decode server answer"},
		{"oversized", 201, strings.Repeat("x", 1<<20+1), "over"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			srv := serveCollectionCommands(t, []commandAnswer{
				{"POST", collectionsPath, `{"name":"prod"}`, tc.status, tc.answer},
			})
			got := runCommand(t, "collection create", []string{"--server", srv.URL, "acme/prod"}, "variable")
			checkCollectionResult(t, got, "", tc.wantErr)
		})
	}
}

func TestCollectionListPrintsServerCode(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	srv := serveCollectionCommands(t, []commandAnswer{
		{"GET", collectionsPath, "", 404, `{"message":"namespace not found","errorCode":"namespace.not-found"}`},
	})
	got := runCommand(t, "collection list", []string{"--server", srv.URL, "acme"}, "variable")
	checkCollectionResult(t, got, "", "404 Not Found: namespace.not-found")
}

func TestCollectionCommandsRefuseRedirects(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, status := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			destination := serveCollectionCommands(t, nil)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL+"/other", status)
			}))
			t.Cleanup(srv.Close)
			got := runCommand(t, "collection list", []string{"--server", srv.URL, "acme"}, "variable")
			checkCollectionResult(t, got, "", http.StatusText(status))
		})
	}
}

func TestCollectionCommandsCheckServerURL(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, server := range []string{
		"", "not-a-url", "ftp://localhost", "http://localhost?query=x",
		"http://localhost#fragment",
	} {
		t.Run(server, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			got := runCommand(t, "collection list", []string{"--server", server, "acme"}, "variable")
			checkCollectionResult(t, got, "", "opendustpan collection list")
		})
	}
}

func TestCollectionCommandsRejectUnsafeTokens(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, source := range []string{"public file", "line break"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			srv := serveCollectionCommands(t, nil)
			got := runCommand(t, "collection list", []string{"--server", srv.URL, "acme"}, source)
			if got.Error == "" || got.Output != "" {
				t.Fatal("unsafe token did not stop the command")
			}
		})
	}
}

func TestCollectionErrorsContainNoControlCharacters(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	srv := serveCollectionCommands(t, []commandAnswer{
		{"GET", collectionsPath, "", 403, `{"message":"bad\r\n\u001bname","errorCode":"access.\r\ndenied"}`},
	})
	got := runCommand(t, "collection list", []string{"--server", srv.URL, "acme"}, "variable")
	checkCollectionResult(t, got, "", "access.denied")
	if strings.ContainsAny(got.Error, "\r\n\x1b") {
		t.Fatal("server error added a control character to the terminal output")
	}
}

func TestCollectionErrorStatusContainsNoControlCharacters(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
	t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")

	for _, body := range []string{"not JSON", `{"message":"denied"}`, `{"errorCode":"access.denied","message":"denied"}`} {
		t.Run(body, func(t *testing.T) {
			t.Setenv("DUSTPAN_ADMIN_TOKEN", "")
			t.Setenv("DUSTPAN_ADMIN_TOKEN_FILE", "")
			srv := serveCollectionErrorStatus(t, body)
			got := runCommand(t, "collection list", []string{"--server", srv.URL, "acme"}, "variable")
			checkCollectionResult(t, got, "", "403")
			if strings.ContainsAny(got.Error, "\r\n\x1b") {
				t.Fatal("server status added a control character to the terminal output")
			}
		})
	}
}

func serveCollectionErrorStatus(t *testing.T, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
		_, err = fmt.Fprintf(conn,
			"HTTP/1.1 403 Forbidden\x1b[31m\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			len(body), body)
		if err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
