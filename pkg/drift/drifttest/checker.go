package drifttest

import "testing"

func checkerCases() []suiteCase {
	names := []string{
		"checker: Check starts one job with the operation refresh and the purpose drift check",
		"checker: Check needs no policy",
		"checker: Check twice in the same minute gives one job",
		"checker: Check on a manually locked unit queues the job with the wait reason locked",
		"checker: Check on a unit of a tool with no refresh-only plan returns job.ErrUnsupported",
		"checker: Scheduled with the policy off returns ErrDisabled and starts no job",
		"checker: Scheduled has the source kind schedule and never waits for an approval",
		"checker: Scheduled twice for one slot gives one job",
		"checker: a drift check never holds the unit for a change",
		"checker: a change freeze does not hold a drift check",
	}

	cases := make([]suiteCase, 0, len(names))

	for _, name := range names {
		cases = append(cases, suiteCase{name, pendingChecker})
	}

	return cases
}

// pendingChecker skips a case of Checker until the first slice of drift detection gives
// the harness a job service. The case names above are the contract that the
// slice has to write.
func pendingChecker(t *testing.T, h Harness) {
	t.Helper()

	if h.Checker == nil {
		t.Skip("the harness has no Checker")
	}

	t.Skip("not implemented: the first slice of drift detection writes this case with a job service")
}
