// Package cliexec is the CLI execution port of the CLI engine: one command
// out, one result back. The engine builds each command from the declaration
// and parses the JSON output. An adapter only runs.
//
// A command is an argument list. The adapter starts the program with no
// shell, so no character of an argument has a special meaning. It passes no
// variable of its own environment to the program.
package cliexec

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// The errors of the port.
var (
	// ErrInvalidCommand reports a command that breaks a rule of Validate.
	// The adapter does not start it.
	ErrInvalidCommand = errors.New("invalid command")
	// ErrTimeout reports a command that ran longer than its timeout. The
	// adapter stopped its whole process group.
	ErrTimeout = errors.New("command timed out")
	// ErrTooLarge reports an output larger than the limit of the runner. The
	// adapter stopped the process group.
	ErrTooLarge = errors.New("command output too large")
)

// Command is one run of a program.
type Command struct {
	// Program is a bare name that the runner finds on its PATH, or a clean
	// absolute path.
	Program string
	// Args are the arguments after the program, one element each.
	Args []string
	// Env holds the variables of the program. The runner adds PATH, and
	// HOME and TMPDIR in a private directory of the run, and nothing else.
	Env map[string]string
	// Stdin is the standard input, or nil for an empty one.
	Stdin []byte
	// Timeout limits the run. It must be positive.
	Timeout time.Duration
}

// Result is the outcome of a command that ran to its end.
type Result struct {
	// ExitCode is the exit code of the program.
	ExitCode int
	// Stdout is the standard output. The engine parses it as JSON.
	Stdout []byte
	// Stderr is the standard error. The engine masks sensitive values in it
	// before it puts it in a diagnostic.
	Stderr []byte
}

// Runner is the port of the CLI engine.
type Runner interface {
	// Run starts the program with the arguments and no shell, waits for it,
	// and returns its result. A non-zero exit code is a result, not an
	// error. The program runs in its own process group, and the runner
	// stops the whole group at the timeout, at the output limit and when
	// ctx ends. Errors: ErrInvalidCommand, ErrTimeout, ErrTooLarge, the
	// error of ctx, or a failure to start the program, wrapped.
	Run(ctx context.Context, cmd Command) (Result, error)
}

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// ValidProgram reports whether p is a bare name or a clean absolute path.
func ValidProgram(p string) bool {
	if p == "" || strings.ContainsRune(p, 0) {
		return false
	}

	if !strings.ContainsRune(p, '/') {
		return true
	}

	return path.IsAbs(p) && path.Clean(p) == p
}

// ReservedEnv reports whether a command cannot set the variable: PATH, HOME
// and TMPDIR belong to the runner, and a name that starts with DUSTPAN_
// belongs to the job launcher. A name that is not upper-case letters, digits
// and underscores is also reserved.
func ReservedEnv(name string) bool {
	return !envNamePattern.MatchString(name) ||
		name == "PATH" || name == "HOME" || name == "TMPDIR" || strings.HasPrefix(name, "DUSTPAN_")
}

// Validate returns ErrInvalidCommand when the program is not a bare name or a
// clean absolute path, when an argument holds a NUL byte, when a variable is
// reserved, or when the timeout is not positive.
func (c Command) Validate() error {
	if !ValidProgram(c.Program) {
		return fmt.Errorf("%w: program %q must be a bare name or a clean absolute path", ErrInvalidCommand, c.Program)
	}

	for i, a := range c.Args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("%w: argument %d holds a NUL byte", ErrInvalidCommand, i)
		}
	}

	for name, v := range c.Env {
		if ReservedEnv(name) || strings.ContainsRune(v, 0) {
			return fmt.Errorf("%w: variable %q is not allowed", ErrInvalidCommand, name)
		}
	}

	if c.Timeout <= 0 {
		return fmt.Errorf("%w: timeout %s is not positive", ErrInvalidCommand, c.Timeout)
	}

	return nil
}
