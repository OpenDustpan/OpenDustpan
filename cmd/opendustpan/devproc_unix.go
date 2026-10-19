//go:build !linux && !windows

package main

import (
	"errors"
	"syscall"
	"time"
)

// processStartTime answers errNoProcess for an ID that no process has, and
// errStartUnknown for a process whose start time this platform does not
// give: the caller then treats the process as live.
func processStartTime(pid int) (time.Time, error) {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return time.Time{}, errNoProcess
	}

	return time.Time{}, errStartUnknown
}
