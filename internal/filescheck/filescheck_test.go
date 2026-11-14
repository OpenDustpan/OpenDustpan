package filescheck_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type filesCase struct {
	name       string
	files      map[string]string
	allowlist  string
	args       []string
	outside    bool
	wantExit   int
	wantOutput string
}

func TestCheckFiles(t *testing.T) {
	t.Parallel()

	script, err := filepath.Abs("../../scripts/check-files.sh")
	if err != nil {
		t.Fatalf("Locate script: %v", err)
	}

	large := strings.Repeat("x", (1<<20)+1)
	binary := "text\x00data\n"
	tests := []filesCase{
		{
			name:       "large file",
			files:      map[string]string{"big.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big.txt is 1048577 bytes, over 1048576",
		},
		{
			name:       "binary outside testdata",
			files:      map[string]string{"small.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "small.bin is a binary file outside testdata/",
		},
		{
			name:       "binary in root testdata",
			files:      map[string]string{"testdata/small.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "binary in nested testdata",
			files:      map[string]string{"pkg/testdata/small.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "binary in similarly named directory",
			files:      map[string]string{"not-testdata/small.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "not-testdata/small.bin is a binary file outside testdata/",
		},
		{
			name:       "allowlisted large file",
			files:      map[string]string{"big.txt": large},
			allowlist:  "#\n\n# ../* is a comment.\nbig.txt Large test fixture.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "allowlisted binary",
			files:      map[string]string{"small.bin": binary},
			allowlist:  "small.bin Binary test fixture.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "path with spaces",
			files:      map[string]string{"big file.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big file.txt is 1048577 bytes",
		},
		{
			name:       "path with leading dash",
			files:      map[string]string{"-big.bin": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "-big.bin is 1048577 bytes",
		},
		{
			name:       "path with tab and newline",
			files:      map[string]string{"big\tfile\n.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big\tfile\n.txt is 1048577 bytes",
		},
		{
			name:       "missing base",
			files:      nil,
			allowlist:  "",
			args:       []string{"missing-base"},
			outside:    false,
			wantExit:   128,
			wantOutput: "missing-base...HEAD",
		},
		{
			name:       "base with leading dash",
			files:      nil,
			allowlist:  "",
			args:       []string{"--help"},
			outside:    false,
			wantExit:   2,
			wantOutput: "base ref --help starts with -",
		},
		{
			name:       "outside a repository",
			files:      nil,
			allowlist:  "",
			args:       []string{"main"},
			outside:    true,
			wantExit:   128,
			wantOutput: "not a git repository",
		},
		{
			name:       "small text file",
			files:      map[string]string{"small.txt": "Small text file.\n"},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "file at size limit",
			files:      map[string]string{"limit.txt": large[:1<<20]},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "large file in testdata",
			files:      map[string]string{"testdata/big.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "testdata/big.txt is 1048577 bytes",
		},
		{
			name:       "too many arguments",
			files:      nil,
			allowlist:  "",
			args:       []string{"main", "extra"},
			outside:    false,
			wantExit:   2,
			wantOutput: "usage: check-files.sh [base-ref]",
		},
		{
			name:       "comment does not allow a path",
			files:      map[string]string{"#": large},
			allowlist:  "# This is not an entry.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "# is 1048577 bytes",
		},
	}
	for _, tt := range slices.Concat(tests, allowlistCases(large)) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFilesCase(t, script, tt)
		})
	}
}

func allowlistCases(large string) []filesCase {
	return []filesCase{
		{
			name:       "allowlist without reason",
			files:      nil,
			allowlist:  "big.txt\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists big.txt with no reason",
		},
		{
			name:       "allowlist parent path",
			files:      map[string]string{"big.txt": large},
			allowlist:  "../big.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path ../big.txt",
		},
		{
			name:       "allowlist nested parent path",
			files:      map[string]string{"big.txt": large},
			allowlist:  "dir/../big.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path dir/../big.txt",
		},
		{
			name:       "allowlist absolute path",
			files:      map[string]string{"big.txt": large},
			allowlist:  "/big.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path /big.txt",
		},
		{
			name:       "allowlist star glob",
			files:      map[string]string{"big.txt": large},
			allowlist:  "*.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path *.txt",
		},
		{
			name:       "allowlist question glob",
			files:      map[string]string{"big.txt": large},
			allowlist:  "?ig.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path ?ig.txt",
		},
		{
			name:       "allowlist bracket glob",
			files:      map[string]string{"big.txt": large},
			allowlist:  "[b]ig.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path [b]ig.txt",
		},
		{
			name:       "path escape does not match another path",
			files:      map[string]string{`big\056txt`: large},
			allowlist:  "big.txt Different test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: `big\056txt is 1048577 bytes`,
		},
		{
			name:       "allowlisted literal backslash",
			files:      map[string]string{`big\056txt`: large},
			allowlist:  "big\\056txt Large test fixture.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "numeric paths match literally",
			files:      map[string]string{"01": large},
			allowlist:  "1 Different test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "01 is 1048577 bytes",
		},
	}
}

func checkFilesCase(t *testing.T, script string, tt filesCase) {
	t.Helper()

	dir, env := newRepository(t)
	for path, content := range tt.files {
		writeFile(t, filepath.Join(dir, path), content)
		runGit(t, dir, env, "add", "--", path)
	}
	if tt.allowlist != "" {
		writeFile(t, filepath.Join(dir, ".large-files-allowlist"), tt.allowlist)
		runGit(t, dir, env, "add", "--", ".large-files-allowlist")
	}
	runGit(t, dir, env, "commit", "--quiet", "--allow-empty", "-m", "Add test files")
	if tt.outside {
		dir = t.TempDir()
	}
	args := append([]string{script}, tt.args...)
	//nolint:gosec // The script path is fixed in this checkout. Each test value is a separate argument.
	cmd := exec.CommandContext(t.Context(), "bash", args...)
	cmd.Dir = dir
	cmd.Env = env
	output, runErr := cmd.CombinedOutput()
	code := exitCode(t, runErr)
	if code != tt.wantExit {
		t.Errorf("Exit code = %d, want %d. Output:\n%s", code, tt.wantExit, output)
	}
	if !strings.Contains(string(output), tt.wantOutput) {
		t.Errorf("Output does not contain %q:\n%s", tt.wantOutput, output)
	}
	if tt.wantExit != 0 && strings.Contains(string(output), "check-files: no file over") {
		t.Errorf("Failed check reports success:\n%s", output)
	}
}

func newRepository(t *testing.T) (string, []string) {
	t.Helper()

	dir := t.TempDir()
	home := t.TempDir()
	config := filepath.Join(home, "gitconfig")
	writeFile(t, config, "")
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + config,
		"GIT_CONFIG_NOSYSTEM=1",
		"LC_ALL=C",
		"TMPDIR=" + home,
	}
	runGit(t, dir, env, "init", "--quiet", "--initial-branch=main")
	runGit(t, dir, env, "config", "--local", "user.name", "Files check test")
	runGit(t, dir, env, "config", "--local", "user.email", "filescheck@example.invalid")
	runGit(t, dir, env, "commit", "--quiet", "--allow-empty", "-m", "Initial test commit")
	runGit(t, dir, env, "switch", "--quiet", "-c", "test-files")
	return dir, env
}

func runGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()

	//nolint:gosec // Git receives only test arguments in an isolated repository. No shell runs.
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git %q failed: %v\n%s", args, err, output)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("Create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("Write test file: %v", err)
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()

	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Start script: %v", err)
	}
	return exitErr.ExitCode()
}
