package reviewrequesttest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/reviewrequest/reviewrequesttest"
)

func TestCaseNamesAreUniqueAndNotEmpty(t *testing.T) {
	t.Parallel()

	for suite, names := range map[string][]string{
		"Store":           reviewrequesttest.StoreCaseNames(),
		"GitHost":         reviewrequesttest.GitHostCaseNames(),
		"ClosureResolver": reviewrequesttest.ClosureCaseNames(),
		"ConflictChecker": reviewrequesttest.ConflictCaseNames(),
	} {
		if len(names) == 0 {
			t.Errorf("suite %s has no case", suite)
		}

		seen := map[string]bool{}

		for _, name := range names {
			if name == "" {
				t.Errorf("suite %s has a case with an empty name", suite)
			}

			if seen[name] {
				t.Errorf("suite %s uses case name %q twice", suite, name)
			}

			seen[name] = true
		}
	}
}

// TestSuitesRun runs each suite with no adapter. Each case skips until the
// first adapter of its port gives it a body.
func TestSuitesRun(t *testing.T) {
	t.Parallel()

	t.Run("Store", func(t *testing.T) {
		t.Parallel()
		reviewrequesttest.RunStore(t, nil)
	})
	t.Run("GitHost", func(t *testing.T) {
		t.Parallel()
		reviewrequesttest.RunGitHost(t, nil)
	})
	t.Run("ClosureResolver", func(t *testing.T) {
		t.Parallel()
		reviewrequesttest.RunClosure(t, nil)
	})
	t.Run("ConflictChecker", func(t *testing.T) {
		t.Parallel()
		reviewrequesttest.RunConflicts(t, nil)
	})
}
