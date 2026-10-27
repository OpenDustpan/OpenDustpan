//go:build windows

package main

import (
	"errors"
	"fmt"
	"math"
	"syscall"
	"time"
)

const (
	// processQueryLimitedInformation is the access right that the start time
	// needs. The syscall package does not define it. It is the weakest right
	// that GetProcessTimes accepts.
	processQueryLimitedInformation = 0x1000
	// errorInvalidParameter is what OpenProcess answers for an ID that no
	// process has.
	errorInvalidParameter syscall.Errno = 87
)

// processStartTime answers the creation time of the process with the ID pid.
// It answers errNoProcess for an ID that no process has, and errStartUnknown
// when Windows refuses the query (for example access denied): the caller then
// treats the process as live.
func processStartTime(pid int) (time.Time, error) {
	// A Windows process ID has 32 bits. No process has an ID outside that range.
	if pid <= 0 || pid > math.MaxUint32 {
		return time.Time{}, errNoProcess
	}

	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		if errors.Is(err, errorInvalidParameter) {
			return time.Time{}, errNoProcess
		}

		return time.Time{}, fmt.Errorf("%w: open process %d: %w", errStartUnknown, pid, err)
	}

	defer func() { _ = syscall.CloseHandle(handle) }()

	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, fmt.Errorf("%w: get process times %d: %w", errStartUnknown, pid, err)
	}

	return time.Unix(0, created.Nanoseconds()), nil
}
