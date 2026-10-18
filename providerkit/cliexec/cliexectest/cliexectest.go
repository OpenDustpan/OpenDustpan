// Package cliexectest is the conformance suite for cliexec.Runner adapters.
// The suite needs a helper program. The test binary of the adapter is the
// helper: its TestMain calls RunHelper first.
//
//	func TestMain(m *testing.M) {
//		if code, ok := cliexectest.RunHelper(os.Args, os.Stdin, os.Stdout, os.Stderr); ok {
//			os.Exit(code)
//		}
//		goleak.VerifyTestMain(m)
//	}
//
//	func TestConformance(t *testing.T) {
//		t.Parallel()
//		cliexectest.Run(t, func(t *testing.T, maxOutput int64) cliexec.Runner {
//			return newRunner(maxOutput)
//		})
//	}
package cliexectest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/providerkit/cliexec"
)

// HelperArg as the first argument makes a test binary act as the helper
// program.
const HelperArg = "-cliexectest.helper"

// Factory returns a runner for one subtest, with a limit for the output of
// one command.
type Factory func(t *testing.T, maxOutput int64) cliexec.Runner

// The limits of the suite.
const (
	// maxOutput is the output limit of a runner under test.
	maxOutput = 64 << 10
	// floodBytes is the output of the mode "flood": more than the limit.
	floodBytes = 4 * maxOutput
	// shortTimeout is the timeout of the mode "sleep".
	shortTimeout = 200 * time.Millisecond
	// spawnTimeout is the timeout of the mode "spawn". It leaves time to
	// start the grandchild and write its process ID.
	spawnTimeout = 2 * time.Second
	// goneWithin is how long a process of a stopped group can take to end.
	goneWithin = 10 * time.Second
	// pollEvery is the wait between two checks of a process.
	pollEvery = 10 * time.Millisecond
)

// Run executes the conformance suite against runners from newRunner.
func Run(t *testing.T, newRunner Factory) {
	t.Helper()

	helper, err := os.Executable()
	if err != nil {
		t.Fatalf("find the helper program: %v", err)
	}

	cases := []struct {
		name string
		test func(t *testing.T, r cliexec.Runner, helper string)
	}{
		{name: "arguments arrive as they are, with no shell", test: testLiteralArgs},
		{name: "the program gets only the declared environment", test: testEnvironment},
		{name: "the standard input reaches the program", test: testStdin},
		{name: "a non-zero exit code is a result", test: testExitCode},
		{name: "the timeout stops the program", test: testTimeout},
		{name: "the timeout stops the whole process group", test: testProcessGroup},
		{name: "a canceled context stops the program", test: testCanceled},
		{name: "output over the limit is ErrTooLarge", test: testTooLarge},
		{name: "standard error over the limit is ErrTooLarge", test: testStderrTooLarge},
		{name: "an invalid command does not start", test: testInvalid},
		{name: "a missing program is an error", test: testMissing},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.test(t, newRunner(t, maxOutput), helper)
		})
	}
}

// echo is what the helper prints for the mode "echo".
type echo struct {
	Args  []string `json:"args"`
	Env   []string `json:"env"`
	Stdin string   `json:"stdin"`
}

// RunHelper acts as the helper program when args[1] is HelperArg, and
// returns the exit code and true. Otherwise it returns false and does
// nothing. The modes are "echo", "exit <code>", "sleep", "flood",
// "flood-stderr" and "spawn <file>". The mode "spawn" starts a grandchild
// in the mode "sleep", writes the process ID of the grandchild to the file,
// and sleeps.
func RunHelper(args []string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	if len(args) < 3 || args[1] != HelperArg {
		return 0, false
	}

	mode, ok := helperModes()[args[2]]
	if !ok {
		return 1, true
	}

	return mode(args, stdin, stdout, stderr), true
}

// modeArg is the index of the first argument of a mode: after the program,
// HelperArg and the mode.
const modeArg = 3

