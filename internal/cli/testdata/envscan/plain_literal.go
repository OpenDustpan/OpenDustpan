package envfixture

import "os"

func plain() string { return os.Getenv("PLAIN_SECRET") }
