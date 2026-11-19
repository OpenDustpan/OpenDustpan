package envfixture

import "syscall"

func sysRead() (string, bool) { return syscall.Getenv("SYSCALL_SECRET") }
