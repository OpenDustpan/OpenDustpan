package envfixture

import "os"

const secretName = "CONST_SECRET"

func plainConst() string { return os.Getenv(secretName) }
