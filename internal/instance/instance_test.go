package instance_test

import (
	"regexp"
	"testing"

	"opendustpan.org/opendustpan/internal/instance"
)

// idPattern is the form of an instance ID: the prefix and 20 lowercase
// base32 characters.
var idPattern = regexp.MustCompile(`^ins-[a-z2-7]{20}$`)

func TestNewIDFormAndUniqueness(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 100 {
		id := instance.NewID()

		if !idPattern.MatchString(id) {
			t.Fatalf("NewID = %q, want the form %s", id, idPattern)
		}

		if seen[id] {
			t.Fatalf("NewID answered %q two times", id)
		}

		seen[id] = true
	}
}
