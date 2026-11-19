package envfixture

import "os"

func expand() string { return os.Expand("${BULK_SECRET}", func(name string) string { return name }) }
