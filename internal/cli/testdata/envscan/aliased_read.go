package envfixture

import osalias "os"

func aliasedRead() string { return osalias.Getenv("ALIASED_SECRET") }
