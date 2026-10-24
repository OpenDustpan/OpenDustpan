package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestUsageNamesEveryCommand proves that the usage line names each command of
// the command table, so a new command shows in it.
func TestUsageNamesEveryCommand(t *testing.T) {
	t.Parallel()

	for _, cmd := range commandTable() {
		if want := "opendustpan " + strings.Join(cmd.Path, " "); !strings.Contains(errUsage.Error(), want) {
			t.Errorf("usage = %q, want it to name %q", errUsage.Error(), want)
		}
	}
}

// TestHelpExitsZero proves that a request for help is not an error: the
// command answers no error, so the binary exits with status 0. The top-level
// request prints the usage line to standard output.
func TestHelpExitsZero(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--help"},
		{"-h"},
		{"serve", "-h"},
		{"namespace", "create", "-h"},
		{"host", "create-temp-admin", "-h"},
		{"backup", "check", "--help"},
	} {
		var stdout, stderr bytes.Buffer

		if err := run(t.Context(), args, &stdout, &stderr); err != nil {
			t.Errorf("run %v: err = %v, want nil for a help request", args, err)
		}

		if len(args) == 1 && !strings.Contains(stdout.String(), "opendustpan serve") {
			t.Errorf("run %v: stdout = %q, want the usage line", args, stdout.String())
		}
	}
}
