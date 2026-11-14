package checkfiles_test

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
	state      string
	wantExit   int
	wantOutput string
}

func TestCheckFiles(t *testing.T) {
	t.Parallel()

	script, err := filepath.Abs("../check-files.sh")
	if err != nil {
		t.Fatalf("Locate script: %v", err)
	}

	large := strings.Repeat("x", (1<<20)+1)
	binary := "text\x00data\n"
	tests := []filesCase{
		{
			name: "large file", files: map[string]string{"big.txt": large},
			allowlist: "", state: "", wantExit: 1,
			wantOutput: "big.txt is 1048577 bytes, over 1048576",
		},
		{
			name: "binary file", files: map[string]string{"small.bin": binary},
			allowlist: "", state: "", wantExit: 1, wantOutput: "small.bin is binary",
		},
		{
			name: "NUL at byte 8000", files: map[string]string{"edge.txt": strings.Repeat("x", 7999) + "\x00"},
			allowlist: "", state: "", wantExit: 1, wantOutput: "edge.txt is binary",
		},
		{
			name: "NUL after byte 8000", files: map[string]string{"late.txt": strings.Repeat("x", 8000) + "\x00"},
			allowlist: "", state: "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "binary in root testdata", files: map[string]string{"testdata/small.bin": binary},
			allowlist: "", state: "", wantExit: 1, wantOutput: "testdata/small.bin is binary",
		},
		{
			name: "binary in nested testdata", files: map[string]string{"pkg/testdata/small.bin": binary},
			allowlist: "", state: "", wantExit: 1, wantOutput: "pkg/testdata/small.bin is binary",
		},
		{
			name: "allowlisted large file", files: map[string]string{"big.txt": large},
			allowlist: "#\n\n# ../* is a comment.\nbig.txt # Large test fixture.\n",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "allowlisted binary", files: map[string]string{"small.bin": binary},
			allowlist: "small.bin # Binary test fixture.\n",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "path with spaces", files: map[string]string{"a b.txt": large},
			allowlist: "", state: "", wantExit: 1, wantOutput: "a b.txt is 1048577 bytes",
		},
		{
			name: "allowlisted path with spaces", files: map[string]string{"a b.txt": large},
			allowlist: "a b.txt # Large test fixture.\n",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "path with leading dash", files: map[string]string{"-x.txt": large},
			allowlist: "", state: "", wantExit: 1, wantOutput: "-x.txt is 1048577 bytes",
		},
		{
			name:      "binary with diff attribute",
			files:     map[string]string{".gitattributes": "*.exe2 diff\n", "tool.exe2": binary},
			allowlist: "", state: "", wantExit: 1, wantOutput: "tool.exe2 is binary",
		},
		{
			name:      "binary with text attribute",
			files:     map[string]string{".gitattributes": "*.exe2 text\n", "tool.exe2": binary},
			allowlist: "", state: "", wantExit: 1, wantOutput: "tool.exe2 is binary",
		},
		{
			name: "small text file", files: map[string]string{"small.txt": "Small text file.\n"},
			allowlist: "", state: "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "empty file", files: map[string]string{"empty.txt": ""},
			allowlist: "", state: "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "file at size limit", files: map[string]string{"limit.txt": large[:1<<20]},
			allowlist: "", state: "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "large file in testdata", files: map[string]string{"testdata/big.txt": large},
			allowlist: "", state: "", wantExit: 1, wantOutput: "testdata/big.txt is 1048577 bytes",
		},
		{
			name: "comment does not allow a path", files: map[string]string{"#": large},
			allowlist: "# # This is not an entry.\n",
			state:     "", wantExit: 1, wantOutput: "# is 1048577 bytes",
		},
		{
			name: "outside a repository", files: nil, allowlist: "", state: "outside",
			wantExit: 1, wantOutput: "not a git repository",
		},
		{
			name: "git listing error", files: nil, allowlist: "", state: "bad index",
			wantExit: 1, wantOutput: "cannot list tracked files",
		},
		{
			name: "unreadable blob", files: map[string]string{"unreadable\ninjected.txt": "Missing blob.\n"},
			allowlist: "", state: "missing blob", wantExit: 1, wantOutput: "cannot read unreadable?injected.txt",
		},
		{
			name: "allowlisted unreadable blob", files: map[string]string{"unreadable.txt": "Missing blob.\n"},
			allowlist: "unreadable.txt # Test missing blob.\n",
			state:     "missing blob", wantExit: 1, wantOutput: "cannot read unreadable.txt",
		},
	}
	for _, tt := range slices.Concat(tests, allowlistCases(large), controlPathCases(large, binary)) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFilesCase(t, script, tt)
		})
	}
}

