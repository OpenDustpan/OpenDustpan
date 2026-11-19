package envfixture

import "os"

func conversion() (string, bool) { return os.LookupEnv(string("CONVERTED_SECRET")) }
