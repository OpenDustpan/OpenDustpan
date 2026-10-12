//go:build e2e

package e2e

// ServerPID returns the process ID of the binary backend, so the soak run
// can count its open file descriptors under /proc/<pid>/fd. It reports
// false in the in-process mode, which has no backend process. The soak
// driver always runs with E2E_SERVER=binary.
func (e *Env) ServerPID() (int, bool) {
	if e.backendCmd == nil || e.backendCmd.Process == nil {
		return 0, false
	}

	return e.backendCmd.Process.Pid, true
}
