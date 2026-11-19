package envfixture

import . "os"

func dot() string { return Getenv("DOT_SECRET") }
