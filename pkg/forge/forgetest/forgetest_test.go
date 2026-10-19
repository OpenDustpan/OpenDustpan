package forgetest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/forge/forgetest"
)

func TestCaseNamesAreUnique(t *testing.T) {
	t.Parallel()

	names := forgetest.CaseNames()
	if len(names) == 0 {
		t.Fatal("the suite has no case")
	}

	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			t.Errorf("the case %q is in the suite twice", n)
		}

		seen[n] = true
	}
}

// TestRunSkipsCasesWithNoBody runs the suite before any adapter exists.
// Each case has no body yet, so no case asks for a harness.
func TestRunSkipsCasesWithNoBody(t *testing.T) {
	t.Parallel()

	forgetest.Run(t, func(t *testing.T) forgetest.Harness {
		t.Helper()
		t.Fatal("a case with no body asked for a harness")

		return forgetest.Harness{Forge: nil, Repo: "", Fixture: nil}
	})
}
