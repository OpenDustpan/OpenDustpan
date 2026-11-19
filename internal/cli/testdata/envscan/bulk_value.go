package envfixture

import "os"

func bulkValue() func() []string { return os.Environ }
