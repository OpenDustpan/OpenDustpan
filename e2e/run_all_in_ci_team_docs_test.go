//go:build e2e

package e2e_test

import (
	"os"
	"strings"
	"testing"
)

// TestE2ETerragruntCiDocsPage proves that the team CI section of the Terragrunt
// guide holds the commands of the CI job as written, and the note that
// profiles and blueprints stay the first choice. The page names no route.
func TestE2ETerragruntCiDocsPage(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("..")
	if err != nil {
		t.Fatalf("open the repository: %v", err)
	}

	page, pageErr := root.ReadFile(tgLocalDocsPage)

	if closeErr := root.Close(); pageErr != nil || closeErr != nil {
		t.Fatalf("read %s: %v %v", tgLocalDocsPage, pageErr, closeErr)
	}

	_, section, found := strings.Cut(string(page), "## Team CI")
	if !found {
		t.Fatalf("%s has no Team CI section", tgLocalDocsPage)
	}

	section, _, _ = strings.Cut(section, "\n## ")

	commands := []string{
		"TF_CLI_CONFIG_FILE=$CREDS",
		"terragrunt " + strings.Join(tgLocalDocsRunAllArgs(), " "),
		"../explanation/profiles-and-blueprints.md",
		"Profiles and blueprints stay the first choice",
	}

	for _, want := range commands {
		if !strings.Contains(section, want) {
			t.Errorf("the Team CI section lacks %q", want)
		}
	}
}
