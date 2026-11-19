package envfixture

import "os"

func concat() string { return os.Getenv("A_" + "B") }
