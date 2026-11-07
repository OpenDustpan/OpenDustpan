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
