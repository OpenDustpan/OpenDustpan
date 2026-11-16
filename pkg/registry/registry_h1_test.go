package registry_test

import (
	"testing"
)

// TestSameContent_countsH1 is hostile: a republish with different bytes must
// not compare equal to the stored release. It fails when SameContent drops H1.
func TestSameContent_countsH1(t *testing.T) {
	t.Parallel()

	base := provider()
	base.Archives[0].H1 = "h1:stored-digest-value"

	same := provider()
	same.Archives[0].H1 = "h1:stored-digest-value"
	if !base.SameContent(same) {
		t.Fatal("identical releases do not compare equal")
	}

	changed := provider()
	changed.Archives[0].H1 = "h1:changed-digest-value"
	if base.SameContent(changed) {
		t.Fatal("releases with different H1 compare equal")
	}

	empty := provider()
	empty.Archives[0].H1 = ""
	if base.SameContent(empty) {
		t.Fatal("release with H1 compares equal to one without H1")
	}
}
