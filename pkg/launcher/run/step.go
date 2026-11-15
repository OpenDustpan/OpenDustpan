package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// stepResult is the end of one step: the exit code of its process, -1 when
// no process ended on its own, and the reason why the phase fails when the
// exit code does not tell it.
type stepResult struct {
	exitCode int
	reason   string
}

// pumpWait is the longest wait for the end of the output of a step after
// its process group ended. A process that left the group can hold the pipe
// open; the launcher then closes its end.
const pumpWait = 5 * time.Second

// runStep records the begin of the step, runs it, sends its log and records
// its end.
func (r *phaseRun) runStep(ctx context.Context, step *protocol.Step) stepResult {
	// Defense in depth: the server checks the names of the steps that it
	// stores, and the session client checks the name of each path; this
	// check stays so that a hostile name never reaches a path or a message.
	if !job.ValidStepName(step.Name) {
		return stepResult{exitCode: -1, reason: "the step name is not valid"}
	}

	logs := newLogSink(ctx, r, step.Name)
	masker := launcher.NewMasker(logs.add)

	// Defense in depth: a handoff token has at most launcher.MaxHandoff
	// bytes, so the masker holds it; this check stays so that a step whose
	// secrets the masker cannot hold never starts.
	if err := masker.Add([]byte(r.cfg.Handoff)); err != nil {
		return stepResult{exitCode: -1, reason: "the launcher cannot mask the secrets of the step"}
	}

	if err := r.retry(ctx, func() error { return r.sess.BeginStep(ctx, step.Name) }); err != nil {
		return r.callFailed("record the begin of the step", err)
	}

	started := time.Now()

	stopFlush := logs.flushEvery(r.flushEvery())
	res := r.execStep(ctx, step, masker)

	if err := masker.Close(); err != nil {
		r.log.WarnContext(ctx, "close the log of the step", slog.String("step", step.Name), slog.Any("error", err))
	}

	stopFlush()
	logs.flush()

	if r.isLost() {
		return res
	}

	end := protocol.StepEnd{
		ExitCode: res.exitCode, StartedAt: started.UTC(), FinishedAt: time.Now().UTC(),
		LogChunks: logs.chunks, LogSHA256: logs.sum(),
	}

	if err := r.retry(ctx, func() error { return r.sess.EndStep(ctx, step.Name, end) }); err != nil {
		return r.callFailed("record the end of the step", err)
	}

	if logs.err != nil && res.reason == "" {
		res.reason = "the log of the step could not be stored"
	}

	return res
}

// callFailed records a lost claim, or returns the reason of a failed call.
func (r *phaseRun) callFailed(what string, err error) stepResult {
	if errors.Is(err, session.ErrClaimLost) {
		r.markLost()

		return stepResult{exitCode: -1, reason: claimLostReason}
	}

	r.log.Warn("a call of the step failed", slog.String("call", what), slog.Any("error", err))

	return stepResult{exitCode: -1, reason: "the server did not " + what}
}

// flushEvery returns the longest wait of output before it is sent.
func (r *phaseRun) flushEvery() time.Duration {
	if r.cfg.FlushEvery > 0 {
		return r.cfg.FlushEvery
	}

	return DefaultFlushEvery
}

// claimLostReason is the reason of a step whose claim is lost: the renewal
// loop recorded the loss, and the launcher reports nothing more.
const claimLostReason = "the claim is lost"