// helperMode runs one mode of the helper and returns its exit code.
type helperMode func(args []string, stdin io.Reader, stdout, stderr io.Writer) int

func helperModes() map[string]helperMode {
	return map[string]helperMode{
		"echo": func(args []string, stdin io.Reader, stdout, _ io.Writer) int {
			in, err := io.ReadAll(stdin)
			if err != nil {
				return 1
			}

			e := echo{Args: args[modeArg:], Env: os.Environ(), Stdin: string(in)}
			if err := json.NewEncoder(stdout).Encode(e); err != nil {
				return 1
			}

			return 0
		},
		"exit": func(args []string, _ io.Reader, _, _ io.Writer) int {
			if len(args) <= modeArg {
				return 1
			}

			code, err := strconv.Atoi(args[modeArg])
			if err != nil {
				return 1
			}

			return code
		},
		"sleep":        func([]string, io.Reader, io.Writer, io.Writer) int { return sleep() },
		"flood":        func(_ []string, _ io.Reader, stdout, _ io.Writer) int { return flood(stdout) },
		"flood-stderr": func(_ []string, _ io.Reader, _, stderr io.Writer) int { return flood(stderr) },
		"spawn": func(args []string, _ io.Reader, _, _ io.Writer) int {
			if len(args) <= modeArg || spawn(args[0], args[modeArg]) != nil {
				return 1
			}

			return sleep()
		},
	}
}

func sleep() int {
	<-time.NewTimer(time.Hour).C

	return 0
}

func flood(w io.Writer) int {
	if _, err := w.Write(bytes.Repeat([]byte("x"), floodBytes)); err != nil {
		return 1
	}

	return 0
}

// spawn starts the helper as a grandchild in the mode "sleep", in the process
// group of the helper, and writes its process ID to pidFile.
func spawn(helper, pidFile string) error {
	c := exec.Command(helper, HelperArg, "sleep") //nolint:noctx // The test binary itself; the runner stops it.
	if err := c.Start(); err != nil {
		return fmt.Errorf("start the grandchild: %w", err)
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(c.Process.Pid)), 0o600); err != nil {
		return fmt.Errorf("write the process ID: %w", err)
	}

	return nil
}

func helperCommand(helper string, timeout time.Duration, args ...string) cliexec.Command {
	return cliexec.Command{
		Program: helper, Args: append([]string{HelperArg}, args...), Env: nil, Stdin: nil, Timeout: timeout,
	}
}

func runEcho(t *testing.T, r cliexec.Runner, cmd cliexec.Command) echo {
	t.Helper()

	res, err := r.Run(t.Context(), cmd)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run() = exit %d, %v, stderr %q", res.ExitCode, err, res.Stderr)
	}

	var e echo
	if err := json.Unmarshal(res.Stdout, &e); err != nil {
		t.Fatalf("helper output %q: %v", res.Stdout, err)
	}

	return e
}

func testLiteralArgs(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	args := []string{"$(id)", "a b", "*", "'q'", "; true", "", "--x=$HOME", "\"", "\\"}
	got := runEcho(t, r, helperCommand(helper, time.Minute, append([]string{"echo"}, args...)...))

	if !slices.Equal(got.Args, args) {
		t.Fatalf("program got %q, want %q", got.Args, args)
	}
}

func testEnvironment(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	cmd := helperCommand(helper, time.Minute, "echo")
	cmd.Env = map[string]string{"ONCALL_TEAM": "ops"}
	got := runEcho(t, r, cmd)

	names := map[string]string{}
	for _, kv := range got.Env {
		name, value, _ := strings.Cut(kv, "=")
		names[name] = value
	}

	if names["ONCALL_TEAM"] != "ops" {
		t.Error("program did not get ONCALL_TEAM")
	}

	for _, name := range []string{"PATH", "HOME", "TMPDIR"} {
		if names[name] == "" {
			t.Errorf("program did not get %s", name)
		}
	}

	for name := range names {
		if !slices.Contains([]string{"ONCALL_TEAM", "PATH", "HOME", "TMPDIR"}, name) {
			t.Errorf("program got the variable %s, which the command did not declare", name)
		}
	}

	if home, err := os.UserHomeDir(); err == nil && names["HOME"] == home {
		t.Error("program got the runner's own HOME, want a private directory of the run")
	}
}

