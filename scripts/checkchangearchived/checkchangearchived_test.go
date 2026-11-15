package checkchangearchived_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir and fails the test on an error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo makes a repository with a base commit "base" and a second commit that
// adds the change "demo" with the given tasks.md text.
func newRepo(t *testing.T, tasks string) string {
	t.Helper()

	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, dir, "tag", "base")

	change := filepath.Join(dir, "openspec", "changes", "demo")
	if err := os.MkdirAll(change, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(change, "proposal.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(change, "tasks.md"), []byte(tasks), 0o600); err != nil {
		t.Fatal(err)
	}

	gitIn(t, dir, "add", "openspec/changes/demo/proposal.md", "openspec/changes/demo/tasks.md")
	gitIn(t, dir, "commit", "-q", "-m", "change")

	return dir
}

// run runs the script in dir with args and the given PATH. It returns the exit
// code and the text on standard error.
func run(t *testing.T, dir, path string, args ...string) (int, string) {
	t.Helper()

	script, err := filepath.Abs(filepath.Join("..", "check-change-archived.sh"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+path)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), stderr.String()
	}

	t.Fatalf("run script: %v", err)

	return -1, ""
}

func TestCheckChangeArchived(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tasks    string
		wantExit int
		wantErr  string
	}{
		{"open task keeps the change open", "- [x] a\n- [ ] b\n", 0, ""},
		{"no task passes", "# no tasks\n", 0, ""},
		{"dash marker complete", "- [x] a\n- [x] b\n", 1, "2 of 2 tasks done"},
		{"star marker complete", "* [x] a\n* [x] b\n", 1, "2 of 2 tasks done"},
		{"plus marker complete", "+ [x] a\n+ [x] b\n", 1, "2 of 2 tasks done"},
		{"numbered marker complete", "1. [x] a\n2. [x] b\n", 1, "2 of 2 tasks done"},
		{"nested last task complete", "- [x] a\n  - [x] b\n", 1, "2 of 2 tasks done"},
		{"odd marker keeps the change open", "- [x] a\n- [~] b\n", 0, ""},
		{"open nested task keeps the change open", "- [x] a\n  - [ ] b\n", 0, ""},
		{"open star task keeps the change open", "* [x] a\n* [ ] b\n", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := newRepo(t, tt.tasks)

			code, stderr := run(t, dir, os.Getenv("PATH"), "base")
			if code != tt.wantExit {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, tt.wantExit, stderr)
			}

			if !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tt.wantErr)
			}

			if tt.wantExit == 1 && !strings.Contains(stderr, "archive demo --yes") {
				t.Fatalf("stderr = %q, want the archive command", stderr)
			}
		})
	}
}

func TestCheckChangeArchivedRefusesBadBase(t *testing.T) {
	t.Parallel()

	dir := newRepo(t, "- [x] a\n")

	for _, base := range []string{"", "-h", "--output=/tmp/x", "-base", "bad ref", "nope"} {
		code, stderr := run(t, dir, os.Getenv("PATH"), base)
		if code != 2 {
			t.Errorf("base %q: exit = %d, want 2; stderr: %s", base, code, stderr)
		}
	}
}

func TestCheckChangeArchivedFailsClosedWithoutCLI(t *testing.T) {
	t.Parallel()

	dir := newRepo(t, "- [x] a\n")

	bin := t.TempDir()

	for _, name := range []string{"git", "jq", "bash", "awk", "sort", "grep", "dirname", "printf"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}

		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}

	code, stderr := run(t, dir, bin, "base")
	if code != 2 || !strings.Contains(stderr, "list --json\" failed") {
		t.Fatalf("exit = %d, stderr = %q, want exit 2 and the CLI failure", code, stderr)
	}
}
