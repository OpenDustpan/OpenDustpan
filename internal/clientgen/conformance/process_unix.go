//go:build unix

package conformance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// prepareGroup puts the adapter in a process group of its own, so that a kill
// reaches the processes that the adapter starts. A stop after the post-exit
// step has ended the group sends no signal.
func prepareGroup(cmd *exec.Cmd, a *ProcessAdapter) {
	cmd.SysProcAttr = new(syscall.SysProcAttr)
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		a.groupMu.Lock()
		defer a.groupMu.Unlock()

		if a.groupEnded.Load() {
			return nil
		}

		return killGroup(cmd.Process)
	}
}

// killGroup stops the process group of the adapter. The group ID is the
// process ID, because of Setpgid.
func killGroup(p *os.Process) error {
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill the process group %d: %w", p.Pid, err)
	}

	return nil
}

// endGroupAfterExit waits until the adapter has exited, then stops the
// processes that the adapter left behind: an adapter that exits on its own
// does not stop them. It marks the group as ended, because the wait that
// follows can reap the adapter, and a later kill must send no signal to a
// group ID that can belong to another process.
func endGroupAfterExit(a *ProcessAdapter) {
	p := a.cmd.Process

	if err := waitForExitNoReap(p.Pid); err != nil {
		a.stderr.note(err)
	}

	// The mark and the kill are one step under groupMu: a kill that is in
	// progress ends before the mark, and a kill after the mark sends nothing.
	a.groupMu.Lock()
	defer a.groupMu.Unlock()

	if err := killGroup(p); err != nil {
		a.stderr.note(err)
	}

	a.groupEnded.Store(true)
}

// waitForExitNoReap waits until the process exits without reaping it. Nothing
// reaps the adapter before cmd.Wait, so its process ID stays reserved without
// this wait. What this wait adds is that the group kill happens only after
// the exit: the kill cannot end a clean adapter while it exits. The waitid
// constants are in the file of each operating system.
func waitForExitNoReap(pid int) error {
	var info [siginfoBytes]byte

	for {
		_, _, errno := syscall.Syscall6(
			syscall.SYS_WAITID,
			waitidPid,
			uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])), // #nosec G103 // Buffer holds a full siginfo_t.
			waitidExited|waitidNowait,
			0,
			0,
		)
		if errno == 0 {
			return nil
		}

		if errno != syscall.EINTR {
			return fmt.Errorf("waitid %d: %w", pid, errno)
		}
	}
}
