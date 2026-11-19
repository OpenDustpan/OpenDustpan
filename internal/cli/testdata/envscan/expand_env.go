package envfixture

import "os"

func expandEnv() string { return os.ExpandEnv("${BULK_SECRET}") }
