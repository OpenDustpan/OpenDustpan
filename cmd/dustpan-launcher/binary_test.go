package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
)

// buildLauncher builds the launcher binary in a new directory.
func buildLauncher(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), launcher.Binary)

	cmd := goCommand(t, "build", "-o", bin, ".")
	cmd.Env = append(cmd.Env, "CGO_ENABLED=0")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the launcher: %v\n%s", err, out)
	}

	return bin
}

// startLauncher runs the binary with args and the extra file fd3 as file
// descriptor 3, and returns its exit code and its standard error.
func startLauncher(t *testing.T, bin string, fd3 *os.File, args ...string) (int, string) {
	t.Helper()

	var stderr bytes.Buffer

	cmd := command(bin, args...)
	cmd.Stderr = &stderr

	if fd3 != nil {
		cmd.ExtraFiles = []*os.File{fd3}
	}

	err := cmd.Run()

	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run the launcher: %v", err)
	}

	return cmd.ProcessState.ExitCode(), stderr.String()
}

// TestTheBinary drives the modes of the binary that need no server: the
// command line, the handoff from a pipe only, and the copy.
func TestTheBinary(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the mode run needs Linux")
	}

	bin := buildLauncher(t)
	memDir := "/dev/shm"

	if !onMemory(memDir) {
		t.Skip("needs /dev/shm on tmpfs")
	}

	const sample = "dph_" + "0123456789abcdef"

	tokenFile := fileWith(t, sample)

	runArgs := func(handoff string) []string {
		return []string{
			"run", "--server", "https://127.0.0.1:1", "--handoff", handoff, "--secrets-dir", memDir,
			"--work-dir", t.TempDir(),
		}
	}

	cases := []struct {
		name string
		fd3  *os.File
		args []string
		code launcher.ExitCode
		text string
	}{
		{"unknown mode", nil, []string{"walk"}, launcher.ExitUsage, "unknown mode"},
		{
			"token in argv", nil,
			[]string{"run", "--server", "https://x", "--handoff", "pipe", "--token", sample},
			launcher.ExitUsage, "unknown flag",
		},
		{"no handoff pipe", nil, runArgs("pipe"), launcher.ExitHandoff, "handoff"},
		{"handoff from a file", tokenFile, runArgs("pipe"), launcher.ExitHandoff, "not a pipe"},
		{"handoff vsock", nil, runArgs("vsock"), launcher.ExitHandoff, "handoff=vsock"},
	}

	// A test directory on tmpfs would pass the check: the case runs only on
	// a disk.
	if disk := t.TempDir(); !onMemory(disk) {
		cases = append(cases, struct {
			name string
			fd3  *os.File
			args []string
			code launcher.ExitCode
			text string
		}{"secrets on a disk", nil, []string{
			"run", "--server", "https://127.0.0.1:1", "--handoff", "pipe",
			"--secrets-dir", disk,
		}, launcher.ExitSandbox, "tmpfs"})
	}

	for _, tc := range cases {
		code, stderr := startLauncher(t, bin, tc.fd3, tc.args...)
		if code != int(tc.code) || !strings.Contains(stderr, tc.text) || strings.Contains(stderr, sample) {
			t.Errorf("%s: exit %d %q, want %d with %q and no token", tc.name, code, stderr, tc.code, tc.text)
		}
	}

	copiesItself(t, bin)
}

// copiesItself runs the mode copy of bin.
func copiesItself(t *testing.T, bin string) {
	t.Helper()

	dest := t.TempDir()
	if code, stderr := startLauncher(t, bin, nil, "copy", "--dest", dest); code != 0 {
		t.Fatalf("copy: exit %d %s", code, stderr)
	}

	info, err := os.Stat(filepath.Join(dest, launcher.Binary))
	if err != nil || info.Mode().Perm() != launcher.ModeBinary {
		t.Fatalf("copy = %v, %v; want the binary with mode 0755", info, err)
	}
}

// fileWith returns a regular file that holds data, open for reading.
func fileWith(t *testing.T, data string) *os.File {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(data), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	f, err := os.OpenInRoot(dir, "token")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := f.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return f
}

// onMemory reports whether dir passes the check of a memory-backed volume.
func onMemory(dir string) bool {
	made, err := (launcher.TmpfsVolume{}).Prepare(dir)
	if err != nil {
		return false
	}

	return os.Remove(made) == nil
}
