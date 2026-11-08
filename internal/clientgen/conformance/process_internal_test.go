package conformance

import (
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
)

// CountKills makes a count each signal that kill sends to the group of a.
// The signal still goes to the group. Call it before the first request.
func CountKills(a *ProcessAdapter) *atomic.Int64 {
	n := new(atomic.Int64)
	a.killProcess = func(p *os.Process) error {
		n.Add(1)

		return killGroup(p)
	}

	return n
}

// Kill exports kill.
func Kill(a *ProcessAdapter) { a.kill() }

// NewKillAdapter returns an adapter for a test of the guard of kill. The
// adapter has this process as its process, an open done channel, and a hook
// that counts each call of killProcess and sends no signal, so no signal
// leaves the test. groupEnded sets the flag that the post-exit step sets when
// it ends the group.
func NewKillAdapter(t *testing.T, groupEnded bool) (*ProcessAdapter, *atomic.Int64) {
	t.Helper()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find this process: %v", err)
	}

	n := new(atomic.Int64)

	a := new(ProcessAdapter)
	a.cmd = new(exec.Cmd)
	a.cmd.Process = proc
	a.stdin = nopWriteCloser{}
	a.done = make(chan struct{})
	a.stderr = new(tailBuffer)
	a.stderr.max = stderrKeepBytes
	a.stop = make(chan struct{})
	a.killProcess = func(*os.Process) error {
		n.Add(1)

		return nil
	}
	a.groupEnded.Store(groupEnded)

	return a, n
}

// NewEndedGroupCommand returns a command that prepareGroup prepared, with the
// group of its adapter ended. The command has no process.
func NewEndedGroupCommand() *exec.Cmd {
	cmd := new(exec.Cmd)
	a := new(ProcessAdapter)

	prepareGroup(cmd, a)
	a.groupEnded.Store(true)

	return cmd
}

// GroupEnded reports whether the post-exit step has ended the group of a.
func GroupEnded(a *ProcessAdapter) bool { return a.groupEnded.Load() }

// nopWriteCloser is an io.WriteCloser that does nothing.
type nopWriteCloser struct{}

// Write implements io.Writer.
func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }

// Close implements io.Closer.
func (nopWriteCloser) Close() error { return nil }
