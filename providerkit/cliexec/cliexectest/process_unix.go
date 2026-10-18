//go:build unix

package cliexectest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// alive reports whether the process runs. A zombie, a process that ended and
// that no parent has waited for yet, does not run.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}

	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}

	// The state follows the command name in parentheses: "pid (comm) S ...".
	// The name can hold ") " too, so the last one counts.
	i := bytes.LastIndex(stat, []byte(") "))

	return i < 0 || len(stat) <= i+2 || stat[i+2] != 'Z'
}

// stop ends a process that a starter left, so that a failed test does not
// leave it running.
func stop(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck // The test fails anyway; the process can be gone.
}