func controlPathCases(large, binary string) []filesCase {
	tests := make([]filesCase, 0, 8)
	for _, control := range []struct {
		name string
		text string
	}{
		{name: "newline", text: "\n"},
		{name: "carriage return", text: "\r"},
		{name: "escape", text: "\x1b"},
		{name: "tab and other control bytes", text: "\t\x01\x7f"},
	} {
		tests = append(tests,
			filesCase{
				name:      "large file with " + control.name,
				files:     map[string]string{"big" + control.text + "injected.txt": large},
				allowlist: "", state: "", wantExit: 1,
				wantOutput: "big" + strings.Repeat("?", len(control.text)) + "injected.txt is 1048577 bytes",
			},
			filesCase{
				name:      "binary file with " + control.name,
				files:     map[string]string{"small" + control.text + "injected.bin": binary},
				allowlist: "", state: "", wantExit: 1,
				wantOutput: "small" + strings.Repeat("?", len(control.text)) + "injected.bin is binary",
			},
		)
	}
	return tests
}

func allowlistCases(large string) []filesCase {
	return []filesCase{
		{
			name: "allowlist without reason", files: nil, allowlist: "big.txt\n", state: "",
			wantExit: 1, wantOutput: "lists big.txt with no reason",
		},
		{
			name: "allowlist with empty reason", files: nil, allowlist: "big.txt #\n", state: "",
			wantExit: 1, wantOutput: "lists big.txt with no reason",
		},
		{
			name: "allowlist with blank reason", files: nil, allowlist: "big.txt #   \n", state: "",
			wantExit: 1, wantOutput: "lists big.txt with no reason",
		},
		{
			name: "allowlist without separator", files: nil, allowlist: "big.txt Test fixture.\n", state: "",
			wantExit: 1, wantOutput: "with no reason",
		},
		{
			name: "allowlist parent path", files: nil, allowlist: "../x # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path ../x",
		},
		{
			name: "allowlist nested parent path", files: nil, allowlist: "dir/../x # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path dir/../x",
		},
		{
			name: "allowlist final parent element", files: nil, allowlist: "dir/.. # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path dir/..",
		},
		{
			name: "allowlist absolute path", files: nil, allowlist: "/abs # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path /abs",
		},
		{
			name: "allowlist leading dash", files: nil, allowlist: "-x # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path -x",
		},
		{
			name: "allowlist empty path", files: nil, allowlist: " # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path",
		},
		{
			name: "allowlist control bytes in path", files: nil, allowlist: "bad\x1b\r.txt # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path bad??.txt",
		},
		{
			name: "allowlist control bytes without reason", files: nil, allowlist: "bad\x1b\r.txt\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path bad??.txt",
		},
		{
			name: "allowlist control bytes in reason", files: nil, allowlist: "x # bad\x1b\r\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path x",
		},
		{
			name: "allowlist NUL byte", files: nil, allowlist: "x\x00 # reason\n", state: "",
			wantExit: 1, wantOutput: "lists invalid path x?",
		},
		{
			name: "allowlist star is literal", files: map[string]string{"big.txt": large},
			allowlist: "*.txt # Different test path.\n",
			state:     "", wantExit: 1, wantOutput: "big.txt is 1048577 bytes",
		},
		{
			name: "allowlist question mark is literal", files: map[string]string{"big.txt": large},
			allowlist: "?ig.txt # Different test path.\n",
			state:     "", wantExit: 1, wantOutput: "big.txt is 1048577 bytes",
		},
		{
			name: "allowlist brackets are literal", files: map[string]string{"big.txt": large},
			allowlist: "[b]ig.txt # Different test path.\n",
			state:     "", wantExit: 1, wantOutput: "big.txt is 1048577 bytes",
		},
		{
			name: "allowlisted literal star", files: map[string]string{"*.txt": large},
			allowlist: "*.txt # Large test fixture.\n",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "path escape does not match another path", files: map[string]string{`big\056txt`: large},
			allowlist: "big.txt # Different test path.\n",
			state:     "", wantExit: 1, wantOutput: `big\056txt is 1048577 bytes`,
		},
		{
			name: "allowlisted literal backslash", files: map[string]string{`big\056txt`: large},
			allowlist: "big\\056txt # Large test fixture.\n",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
		},
		{
			name: "numeric paths match literally", files: map[string]string{"01": large},
			allowlist: "1 # Different test path.\n",
			state:     "", wantExit: 1, wantOutput: "01 is 1048577 bytes",
		},
		{
			name: "allowlist final line without newline", files: map[string]string{"big.txt": large},
			allowlist: "big.txt # Large test fixture.",
			state:     "", wantExit: 0, wantOutput: "check-files: no file over",
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
	dir = prepareState(t, dir, env, tt)
	//nolint:gosec // The script path is fixed in this checkout. No shell text comes from a test case.
	cmd := exec.CommandContext(t.Context(), "bash", script)
	cmd.Dir = dir
	cmd.Env = env
	output, runErr := cmd.CombinedOutput()
	code := exitCode(t, runErr)
	if code != tt.wantExit {
		t.Errorf("Exit code = %d, want %d. Output:\n%s", code, tt.wantExit, output)
	}
	if !bytes.Contains(output, []byte(tt.wantOutput)) {
		t.Errorf("Output does not contain %q:\n%s", tt.wantOutput, output)
	}
	checkOutput(t, output, tt.wantExit)
}

func checkOutput(t *testing.T, output []byte, wantExit int) {
	t.Helper()

	if wantExit != 0 && bytes.Contains(output, []byte("check-files: no file over")) {
		t.Errorf("Failed check reports success:\n%s", output)
	}
	for _, b := range output {
		if (b < ' ' && b != '\n') || b == '\x7f' {
			t.Errorf("Output contains a control byte: %q", output)
			break
		}
	}
	if bytes.Contains(output, []byte("\ninjected")) {
		t.Errorf("Output contains an injected line: %q", output)
	}
	if bytes.Contains(output, []byte(" is 1048577 bytes")) || bytes.Contains(output, []byte(" is binary")) {
		if !bytes.Contains(output, []byte("add to .large-files-allowlist with a reason, or remove it")) {
			t.Errorf("Output does not name the fix: %q", output)
		}
	}
}

func prepareState(t *testing.T, dir string, env []string, tt filesCase) string {
	t.Helper()

	switch tt.state {
	case "":
	case "outside":
		return t.TempDir()
	case "bad index":
		writeFile(t, filepath.Join(dir, ".git", "index"), "Invalid index.\n")
	case "missing blob":
		for path := range tt.files {
			hash := strings.TrimSpace(string(runGit(t, dir, env, "rev-parse", ":"+path)))
			object := filepath.Join(dir, ".git", "objects", hash[:2], hash[2:])
			if err := os.Remove(object); err != nil {
				t.Fatalf("Remove test blob: %v", err)
			}
		}
	default:
		t.Fatalf("Unknown repository state %q", tt.state)
	}
	return dir
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
	runGit(t, dir, env, "init", "--quiet")
	runGit(t, dir, env, "config", "--local", "user.name", "Files check test")
	runGit(t, dir, env, "config", "--local", "user.email", "checkfiles@example.invalid")
	return dir, env
}

func runGit(t *testing.T, dir string, env []string, args ...string) []byte {
	t.Helper()

	//nolint:gosec // Git receives only test arguments in an isolated repository. No shell runs.
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Git %q failed: %v\n%s", args, err, output)
	}
	return output
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
