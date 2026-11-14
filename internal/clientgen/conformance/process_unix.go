//go:build unix

package conformance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// prepareGroup puts the adapter in a process group of its own, so that a kill
// reaches the processes that the adapter starts. A stop after the post-exit
// step has ended the group sends no signal.
func prepareGroup(cmd *exec.Cmd, a *ProcessAdapter) {
	cmd.SysProcAttr = new(syscall.SysProcAttr)
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
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
