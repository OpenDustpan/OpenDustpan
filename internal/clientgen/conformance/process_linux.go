//go:build linux

package conformance

import (
	"fmt"
	"syscall"
	"unsafe"
)

// The selectors and the options of waitid, and the size of the buffer that
// the kernel fills.
const (
	waitidPid    = 1
	waitidExited = 4
	waitidNowait = 0x01000000
	siginfoBytes = 128
)

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

	if err := killGroup(p); err != nil {
		a.stderr.note(err)
	}

	a.groupEnded.Store(true)
}

// waitForExitNoReap waits until the process exits without reaping it. Nothing
// reaps the adapter before cmd.Wait, so its process ID stays reserved without
// this wait. What this wait adds is that the group kill happens only after
// the exit: the kill cannot end a clean adapter while it exits.
func waitForExitNoReap(pid int) error {
	var info [siginfoBytes]byte

	for {
		_, _, errno := syscall.Syscall6(
			syscall.SYS_WAITID,
			waitidPid,
			uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])), // #nosec G103 // Buffer is 128 bytes; siginfo_t is 128 bytes on Linux.
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
