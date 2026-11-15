package filescheck_test

import (
	"bytes"
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

	raw, err := os.ReadFile("../../scripts/check-files.sh")
	if err != nil {
		t.Fatalf("Read script: %v", err)
	}
	script := string(raw)

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
			wantOutput: "big?file?.txt is 1048577 bytes",
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
	for _, tt := range slices.Concat(tests, allowlistCases(large), controlPathCases(large, binary), attributeCases(binary)) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFilesCase(t, script, tt)
		})
	}
}

func controlPathCases(large, binary string) []filesCase {
	return []filesCase{
		{
			name:       "large file with newline",
			files:      map[string]string{"big\ninjected.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big?injected.txt is 1048577 bytes",
		},
		{
			name:       "binary file with newline",
			files:      map[string]string{"small\ninjected.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "small?injected.bin is a binary file outside testdata/",
		},
		{
			name:       "large file with carriage return",
			files:      map[string]string{"big\rinjected.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big?injected.txt is 1048577 bytes",
		},
		{
			name:       "binary file with carriage return",
			files:      map[string]string{"small\rinjected.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "small?injected.bin is a binary file outside testdata/",
		},
		{
			name:       "large file with other control bytes",
			files:      map[string]string{"big\x01\x1b\x7f.txt": large},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "big???.txt is 1048577 bytes",
		},
		{
			name:       "binary file with other control bytes",
			files:      map[string]string{"small\x01\x1b\x7f.bin": binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "small???.bin is a binary file outside testdata/",
		},
	}
}

// attributeCases are the rows where the branch tries to hide a binary file
// from git's diff heuristic. The script reads the blob, so each one fails.
func attributeCases(binary string) []filesCase {
	exe := "MZ\x00\x00\x01\x02binary"
	return []filesCase{
		{
			name:       "diff attribute does not hide a binary file",
			files:      map[string]string{"tool.exe2": exe, ".gitattributes": "*.exe2 diff\n"},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "tool.exe2 is a binary file outside testdata/",
		},
		{
			name:       "text attribute does not hide a binary file",
			files:      map[string]string{"tool.exe2": exe, ".gitattributes": "*.exe2 text\n"},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "tool.exe2 is a binary file outside testdata/",
		},
		{
			name:       "binary attribute on a text file does not refuse it",
			files:      map[string]string{"notes.txt": "Plain text.\n", ".gitattributes": "*.txt binary\n"},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
		{
			name:       "NUL byte at the end of the first 8000 bytes",
			files:      map[string]string{"edge.dat": strings.Repeat("x", 7999) + "\x00"},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "edge.dat is a binary file outside testdata/",
		},
		{
			name:       "NUL byte after the first 8000 bytes",
			files:      map[string]string{"late.dat": strings.Repeat("x", 8000) + "\x00" + binary},
			allowlist:  "",
			args:       []string{"main"},
			outside:    false,
			wantExit:   0,
			wantOutput: "check-files: no file over",
		},
	}
}

func allowlistCases(large string) []filesCase {
	return []filesCase{
		{
			name:       "allowlist control characters are not printed",
			files:      nil,
			allowlist:  "x\x1b[2Jy\rFAKE\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists x?[2Jy?FAKE with no reason",
		},
		{
			name:       "allowlist path with leading dash",
			files:      map[string]string{"-big.txt": large},
			allowlist:  "-big.txt Invalid test path.\n",
			args:       []string{"main"},
			outside:    false,
			wantExit:   1,
			wantOutput: "lists invalid path -big.txt",
		},
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
	}
	if tt.allowlist != "" {
		writeFile(t, filepath.Join(dir, ".large-files-allowlist"), tt.allowlist)
	}
	// The repository holds only the files of the case, so --all adds them.
	runGit(t, exec.CommandContext(t.Context(), "git", "add", "--all"), dir, env)
	runGit(
		t,
		exec.CommandContext(t.Context(), "git", "commit", "--quiet", "--allow-empty", "-m", "Add test files"),
		dir,
		env,
	)
	if tt.outside {
		dir = t.TempDir()
	}
	// The script goes in on standard input, after a set line that gives it
	// the arguments of the case, so that the command line holds no variable.
	cmd := exec.CommandContext(t.Context(), "bash", "-s")
	cmd.Stdin = strings.NewReader("set --" + shellWords(tt.args) + "\n" + script)
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
	if bytes.ContainsRune(output, '\r') {
		t.Errorf("Output contains a carriage return: %q", output)
	}
	if bytes.ContainsRune(output, '\x1b') {
		t.Errorf("Output contains an escape character: %q", output)
	}
	if bytes.Contains(output, []byte("\ninjected")) {
		t.Errorf("Output contains an injected line: %q", output)
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
	runGit(t, exec.CommandContext(t.Context(), "git", "init", "--quiet", "--initial-branch=main"), dir, env)
	runGit(t, exec.CommandContext(t.Context(), "git", "config", "--local", "user.name", "Files check test"), dir, env)
	runGit(
		t,
		exec.CommandContext(t.Context(), "git", "config", "--local", "user.email", "filescheck@example.invalid"),
		dir,
		env,
	)
	runGit(
		t,
		exec.CommandContext(t.Context(), "git", "commit", "--quiet", "--allow-empty", "-m", "Initial test commit"),
		dir,
		env,
	)
	runGit(t, exec.CommandContext(t.Context(), "git", "switch", "--quiet", "-c", "test-files"), dir, env)
	return dir, env
}

// runGit runs a git command that the caller built with fixed arguments in
// the test repository.
func runGit(t *testing.T, cmd *exec.Cmd, dir string, env []string) {
	t.Helper()

	cmd.Dir = dir
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git %q failed: %v\n%s", cmd.Args, err, output)
	}
}

// shellWords quotes each argument for a POSIX shell, each with a space
// before it.
func shellWords(args []string) string {
	var b strings.Builder
	for _, arg := range args {
		b.WriteString(" '" + strings.ReplaceAll(arg, "'", `'\''`) + "'")
	}
	return b.String()
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
