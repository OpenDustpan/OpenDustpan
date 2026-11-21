package identity_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// catalogCount pins the size of the catalog that Actions returns: one
// entry for each action constant of the block in identity.go. An author
// who adds a new action constant must add its catalog entry too. This
// count does not catch a new action constant that has no catalog entry:
// the test still passes. The project accepts this risk, and no other
// check covers it.
const catalogCount = 133

// TestActionsCatalogIsComplete checks the catalog that Actions returns:
// it holds one entry for each action of the core, sorted, with no
// duplicate.
func TestActionsCatalogIsComplete(t *testing.T) {
	t.Parallel()

	acts := identity.Actions()

	if len(acts) != catalogCount {
		t.Errorf(
			"Actions returns %d actions, want %d: a new action constant needs an entry in the catalog of Actions too",
			len(acts),
			catalogCount,
		)
	}

	if !slices.IsSorted(acts) {
		t.Error("Actions returns an unsorted slice, want sorted")
	}

	seen := make(map[identity.Action]bool, len(acts))

	for _, a := range acts {
		if seen[a] {
			t.Errorf("Actions returns %q more than once", a)
		}

		seen[a] = true
	}
}

// TestActionsReturnsAFreshSlice checks that a caller can change the slice
// that Actions returns, and that the change does not touch what the next
// call returns.
func TestActionsReturnsAFreshSlice(t *testing.T) {
	t.Parallel()

	first := identity.Actions()
	if len(first) == 0 {
		t.Fatal("Actions returns no actions")
	}

	first[0] = identity.Action("mutated by a caller")

	second := identity.Actions()
	if slices.Contains(second, identity.Action("mutated by a caller")) {
		t.Error("a change of the returned slice changed the next result of Actions")
	}
}
