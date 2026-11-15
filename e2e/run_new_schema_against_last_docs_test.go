//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestDocsPageHoldsTheMixedVersionMatrix reads the backup, restore and
// upgrade guide as text and checks that it holds the mixed-version matrix:
// the tag list, the zero old-binary report, the refusal error with its one
// command, and the fetch command for an incomplete clone. It also checks
// that the Makefile holds the recipe that the page names. The section holds
// contributor commands and names no route.
func TestDocsPageHoldsTheMixedVersionMatrix(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	raw, pageErr := root.ReadFile("docs/guides/backup-restore-upgrade.md")
	makefile, makeErr := root.ReadFile("Makefile")

	if closeErr := root.Close(); pageErr != nil || makeErr != nil || closeErr != nil {
		t.Fatalf("read the guide and the Makefile: %v %v %v", pageErr, makeErr, closeErr)
	}

	page := flatten(string(raw))

	for _, want := range []string{
		"make mixed-version-test",
		"last 2 release tags",
		"zero old binaries",
		"schema.binary-below-minimum",
		"git fetch --tags",
		"DUSTPAN_TEST_POSTGRES_URL",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the backup, restore and upgrade guide lacks %q", want)
		}
	}

	recipe := flatten(string(makefile))

	for _, want := range []string{"mixed-version-test:", "TestMixedVersion"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the Makefile lacks the recipe %q that the page names", want)
		}
	}
}

// flatten joins the lines of a document into one line, so that a phrase
// matches across a line break.
func flatten(document string) string {
	return strings.Join(strings.Fields(document), " ")
}