func testStdin(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	cmd := helperCommand(helper, time.Minute, "echo")
	cmd.Stdin = []byte(`{"person":"ana"}`)

	if got := runEcho(t, r, cmd); got.Stdin != `{"person":"ana"}` {
		t.Fatalf("program read %q from the standard input", got.Stdin)
	}
}

func testExitCode(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	res, err := r.Run(t.Context(), helperCommand(helper, time.Minute, "exit", "4"))
	if err != nil || res.ExitCode != 4 {
		t.Fatalf("Run() = exit %d, %v, want exit 4 and nil", res.ExitCode, err)
	}
}

func testTimeout(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	start := time.Now()

	_, err := r.Run(t.Context(), helperCommand(helper, shortTimeout, "sleep"))
	if !errors.Is(err, cliexec.ErrTimeout) {
		t.Fatalf("Run() error = %v, want ErrTimeout", err)
	}

	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("Run() returned after %s, want soon after the timeout", d)
	}
}

func testProcessGroup(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")

	_, err := r.Run(t.Context(), helperCommand(helper, spawnTimeout, "spawn", pidFile))
	if !errors.Is(err, cliexec.ErrTimeout) {
		t.Fatalf("Run() error = %v, want ErrTimeout", err)
	}

	data, err := os.ReadFile(pidFile) //nolint:gosec // A file of this test.
	if err != nil {
		t.Fatalf("the helper did not start its grandchild: %v", err)
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("grandchild process ID %q: %v", data, err)
	}

	deadline := time.Now().Add(goneWithin)
	for alive(pid) {
		if time.Now().After(deadline) {
			stop(pid)
			t.Fatalf("grandchild %d still runs %s after the timeout, want the whole group stopped", pid, goneWithin)
		}

		<-time.After(pollEvery)
	}
}

func testCanceled(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(shortTimeout, cancel)

	defer timer.Stop()

	start := time.Now()

	_, err := r.Run(ctx, helperCommand(helper, time.Hour, "sleep"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}

	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("Run() returned after %s, want soon after the cancel", d)
	}
}

func testStderrTooLarge(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	_, err := r.Run(t.Context(), helperCommand(helper, time.Minute, "flood-stderr"))
	if !errors.Is(err, cliexec.ErrTooLarge) {
		t.Fatalf("Run() error = %v, want ErrTooLarge", err)
	}
}

func testTooLarge(t *testing.T, r cliexec.Runner, helper string) {
	t.Helper()

	if _, err := r.Run(t.Context(), helperCommand(helper, time.Minute, "flood")); !errors.Is(err, cliexec.ErrTooLarge) {
		t.Fatalf("Run() error = %v, want ErrTooLarge", err)
	}
}

func testInvalid(t *testing.T, r cliexec.Runner, _ string) {
	t.Helper()

	cmd := cliexec.Command{Program: "bin/x", Args: nil, Env: nil, Stdin: nil, Timeout: time.Minute}
	if _, err := r.Run(t.Context(), cmd); !errors.Is(err, cliexec.ErrInvalidCommand) {
		t.Fatalf("Run() error = %v, want ErrInvalidCommand", err)
	}
}

func testMissing(t *testing.T, r cliexec.Runner, _ string) {
	t.Helper()

	cmd := cliexec.Command{
		Program: "/nonexistent/cliexectest-program", Args: nil, Env: nil, Stdin: nil, Timeout: time.Minute,
	}
	if _, err := r.Run(t.Context(), cmd); err == nil || errors.Is(err, cliexec.ErrInvalidCommand) {
		t.Fatalf("Run() error = %v, want a start error", err)
	}
}
