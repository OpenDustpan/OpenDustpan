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

// TestSuiteSensitivity runs, for each broken store, the case that must
// catch it, and checks that this case fails.
func TestSuiteSensitivity(t *testing.T) {
	t.Parallel()

	if rule := os.Getenv(durabletestFake); rule != "" {
		durabletest.Run(t, func(t *testing.T) durabletest.Harness {
			t.Helper()

			return durabletest.BrokenHarness(rule)
		})

		return
	}

	cases := []struct {
		rule, catcher string
	}{
		{durabletest.BreakToken, "a_replaced_claim_cannot_write"},
		{durabletest.BreakExpiry, "an_expired_claim_cannot_write"},
		{durabletest.BreakEndExpiry, "an_expired_claim_cannot_write"},
		{durabletest.BreakTakeoverSteps, "a_recorded_step_stays_done_after_a_takeover"},
		{durabletest.BreakClock, "an_instance_clock_ahead_or_behind_cannot_move_a_claim"},
		{durabletest.BreakTokenStays, "another_instance_claims_an_expired_operation_with_a_new_token"},
		{durabletest.BreakLive, "a_live_claim_blocks_another_claim"},
		{durabletest.BreakTxStart, "a_claim_that_expires_inside_a_step_transaction_cannot_write"},
		{durabletest.BreakExpiryCount, "only_a_takeover_of_an_expired_claim_counts_an_expiry"},
	}

	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			t.Parallel()

			out := runCaseAgainstFake(t, tc.rule, tc.catcher)

			if !strings.Contains(out, "--- FAIL: TestSuiteSensitivity/"+tc.catcher+" ") {
				t.Fatalf("the case %s did not fail against the store that breaks %q:\n%s", tc.catcher, tc.rule, out)
			}
		})
	}
}

// runCaseAgainstFake runs the suite case catcher in a child process against
// the store that breaks rule, and returns the output of the child.
func runCaseAgainstFake(t *testing.T, rule, catcher string) string {
	t.Helper()

	//nolint:gosec // The child is this test binary with a fixed filter; both come from the test itself.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSuiteSensitivity$/^"+catcher+"$",
		"-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), durabletestFake+"="+rule)

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the case %s passed against the store that breaks %q, want failure:\n%s", catcher, rule, out)
	}

	return string(out)
}
