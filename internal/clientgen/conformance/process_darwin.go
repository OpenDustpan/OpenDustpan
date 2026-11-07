//go:build darwin

package conformance

import (
	"fmt"
	"syscall"
	"unsafe"
)

// The selectors and the options of waitid, and the size of the buffer that
// the kernel fills. The idtype_t of macOS is P_ALL = 0, P_PID = 1, P_PGID = 2.
const (
	waitidPid    = 1
	waitidExited = 0x04
	waitidNowait = 0x20
	siginfoBytes = 128
)

// waitForExitNoReap waits until the process exits without reaping it, so that
// its process ID stays reserved and cannot belong to another process.
func waitForExitNoReap(pid int) error {
	var info [siginfoBytes]byte

	for {
		_, _, errno := syscall.Syscall6(
			syscall.SYS_WAITID,
			waitidPid,
			uintptr(pid),
			uintptr(unsafe.Pointer(&info[0])), // #nosec G103 // waitid writes its result into this buffer.
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
