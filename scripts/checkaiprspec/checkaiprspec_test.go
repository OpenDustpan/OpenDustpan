package checkaiprspec_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixLine is the last line of the output when the check fails. It names the
// one fix. The script writes it after the lines that describe the links that
// did not count.
const fixLine = "check-ai-pr-spec: the pull request names no spec-ready change: add a line " +
	"\"Change: openspec/changes/<id>\" for a change whose proposal says \"Open questions: none\".\n"

// proposalReady is the proposal of a spec-ready change in the long form: the
// first line of the section "## Open questions" reads "none".
const proposalReady = "# A change\n\nProblem: a helper is missing.\n\n" +
	"## Open questions\n\nnone\n\nQuestions considered and answered:\n\n- None.\n"

// proposalNotReady is the proposal of a change that is not spec-ready: the
// section "## Open questions" names a question.
const proposalNotReady = "# A change\n\n## Open questions\n\n- Which store holds the value?\n"

// proposalShortReady is the short form of a spec-ready proposal: the line
// "Open questions: none".
const proposalShortReady = "# A change\n\nProblem: a helper is missing.\n\nOpen questions: none\n"

// The author email of an agent commit and the author email of a person.
const (
	agentEmail  = "noreply@anthropic.com\n"
	personEmail = "person@example.com\n"
)

// fixture holds the body file, the commit file and the change folders of one
// case. Each key of files is a path under the change folder.
type fixture struct {
	body    string
	commits string
	files   map[string]string
}

// checkCase is one row of the table. check asserts the exit code and the
// output of the script.
type checkCase struct {
	name    string
	fixture fixture
	check   func(t *testing.T, code int, stderr string)
}