// refuseStep returns the fail-closed result of a step that this release must
// not start, and whether the step is refused. It starts no process.
func (r *phaseRun) refuseStep(step *protocol.Step, log launcher.Masker) (stepResult, bool) {
	// This release cannot deliver the credentials of a tool step that needs
	// a profile, so the step fails closed before its program starts. A tool
	// step with no profile needs no credentials, and it runs.
	if step.Role == string(job.RoleTool) && r.profile != "" {
		// The profile name comes from the assignment, so it may hold CR or
		// LF: remove both before the name goes into the reason and the log.
		name := strings.NewReplacer("\r", "", "\n", "").Replace(r.profile)
		reason := fmt.Sprintf("this launcher release cannot deliver the credentials of profile %q", name)
		r.note(log, reason)

		return stepResult{exitCode: -1, reason: reason}, true
	}

	// This release cannot upload declared outputs. A step whose output a
	// later phase of the job reads fails closed before its program starts;
	// an output that no later phase reads does not stop the step.
	for _, o := range step.Outputs {
		if !o.Needed {
			continue
		}

		// The kind comes from the assignment, so it may hold CR or LF:
		// remove both before the kind goes into the reason and the log.
		kind := strings.NewReplacer("\r", "", "\n", "").Replace(o.Kind)
		reason := fmt.Sprintf("this launcher release cannot upload output %q that a later phase needs", kind)
		r.note(log, reason)

		return stepResult{exitCode: -1, reason: reason}, true
	}

	// Defense in depth: the renewal loop records a lost claim, and the wait
	// loop kills the process group of a step that runs; this check stays so
	// that a step whose claim is lost before its start never starts a
	// process.
	if r.isLost() {
		return stepResult{exitCode: -1, reason: claimLostReason}, true
	}

	// Defense in depth: the renewal loop records a cancel, and the wait loop
	// interrupts a step that runs; this check stays so that a canceled step
	// never starts a process.
	if r.isCanceled() {
		return stepResult{exitCode: -1, reason: "the job was canceled"}, true
	}

	// Defense in depth: the wait loop interrupts a step that runs at a stop
	// signal; this check stays so that a step after a stop never starts a
	// process.
	if stop := r.cfg.Stop; stop != nil {
		select {
		case <-stop:
			return stepResult{exitCode: -1, reason: "the launcher got a stop signal"}, true
		default:
		}
	}

	return stepResult{exitCode: 0, reason: ""}, false
}

// execStep starts the process of the step and waits for its end, its
// timeout, a cancel, a lost claim or a stop. The output goes to log.
func (r *phaseRun) execStep(ctx context.Context, step *protocol.Step, log launcher.Masker) stepResult {
	if res, refused := r.refuseStep(step, log); refused {
		return res
	}

	// This release does not upload declared outputs. A step whose output a
	// later phase reads was refused above, so a step that reaches this point
	// declares outputs that no later phase reads.
	if len(step.Outputs) > 0 {
		r.note(log, "this launcher release does not upload declared outputs")
	}

	cmd, err := r.command(step)
	if err != nil {
		r.note(log, err.Error())

		return stepResult{exitCode: -1, reason: err.Error()}
	}

	// This release does not deliver credentials. A tool step whose
	// assignment names a profile was refused above, so a tool step that
	// reaches this point runs with no credentials.
	if step.Role == string(job.RoleTool) {
		r.note(log, "this launcher release gives no credentials to a tool step")
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return stepResult{exitCode: -1, reason: "the launcher could not make the pipe of the output"}
	}

	cmd.Stdout, cmd.Stderr = pw, pw

	if err = cmd.Start(); err != nil {
		r.closeFile(pr)
		r.closeFile(pw)
		r.note(log, "the program of the step did not start")

		return stepResult{exitCode: -1, reason: "the program of the step did not start"}
	}

	// The launcher keeps only the read end: the output ends when every
	// process of the step closed the write end.
	r.closeFile(pw)

	pumped := make(chan struct{})

	go func() {
		defer close(pumped)

		if pumpErr := pump(pr, log); pumpErr != nil {
			r.log.WarnContext(
				ctx,
				"read the output of the step",
				slog.String("step", step.Name),
				slog.Any("error", pumpErr),
			)
		}
	}()

	res := r.wait(ctx, cmd, step)

	// The process group ends with the step, also the processes that the
	// step left running.
	r.killGroup(cmd.Process.Pid, syscall.SIGKILL)

	t := time.NewTimer(pumpWait)

	select {
	case <-pumped:
	case <-t.C:
	}

	t.Stop()
	r.closeFile(pr)
	<-pumped

	return res
}

// closeFile closes f, and writes a message when the close fails.
func (r *phaseRun) closeFile(f *os.File) {
	if err := f.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		r.log.Warn("close a pipe of a step", slog.Any("error", err))
	}
}

// note writes one line of the launcher to the log of a step.
func (r *phaseRun) note(log launcher.Masker, text string) {
	if err := log.Mask([]byte(notePrefix + text + "\n")); err != nil {
		r.log.Warn("write to the log of a step", slog.Any("error", err))
	}
}

