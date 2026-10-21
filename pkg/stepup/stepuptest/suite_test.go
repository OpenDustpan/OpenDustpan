package stepuptest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stepup/stepuptest"
)

// TestCaseNames checks that each case name is set and unique, because a
// subtest name that repeats hides a case.
func TestCaseNames(t *testing.T) {
	t.Parallel()

	names := stepuptest.CaseNames()
	if len(names) == 0 {
		t.Fatal("the suite has no case")
	}

	seen := make(map[string]bool, len(names))

	for _, name := range names {
		if name == "" {
			t.Fatal("a case has no name")
		}

		if seen[name] {
			t.Fatalf("the case name %q repeats", name)
		}

		seen[name] = true
	}
}
