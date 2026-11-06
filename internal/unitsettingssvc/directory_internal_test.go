package unitsettingssvc

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// TestCheckDirectoryRefusesHostileDirectories sends hostile directories straight
// to checkDirectory, as if Validate had not run. The layer must stop each one on
// its own.
func TestCheckDirectoryRefusesHostileDirectories(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{
		".github", ".github/workflows", "a/.github/b", "a/.GITHUB", ".gitlab-ci.yml", ".git/hooks", "../x", "a/../../x",
		"/etc", "a\\b",
	} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			if err := checkDirectory(dir); !errors.Is(err, unitsettings.ErrInvalid) {
				t.Fatalf("checkDirectory(%q) = %v, want ErrInvalid", dir, err)
			}
		})
	}

	for _, dir := range []string{"", "stacks/prod", "a/b-c"} {
		if err := checkDirectory(dir); err != nil {
			t.Errorf("checkDirectory(%q) = %v, want nil", dir, err)
		}
	}
}
