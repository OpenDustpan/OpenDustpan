package conformance

import (
	"os"
	"sync/atomic"
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
