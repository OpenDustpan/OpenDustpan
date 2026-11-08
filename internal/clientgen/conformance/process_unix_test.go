//go:build unix

package conformance_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// pidFileEnv names the file where the fake adapter writes the PID of its child.
const pidFileEnv = "CONFORMANCE_FAKE_PIDFILE"

// lockFileEnv names the file that the child of the fake adapter locks. The
// child holds the lock until it ends, so a test can wait on the same lock
// instead of a clock.
const lockFileEnv = "CONFORMANCE_FAKE_LOCKFILE"

// startGrandchild starts a process that the fake adapter owns, and writes its
// PID to the file that the test names. The process has no pipe of the harness,
// so only a kill of the group can end it.
func startGrandchild() {
	cmd := exec.CommandContext(context.Background(), os.Args[0]) //nolint:gosec // The test starts its own binary.
	cmd.Env = []string{fakeModeEnv + "=sleeper"}

	if lockFile := os.Getenv(lockFileEnv); lockFile != "" {
		cmd.Env = append(cmd.Env, lockFileEnv+"="+lockFile)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}

	if err := cmd.Start(); err != nil {
		return
	}

	// The child holds the lock before the adapter goes on.
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		return
	}

	if err := stdout.Close(); err != nil {
		return
	}

	//nolint:gosec // G703: the path is from the test, which names the file in its own directory.
	if err := os.WriteFile(os.Getenv(pidFileEnv), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		return
	}
}

// runSleeper is the sleeper mode of the fake adapter. With the lock file
// variable set, the process takes an exclusive lock on that file and blocks
// until a kill ends it, so that a test can wait on the same lock instead of a
// clock. Without the variable, it ends after a minute, as before.
func runSleeper() {
	lockFile := os.Getenv(lockFileEnv)
	if lockFile == "" {
		<-time.After(time.Minute)

		return
	}

	fd, err := syscall.Open(lockFile, syscall.O_CREAT|syscall.O_RDWR, 0o600)
	if err != nil {
		return
	}

	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return
	}

	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		return
	}

	// The process must not end on its own, or a test that misses a kill of
	// the group would pass. A select with no case would let the runtime stop
	// the program, so the loop waits in one.
	for {
		<-time.After(time.Hour)
	}
}

// waitUntilGone waits until the process that holds the lock on the file has
// exited. The kernel releases the lock when the process ends, so the test
// needs no clock. A regression that misses the kill makes this wait block
// until the go test timeout.
func waitUntilGone(t *testing.T, lockFile string) {
	t.Helper()

	fd, err := syscall.Open(lockFile, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open the lock file: %v", err)
	}

	defer func() {
		if cerr := syscall.Close(fd); cerr != nil {
			t.Errorf("close the lock file: %v", cerr)
		}
	}()

	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	if err := syscall.Flock(fd, syscall.LOCK_UN); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
}

// TestProcessAdapterKillsTheGroup checks that a stop ends the processes that
// the adapter started, and not only the adapter.
func TestProcessAdapterKillsTheGroup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pidFile, lockFile := dir+"/pid", dir+"/lock"

	a, err := startFakeWith(t, "grandchild", 500*time.Millisecond,
		pidFileEnv+"="+pidFile, lockFileEnv+"="+lockFile)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err = callOnce(t.Context(), a); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}

	_ = a.Close() //nolint:errcheck // The test checks the child.

	// The fake adapter wrote the PID before the stop: the child started.
	if _, err = os.Stat(pidFile); err != nil {
		t.Fatalf("the fake adapter wrote no PID: %v", err)
	}

	waitUntilGone(t, lockFile)
}

// TestProcessAdapterCleanShutdownEndsTheGroup checks that a clean shutdown
// ends the processes that the adapter started, and not only the adapter.
func TestProcessAdapterCleanShutdownEndsTheGroup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pidFile, lockFile := dir+"/pid", dir+"/lock"

	a, err := startFakeWith(t, "grandchildclean", 0,
		pidFileEnv+"="+pidFile, lockFileEnv+"="+lockFile)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err = a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The fake adapter wrote the PID before the close: the child started.
	if _, err = os.Stat(pidFile); err != nil {
		t.Fatalf("the fake adapter wrote no PID: %v", err)
	}

	waitUntilGone(t, lockFile)
}
