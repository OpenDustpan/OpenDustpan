package identity_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestKindKnown proves that Known accepts each kind of Kinds and refuses a
// kind that differs in any byte.
func TestKindKnown(t *testing.T) {
	t.Parallel()

	for _, k := range identity.Kinds() {
		if !k.Known() {
			t.Errorf("Known(%q) = false, want true", k)
		}
	}

	for _, k := range []identity.Kind{"", "robot", "Person", "person\n", " service"} {
		if k.Known() {
			t.Errorf("Known(%q) = true, want false", k)
		}
	}
}
