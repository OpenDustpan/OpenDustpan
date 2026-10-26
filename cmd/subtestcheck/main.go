// Command subtestcheck reports a t.Error or t.Fatal in a parent test body that
// reads a variable which a parallel subtest writes (make subtestcheck).
package main

import (
	"os"

	"opendustpan.org/opendustpan/internal/subtestcheck"
)

func main() {
	os.Exit(subtestcheck.Run(os.Args[1:], os.Stdout, os.Stderr))
}
