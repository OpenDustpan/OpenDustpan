package envfixture

import "os"

func name() string { return "X" }

func call() string { return os.Getenv(name()) }
