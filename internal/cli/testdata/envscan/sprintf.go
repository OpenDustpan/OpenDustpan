package envfixture

import (
	"fmt"
	"os"
)

func sprintf() string { return os.Getenv(fmt.Sprintf("X_%s", "Y")) }