func TestCheckAIPRSpec(t *testing.T) {
	t.Parallel()

	tests := []checkCase{
		{
			name: "agent commit and a spec-ready link",
			fixture: fixture{
				body:    "Change: openspec/changes/ready-change\n",
				commits: agentEmail,
				files:   map[string]string{"ready-change/proposal.md": proposalReady},
			},
			check: wantPass,
		},
		{
			name: "disclosure line and a spec-ready link",
			fixture: fixture{
				body:    "Tool: Claude\n\nChange: openspec/changes/ready-change\n",
				commits: personEmail,
				files:   map[string]string{"ready-change/proposal.md": proposalReady},
			},
			check: wantPass,
		},
		{
			name: "short form proposal",
			fixture: fixture{
				body:    "Change: openspec/changes/short-change\n",
				commits: agentEmail,
				files:   map[string]string{"short-change/proposal.md": proposalShortReady},
			},
			check: wantPass,
		},
		{
			name: "archived change",
			fixture: fixture{
				body:    "Change: openspec/changes/old-change\n",
				commits: agentEmail,
				files:   map[string]string{"archive/2026-11-01-old-change/proposal.md": proposalReady},
			},
			check: wantPass,
		},
		{
			name: "person pull request with no link",
			fixture: fixture{
				body:    "Adds a helper.\n",
				commits: personEmail,
				files:   nil,
			},
			check: wantPass,
		},
		{
			name: "the word tool in a sentence is no disclosure",
			fixture: fixture{
				body:    "This tool is part of the system.\n",
				commits: personEmail,
				files:   nil,
			},
			check: wantPass,
		},
		{
			name: "agent commit with no link",
			fixture: fixture{
				body:    "Adds a helper.\n",
				commits: agentEmail,
				files:   nil,
			},
			check: wantFix,
		},
		{
			name: "disclosure line with no link",
			fixture: fixture{
				body:    "AI tool: Claude\n\nAdds a helper.\n",
				commits: personEmail,
				files:   nil,
			},
			check: wantFix,
		},
		{
			// The disclosure line comes first, so grep finds it in its first
			// read and exits. A pipe would give the writer SIGPIPE on a body
			// over the pipe buffer, under pipefail the pipeline would fail,
			// and the signal would be lost: an AI-authored pull request would
			// pass. The filler is 520 KB, over the pipe buffer of the test
			// host (a 78 KB body did not fill it).
			name: "disclosure line in a body over 64 KiB",
			fixture: fixture{
				body:    "AI tool: Claude\n\n" + strings.Repeat("Filler line with no link.\n", 20000),
				commits: personEmail,
				files:   nil,
			},
			check: wantFix,
		},
		{
			name: "link only inside a fenced code block",
			fixture: fixture{
				body:    "A fence:\n\n```\nChange: openspec/changes/ready-change\n```\n",
				commits: agentEmail,
				files:   map[string]string{"ready-change/proposal.md": proposalReady},
			},
			check: wantFix,
		},
		{
			name: "link only inside a tilde fence",
			fixture: fixture{
				body:    "A fence:\n\n~~~\nChange: openspec/changes/ready-change\n~~~\n",
				commits: agentEmail,
				files:   map[string]string{"ready-change/proposal.md": proposalReady},
			},
			check: wantFix,
		},
		{
			name: "link after a tilde fence closes",
			fixture: fixture{
				body:    "~~~\nCode, not prose.\n~~~\n\nChange: openspec/changes/ready-change\n",
				commits: agentEmail,
				files:   map[string]string{"ready-change/proposal.md": proposalReady},
			},
			check: wantPass,
		},
		{
			name: "linked change is not spec-ready",
			fixture: fixture{
				body:    "Change: openspec/changes/not-ready\n",
				commits: agentEmail,
				files:   map[string]string{"not-ready/proposal.md": proposalNotReady},
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, notReady("not-ready", "- Which store holds the value?"))
			},
		},
		{
			name: "linked change has no proposal",
			fixture: fixture{
				body:    "Change: openspec/changes/absent\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFailContains(t, code, stderr, "absent", fixLine)
			},
		},
		{
			name: "id with a parent directory",
			fixture: fixture{
				body:    "Change: openspec/changes/../x\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("../x"))
			},
		},
		{
			name: "id with a path outside the change folder",
			fixture: fixture{
				body:    "Change: openspec/changes/../outside\n",
				commits: agentEmail,
				files:   map[string]string{"../outside/proposal.md": proposalReady},
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("../outside"))
			},
		},
		{
			name: "id with a slash",
			fixture: fixture{
				body:    "Change: openspec/changes/a/b\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("a/b"))
			},
		},
		{
			name: "id with capital letters",
			fixture: fixture{
				body:    "Change: openspec/changes/Ready\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("Ready"))
			},
		},
		{
			name: "id with a space",
			fixture: fixture{
				body:    "Change: openspec/changes/not ready\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("not ready"))
			},
		},
		{
			name: "id of 81 bytes is refused by the length cap",
			fixture: fixture{
				body:    "Change: openspec/changes/" + strings.Repeat("a", 81) + "\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignoredLong(strings.Repeat("a", 80)))
			},
		},
		{
			name: "id with a CR and an ANSI escape writes one plain line",
			fixture: fixture{
				body:    "Change: openspec/changes/evil\x1b[31m\rx\r\n",
				commits: agentEmail,
				files:   nil,
			},
			check: func(t *testing.T, code int, stderr string) {
				t.Helper()
				wantFail(t, code, stderr, ignored("evil[31mx"))
				wantNoCRorESC(t, stderr)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, stderr := runFixture(t, tc.fixture)
			tc.check(t, code, stderr)
		})
	}
}

// ignored is the output of a refused change id.
func ignored(id string) string {
	return "check-ai-pr-spec: ignored a change id that is not valid: " + id + "\n" + fixLine
}

// ignoredLong is the output of a change id over the length cap. It is the own
// message of the cap, so the case fails when the cap goes away.
func ignoredLong(id string) string {
	return "check-ai-pr-spec: ignored a change id that is longer than 80 bytes: " + id + "\n" + fixLine
}

