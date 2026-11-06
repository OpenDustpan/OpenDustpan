// Package testchild runs the current test binary again in a child process.
// A conformance suite uses it to prove that the suite fails on a bad
// adapter or a bad fixture: the parent test starts the child with a fixed
// test filter and a marker in the environment, and reads the output and the
// exit status of the child.
package testchild

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var (
	errNoEquals = errors.New("environment entry has no \"=\"")
	errBadName  = errors.New("environment name does not match ^[A-Z][A-Z0-9_]*$")
	errBadValue = errors.New("environment value has NUL, CR or LF")
)

// envName is the form of the name of an environment entry that the caller
// gives.
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Run runs the current test binary again with the filter -test.run=filter,
// -test.count=1 and -test.v. The child gets the environment of the parent
// and then each entry of env, in the form NAME=value. Run returns the
// combined output of the child and the error of the child. The error is nil
// when the child exits with status 0, and an *exec.ExitError when the child
// fails.
//
// Run stops the test with t.Fatal before it starts the child when an entry
// of env has a name that does not match ^[A-Z][A-Z0-9_]*$, or has a value
// with NUL, CR or LF. The caller gives filter as a fixed string of the test.
func Run(t *testing.T, filter string, env ...string) (string, error) {
	t.Helper()

	for _, entry := range env {
		if err := checkEnvEntry(entry); err != nil {
			t.Fatalf("testchild: %v", err)
		}
	}

	// Defense in depth: the command is the test binary itself, so the name is
	// not input from outside. The filter is one argument in an argument list,
	// and the shell never sees it. The environment check above is a separate
	// layer and this call does not rely on it, and the check does not rely on
	// the fixed filter or on the argument list.
	//nolint:gosec // The command is the test binary itself (os.Args[0]); the arguments come from the test.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run="+filter, "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("run test child %q: %w", filter, err)
	}

	return string(out), nil
}

// checkEnvEntry returns an error when entry is not NAME=value with a name
// that matches envName and a value without NUL, CR or LF.
func checkEnvEntry(entry string) error {
	name, value, found := strings.Cut(entry, "=")
	if !found {
		return fmt.Errorf("%w: %q", errNoEquals, entry)
	}

	if !envName.MatchString(name) {
		return fmt.Errorf("%w: %q", errBadName, name)
	}

	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%w: entry %s", errBadValue, name)
	}

	return nil
}
