package envfixture

import "syscall"

func syscallEnviron() []string { return syscall.Environ() }
