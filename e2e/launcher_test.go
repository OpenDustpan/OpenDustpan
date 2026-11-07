//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/launcher"
)

var errLauncherRun = errors.New("the phase that the launcher ran is not as expected")

// launcherLimit is the longest run of the launcher binary in the suite.
const launcherLimit = 2 * time.Minute

// memoryDir is the memory-backed volume of the secrets directory.
const memoryDir = "/dev/shm"

// TestLauncher drives the real launcher binary against the server of each
// store: a fake runner claims the phase of a started job and hands the
// handoff token to the launcher on a pipe. The launcher refuses the tool
// step of the phase, records its log and finishes the phase.
func TestLauncher(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the launcher runs steps on Linux only")
	}

	if !onMemoryVolume(memoryDir) {
		t.Skip("the launcher needs " + memoryDir + " on tmpfs")
	}

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	bin := buildLauncherBinary(t)

	t.Run("one_phase", func(t *testing.T) {
		t.Parallel()

		for _, store := range stores {
			t.Run(string(store), func(t *testing.T) {
				t.Parallel()

				onePhase(t, e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store), bin)
			})
		}
	})
}

// onePhase starts a plan job, claims its phase, runs the launcher on it and
// reads the phase and the log back. The first step of the plan is a tool
// step: this launcher release cannot get its credentials, so the step fails
// before its program starts and the phase fails.
func onePhase(t *testing.T, env *e2e.Env, bin string) {
	t.Helper()

	c := claimAPhase(t, env)
	env.LogJobStepsOnFailure(t, e2e.Namespace, c.job, c.handoff)

	code, stderr := runLauncher(t, env, bin, c.handoff)
	if code != int(launcher.ExitOK) {
		t.Fatalf("the launcher ended with %d, want 0:\n%s", code, stderr)
	}

	if strings.Contains(stderr, c.handoff) {
		t.Fatal("the messages of the launcher hold the handoff token")
	}

	readsThePhase(t, env, c.job)

	if env.InProcess() {
		// The server log and the stored log are read from the binary server.
		return
	}

	if found := env.ServerLogHolds(t, c.handoff); len(found) > 0 {
		t.Fatal("the server log holds the handoff token")
	}

	// The default plan steps of OpenTofu run the program "tofu". The
	// launcher refuses the tool step before the program starts, so the log
	// holds the note of the refusal only.
	want := "dustpan-launcher: this launcher release does not run a tool step\n"
	if got := env.JobStepLog(t.Context(), t, e2e.Namespace, c.job, "init"); got != want {
		t.Fatalf("stored log of init = %q, want %q", got, want)
	}
}

// readsThePhase reads the job through the API: the phase failed at its first
// step, which has the exit code -1 and a closed log.
func readsThePhase(t *testing.T, env *e2e.Env, jobID string) {
	t.Helper()

	env.APIStepA(t.Context(), t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "read the job that the launcher ran", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, jobID), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: checkLauncherJob,
	})
}

// launcherJob is the part of the job document that the launcher test reads.
type launcherJob struct {
	State  string          `json:"state"`
	Reason string          `json:"reason"`
	Phases []launcherPhase `json:"phases"`
}

// launcherPhase is a phase of launcherJob.
type launcherPhase struct {
	Kind       string         `json:"kind"`
	FinishedAt *time.Time     `json:"finishedAt"`
	Steps      []launcherStep `json:"steps"`
}

// launcherStep is the record of a step of launcherPhase.
type launcherStep struct {
	Name     string       `json:"name"`
	ExitCode int          `json:"exitCode"`
	Log      *launcherLog `json:"log"`
}

// launcherLog is the log of launcherStep.
type launcherLog struct {
	Closed bool `json:"closed"`
}

// checkLauncherJob checks that the plan phase ended, that its first step
// failed before its program started, and that the phase failed with the
// refusal.
func checkLauncherJob(body string) error {
	var doc launcherJob
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return fmt.Errorf("decode job: %w", err)
	}

	if len(doc.Phases) == 0 || doc.Phases[0].Kind != "plan" || doc.Phases[0].FinishedAt == nil {
		return fmt.Errorf("%w: state %s, phases %s", errLauncherRun, doc.State, body)
	}

	steps := doc.Phases[0].Steps
	if len(steps) != 1 || steps[0].Name != "init" || steps[0].ExitCode != -1 ||
		steps[0].Log == nil || !steps[0].Log.Closed {
		return fmt.Errorf("%w: steps %+v; want init with the exit code -1 and a closed log", errLauncherRun, steps)
	}

	const reason = "this launcher release does not run a tool step"
	if doc.State != "failed" || !strings.Contains(doc.Reason, reason) {
		return fmt.Errorf("%w: state %s, reason %q; want failed with %q", errLauncherRun, doc.State, doc.Reason, reason)
	}

	return nil
}

