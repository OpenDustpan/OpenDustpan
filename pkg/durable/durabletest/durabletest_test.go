// Tests for the conformance suite. TestMemStore runs the whole suite against
// a reference store in memory, so the cases themselves run on each change.
// The sensitivity tests run one case in a child process against a store that
// breaks one rule, and check that the case fails. The child re-executes the
// same test with durabletestFake set, so the real case code does the
// checking.
package durabletest_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/durable/durabletest"
)

// durabletestFake selects the broken rule of the store that the child
// process serves to the suite.
const durabletestFake = "OPENDUSTPAN_TEST_DURABLETEST_FAKE"

func TestMemStore(t *testing.T) {
	t.Parallel()

	durabletest.Run(t, func(t *testing.T) durabletest.Harness {
		t.Helper()

		return durabletest.NewMem().Harness()
	})
}

func TestSuiteDetectsTokenIgnored(t *testing.T) {
	t.Parallel()

	if os.Getenv(durabletestFake) == durabletest.BreakToken {
		durabletest.Run(t, func(t *testing.T) durabletest.Harness {
			t.Helper()

			return durabletest.BrokenHarness(durabletest.BreakToken)
		})

		return
	}

	out := runSingleCaseAgainstFake(t, durabletest.BreakToken, "a_replaced_claim_cannot_write")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the replaced-claim case failing:\n%s", out)
	}
}

func TestSuiteDetectsLiveClaimTaken(t *testing.T) {
	t.Parallel()

	if os.Getenv(durabletestFake) == durabletest.BreakLive {
		durabletest.Run(t, func(t *testing.T) durabletest.Harness {
			t.Helper()

			return durabletest.BrokenHarness(durabletest.BreakLive)
		})

		return
	}

	out := runSingleCaseAgainstFake(t, durabletest.BreakLive, "a_live_claim_blocks_another_claim")

	if !strings.Contains(out, "--- FAIL: "+t.Name()+"/") {
		t.Fatalf("child output does not show the live-claim case failing:\n%s", out)
	}
}

func runSingleCaseAgainstFake(t *testing.T, fake, subtest string) string {
	t.Helper()

	//nolint:gosec // The child is this test binary with a fixed filter; both come from the test itself.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$/"+subtest, "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), durabletestFake+"="+fake)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("case matching %q passed against the %s store, want failure", subtest, fake)
	}

	return string(out)
}
