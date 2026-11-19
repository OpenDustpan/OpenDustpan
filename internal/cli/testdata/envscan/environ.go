package envfixture

import "os"

func environ() []string { return os.Environ() }