// killGroup sends sig to the process group of the step whose first process
// is pid. A group with no process left is not an error.
func (r *phaseRun) killGroup(pid int, sig syscall.Signal) {
	if pid <= 0 {
		return
	}

	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		r.log.Warn("signal the process group of a step", slog.Int("pid", pid), slog.Any("error", err))
	}
}

// stopKind is why the launcher stops a step.
type stopKind int

const (
	stopNone stopKind = iota
	stopTimeout
	stopCanceled
	stopSignal
)

// waiter is the state of the wait for one step.
type waiter struct {
	r    *phaseRun
	cmd  *exec.Cmd
	step *protocol.Step
	why  stopKind
	kill <-chan time.Time
}

// interrupt sends SIGINT to the process group once, for the reason k, and
// arms SIGKILL after the grace period.
func (w *waiter) interrupt(k stopKind) {
	if w.why != stopNone {
		return
	}

	w.why = k
	w.r.killGroup(w.cmd.Process.Pid, syscall.SIGINT)
	w.kill = time.After(w.r.grace(w.step))
}

// wait waits for the end of the process of the step. At the timeout, a
// cancel or a stop, it sends SIGINT to the process group, and SIGKILL after
// the grace period. On a lost claim it sends SIGKILL at once.
func (r *phaseRun) wait(ctx context.Context, cmd *exec.Cmd, step *protocol.Step) stepResult {
	waited := make(chan error, 1)

	go func() { waited <- cmd.Wait() }()

	timeout := step.Timeout.Std()
	if timeout <= 0 {
		timeout = job.DefaultStepTimeout
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	w := &waiter{r: r, cmd: cmd, step: step, why: stopNone, kill: nil}
	stop, done := r.cfg.Stop, ctx.Done()

	for {
		select {
		case err := <-waited:
			return result(err, w.why, timeout)
		case <-deadline.C:
			w.interrupt(stopTimeout)
		case <-w.kill:
			r.killGroup(cmd.Process.Pid, syscall.SIGKILL)
		case <-stop:
			stop = nil

			w.interrupt(stopSignal)
		case <-done:
			done = nil

			r.killGroup(cmd.Process.Pid, syscall.SIGKILL)
		case <-r.events:
			if r.isLost() {
				r.killGroup(cmd.Process.Pid, syscall.SIGKILL)
			} else if r.isCanceled() {
				w.interrupt(stopCanceled)
			}
		}
	}
}

// result returns the end of a step from the error of its wait.
func result(err error, why stopKind, timeout time.Duration) stepResult {
	code := 0

	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return stepResult{exitCode: -1, reason: "the launcher lost the process of the step"}
		}

		code = exit.ExitCode()
	}

	switch why {
	case stopTimeout:
		return stepResult{exitCode: code, reason: fmt.Sprintf("the step did not end in %v", timeout)}
	case stopCanceled:
		return stepResult{exitCode: code, reason: "the job was canceled"}
	case stopSignal:
		return stepResult{exitCode: code, reason: "the launcher got a stop signal"}
	case stopNone:
		return stepResult{exitCode: code, reason: ""}
	default:
		return stepResult{exitCode: code, reason: "the launcher stopped the step"}
	}
}

// grace returns the grace period of the step after SIGINT.
func (r *phaseRun) grace(step *protocol.Step) time.Duration {
	if r.cfg.Grace != nil {
		return r.cfg.Grace(step.ChangesResources)
	}

	if step.ChangesResources {
		return runner.GraceChanges
	}

	return runner.GraceOther
}

// readSize is the size of one read of the output of a step.
const readSize = 32 << 10

// pump reads the output of a step from r until its end and gives it to the
// masker. The end of the pipe, also when the launcher closed it, is no
// error. After a failed Mask it reads on and drops the output, so that the
// step never waits on a full pipe; it returns the first error.
func pump(r io.Reader, log launcher.Masker) error {
	buf := make([]byte, readSize)

	var maskErr error

	for {
		n, err := r.Read(buf)
		if n > 0 && maskErr == nil {
			if mErr := log.Mask(buf[:n]); mErr != nil {
				maskErr = fmt.Errorf("mask the output: %w", mErr)
			}
		}

		switch {
		case err == nil:
		case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed):
			return maskErr
		default:
			return errors.Join(maskErr, fmt.Errorf("read the output: %w", err))
		}
	}
}
