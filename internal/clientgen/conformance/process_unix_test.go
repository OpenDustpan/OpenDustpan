//go:build unix

package conformance_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pidFileEnv names the file where the fake adapter writes the PID of its child.
const pidFileEnv = "CONFORMANCE_FAKE_PIDFILE"

// startGrandchild starts a process that the fake adapter owns, and writes its
// PID to the file that the test names. The process has no pipe of the harness,
// so only a kill of the group can end it.
func startGrandchild() {
	cmd := exec.CommandContext(context.Background(), os.Args[0]) //nolint:gosec // The test starts its own binary.
	cmd.Env = []string{fakeModeEnv + "=sleeper"}

	if err := cmd.Start(); err != nil {
		return
	}

	//nolint:gosec // G703: the path is from the test, which names the file in its own directory.
	if err := os.WriteFile(os.Getenv(pidFileEnv), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		return
	}
}

// alive says that a process runs. A zombie, which waits for its parent, is not
// alive.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}

	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}

	// The state is the field after the name, which is in parentheses.
	_, after, found := strings.Cut(string(stat), ") ")

	return !found || !strings.HasPrefix(after, "Z")
}

// TestProcessAdapterKillsTheGroup checks that a stop ends the processes that
// the adapter started, and not only the adapter.
func TestProcessAdapterKillsTheGroup(t *testing.T) {
	t.Parallel()

	pidFile := t.TempDir() + "/pid"

	a, err := startFakeWith(t, "grandchild", 500*time.Millisecond, pidFileEnv+"="+pidFile)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err = callOnce(t.Context(), a); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}

	_ = a.Close() //nolint:errcheck // The test checks the child.

	raw, err := os.ReadFile(pidFile) //nolint:gosec // The path is in the directory of the test.
	if err != nil {
		t.Fatalf("the fake adapter wrote no PID: %v", err)
	}

	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatalf("PID: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		<-time.After(20 * time.Millisecond)
	}

	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck // Clean up after a failed test.

		t.Fatalf("the child %d of the adapter is still alive after the stop", pid)
	}
}
