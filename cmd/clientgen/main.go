// Command clientgen reads the API description of the server and writes a
// client from it. See "clientgen -h".
package main

import (
	"os"

	"opendustpan.org/opendustpan/internal/clientgen/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
