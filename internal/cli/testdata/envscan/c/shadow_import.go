package envfixture

import (
	"os"

	a "opendustpan.org/opendustpan/internal/cli/testdata/envscan/a"
)

var _ = a.Name

func shadowImport() string {
	a := struct{ Name string }{Name: "SHADOW_IMPORT_SECRET"}

	return os.Getenv(a.Name)
}
