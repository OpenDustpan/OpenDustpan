package checkprbody_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const wantMessage = "check-pr-body: the PR body names no issue. Add 'Closes #N', 'Part of #N', 'Issue: #N' or 'No issue: <reason>'.\n"

// runScript runs scripts/check-pr-body.sh with args. The script goes in on
// standard input, and each argument goes in through an environment variable,
// so that the command line holds no variable. It returns the exit code and
// the text that the script wrote to standard error.
func runScript(t *testing.T, args ...string) (int, string) {
	t.Helper()

	script, err := os.ReadFile(filepath.Join("..", "check-pr-body.sh"))
	if err != nil {
		t.Fatalf("read check-pr-body.sh: %v", err)
	}

	// Build "set -- "$CHECK_ARG_0" "$CHECK_ARG_1"", so the script sees the
	// same arguments that a direct call would pass.
	var prefix strings.Builder

	prefix.WriteString("set --")

	env := os.Environ()

	for i, arg := range args {
		name := fmt.Sprintf("CHECK_ARG_%d", i)
		fmt.Fprintf(&prefix, " \"$%s\"", name)
		env = append(env, name+"="+arg)
	}

	prefix.WriteString("\n")

	cmd := exec.CommandContext(t.Context(), "bash", "-s")
	cmd.Stdin = strings.NewReader(prefix.String() + string(script))
	cmd.Env = env

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), stderr.String()
	}

	t.Fatalf("run check-pr-body.sh: %v", err)

	return -1, ""
}

// runOnBody writes body to a file and runs the script on it.
func runOnBody(t *testing.T, body string) (int, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the body file: %v", err)
	}

	return runScript(t, path)
}

func TestCheckPRBodyNamesAnIssue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		pass bool
	}{
		{name: "closes", body: "Closes #12\n", pass: true},
		{name: "closes lowercase", body: "closes #12\n", pass: true},
		{name: "fixes", body: "Fixes #3\n", pass: true},
		{name: "resolves", body: "Resolves #4\n", pass: true},
		{name: "part of", body: "Part of #9\n", pass: true},
		{name: "no issue", body: "No issue\n", pass: true},
		{name: "no issue with a colon reason", body: "No issue: docs only\n", pass: true},
		{name: "no issue with a dash reason", body: "No issue - docs only\n", pass: true},
		{name: "no issue after other text", body: "Summary\r\nNo issue: docs\r\n", pass: true},
		{name: "closes after a code block", body: "```\ncode\n```\n\nCloses #5\n", pass: true},
		{name: "issue line", body: "Issue: #3568\n", pass: true},
		{name: "issue line lowercase and indented", body: "  issue: #7\n", pass: true},
		{name: "issue line with a final period", body: "Issue: #3568.\n", pass: true},
		{name: "issue line with CRLF", body: "Summary\r\nIssue: #12\r\n", pass: true},
		{name: "issue line without a space", body: "Issue:#12\n", pass: true},
		{name: "issue line after a code block", body: "```\ncode\n```\nIssue: #5\n", pass: true},
		{name: "issue line after a very long line", body: strings.Repeat("a", 200000) + "\nIssue: #5\n", pass: true},
		{name: "issue line inside a code block", body: "```\nIssue: #12\n```\n", pass: false},
		{name: "issue without a number", body: "Issue: #\n", pass: false},
		{name: "issue with a word for a number", body: "Issue: #abc\n", pass: false},
		{name: "issue with a letter after the number", body: "Issue: #12abc\n", pass: false},
		{name: "issue without a hash", body: "Issue: 12\n", pass: false},
		{name: "issue of another repository", body: "Issue: other/repo#12\n", pass: false},
		{
			name: "issue URL of another repository",
			body: "Issue: https://github.com/other/repo/issues/12\n",
			pass: false,
		},
		{name: "issue inside a sentence", body: "See the Issue: #12 for details.\n", pass: false},
		{name: "issue after a bare CR", body: "text\rIssue: #12\n", pass: false},
		{name: "issue split by a line break", body: "Issue:\n#12\n", pass: false},
		{name: "none", body: "Adds a helper.\n\nNo link here.\n", pass: false},
		{name: "closes inside a code block", body: "```\nCloses #12\n```\n", pass: false},
		{name: "closes without digits", body: "Closes #\n", pass: false},
		{name: "no issue inside a sentence", body: "This has no issue for review.\n", pass: false},
		{name: "empty body", body: "", pass: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, stderr := runOnBody(t, tc.body)

			if tc.pass {
				if code != 0 {
					t.Fatalf("exit code = %d, want 0; stderr: %q", code, stderr)
				}

				return
			}

			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}

			if stderr != wantMessage {
				t.Fatalf("stderr = %q, want %q", stderr, wantMessage)
			}
		})
	}
}

func TestCheckPRBodyNeedsOneReadableFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no argument", args: nil},
		{name: "two arguments", args: []string{"a.md", "b.md"}},
		{name: "missing file", args: []string{filepath.Join(t.TempDir(), "absent.md")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, stderr := runScript(t, tc.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr: %q", code, stderr)
			}

			if stderr == "" {
				t.Fatal("stderr is empty, want a usage or a file error")
			}
		})
	}
}
