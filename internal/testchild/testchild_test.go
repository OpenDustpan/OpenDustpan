package testchild_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/testchild"
)

// childMode selects the behavior of the child tests below.
const childMode = "OPENDUSTPAN_TESTCHILD_MODE"

// depthEnv marks a probe that runs inside another probe. A probe that finds it
// set stops at once, so a checker that stopped refusing cannot start a chain of
// children.
const depthEnv = "OPENDUSTPAN_TESTCHILD_DEPTH"

// TestChildProbe is the test that the parent tests run as a child. In a
// normal run, childMode is not set and the test does nothing.
func TestChildProbe(t *testing.T) {
	t.Parallel()

	// Defense in depth: the checker of testchild refuses a bad entry, and this
	// check stays so that a weakened checker fails the test in one step instead
	// of starting another child.
	if os.Getenv(childMode) != "" && os.Getenv(depthEnv) != "" {
		t.Fatal("the child probe ran inside the child probe: the checker did not refuse")
	}

	switch os.Getenv(childMode) {
	case "":
		return
	case "pass":
		t.Log("probe-pass-marker")
	case "fail":
		t.Fatal("probe-fail-marker")
	case "badname":
		out, err := testchild.Run(t, "^TestChildProbe$", "lower=1", depthEnv+"=1")
		t.Logf("probe-reached-after-bad-entry: %q %v", out, err)
	case "badvalue":
		out, err := testchild.Run(t, "^TestChildProbe$", "GOOD_NAME=a\nb", depthEnv+"=1")
		t.Logf("probe-reached-after-bad-entry: %q %v", out, err)
	case "noequals":
		out, err := testchild.Run(t, "^TestChildProbe$", "lower", depthEnv+"=1")
		t.Logf("probe-reached-after-bad-entry: %q %v", out, err)
	case "valuenul":
		out, err := testchild.Run(t, "^TestChildProbe$", "GOOD_NAME=a\x00b", depthEnv+"=1")
		t.Logf("probe-reached-after-bad-entry: %q %v", out, err)
	case "valuecr":
		out, err := testchild.Run(t, "^TestChildProbe$", "GOOD_NAME=a\rb", depthEnv+"=1")
		t.Logf("probe-reached-after-bad-entry: %q %v", out, err)
	default:
		t.Fatalf("unknown probe mode %q", os.Getenv(childMode))
	}
}

func TestRunReturnsOutputOfAPassingChild(t *testing.T) {
	t.Parallel()

	out, err := testchild.Run(t, "^TestChildProbe$", childMode+"=pass")
	if err != nil {
		t.Fatalf("Run error = %v, want nil:\n%s", err, out)
	}

	if !strings.Contains(out, "probe-pass-marker") {
		t.Fatalf("output does not hold the marker of the child:\n%s", out)
	}
}

func TestRunReturnsTheExitErrorOfAFailingChild(t *testing.T) {
	t.Parallel()

	out, err := testchild.Run(t, "^TestChildProbe$", childMode+"=fail")

	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr == nil {
		t.Fatalf("Run error = %v, want an *exec.ExitError:\n%s", err, out)
	}

	if !strings.Contains(out, "probe-fail-marker") {
		t.Fatalf("output does not hold the failure of the child:\n%s", out)
	}
}

func TestRunUsesTheFixedFilter(t *testing.T) {
	t.Parallel()

	out, err := testchild.Run(t, "^TestChildProbe$", childMode+"=pass")
	if err != nil {
		t.Fatalf("Run error = %v, want nil:\n%s", err, out)
	}

	if !strings.Contains(out, "=== RUN   TestChildProbe") {
		t.Fatalf("child did not run the filtered test:\n%s", out)
	}

	if strings.Contains(out, "=== RUN   TestRunUsesTheFixedFilter") {
		t.Fatalf("child ran a test outside the filter:\n%s", out)
	}
}

// TestRunRefusesABadEnvironmentEntry runs, in a child, a test that gives Run
// a bad entry. The t.Fatal of Run cannot be caught in the same test, so the
// parent checks that the child failed with the refusal and did not go on.
func TestRunRefusesABadEnvironmentEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "NameWithLowercase", mode: "badname", want: `environment name does not match`},
		{name: "ValueWithLF", mode: "badvalue", want: "environment value has NUL, CR or LF"},
		{name: "EntryWithoutEquals", mode: "noequals", want: `environment entry has no "="`},
		{name: "ValueWithNUL", mode: "valuenul", want: "environment value has NUL, CR or LF"},
		{name: "ValueWithCR", mode: "valuecr", want: "environment value has NUL, CR or LF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := testchild.Run(t, "^TestChildProbe$", childMode+"="+tt.mode)
			if err == nil {
				t.Fatalf("child passed with a bad entry, want failure:\n%s", out)
			}

			if !strings.Contains(out, "testchild: ") || !strings.Contains(out, tt.want) {
				t.Fatalf("child output does not hold the refusal %q:\n%s", tt.want, out)
			}

			if strings.Contains(out, "probe-reached-after-bad-entry") {
				t.Fatalf("child went on after the bad entry:\n%s", out)
			}
		})
	}
}
