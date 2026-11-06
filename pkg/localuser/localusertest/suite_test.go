package localusertest_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser/localusertest"
)

// TestCaseNames checks that each case name of each suite is set and unique,
// because a subtest name that repeats hides a case.
func TestCaseNames(t *testing.T) {
	t.Parallel()

	suites := []struct {
		name  string
		names []string
	}{
		{name: "store", names: localusertest.CaseNames()},
		{name: "hasher", names: localusertest.HasherCaseNames()},
		{name: "blocklist", names: localusertest.BlocklistCaseNames()},
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
