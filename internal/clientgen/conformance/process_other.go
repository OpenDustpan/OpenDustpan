//go:build !unix

package conformance

import (
	"fmt"
	"os"
	"os/exec"
)

// prepareGroup does nothing: this platform has no process groups here, so a
// kill reaches the adapter only. An adapter must not start other processes.
func prepareGroup(*exec.Cmd, *ProcessAdapter) {}

// killGroup stops the adapter process.
func killGroup(p *os.Process) error {
	if err := p.Kill(); err != nil {
		return fmt.Errorf("kill the process %d: %w", p.Pid, err)
	}

	return nil
}
