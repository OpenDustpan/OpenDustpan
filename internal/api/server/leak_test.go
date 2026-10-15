package server_test

import (
	"fmt"
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running.
func TestMain(m *testing.M) {
	code := m.Run()

	if err := removeTemplates(); err != nil {
		fmt.Fprintln(os.Stderr, err)

		if code == 0 {
			code = 1
		}
	}

	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintln(os.Stderr, err)

			code = 1
		}
	}

	os.Exit(code) //nolint:forbidigo // TestMain must exit with the test result after removing the template directory.
}
