//go:build !linux

package run

import "syscall"

// sysProcAttr returns the attributes of the process of a step: its own
// process group.
func sysProcAttr() *syscall.SysProcAttr {
	a := new(syscall.SysProcAttr)
	a.Setpgid = true

	return a
}
