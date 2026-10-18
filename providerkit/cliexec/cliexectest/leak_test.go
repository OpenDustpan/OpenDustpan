package cliexectest_test

import (
	"os"
	"testing"

	"go.uber.org/goleak"

	"opendustpan.org/opendustpan/providerkit/cliexec/cliexectest"
)

// TestMain runs the helper program of the suite when the suite starts this
// binary as one. Otherwise it runs the tests and fails the package when a
// test leaves a goroutine running.
func TestMain(m *testing.M) {
	if code, ok := cliexectest.RunHelper(os.Args, os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code) //nolint:forbidigo // The helper program ends with its own exit code.
	}

	goleak.VerifyTestMain(m)
}