// buildLauncherBinary builds cmd/dustpan-launcher with no cgo.
func buildLauncherBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), launcher.Binary)

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find the go tool: %v", err)
	}

	cmd := process(goBin, []string{"build", "-o", bin, "opendustpan.org/opendustpan/cmd/dustpan-launcher"},
		append(os.Environ(), "CGO_ENABLED=0"))

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the launcher: %v\n%s", err, out)
	}

	return bin
}

// runLauncher runs the launcher binary in the mode run against env, with the
// handoff token on the pipe of file descriptor 3, and returns its exit code
// and its messages. The program "tofu" on its PATH is a test shim: a link to
// echo in a temporary directory of this test, which no build ships.
func runLauncher(t *testing.T, env *e2e.Env, bin, handoff string) (int, string) {
	t.Helper()

	tools := filepath.Join(t.TempDir(), "test-shim-bin")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatalf("make the test shim directory: %v", err)
	}

	// The shim is a script and not a link to echo: on a host where echo is a
	// multi-call binary, a link with another name fails with exit code 1.
	shim := "#!/bin/sh\necho \"$@\"\n"
	writeShim(t, tools, shim)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	if _, err = w.WriteString(handoff); err != nil {
		t.Fatalf("write the handoff: %v", err)
	}

	if err = w.Close(); err != nil {
		t.Fatalf("close the handoff pipe: %v", err)
	}

	// The launcher gets PATH and the certificate of the server only. The
	// handoff token is never in an argument or a variable.
	var stderr bytes.Buffer

	cmd := process(bin, []string{
		"run", "--server", env.URL, "--handoff", "pipe", "--work-dir", t.TempDir(), "--secrets-dir", memoryDir,
	}, []string{"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH"), "SSL_CERT_FILE=" + env.CABundle()})
	cmd.ExtraFiles = []*os.File{r}
	cmd.Stderr = &stderr

	err = cmd.Start()
	if closeErr := r.Close(); closeErr != nil {
		t.Errorf("close the read end: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("start the launcher: %v", err)
	}

	return waitLauncher(t, cmd), stderr.String()
}

// waitLauncher waits for the end of the launcher, at most launcherLimit.
func waitLauncher(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(launcherLimit)
	defer timer.Stop()

	select {
	case err := <-done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatalf("wait for the launcher: %v", err)
		}
	case <-timer.C:
		if err := cmd.Process.Kill(); err != nil {
			t.Errorf("kill the launcher: %v", err)
		}

		waitErr := <-done
		t.Fatalf("the launcher did not end in %v: %v", launcherLimit, waitErr)
	}

	return cmd.ProcessState.ExitCode()
}

// process returns the process of the program at the absolute path prog with
// args and the environment env: no shell, no lookup.
func process(prog string, args, env []string) *exec.Cmd {
	return &exec.Cmd{
		Path: prog, Args: append([]string{prog}, args...), Env: env, Dir: "", Stdin: nil, Stdout: nil, Stderr: nil,
		ExtraFiles: nil, SysProcAttr: nil, Process: nil, ProcessState: nil, Err: nil, Cancel: nil, WaitDelay: 0,
	}
}

// onMemoryVolume reports whether dir passes the check of the launcher for a
// memory-backed volume.
func onMemoryVolume(dir string) bool {
	made, err := (launcher.TmpfsVolume{}).Prepare(dir)
	if err != nil {
		return false
	}

	return os.Remove(made) == nil
}

// writeShim writes the executable script "tofu" with the text body in dir.
func writeShim(t *testing.T, dir, body string) {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open the test shim directory: %v", err)
	}

	f, err := root.OpenFile("tofu", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatalf("create the tofu test shim: %v", err)
	}

	if _, err = f.WriteString(body); err != nil {
		t.Fatalf("write the tofu test shim: %v", err)
	}

	if err = f.Close(); err != nil {
		t.Fatalf("close the tofu test shim: %v", err)
	}

	if err = root.Close(); err != nil {
		t.Fatalf("close the test shim directory: %v", err)
	}
}
