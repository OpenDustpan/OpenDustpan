package envfixture

import "os"

func passed() func(string) (string, bool) { return os.LookupEnv }
