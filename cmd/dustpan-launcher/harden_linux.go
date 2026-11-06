package main

import (
	"fmt"
	"syscall"
)

// prctl options.
const (
	prSetDumpable   = 4
	prSetNoNewPrivs = 38
)

// harden sets PR_SET_DUMPABLE to 0, so that a process of a step with the
// same user ID cannot attach to the launcher or read its memory, and
// PR_SET_NO_NEW_PRIVS, which each step inherits.
//
// PR_SET_DUMPABLE applies to the whole process. PR_SET_NO_NEW_PRIVS is an
// attribute of each thread, and a step forks from the thread that runs its
// goroutine, so the flag goes on every thread of the runtime. Threads that
// the runtime makes later inherit it. AllThreadsSyscall fails with ENOTSUP
// in a binary with cgo: the launcher then stops, so that no step runs
// without the flag.
func harden() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		return fmt.Errorf("set PR_SET_DUMPABLE: %w", errno)
	}

	if _, _, errno := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("set PR_SET_NO_NEW_PRIVS on each thread: %w", errno)
	}

	return nil
}
