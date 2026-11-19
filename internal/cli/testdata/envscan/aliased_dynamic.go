package envfixture

import osalias "os"

func name() string { return "X" }

func aliasedDynamic() string { return osalias.Getenv(name()) }
