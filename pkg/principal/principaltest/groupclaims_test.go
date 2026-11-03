package principaltest_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal/principaltest"
)

// TestGroupClaimsSuite runs the suites of the group claims ports with no
// implementation, so that each case shows as skipped until one runs it.
func TestGroupClaimsSuite(t *testing.T) {
	t.Parallel()

	t.Run("GroupMapper", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupMapper(t, nil)
	})
	t.Run("GroupClaims", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupClaims(t, nil)
	})
}

// TestRefusesAFixture runs each child subtest in its own process. A t.Fatal
// stops the test that calls it, so the refusal can be seen only from outside.
// The child gets one variable and no copy of the parent environment.
func TestRefusesAFixture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		child    string
		suite    string
		wantFail bool
	}{
		{name: "GroupMapperFixtureFails", child: "RunGroupMapperFixture", suite: "RunGroupMapper", wantFail: true},
		{name: "GroupClaimsFixtureFails", child: "RunGroupClaimsFixture", suite: "RunGroupClaims", wantFail: true},
		{name: "GroupMapperNilFixturePasses", child: "RunGroupMapperNil", suite: "RunGroupMapper", wantFail: false},
		{name: "GroupClaimsNilFixturePasses", child: "RunGroupClaimsNil", suite: "RunGroupClaims", wantFail: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, exitCode := runChild(t, tt.child)
			if !tt.wantFail {
				if exitCode != 0 {
					t.Fatalf("exit code = %d, want 0; output:\n%s", exitCode, out)
				}
				return
			}
			if exitCode == 0 {
				t.Fatalf("exit code = 0, want non-zero; output:\n%s", out)
			}
			if !strings.Contains(out, tt.suite) {
				t.Fatalf("output does not name %s:\n%s", tt.suite, out)
			}
		})
	}
}

// runChild runs one subtest of TestRefusesAFixtureChild in a new process and
// returns its combined output and its exit code.
func runChild(t *testing.T, child string) (string, int) {
	t.Helper()

	run := "-test.run=^TestRefusesAFixtureChild$/^" + child + "$"
	// The command is the running test binary, and its arguments are fixed test names.
	//nolint:gosec // The test starts its own binary.
	cmd := exec.CommandContext(t.Context(), os.Args[0], run, "-test.v")
	cmd.Env = []string{"PRINCIPALTEST_CHILD=1"}
	out, err := cmd.CombinedOutput()

	exitCode, err := exitCodeOf(err)
	if err != nil {
		t.Fatalf("run child %s: %v", child, err)
	}
	return string(out), exitCode
}

// exitCodeOf returns the exit code of a finished child process. A nil error
// means exit code 0. An error that is not an exit status is returned.
func exitCodeOf(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, err
	}
	return exitErr.ExitCode(), nil
}

// TestRefusesAFixtureChild runs one suite for the parent. It returns at once
// unless the parent set the child variable.
func TestRefusesAFixtureChild(t *testing.T) {
	t.Parallel()

	if os.Getenv("PRINCIPALTEST_CHILD") != "1" {
		return
	}

	t.Run("RunGroupMapperFixture", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupMapper(t, func(*testing.T) principaltest.GroupMapperFixture {
			return principaltest.GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil}
		})
	})
	t.Run("RunGroupClaimsFixture", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupClaims(t, func(*testing.T) principaltest.GroupClaimsFixture {
			return principaltest.GroupClaimsFixture{
				Applier: nil,
				Store:   nil,
				Mapping: principaltest.GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil},
			}
		})
	})
	t.Run("RunGroupMapperNil", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupMapper(t, nil)
	})
	t.Run("RunGroupClaimsNil", func(t *testing.T) {
		t.Parallel()
		principaltest.RunGroupClaims(t, nil)
	})
}
