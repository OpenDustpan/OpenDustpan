package savedviewtest_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/savedview/savedviewtest"
)

func TestCaseNamesAreUniqueAndNotEmpty(t *testing.T) {
	t.Parallel()

	names := savedviewtest.CaseNames()
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

	if !slices.Contains(names, "get of the view of another owner returns not found") {
		t.Fatal("the suite has no case for the view of another owner")
	}
}
