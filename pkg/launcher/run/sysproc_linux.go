package run

import "syscall"

// sysProcAttr returns the attributes of the process of a step: its own
// process group, and SIGKILL when the launcher ends.
//
// Pdeathsig follows the thread that forked the step, not the process. It
// holds while no code of the launcher calls runtime.LockOSThread: a locked
// thread that ends would send SIGKILL to the step too early.
func sysProcAttr() *syscall.SysProcAttr {
	a := new(syscall.SysProcAttr)
	a.Setpgid = true
	a.Pdeathsig = syscall.SIGKILL

	return a
}
