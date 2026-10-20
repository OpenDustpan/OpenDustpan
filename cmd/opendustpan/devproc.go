package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// Errors of processStartTime.
var (
	// errNoProcess reports that no process has the ID.
	errNoProcess = errors.New("no process has the ID")
	// errStartUnknown reports a platform or a process whose start time dev
	// cannot read.
	errStartUnknown = errors.New("the start time of the process is not known")
)

const (
	// devAddrMaxBytes bounds the read of dev.addr: the file holds one short
	// JSON object.
	devAddrMaxBytes = 64 << 10
	// devStartTolerance is the slack between the start time of a process and
	// the start time that dev wrote. The clock of the process table has a
	// resolution of one second.
	devStartTolerance = 5 * time.Second
)

// devAddrLive reports whether addr names a process that runs now: the
// process ID exists, and the process started no later than the start time
// that dev wrote (plus a small tolerance). A process that started later
// reuses the ID of a process that ended. When the start time of a process
// cannot be read, the answer is true, because deleting the file of a live
// server does more harm than keeping the file of a dead one.
func devAddrLive(addr devAddr, start func(pid int) (time.Time, error)) bool {
	if addr.PID <= 0 {
		return false
	}

	began, err := start(addr.PID)

	switch {
	case err == nil:
		return !began.After(addr.Started.Add(devStartTolerance))
	case errors.Is(err, errNoProcess):
		return false
	default:
		return true
	}
}

// readDevAddr reads dev.addr in dir. It answers false for a file that does
// not exist or that does not hold one JSON object of the dev.addr form.
func readDevAddr(dir string) (devAddr, bool) {
	var none devAddr

	root, err := os.OpenRoot(dir)
	if err != nil {
		return none, false
	}

	defer root.Close() //nolint:errcheck // A read only root.

	file, err := root.Open(devAddrName)
	if err != nil {
		return none, false
	}

	defer file.Close() //nolint:errcheck // A read only file.

	data, err := io.ReadAll(io.LimitReader(file, devAddrMaxBytes))
	if err != nil {
		return none, false
	}

	var addr devAddr
	if json.Unmarshal(data, &addr) != nil {
		return none, false
	}

	return addr, true
}
