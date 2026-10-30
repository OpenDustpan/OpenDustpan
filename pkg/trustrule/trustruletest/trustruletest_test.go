package trustruletest_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule/trustruletest"
)

func TestCaseNamesAreUniqueAndNotEmpty(t *testing.T) {
	t.Parallel()

	names := trustruletest.CaseNames()
	if len(names) == 0 {
		t.Fatal("the suite has no case")
	}

	seen := map[string]bool{}

	for _, name := range names {
		if name == "" {
			t.Fatal("a case has an empty name")
		}

		if seen[name] {
			t.Fatalf("case name %q is used twice", name)
		}

		seen[name] = true
	}

	if !slices.Contains(names, "CreateRefusesAPrincipalOutsideTheNamespace") {
		t.Fatal("the suite has no case for a principal outside the namespace")
	}
}
