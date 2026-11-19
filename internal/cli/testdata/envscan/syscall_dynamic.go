package envfixture

import "syscall"

func name() string { return "X" }

func sysDynamic() (string, bool) { return syscall.Getenv(name()) }
