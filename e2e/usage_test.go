//go:build e2e

package e2e_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// TestUsageGroups proves through the real binary that the usage text lists the
// commands in groups by intent, in the order of the decision. --help prints
// it to the standard output and exits with 0. No command and an unknown
// command print it to the standard error and exit with 1.
func TestUsageGroups(t *testing.T) {
	t.Parallel()

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")

	groups := []string{"Try out:", "Run in production:", "Operate:", "Back up:", "Upgrade:"}

	cases := []struct {
		name string
		args []string
		exit int
	}{
		{"help", []string{"--help"}, 0},
		{"no command", nil, 1},
		{"unknown command", []string{"no-such-command"}, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := runBinary(t, bin, tc.args...)

			code := 0

			if err != nil {
				exit, isExit := errors.AsType[*exec.ExitError](err)
				if !isExit {
					t.Fatalf("opendustpan %v: %v", tc.args, err)
				}

				code = exit.ExitCode()
			}

			if code != tc.exit {
				t.Fatalf("opendustpan %v: exit = %d, want %d\n%s", tc.args, code, tc.exit, out)
			}

			checkUsage(t, out, groups)
		})
	}
}

// checkUsage proves that out lists the groups in order and the named commands
// under them.
func checkUsage(t *testing.T, out string, groups []string) {
	t.Helper()

	last := -1

	for _, group := range groups {
		at := strings.Index(out, "\n"+group+"\n")
		if at < 0 || at < last {
			t.Fatalf("usage lacks the group %q in order:\n%s", group, out)
		}

		last = at
	}

	for _, command := range []string{
		"opendustpan dev", "opendustpan serve", "opendustpan key generate --out <path>", "opendustpan doctor",
		"opendustpan upgrade check",
	} {
		if !strings.Contains(out, "\n  "+command+"\n") {
			t.Fatalf("usage lacks the line %q:\n%s", command, out)
		}
	}
}
