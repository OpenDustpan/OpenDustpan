package authgatetest_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
)

// TestCaseNames checks that each case name of each suite is set and unique,
// because a subtest name that repeats hides a case.
func TestCaseNames(t *testing.T) {
	t.Parallel()

	suites := []struct {
		name  string
		names []string
	}{
		{name: "name locks", names: authgatetest.NameLockCaseNames()},
	}

	for _, s := range suites {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()

			if len(s.names) == 0 {
				t.Fatal("the suite has no case")
			}

			seen := make(map[string]bool, len(s.names))

			for _, name := range s.names {
				if name == "" {
					t.Fatal("a case has no name")
				}

				if seen[name] {
					t.Fatalf("the case name %q repeats", name)
				}

				seen[name] = true
			}
		})
	}
}
