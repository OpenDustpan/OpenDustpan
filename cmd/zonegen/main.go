// Command zonegen writes the list of IANA zone names for pkg/preferences from
// the time zone archive of the Go toolchain (make zones-generate).
package main

import (
	"context"
	"os"

	"opendustpan.org/opendustpan/internal/zonegen"
)

func main() {
	os.Exit(zonegen.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
