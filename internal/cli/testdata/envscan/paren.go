package envfixture

import "os"

func name() string { return "X" }

func paren() string { return (os.Getenv)(name()) }
