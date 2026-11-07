package checkprbody_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const wantMessage = "check-pr-body: the PR body names no issue. Add 'Closes #N', 'Part of #N' or 'No issue: <reason>'.\n"

// scriptPath returns the absolute path of scripts/check-pr-body.sh.
func scriptPath(t *testing.T) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("..", "check-pr-body.sh"))
	if err != nil {
		t.Fatalf("resolve the script path: %v", err)
	}

	return path
}

// runScript runs the script with args under bash. It returns the exit code and
// the text that the script wrote to standard error.
func runScript(t *testing.T, args ...string) (int, string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "bash", append([]string{scriptPath(t)}, args...)...)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stderr.String()
	}

	t.Fatalf("run check-pr-body.sh: %v", err)

	return -1, ""
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
		{name: "none", body: "Adds a helper.\n\nNo link here.\n", pass: false},
		{name: "closes inside a code block", body: "```\nCloses #12\n```\n", pass: false},
		{name: "closes without digits", body: "Closes #\n", pass: false},
		{name: "no issue inside a sentence", body: "This has no issue for review.\n", pass: false},
		{name: "empty body", body: "", pass: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "body.md")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write the body file: %v", err)
			}

			code, stderr := runScript(t, path)

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
