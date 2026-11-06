package run

import "syscall"

// sysProcAttr returns the attributes of the process of a step: its own
// process group, and SIGKILL when the launcher ends.
func sysProcAttr() *syscall.SysProcAttr {
	a := new(syscall.SysProcAttr)
	a.Setpgid = true
	a.Pdeathsig = syscall.SIGKILL

	return a
}
