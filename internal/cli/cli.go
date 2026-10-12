// Package cli holds the command table and the setting declarations of the
// opendustpan command.
//
// The table lists every command with its path, synopsis, settings and run
// function, so cmd/opendustpan dispatches without a hand-written switch.
// Each setting is declared once with its flag, environment variable,
// default and help. The same declaration registers the flag, prints --help
// and generates docs/reference/configuration.md.
//
// The declarations live here, in the parent of internal/cli/client, so the
// serve wiring in cmd/opendustpan and the API clients share them without an
// import cycle. Serve stays in cmd/opendustpan: it is the only command that
// opens stores, and only cmd/ may exit. The depguard rule clientpkg keeps
// the API clients from importing the store adapters, the blob adapters and
// the key-encryption key code.
package cli

import (
	"context"
	"io"
)

// Command path elements: one constant for each, so the table and the help
// never spell a command two ways.
const (
	commandBackup    = "backup"
	commandCheck     = "check"
	commandCreate    = "create"
	commandHealth    = "health"
	commandNamespace = "namespace"
	commandRestore   = "restore"
	commandServe     = "serve"
	commandUpgrade   = "upgrade"
)

// RunFunc runs one command: args holds the flags and operands after the
// command path, stdout takes the report and stderr takes the flag errors.
// A nil error means exit code 0, a non-nil error means exit code 1.
type RunFunc func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// Command is one row of the command table: the argument path that selects
// it, a one-line synopsis, the settings its flags use and the function that
// runs it. A nil Run means cmd/opendustpan fills the run function: only it
// sees both the serve wiring that opens stores and the API clients.
type Command struct {
	Path     []string
	Synopsis string
	Settings []*Setting
	Run      RunFunc
}

// Table is the command table of the binary in dispatch order: longer paths
// come before their prefixes, so Match finds backup check before backup.
type Table []Command

// Match answers the command whose path is the longest prefix of args with
// the rest of the arguments. It answers ok=false when no path matches, and
// the caller prints the usage.
func (t Table) Match(args []string) (Command, []string, bool) {
	for _, cand := range t {
		if len(cand.Path) > len(args) {
			continue
		}

		match := true

		for i, elem := range cand.Path {
			if args[i] != elem {
				match = false

				break
			}
		}

		if match {
			return cand, args[len(cand.Path):], true
		}
	}

	var none Command

	return none, nil, false
}