// notReady is the output of a linked change whose open questions are not
// "none".
func notReady(id, questions string) string {
	return "check-ai-pr-spec: the change " + id + " is not spec-ready: Open questions: " +
		questions + "\n" + fixLine
}

// wantPass asserts that the check passes with no output.
func wantPass(t *testing.T, code int, stderr string) {
	t.Helper()

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %q", code, stderr)
	}

	if stderr != "" {
		t.Fatalf("stderr = %q, want no output", stderr)
	}
}

// wantFix asserts that the check fails with the line that names the fix.
func wantFix(t *testing.T, code int, stderr string) {
	t.Helper()

	wantFail(t, code, stderr, fixLine)
}

// wantFail asserts that the check fails with exactly this output.
func wantFail(t *testing.T, code int, stderr, want string) {
	t.Helper()

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %q", code, stderr)
	}

	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

// wantFailContains asserts that the check fails and that the output holds
// each part.
func wantFailContains(t *testing.T, code int, stderr string, parts ...string) {
	t.Helper()

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %q", code, stderr)
	}

	for _, part := range parts {
		if !strings.Contains(stderr, part) {
			t.Fatalf("stderr = %q, want it to contain %q", stderr, part)
		}
	}
}

// wantNoCRorESC asserts that the output holds no carriage return and no
// escape, so that a hostile id cannot forge a log line or an ANSI escape.
func wantNoCRorESC(t *testing.T, stderr string) {
	t.Helper()

	if strings.ContainsAny(stderr, "\r\x1b") {
		t.Fatalf("stderr = %q, want no CR and no ESC", stderr)
	}
}

// runFixture writes the fixture in a temporary directory and runs the script
// on it. The change folder is a subdirectory of the temporary directory, so a
// case can try to reach outside it.
func runFixture(t *testing.T, f fixture) (int, string) {
	t.Helper()

	root := t.TempDir()
	changes := filepath.Join(root, "changes")

	if err := os.MkdirAll(changes, 0o750); err != nil {
		t.Fatalf("make the change folder: %v", err)
	}

	for name, content := range f.files {
		path := filepath.Join(changes, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("make %s: %v", filepath.Dir(path), err)
		}

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	body := filepath.Join(root, "body.md")
	if err := os.WriteFile(body, []byte(f.body), 0o600); err != nil {
		t.Fatalf("write the body file: %v", err)
	}

	commits := filepath.Join(root, "commits.txt")
	if err := os.WriteFile(commits, []byte(f.commits), 0o600); err != nil {
		t.Fatalf("write the commit file: %v", err)
	}

	return runScript(t, root, map[string]string{
		"PR_BODY_FILE":         body,
		"PR_COMMITS_FILE":      commits,
		"OPENSPEC_CHANGES_DIR": changes,
	})
}

// runScript runs scripts/check-ai-pr-spec.sh with dir as its working
// directory. The script goes in on standard input, and the values go in
// through the environment, so that the command line holds no value. It
// returns the exit code and the text that the script wrote to standard error.
func runScript(t *testing.T, dir string, extra map[string]string) (int, string) {
	t.Helper()

	script, err := os.ReadFile(filepath.Join("..", "check-ai-pr-spec.sh"))
	if err != nil {
		t.Fatalf("read check-ai-pr-spec.sh: %v", err)
	}

	cmd := exec.CommandContext(t.Context(), "bash", "-s")
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(script)
	cmd.Env = testEnv(extra)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), stderr.String()
	}

	t.Fatalf("run check-ai-pr-spec.sh: %v", err)

	return -1, ""
}

// testEnv returns the environment of the test with the named variables set to
// the extra values. It removes an earlier value of each named variable, so
// that the child reads the value of this test.
func testEnv(extra map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))

	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if _, replaced := extra[name]; found && replaced {
			continue
		}

		env = append(env, entry)
	}

	for name, value := range extra {
		env = append(env, name+"="+value)
	}

	return env
}
