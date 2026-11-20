package envfixture

import (
	"os"

	a "opendustpan.org/opendustpan/internal/cli/testdata/envscan/a"
	b "opendustpan.org/opendustpan/internal/cli/testdata/envscan/b"
)

func dirKeyed() (string, string) { return os.Getenv(a.Name), os.Getenv(b.Name) }
